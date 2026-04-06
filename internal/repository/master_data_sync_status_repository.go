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
		if err := insertSyncStatusPostgres(ctx, repository.db, status); err != nil {
			return err
		}
	} else {
		if err := insertSyncStatusSQLite(ctx, repository.db, status); err != nil {
			return err
		}
	}

	return nil
}

func insertSyncStatusSQLite(ctx context.Context, db *sql.DB, status masterdata.SyncStatus) error {
	insertQuery := `
INSERT INTO master_data_sync_status (
	region, status, file_count, sync_duration_ms, last_synced_at, source_commit, error_message,
	source_owner, source_repo, source_ref, source_path
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

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
	); err != nil {
		return fmt.Errorf("insert sync status: %w", err)
	}

	return nil
}

func insertSyncStatusPostgres(ctx context.Context, db *sql.DB, status masterdata.SyncStatus) error {
	insertPostgres := `
INSERT INTO master_data_sync_status (
	region, status, file_count, sync_duration_ms, last_synced_at, source_commit, error_message,
	source_owner, source_repo, source_ref, source_path
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

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
	); pgErr != nil {
		return fmt.Errorf("insert sync status: %w", pgErr)
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
	last_updated_at
FROM master_data_sync_status_latest
ORDER BY last_updated_at DESC, region ASC`)
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

func (repository *MasterDataSyncStatusRepository) ListLatestSuccess(ctx context.Context) ([]masterdata.SyncStatus, error) {
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
	created_at AS last_updated_at
FROM (
	SELECT
		region,
		status,
		file_count,
		sync_duration_ms,
		last_synced_at,
		source_commit,
		error_message,
		source_owner,
		source_repo,
		source_ref,
		source_path,
		created_at,
		ROW_NUMBER() OVER (PARTITION BY region ORDER BY created_at DESC, last_synced_at DESC) AS row_num
	FROM master_data_sync_status
	WHERE status = 'success'
) latest_success
WHERE row_num = 1
ORDER BY last_updated_at DESC, region ASC`)
	if err != nil {
		return nil, fmt.Errorf("list latest success sync status: %w", err)
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
			return nil, fmt.Errorf("scan latest success sync status row: %w", err)
		}
		source.Region = status.Region
		status.Source = source
		statuses = append(statuses, status)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest success sync status rows: %w", err)
	}

	return statuses, nil
}

func (repository *MasterDataSyncStatusRepository) ListLatestStable(ctx context.Context) ([]masterdata.SyncStatus, error) {
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
	created_at AS last_updated_at
FROM (
	SELECT
		region,
		status,
		file_count,
		sync_duration_ms,
		last_synced_at,
		source_commit,
		error_message,
		source_owner,
		source_repo,
		source_ref,
		source_path,
		created_at,
		ROW_NUMBER() OVER (PARTITION BY region ORDER BY created_at DESC, last_synced_at DESC) AS row_num
	FROM master_data_sync_status
	WHERE status <> 'running'
) latest_stable
WHERE row_num = 1
ORDER BY last_updated_at DESC, region ASC`)
	if err != nil {
		return nil, fmt.Errorf("list latest stable sync status: %w", err)
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
			return nil, fmt.Errorf("scan latest stable sync status row: %w", err)
		}
		source.Region = status.Region
		status.Source = source
		statuses = append(statuses, status)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest stable sync status rows: %w", err)
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
