package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"sekai-master-api/internal/domain/masterdata"
)

func newPostgresStoreForTest(t *testing.T, leaseName string) (*PostgresMasterDataStore, *DB) {
	t.Helper()
	db := openMigratedTestDB(t)
	return NewPostgresMasterDataStore(db.Pool, 4, leaseName), db
}

func TestPostgresStoreBlocksSplitByCountAndBytes(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "")
	ctx := context.Background()

	records := make([]string, 0, 100)
	for id := 100; id >= 1; id-- {
		body := "small"
		if id > 90 {
			body = strings.Repeat("y", 20000)
		}
		records = append(records, fmt.Sprintf(`{"id":%d,"body":%q}`, id, body))
	}
	if err := store.StoreRegion(ctx, "jp", map[string]any{"cards.json": rawRecords(t, records...)}); err != nil {
		t.Fatalf("store: %v", err)
	}

	rows, err := db.Pool.Query(ctx, `SELECT first_key, body FROM master_blocks WHERE region = 'jp' AND entity = 'cards' ORDER BY first_key`)
	if err != nil {
		t.Fatalf("query blocks: %v", err)
	}
	type block struct {
		FirstKey string
		Body     []byte
	}
	blocks, err := pgx.CollectRows(rows, pgx.RowToStructByPos[block])
	if err != nil {
		t.Fatalf("collect blocks: %v", err)
	}

	var sizes []int
	previousLast := ""
	for _, stored := range blocks {
		entries, err := decodeBlock(stored.Body)
		if err != nil {
			t.Fatalf("decode block: %v", err)
		}
		if len(entries) == 0 || len(entries) > masterBlockMaxRecords {
			t.Fatalf("block %s holds %d records", stored.FirstKey, len(entries))
		}
		if masterdata.BlockSortKey(entries[0].key) != stored.FirstKey {
			t.Fatalf("block first_key %s, first record %s", stored.FirstKey, entries[0].key)
		}
		for _, entry := range entries {
			sortKey := masterdata.BlockSortKey(entry.key)
			if sortKey <= previousLast {
				t.Fatalf("record %s out of order", entry.key)
			}
			previousLast = sortKey
		}
		sizes = append(sizes, len(entries))
	}
	// Blocks close at 32 records, or on the record that brings them to 64 kB
	// of JSON: ids 1-32 and 33-64; 65-94 (91-94 carry 20 kB each); 95-98;
	// 99-100.
	if fmt.Sprint(sizes) != "[32 32 30 4 2]" {
		t.Fatalf("block sizes = %v", sizes)
	}

	var recordCount int
	var orderBody []byte
	if err := db.Pool.QueryRow(ctx, `SELECT record_count, order_keys FROM master_entities WHERE region = 'jp' AND entity = 'cards'`).Scan(&recordCount, &orderBody); err != nil {
		t.Fatalf("read entity: %v", err)
	}
	orderKeys, err := decodeOrderKeys(orderBody)
	if err != nil || recordCount != 100 || orderKeys[0] != "100" || orderKeys[99] != "1" {
		t.Fatalf("order keys = %d keys (%v…), count %d, err %v", len(orderKeys), orderKeys[:2], recordCount, err)
	}
}

