-- +goose Up
DROP VIEW IF EXISTS master_data_sync_status_latest;

CREATE TABLE IF NOT EXISTS master_data_sync_status_history (
  region TEXT NOT NULL,
  status TEXT NOT NULL,
  file_count INTEGER NOT NULL,
  sync_duration_ms INTEGER NOT NULL DEFAULT 0,
  last_synced_at TIMESTAMP NOT NULL,
  source_commit TEXT,
  error_message TEXT,
  source_owner TEXT NOT NULL,
  source_repo TEXT NOT NULL,
  source_ref TEXT NOT NULL,
  source_path TEXT,
  updated_at TIMESTAMP NOT NULL
);

INSERT INTO master_data_sync_status_history (
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
  updated_at
)
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
  updated_at
FROM master_data_sync_status;

DROP TABLE master_data_sync_status;
ALTER TABLE master_data_sync_status_history RENAME TO master_data_sync_status;

CREATE VIEW master_data_sync_status_latest AS
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
  updated_at
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
    updated_at,
    ROW_NUMBER() OVER (
      PARTITION BY region
      ORDER BY updated_at DESC, last_synced_at DESC
    ) AS row_num
  FROM master_data_sync_status
) latest
WHERE latest.row_num = 1;

-- +goose Down
DROP VIEW IF EXISTS master_data_sync_status_latest;

CREATE TABLE IF NOT EXISTS master_data_sync_status_current (
  region TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  file_count INTEGER NOT NULL,
  sync_duration_ms INTEGER NOT NULL DEFAULT 0,
  last_synced_at TIMESTAMP NOT NULL,
  source_commit TEXT,
  error_message TEXT,
  source_owner TEXT NOT NULL,
  source_repo TEXT NOT NULL,
  source_ref TEXT NOT NULL,
  source_path TEXT,
  updated_at TIMESTAMP NOT NULL
);

INSERT INTO master_data_sync_status_current (
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
  updated_at
)
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
  updated_at
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
    updated_at,
    ROW_NUMBER() OVER (
      PARTITION BY region
      ORDER BY updated_at DESC, last_synced_at DESC
    ) AS row_num
  FROM master_data_sync_status
) latest
WHERE latest.row_num = 1;

DROP TABLE master_data_sync_status;
ALTER TABLE master_data_sync_status_current RENAME TO master_data_sync_status;
