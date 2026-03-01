-- +goose Up
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
);

-- +goose Down
DROP TABLE IF EXISTS master_data_sync_status;