func TestPostgresStoreFencesWritesByLeaseToken(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "master-data-sync")
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `
INSERT INTO master_data_sync_leases (name, holder, fencing_token, acquired_at, expires_at, last_heartbeat_at)
VALUES ('master-data-sync', 'holder-b', 5, now(), now() + interval '1 minute', now())`); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	payload := func(prefix string) map[string]any {
		return map[string]any{"cards.json": rawRecords(t, fmt.Sprintf(`{"id":1,"prefix":%q}`, prefix))}
	}
	prefix := func() any {
		record, _, err := store.GetByID(ctx, "jp", "cards", "1")
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return record["prefix"]
	}

	if err := store.StoreRegion(masterdata.WithFencingToken(ctx, 5), "jp", payload("current owner")); err != nil {
		t.Fatalf("current-token write: %v", err)
	}
	if err := store.StoreRegion(masterdata.WithFencingToken(ctx, 4), "jp", payload("stale owner")); !errors.Is(err, masterdata.ErrFencedOut) {
		t.Fatalf("stale-token write error = %v, want ErrFencedOut", err)
	}
	if err := store.StoreRegionVersionPayload(masterdata.WithFencingToken(ctx, 4), "jp", map[string]any{"dataVersion": "stale"}); !errors.Is(err, masterdata.ErrFencedOut) {
		t.Fatalf("stale-token version write error = %v, want ErrFencedOut", err)
	}
	if _, err := store.PruneRegionEntities(masterdata.WithFencingToken(ctx, 4), "jp", []string{"gachas.json"}); !errors.Is(err, masterdata.ErrFencedOut) {
		t.Fatalf("stale-token prune error = %v, want ErrFencedOut", err)
	}
	if got := prefix(); got != "current owner" {
		t.Fatalf("record after fenced writes = %v", got)
	}

	// Unleased writes (no token) pass, as they do for sync status.
	if err := store.StoreRegion(ctx, "jp", payload("unleased")); err != nil {
		t.Fatalf("unleased write: %v", err)
	}
	if got := prefix(); got != "unleased" {
		t.Fatalf("record after unleased write = %v", got)
	}

	// A token for a lease row that does not exist is fenced out.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM master_data_sync_leases`); err != nil {
		t.Fatalf("delete lease: %v", err)
	}
	if err := store.StoreRegion(masterdata.WithFencingToken(ctx, 5), "jp", payload("no lease")); !errors.Is(err, masterdata.ErrFencedOut) {
		t.Fatalf("write without lease row error = %v, want ErrFencedOut", err)
	}
}

func TestPostgresStoreWithoutLeaseNameDoesNotFence(t *testing.T) {
	store, _ := newPostgresStoreForTest(t, "")
	if err := store.StoreRegion(masterdata.WithFencingToken(context.Background(), 9), "jp", map[string]any{"cards.json": rawRecords(t, `{"id":1}`)}); err != nil {
		t.Fatalf("write with fencing disabled: %v", err)
	}
}

func TestPostgresStorePrunesEntitiesTheSourceDropped(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "")
	ctx := context.Background()
	storeContractPayload(t, store)
	if err := store.StoreRegion(ctx, "en", map[string]any{"gachas.json": contractPayload(t)["gachas.json"]}); err != nil {
		t.Fatalf("store en: %v", err)
	}

	removed, err := store.PruneRegionEntities(ctx, "jp", []string{"master/cards.json", "cardEpisodes.json", "resourceBoxes.json", "resourceBoxDetails.json", "musicVocals.json", "versions.json"})
	if err != nil || strings.Join(removed, ",") != "gachas" {
		t.Fatalf("removed = %v %v", removed, err)
	}
	for _, table := range []string{"master_entities", "master_blocks", "master_record_index", "master_projections"} {
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE region = 'jp' AND entity = 'gachas'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s keeps %d gachas rows (%v)", table, count, err)
		}
	}
	if gachas, err := store.ListAll(ctx, "en", "gachas"); err != nil || len(gachas) != 2 {
		t.Fatalf("other region's gachas = %d %v", len(gachas), err)
	}
	if cards, err := store.ListAll(ctx, "jp", "cards"); err != nil || len(cards) != 7 {
		t.Fatalf("kept cards = %d %v", len(cards), err)
	}

	// An empty keep list prunes nothing.
	if removed, err := store.PruneRegionEntities(ctx, "jp", nil); err != nil || len(removed) != 0 {
		t.Fatalf("empty keep removed %v %v", removed, err)
	}
	entities, err := storedEntities(ctx, db, "jp")
	if err != nil || strings.Join(entities, ",") != "cardepisodes,cards,musicvocals,resourceboxdetails,resourceboxes" {
		t.Fatalf("entities = %v %v", entities, err)
	}
}

func storedEntities(ctx context.Context, db *DB, region string) ([]string, error) {
	rows, err := db.Pool.Query(ctx, `SELECT entity FROM master_entities WHERE region = $1`, region)
	if err != nil {
		return nil, err
	}
	entities, err := pgx.CollectRows(rows, pgx.RowTo[string])
	slices.Sort(entities)
	return entities, err
}

