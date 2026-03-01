package storage

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"

	"sekai-master-api/internal/config"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func RunMigrations(ctx context.Context, db *sql.DB, cfg config.Config) error {
	dialect := "postgres"
	if cfg.DatabaseDriver() == "sqlite" {
		dialect = "sqlite3"
	}

	goose.SetBaseFS(migrationFS)
	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}
