-- Master-data store tables (docs/postgres-master-data-store.md#schema).
-- PostgreSQL-only: first_key relies on COLLATE "C" so the database orders
-- block sort keys byte for byte, exactly as Go compares strings.

-- +goose Up
-- One row per stored entity kind per region.
CREATE TABLE IF NOT EXISTS master_entities (
  region TEXT NOT NULL,
  entity TEXT NOT NULL,
  revision TEXT NOT NULL,
  source_digest TEXT,
  source_commit TEXT,
  record_count INTEGER NOT NULL,
  order_keys BYTEA NOT NULL,
  index_version TEXT NOT NULL DEFAULT '',
  projection_version TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (region, entity)
);

-- Records, key-sorted, in blocks of up to 32 rows (or 64 kB of JSON).
CREATE TABLE IF NOT EXISTS master_blocks (
  region TEXT NOT NULL,
  entity TEXT NOT NULL,
  first_key TEXT COLLATE "C" NOT NULL,
  body BYTEA NOT NULL,
  PRIMARY KEY (region, entity, first_key)
);

-- Relation index postings (masterdata.EntityIndexes), built at sync.
CREATE TABLE IF NOT EXISTS master_record_index (
  region TEXT NOT NULL,
  entity TEXT NOT NULL,
  index_name TEXT NOT NULL,
  index_key TEXT NOT NULL,
  record_keys TEXT[] NOT NULL,
  PRIMARY KEY (region, entity, index_name, index_key)
);

-- List projections (masterdata.ProjectionFields): gob + zstd.
CREATE TABLE IF NOT EXISTS master_projections (
  region TEXT NOT NULL,
  entity TEXT NOT NULL,
  version TEXT NOT NULL,
  body BYTEA NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (region, entity)
);

-- versions.json per region.
CREATE TABLE IF NOT EXISTS master_versions (
  region TEXT PRIMARY KEY,
  payload JSONB NOT NULL,
  source_commit TEXT,
  updated_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS master_versions;
DROP TABLE IF EXISTS master_projections;
DROP TABLE IF EXISTS master_record_index;
DROP TABLE IF EXISTS master_blocks;
DROP TABLE IF EXISTS master_entities;
