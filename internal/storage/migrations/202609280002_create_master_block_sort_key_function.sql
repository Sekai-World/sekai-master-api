-- master_block_sort_key is masterdata.BlockSortKey in SQL, so one statement
-- can locate the block of a record key it reads from master_record_index. A
-- test pins it to the Go function.

-- +goose Up
CREATE OR REPLACE FUNCTION master_block_sort_key(record_key TEXT) RETURNS TEXT
  LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE
  RETURN (CASE
    WHEN record_key ~ '^(0|[1-9][0-9]{0,19})$' THEN '0' || lpad(record_key, 20, '0')
    ELSE '1' || record_key
  END) COLLATE "C";

-- +goose Down
DROP FUNCTION IF EXISTS master_block_sort_key(TEXT);
