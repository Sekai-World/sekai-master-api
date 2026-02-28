package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

type MasterDataSyncStatusRepository struct {
	db *sql.DB
}

func NewMasterDataSyncStatusRepository(db *sql.DB) *MasterDataSyncStatusRepository {
	return &MasterDataSyncStatusRepository{db: db}
}

func (repository *MasterDataSyncStatusRepository) EnsureSchema(ctx context.Context) error {
	if repository.db == nil {
		return nil
	}

	query := `
CREATE TABLE IF NOT EXISTS master_data_sync_status (
	region TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	file_count INTEGER NOT NULL,
	sync_duration_ms INTEGER NOT NULL DEFAULT 0,
	last_synced_at TIMESTAMP NOT NULL,
	error_message TEXT,
	source_owner TEXT NOT NULL,
	source_repo TEXT NOT NULL,
	source_ref TEXT NOT NULL,
	source_path TEXT,
	updated_at TIMESTAMP NOT NULL
);`

	if _, err := repository.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("ensure master_data_sync_status schema: %w", err)
	}

	if _, err := repository.db.ExecContext(ctx, `ALTER TABLE master_data_sync_status ADD COLUMN sync_duration_ms INTEGER NOT NULL DEFAULT 0`); err != nil {
		if !isColumnAlreadyExistsError(err) {
			return fmt.Errorf("ensure sync_duration_ms column: %w", err)
		}
	}

	return nil
}

func (repository *MasterDataSyncStatusRepository) Save(ctx context.Context, status masterdata.SyncStatus) error {
	if repository.db == nil {
		return nil
	}

	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sync status transaction: %w", err)
	}

	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `DELETE FROM master_data_sync_status WHERE region = ?`, status.Region); err != nil {
		if _, pgErr := tx.ExecContext(ctx, `DELETE FROM master_data_sync_status WHERE region = $1`, status.Region); pgErr != nil {
			return fmt.Errorf("delete previous sync status: %w", err)
		}
	}

	if err = insertSyncStatus(ctx, tx, status); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit sync status transaction: %w", err)
	}

	return nil
}

func insertSyncStatus(ctx context.Context, tx *sql.Tx, status masterdata.SyncStatus) error {
	insertQuery := `
INSERT INTO master_data_sync_status (
	region, status, file_count, sync_duration_ms, last_synced_at, error_message,
	source_owner, source_repo, source_ref, source_path, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := tx.ExecContext(
		ctx,
		insertQuery,
		status.Region,
		status.Status,
		status.FileCount,
		status.SyncDurationMS,
		status.LastSyncedAt,
		nullableText(status.ErrorMessage),
		status.Source.Owner,
		status.Source.Repo,
		status.Source.Ref,
		nullableText(status.Source.Path),
		status.UpdatedAt,
	)
	if err == nil {
		return nil
	}

	insertPostgres := `
INSERT INTO master_data_sync_status (
	region, status, file_count, sync_duration_ms, last_synced_at, error_message,
	source_owner, source_repo, source_ref, source_path, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	if _, pgErr := tx.ExecContext(
		ctx,
		insertPostgres,
		status.Region,
		status.Status,
		status.FileCount,
		status.SyncDurationMS,
		status.LastSyncedAt,
		nullableText(status.ErrorMessage),
		status.Source.Owner,
		status.Source.Repo,
		status.Source.Ref,
		nullableText(status.Source.Path),
		status.UpdatedAt,
	); pgErr != nil {
		return fmt.Errorf("insert sync status: %w", err)
	}

	return nil
}

func (repository *MasterDataSyncStatusRepository) List(ctx context.Context) ([]masterdata.SyncStatus, error) {
	if repository.db == nil {
		return []masterdata.SyncStatus{}, nil
	}

	rows, err := repository.db.QueryContext(ctx, `
SELECT
	region,
	status,
	file_count,
	sync_duration_ms,
	last_synced_at,
	COALESCE(error_message, ''),
	source_owner,
	source_repo,
	source_ref,
	COALESCE(source_path, ''),
	updated_at
FROM master_data_sync_status
ORDER BY region ASC`)
	if err != nil {
		return nil, fmt.Errorf("list sync status: %w", err)
	}
	defer rows.Close()

	statuses := make([]masterdata.SyncStatus, 0)
	for rows.Next() {
		var status masterdata.SyncStatus
		var source masterdata.Source
		if err := rows.Scan(
			&status.Region,
			&status.Status,
			&status.FileCount,
			&status.SyncDurationMS,
			&status.LastSyncedAt,
			&status.ErrorMessage,
			&source.Owner,
			&source.Repo,
			&source.Ref,
			&source.Path,
			&status.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan sync status row: %w", err)
		}
		source.Region = status.Region
		status.Source = source
		statuses = append(statuses, status)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sync status rows: %w", err)
	}

	return statuses, nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}

	return value
}

func (repository *MasterDataSyncStatusRepository) SeedPending(ctx context.Context, sources []masterdata.Source) error {
	now := time.Now().UTC()
	for _, source := range sources {
		status := masterdata.SyncStatus{
			Region:         source.Region,
			Status:         "pending",
			FileCount:      0,
			SyncDurationMS: 0,
			LastSyncedAt:   now,
			Source:         source,
			UpdatedAt:      now,
		}
		if err := repository.Save(ctx, status); err != nil {
			return err
		}
	}

	return nil
}

func isColumnAlreadyExistsError(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "duplicate column") || strings.Contains(message, "already exists")
}
