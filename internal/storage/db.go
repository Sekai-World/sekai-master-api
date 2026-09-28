package storage

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"sekai-master-api/internal/config"
)

// DB is the process's single PostgreSQL connection pool. Pool serves callers
// that need native pgx features (arrays, COPY, typed parameters); SQL bridges
// the same pool to database/sql for the status and lease repositories, health
// checks and migrations, so both share one set of connections and limits.
type DB struct {
	Pool *pgxpool.Pool
	SQL  *sql.DB
}

// OpenDB opens the pool described by cfg and verifies it with a ping.
func OpenDB(ctx context.Context, cfg config.Config) (*DB, error) {
	if err := cfg.ValidateDatabaseDriver(); err != nil {
		return nil, err
	}

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	applyPoolSettings(poolConfig, cfg)

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{Pool: pool, SQL: stdlib.OpenDBFromPool(pool)}, nil
}

// applyPoolSettings overrides the pool limits parsed from DATABASE_URL (its
// pool_* query parameters, or pgx defaults) with every setting cfg sets to a
// positive value.
func applyPoolSettings(poolConfig *pgxpool.Config, cfg config.Config) {
	if cfg.DatabaseMaxConns > 0 {
		poolConfig.MaxConns = poolConnCount(cfg.DatabaseMaxConns)
	}
	if cfg.DatabaseMinConns > 0 {
		poolConfig.MinConns = min(poolConnCount(cfg.DatabaseMinConns), poolConfig.MaxConns)
	}
	if cfg.DatabaseConnectTimeout > 0 {
		poolConfig.ConnConfig.ConnectTimeout = cfg.DatabaseConnectTimeout
	}
	if cfg.DatabaseMaxConnLifetime > 0 {
		poolConfig.MaxConnLifetime = cfg.DatabaseMaxConnLifetime
	}
	if cfg.DatabaseMaxConnIdleTime > 0 {
		poolConfig.MaxConnIdleTime = cfg.DatabaseMaxConnIdleTime
	}
}

// poolConnCount converts a positive configured connection count to the int32
// pgxpool takes, capping values beyond its range.
func poolConnCount(value int) int32 {
	if value > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(value)
}

// Close closes the database/sql bridge and then the pool. It is safe on a nil
// DB.
func (db *DB) Close() error {
	if db == nil {
		return nil
	}

	var err error
	if db.SQL != nil {
		err = db.SQL.Close()
	}
	if db.Pool != nil {
		db.Pool.Close()
	}
	return err
}
