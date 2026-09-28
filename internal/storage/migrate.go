package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// RunMigrations applies every pending Goose migration. It uses a Goose
// provider rather than the package-level Goose state, so concurrent callers
// (such as parallel tests on separate databases) do not share configuration.
func RunMigrations(ctx context.Context, db *sql.DB) error {
	migrations, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}
