package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

// SourceParityDirEnv names a checked-out master data repository (for example
// a clone of sekai-master-db-tc-diff) to run TestStoreParityOnSourceDirectory
// against.
const SourceParityDirEnv = "MASTER_DATA_PARITY_DIR"

// TestStoreParityOnSourceDirectory stores a real master data repository in
// every store and requires identical answers to every read of every entity.
// It is opt-in: set MASTER_DATA_PARITY_DIR to the repository's directory.
func TestStoreParityOnSourceDirectory(t *testing.T) {
	dir := strings.TrimSpace(os.Getenv(SourceParityDirEnv))
	if dir == "" {
		t.Skipf("set %s to a master data checkout to run this test", SourceParityDirEnv)
	}
	payload := loadSourceDirectory(t, dir)
	ctx := context.Background()

	backends := contractBackends()
	stores := make([]contractStore, len(backends))
	for index, backend := range backends {
		stores[index] = backend.open(t)
		started := time.Now()
		if err := stores[index].StoreRegion(ctx, "tw", payload); err != nil {
			t.Fatalf("%s: store: %v", backend.name, err)
		}
		t.Logf("%s: stored %d files in %s", backend.name, len(payload), time.Since(started).Round(time.Millisecond))
		started = time.Now()
		if err := stores[index].StoreRegion(ctx, "tw", payload); err != nil {
			t.Fatalf("%s: re-store: %v", backend.name, err)
		}
		t.Logf("%s: re-stored unchanged files in %s", backend.name, time.Since(started).Round(time.Millisecond))
	}
	if postgres, ok := stores[1].(*PostgresMasterDataStore); ok {
		var bytes, blocks int64
		if err := postgres.pool.QueryRow(ctx, `
SELECT sum(pg_total_relation_size(table_name::regclass)), (SELECT count(*) FROM master_blocks)
FROM unnest(ARRAY['master_entities', 'master_blocks', 'master_record_index', 'master_projections', 'master_versions']) AS table_name`).Scan(&bytes, &blocks); err != nil {
			t.Fatalf("measure footprint: %v", err)
		}
		t.Logf("postgres: %d blocks, %.1f MB on disk", blocks, float64(bytes)/(1<<20))
	}

	mismatches := 0
	compare := func(name string, read func(store contractStore) (any, error)) {
		t.Helper()
		answers := make([]any, len(stores))
		for index, store := range stores {
			value, err := read(store)
			if err != nil {
				// Stores word errors differently; handlers answer any
				// storage error the same way.
				value = "error"
			}
			answers[index] = value
		}
		for index := 1; index < len(answers); index++ {
			if !reflect.DeepEqual(answers[0], answers[index]) {
				mismatches++
				if mismatches <= 20 {
					t.Errorf("%s differs:\n%s: %s\n%s: %s", name, backends[0].name, describe(answers[0]), backends[index].name, describe(answers[index]))
				}
			}
		}
	}

	entities := make([]string, 0, len(payload))
	for filePath := range payload {
		entities = append(entities, entityNameFromPath(filePath))
	}
	sort.Strings(entities)
	reads := 0
	for _, entity := range entities {
		all, err := stores[0].ListAll(ctx, "tw", entity)
		if err != nil {
			// Records that are not JSON objects cannot be read back as
			// records; the comparison below still requires every store to
			// fail the same reads.
			t.Logf("list %s: %v", entity, err)
		}
		compare("ListAll "+entity, func(store contractStore) (any, error) { return store.ListAll(ctx, "tw", entity) })
		compare("HasEntityRecords "+entity, func(store contractStore) (any, error) { return store.HasEntityRecords(ctx, "tw", entity) })
		lastPage := len(all)/20 + 1
		for _, page := range []int{1, 2, lastPage, lastPage + 1} {
			compare("ListByPage "+entity, func(store contractStore) (any, error) {
				items, total, err := store.ListByPage(ctx, "tw", entity, page, 20)
				return []any{items, total}, err
			})
		}
		reads += 6

		ids := make([]string, 0, len(all))
		compositeKeys := make([]map[string]any, 0, len(all))
		for _, record := range all {
			ids = append(ids, masterdata.RecordID(record))
			compositeKeys = append(compositeKeys, record)
		}
		compare("GetByIDs "+entity, func(store contractStore) (any, error) { return store.GetByIDs(ctx, "tw", entity, ids) })
		compare("GetByCompositeKeys "+entity, func(store contractStore) (any, error) {
			return store.GetByCompositeKeys(ctx, "tw", entity, compositeKeys)
		})
		if len(ids) > 0 {
			compare("GetByID "+entity, func(store contractStore) (any, error) {
				record, found, err := store.GetByID(ctx, "tw", entity, ids[len(ids)/2])
				return []any{record, found}, err
			})
		}
		reads += 3

		for _, index := range masterdata.EntityIndexes(entity) {
			lookups := make([][]any, 0)
			seen := map[string]bool{}
			for _, record := range all {
				for _, key := range masterdata.IndexKeys(record, index) {
					if !seen[key] && len(lookups) < 200 && !strings.Contains(index, ",") {
						seen[key] = true
						lookups = append(lookups, []any{key})
					}
				}
			}
			if strings.Contains(index, ",") {
				fields := strings.Split(index, ",")
				for _, record := range all {
					lookup := make([]any, len(fields))
					for position, field := range fields {
						lookup[position] = record[field]
					}
					lookups = append(lookups, lookup)
				}
			}
			compare("ListByIndex "+entity+" "+index, func(store contractStore) (any, error) {
				return store.ListByIndex(ctx, "tw", entity, index, lookups)
			})
			reads++
		}
		if masterdata.ProjectionVersion(entity) != "" {
			compare("LoadProjection "+entity, func(store contractStore) (any, error) { return store.LoadProjection(ctx, "tw", entity) })
			reads++
		}
	}
	t.Logf("compared %d reads over %d entities; %d differ", reads, len(entities), mismatches)
}

// loadSourceDirectory reads the JSON files of dir the way the GitHub loader
// does: arrays become compacted raw records, anything else a decoded value.
func loadSourceDirectory(t *testing.T, dir string) map[string]any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no JSON files in %s: %v", dir, err)
	}
	payload := make(map[string]any, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		value, err := splitSourceFile(body)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		payload[filepath.Base(path)] = value
	}
	return payload
}

func splitSourceFile(body []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		var value any
		err := json.Unmarshal(body, &value)
		return value, err
	}
	records := make([]json.RawMessage, 0)
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return nil, err
		}
		records = append(records, append(json.RawMessage(nil), compact.Bytes()...))
	}
	return records, nil
}
