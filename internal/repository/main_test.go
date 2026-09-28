package repository

import (
	"context"
	"database/sql"
	"testing"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/storage"
	"sekai-master-api/internal/storage/pgtest"
)

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

// newPostgresTestDB opens a fresh, fully migrated PostgreSQL database through
// the production pool and returns its database/sql bridge.
func newPostgresTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := storage.OpenDB(context.Background(), config.Config{DatabaseURL: pgtest.NewDatabase(t)})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := storage.RunMigrations(context.Background(), db.SQL); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db.SQL
}
