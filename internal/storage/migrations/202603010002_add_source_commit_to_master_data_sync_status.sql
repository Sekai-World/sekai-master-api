-- +goose Up
ALTER TABLE master_data_sync_status ADD COLUMN IF NOT EXISTS source_commit TEXT;

-- +goose Down
SELECT 1;
