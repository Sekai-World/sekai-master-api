-- +goose Up
ALTER TABLE master_data_sync_status ADD COLUMN fencing_token BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE master_data_sync_status DROP COLUMN fencing_token;
