package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"sekai-master-api/internal/config"
)

func OpenDB(cfg config.Config) (*sql.DB, error) {
	driver := cfg.DatabaseDriver()
	dsn := cfg.EffectiveDatabaseDSN()

	if driver == "sqlite" {
		dir := filepath.Dir(dsn)
		if dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("failed to create sqlite directory: %w", err)
			}
		}
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}