func TestPostgresStoreReportsRegionData(t *testing.T) {
	store, _ := newPostgresStoreForTest(t, "")
	ctx := context.Background()
	if has, err := store.HasRegionData(ctx, "jp"); err != nil || has {
		t.Fatalf("empty store has data: %v %v", has, err)
	}
	if err := store.StoreRegion(ctx, "jp", map[string]any{"cards.json": []json.RawMessage{}}); err != nil {
		t.Fatalf("store empty entity: %v", err)
	}
	if has, _ := store.HasRegionData(ctx, "jp"); has {
		t.Fatal("region with only an empty entity has data")
	}
	if err := store.StoreRegion(ctx, "jp", map[string]any{"gachas.json": rawRecords(t, `{"id":1}`)}); err != nil {
		t.Fatalf("store: %v", err)
	}
	if has, err := store.HasRegionData(ctx, "jp"); err != nil || !has {
		t.Fatalf("populated region has no data: %v %v", has, err)
	}
	if has, _ := store.HasRegionData(ctx, "en"); has {
		t.Fatal("other region has data")
	}
}

func TestPostgresStoreRewritesOnlyWhatChanged(t *testing.T) {
	store, db := newPostgresStoreForTest(t, "")
	ctx := context.Background()
	payload := map[string]any{"cardEpisodes.json": rawRecords(t, `{"id":1,"cardId":1}`, `{"id":2,"cardId":1}`)}
	if err := store.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store: %v", err)
	}
	updatedAt := func() time.Time {
		var updated time.Time
		if err := db.Pool.QueryRow(ctx, `SELECT updated_at FROM master_entities WHERE region = 'jp' AND entity = 'cardepisodes'`).Scan(&updated); err != nil {
			t.Fatalf("read entity: %v", err)
		}
		return updated
	}
	var blockBefore []byte
	if err := db.Pool.QueryRow(ctx, `SELECT xmin::text::bytea FROM master_blocks WHERE region = 'jp' AND entity = 'cardepisodes'`).Scan(&blockBefore); err != nil {
		t.Fatalf("read block: %v", err)
	}
	firstUpdate := updatedAt()

	// The same records again: the blocks are left alone.
	if err := store.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store again: %v", err)
	}
	var blockAfter []byte
	if err := db.Pool.QueryRow(ctx, `SELECT xmin::text::bytea FROM master_blocks WHERE region = 'jp' AND entity = 'cardepisodes'`).Scan(&blockAfter); err != nil {
		t.Fatalf("read block: %v", err)
	}
	if string(blockBefore) != string(blockAfter) {
		t.Fatal("unchanged records rewrote their block")
	}
	if secondUpdate := updatedAt(); !secondUpdate.After(firstUpdate) {
		t.Fatal("entity row not touched by the second store")
	}
}

func TestBlockSortKeySQLFunctionMatchesGo(t *testing.T) {
	db := openMigratedTestDB(t)
	keys := []string{
		"0", "7", "10", "007", "-1", "1.5", "", "18446744073709551615", "99999999999999999999",
		"100000000000000000000", "auto:ab", "composite:v1:13:resourceboxes2:id1:5", "日本", "1a", "a1", " 1",
	}
	for _, key := range keys {
		var got string
		if err := db.Pool.QueryRow(context.Background(), `SELECT master_block_sort_key($1)`, key).Scan(&got); err != nil {
			t.Fatalf("sort key %q: %v", key, err)
		}
		if want := masterdata.BlockSortKey(key); got != want {
			t.Errorf("master_block_sort_key(%q) = %q, Go gives %q", key, got, want)
		}
	}
}

func TestBlockCodecRoundTrip(t *testing.T) {
	keys := []string{"2", "auto:x", "10", "2", "composite:v1:1:a"}
	records := [][]byte{[]byte(`{"id":2,"v":"old"}`), []byte(`{"n":1}`), []byte(`{"id":10}`), []byte(`{"id":2,"v":"new"}`), []byte(`{"a":"<&>"}`)}
	blocks, err := buildBlocks(blockEntries(keys, records))
	if err != nil || len(blocks) != 1 {
		t.Fatalf("blocks = %d %v", len(blocks), err)
	}
	entries, err := decodeBlock(blocks[0].body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, fmt.Sprintf("%s%v%s", entry.key, entry.positions, entry.record))
	}
	want := `2[0 3]{"id":2,"v":"new"}|10[2]{"id":10}|auto:x[1]{"n":1}|composite:v1:1:a[4]{"a":"<&>"}`
	if strings.Join(got, "|") != want {
		t.Fatalf("entries = %s", strings.Join(got, "|"))
	}
	if blocks[0].firstKey != masterdata.BlockSortKey("2") {
		t.Fatalf("first key = %s", blocks[0].firstKey)
	}
}
