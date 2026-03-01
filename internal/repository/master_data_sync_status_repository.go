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
	db     *sql.DB
	driver string
}

func NewMasterDataSyncStatusRepository(db *sql.DB, driver string) *MasterDataSyncStatusRepository {
	return &MasterDataSyncStatusRepository{
		db:     db,
		driver: strings.ToLower(strings.TrimSpace(driver)),
	}
}

func (repository *MasterDataSyncStatusRepository) Save(ctx context.Context, status masterdata.SyncStatus) error {
	if repository.db == nil {
		return nil
	}

	if repository.isPostgres() {
		if err := upsertSyncStatusPostgres(ctx, repository.db, status); err != nil {
			return err
		}
	} else {
		if err := upsertSyncStatusSQLite(ctx, repository.db, status); err != nil {
			return err
		}
	}

	return nil
}

func upsertSyncStatusSQLite(ctx context.Context, db *sql.DB, status masterdata.SyncStatus) error {
	insertQuery := `
INSERT INTO master_data_sync_status (
	region, status, file_count, sync_duration_ms, last_synced_at, source_commit, error_message,
	source_owner, source_repo, source_ref, source_path, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(region) DO UPDATE SET
	status=excluded.status,
	file_count=excluded.file_count,
	sync_duration_ms=excluded.sync_duration_ms,
	last_synced_at=excluded.last_synced_at,
	source_commit=excluded.source_commit,
	error_message=excluded.error_message,
	source_owner=excluded.source_owner,
	source_repo=excluded.source_repo,
	source_ref=excluded.source_ref,
	source_path=excluded.source_path,
	updated_at=excluded.updated_at`

	if _, err := db.ExecContext(
		ctx,
		insertQuery,
		status.Region,
		status.Status,
		status.FileCount,
		status.SyncDurationMS,
		status.LastSyncedAt,
		nullableText(status.SourceCommit),
		nullableText(status.ErrorMessage),
		status.Source.Owner,
		status.Source.Repo,
		status.Source.Ref,
		nullableText(status.Source.Path),
		status.UpdatedAt,
	); err != nil {
		return fmt.Errorf("upsert sync status: %w", err)
	}

	return nil
}

func upsertSyncStatusPostgres(ctx context.Context, db *sql.DB, status masterdata.SyncStatus) error {
	insertPostgres := `
INSERT INTO master_data_sync_status (
	region, status, file_count, sync_duration_ms, last_synced_at, source_commit, error_message,
	source_owner, source_repo, source_ref, source_path, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (region) DO UPDATE SET
	status=EXCLUDED.status,
	file_count=EXCLUDED.file_count,
	sync_duration_ms=EXCLUDED.sync_duration_ms,
	last_synced_at=EXCLUDED.last_synced_at,
	source_commit=EXCLUDED.source_commit,
	error_message=EXCLUDED.error_message,
	source_owner=EXCLUDED.source_owner,
	source_repo=EXCLUDED.source_repo,
	source_ref=EXCLUDED.source_ref,
	source_path=EXCLUDED.source_path,
	updated_at=EXCLUDED.updated_at`

	if _, pgErr := db.ExecContext(
		ctx,
		insertPostgres,
		status.Region,
		status.Status,
		status.FileCount,
		status.SyncDurationMS,
		status.LastSyncedAt,
		nullableText(status.SourceCommit),
		nullableText(status.ErrorMessage),
		status.Source.Owner,
		status.Source.Repo,
		status.Source.Ref,
		nullableText(status.Source.Path),
		status.UpdatedAt,
	); pgErr != nil {
		return fmt.Errorf("upsert sync status: %w", pgErr)
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
	COALESCE(source_commit, ''),
	COALESCE(error_message, ''),
	source_owner,
	source_repo,
	source_ref,
	COALESCE(source_path, ''),
	updated_at
FROM master_data_sync_status
ORDER BY updated_at DESC, region ASC`)
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
			&status.SourceCommit,
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

func (repository *MasterDataSyncStatusRepository) isPostgres() bool {
	return repository.driver == "pgx" || repository.driver == "postgres" || repository.driver == "postgresql"
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
