package storage

import (
	"context"
	"io/fs"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/storage/pgtest"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

var masterDataStoreTables = []string{"master_entities", "master_blocks", "master_record_index", "master_projections", "master_versions"}

// openMigratedTestDB opens a fresh PostgreSQL database through OpenDB and
// applies every migration.
func openMigratedTestDB(t *testing.T) *DB {
	t.Helper()

	db, err := OpenDB(context.Background(), config.Config{DatabaseURL: pgtest.NewDatabase(t)})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := RunMigrations(context.Background(), db.SQL); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func TestOpenDBRejectsUnsupportedDriver(t *testing.T) {
	_, err := OpenDB(context.Background(), config.Config{DatabaseDriverName: "sqlite", DatabaseURL: "postgres://unused/db"})
	if err == nil || !strings.Contains(err.Error(), "SQLite support was removed") {
		t.Fatalf("OpenDB with DATABASE_DRIVER=sqlite error = %v, want the removal message", err)
	}
}

func TestApplyPoolSettingsOverridesOnlyPositiveValues(t *testing.T) {
	poolConfig, err := pgxpool.ParseConfig("postgres://user@localhost/db?pool_max_conns=7&pool_min_conns=1&connect_timeout=3")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	applyPoolSettings(poolConfig, config.Config{})
	if poolConfig.MaxConns != 7 || poolConfig.MinConns != 1 || poolConfig.ConnConfig.ConnectTimeout != 3*time.Second {
		t.Fatalf("zero settings changed the URL's pool config: max=%d min=%d connect=%s", poolConfig.MaxConns, poolConfig.MinConns, poolConfig.ConnConfig.ConnectTimeout)
	}

	applyPoolSettings(poolConfig, config.Config{
		DatabaseMaxConns:        40,
		DatabaseMinConns:        50,
		DatabaseConnectTimeout:  5 * time.Second,
		DatabaseMaxConnLifetime: 10 * time.Minute,
		DatabaseMaxConnIdleTime: 2 * time.Minute,
	})
	if poolConfig.MaxConns != 40 || poolConfig.MinConns != 40 {
		t.Fatalf("pool size = max %d min %d, want 40/40 (min capped at max)", poolConfig.MaxConns, poolConfig.MinConns)
	}
	if poolConfig.ConnConfig.ConnectTimeout != 5*time.Second || poolConfig.MaxConnLifetime != 10*time.Minute || poolConfig.MaxConnIdleTime != 2*time.Minute {
		t.Fatalf("pool timeouts = connect %s lifetime %s idle %s", poolConfig.ConnConfig.ConnectTimeout, poolConfig.MaxConnLifetime, poolConfig.MaxConnIdleTime)
	}

	applyPoolSettings(poolConfig, config.Config{DatabaseMaxConns: math.MaxInt32 + 1, DatabaseMinConns: math.MaxInt32 + 1})
	if poolConfig.MaxConns != math.MaxInt32 || poolConfig.MinConns != math.MaxInt32 {
		t.Fatalf("out-of-range pool size = max %d min %d, want both capped at MaxInt32", poolConfig.MaxConns, poolConfig.MinConns)
	}
}

func TestOpenDBBridgesDatabaseSQLToThePool(t *testing.T) {
	db, err := OpenDB(context.Background(), config.Config{DatabaseURL: pgtest.NewDatabase(t), DatabaseMaxConns: 3})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	if got := db.Pool.Config().MaxConns; got != 3 {
		t.Fatalf("pool max conns = %d, want 3", got)
	}

	// Hold every pool connection: a database/sql query must then wait for
	// one, which proves it draws from the same pool.
	ctx := context.Background()
	held := make([]*pgxpool.Conn, 0, 3)
	for range 3 {
		conn, err := db.Pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire pool connection: %v", err)
		}
		held = append(held, conn)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if err := db.SQL.PingContext(waitCtx); err == nil {
		t.Fatal("database/sql ping succeeded while the pool was exhausted; the bridge is not sharing the pool")
	}

	for _, conn := range held {
		conn.Release()
	}
	if err := db.SQL.PingContext(ctx); err != nil {
		t.Fatalf("database/sql ping after release: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := db.Pool.Ping(ctx); err == nil {
		t.Fatal("pool still usable after Close")
	}
}

func TestMigrationsCreateMasterDataStoreSchema(t *testing.T) {
	db := openMigratedTestDB(t)
	ctx := context.Background()

	// Re-running is a no-op.
	if err := RunMigrations(ctx, db.SQL); err != nil {
		t.Fatalf("re-run migrations: %v", err)
	}
	assertTablesExist(t, db, masterDataStoreTables, true)

	var collation string
	if err := db.Pool.QueryRow(ctx, `
SELECT collation_name FROM information_schema.columns
WHERE table_name = 'master_blocks' AND column_name = 'first_key'`).Scan(&collation); err != nil {
		t.Fatalf("read first_key collation: %v", err)
	}
	if collation != "C" {
		t.Fatalf("master_blocks.first_key collation = %q, want C", collation)
	}

	var timestampColumns int
	if err := db.Pool.QueryRow(ctx, `
SELECT count(*) FROM information_schema.columns
WHERE table_name = ANY($1) AND data_type = 'timestamp without time zone'`, masterDataStoreTables).Scan(&timestampColumns); err != nil {
		t.Fatalf("count timestamp columns: %v", err)
	}
	if timestampColumns != 0 {
		t.Fatalf("master-data store has %d timestamp columns without time zone, want timestamptz only", timestampColumns)
	}

	// Every migration rolls back cleanly and re-applies.
	migrations, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		t.Fatalf("open migrations: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db.SQL, migrations)
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	assertTablesExist(t, db, masterDataStoreTables, false)
	assertTablesExist(t, db, []string{"master_data_sync_status", "master_data_sync_leases"}, false)
	if err := RunMigrations(ctx, db.SQL); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	assertTablesExist(t, db, masterDataStoreTables, true)
}

// TestBlockSortKeysOrderTheSameInPostgresAndGo pins the property the block
// read path relies on: master_blocks.first_key orders and compares exactly
// like Go strings, so "the block with the greatest first_key <= key" is the
// same block in SQL and in Go.
func TestBlockSortKeysOrderTheSameInPostgresAndGo(t *testing.T) {
	db := openMigratedTestDB(t)
	ctx := context.Background()

	recordKeys := []string{
		"0", "1", "2", "9", "10", "11", "99", "100", "1010201", "18446744073709551615",
		"007", "100000000000000000000", "-5",
		"auto:00ff", "auto:0aFF", "auto:Ab", "auto:aB",
		"composite:v1:13:resourceboxes2:id1:5", "composite:v1:13:resourceboxes2:id2:10",
		"Zeta", "alpha", "_x", "-x", "x y", "é", "日本",
	}
	sortKeys := make([]string, 0, len(recordKeys))
	for _, recordKey := range recordKeys {
		sortKey := masterdata.BlockSortKey(recordKey)
		sortKeys = append(sortKeys, sortKey)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO master_blocks (region, entity, first_key, body) VALUES ('jp', 'cards', $1, '\x00')`, sortKey); err != nil {
			t.Fatalf("insert block %q: %v", sortKey, err)
		}
	}

	rows, err := db.Pool.Query(ctx, `SELECT first_key FROM master_blocks WHERE region = 'jp' AND entity = 'cards' ORDER BY first_key`)
	if err != nil {
		t.Fatalf("query blocks: %v", err)
	}
	var fromPostgres []string
	for rows.Next() {
		var sortKey string
		if err := rows.Scan(&sortKey); err != nil {
			t.Fatalf("scan: %v", err)
		}
		fromPostgres = append(fromPostgres, sortKey)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}

	fromGo := append([]string(nil), sortKeys...)
	sort.Strings(fromGo)
	if strings.Join(fromPostgres, "\n") != strings.Join(fromGo, "\n") {
		t.Fatalf("PostgreSQL order differs from Go order:\npostgres: %q\ngo:       %q", fromPostgres, fromGo)
	}

	// Probe keys between and outside the stored ones.
	for _, probe := range []string{"5", "50", "1010200", "1010202", "0", "auto:0", "zzz", "composite:v1:13:resourceboxes2:id1:7"} {
		probeKey := masterdata.BlockSortKey(probe)
		want := ""
		for _, sortKey := range fromGo {
			if sortKey <= probeKey {
				want = sortKey
			}
		}

		var got string
		err := db.Pool.QueryRow(ctx, `
SELECT COALESCE(max(first_key), '') FROM master_blocks
WHERE region = 'jp' AND entity = 'cards' AND first_key <= $1`, probeKey).Scan(&got)
		if err != nil {
			t.Fatalf("probe %q: %v", probe, err)
		}
		if got != want {
			t.Fatalf("probe %q (%q): PostgreSQL block %q, Go block %q", probe, probeKey, got, want)
		}
	}
}

func assertTablesExist(t *testing.T, db *DB, tables []string, wantExist bool) {
	t.Helper()

	for _, table := range tables {
		var exists bool
		if err := db.Pool.QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, "public."+table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if exists != wantExist {
			t.Fatalf("table %s exists = %v, want %v", table, exists, wantExist)
		}
	}
}
