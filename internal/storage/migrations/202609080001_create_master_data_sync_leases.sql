-- +goose Up
CREATE TABLE IF NOT EXISTS master_data_sync_leases (
  name TEXT PRIMARY KEY,
  holder TEXT NOT NULL,
  fencing_token BIGINT NOT NULL,
  acquired_at TIMESTAMP NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  last_heartbeat_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS master_data_sync_leases;
