package storage

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func indexTestPayload(gachas ...string) map[string]any {
	records := make([]json.RawMessage, 0, len(gachas))
	for _, gacha := range gachas {
		records = append(records, json.RawMessage(gacha))
	}
	return map[string]any{
		"gachas.json": records,
		"resourceBoxes.json": []json.RawMessage{
			json.RawMessage(`{"id":5,"resourceBoxPurpose":"event_ranking_reward"}`),
			json.RawMessage(`{"id":6,"resourceBoxPurpose":"mission_reward"}`),
			json.RawMessage(`{"id":5,"resourceBoxPurpose":"mission_reward"}`),
		},
		"resourceBoxDetails.json": []json.RawMessage{
			json.RawMessage(`{"resourceBoxId":5,"resourceBoxPurpose":"mission_reward","seq":2,"resourceType":"coin"}`),
			json.RawMessage(`{"resourceBoxId":5,"resourceBoxPurpose":"mission_reward","seq":1,"resourceType":"jewel"}`),
			json.RawMessage(`{"resourceBoxId":5,"resourceBoxPurpose":"event_ranking_reward","seq":1,"resourceType":"honor"}`),
		},
	}
}

func fieldValues(records []map[string]any, field string) []any {
	values := make([]any, 0, len(records))
	for _, record := range records {
		values = append(values, record[field])
	}
	return values
}

func TestListByIndexReadsRecordsBuiltAtSync(t *testing.T) {
	cache := newStoreRegionTestCache(t, startTestMiniRedis(t))
	ctx := context.Background()
	if err := cache.StoreRegion(ctx, "jp", indexTestPayload(
		`{"id":10,"gachaPickups":[{"cardId":1},{"cardId":2}]}`,
		`{"id":11,"gachaPickups":[{"cardId":2}]}`,
		`{"id":12}`,
	)); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	pickups, err := cache.ListByIndex(ctx, "jp", "gachas", "gachaPickups.cardId", [][]any{{2}, {"1"}, {3}})
	if err != nil {
		t.Fatalf("list gachas by pickup card: %v", err)
	}
	if got := [][]any{fieldValues(pickups[0], "id"), fieldValues(pickups[1], "id"), fieldValues(pickups[2], "id")}; !reflect.DeepEqual(got, [][]any{{10.0, 11.0}, {10.0}, {}}) {
		t.Fatalf("expected pickup gachas in stored order per card, got %v", got)
	}

	boxes, err := cache.ListByIndex(ctx, "jp", "resourceboxes", "id", [][]any{{5}})
	if err != nil {
		t.Fatalf("list resource boxes by id: %v", err)
	}
	if got := fieldValues(boxes[0], "resourceBoxPurpose"); !reflect.DeepEqual(got, []any{"event_ranking_reward", "mission_reward"}) {
		t.Fatalf("expected every purpose sharing the id, got %v", got)
	}

	details, err := cache.ListByIndex(ctx, "jp", "resourceboxdetails", "resourceBoxId,resourceBoxPurpose", [][]any{{5, "mission_reward"}})
	if err != nil {
		t.Fatalf("list details by box: %v", err)
	}
	if got := fieldValues(details[0], "resourceType"); !reflect.DeepEqual(got, []any{"coin", "jewel"}) {
		t.Fatalf("expected only the mission box's details, got %v", got)
	}

	if _, err := cache.ListByIndex(ctx, "jp", "gachas", "name", [][]any{{"x"}}); err == nil {
		t.Fatal("expected an undefined index to fail")
	}
}

func TestStoreRegionRewritesIndexesWhenRecordsChange(t *testing.T) {
	cache := newStoreRegionTestCache(t, startTestMiniRedis(t))
	ctx := context.Background()
	if err := cache.StoreRegion(ctx, "jp", indexTestPayload(`{"id":10,"gachaPickups":[{"cardId":1}]}`)); err != nil {
		t.Fatalf("store payload: %v", err)
	}
	if err := cache.StoreRegion(ctx, "jp", indexTestPayload(`{"id":10,"gachaPickups":[{"cardId":2}]}`)); err != nil {
		t.Fatalf("store changed payload: %v", err)
	}

	pickups, err := cache.ListByIndex(ctx, "jp", "gachas", "gachaPickups.cardId", [][]any{{1}, {2}})
	if err != nil {
		t.Fatalf("list gachas by pickup card: %v", err)
	}
	if len(pickups[0]) != 0 || !reflect.DeepEqual(fieldValues(pickups[1], "id"), []any{10.0}) {
		t.Fatalf("expected the index to follow the changed pickup, got %v", pickups)
	}
}

func TestListByIndexScansUntilIndexIsBuiltAndEnsureBuildsIt(t *testing.T) {
	cache := newStoreRegionTestCache(t, startTestMiniRedis(t))
	ctx := context.Background()
	if err := cache.StoreRegion(ctx, "jp", indexTestPayload(`{"id":10,"gachaPickups":[{"cardId":1}]}`)); err != nil {
		t.Fatalf("store payload: %v", err)
	}
	// Data stored before gacha indexes existed has neither index nor version.
	indexKey := cache.redisEntityIndexKey("jp", "gachas", "gachaPickups.cardId")
	if err := cache.client.Del(ctx, indexKey, cache.redisEntityIndexVersionKey("jp", "gachas")).Err(); err != nil {
		t.Fatalf("drop index: %v", err)
	}

	pickups, err := cache.ListByIndex(ctx, "jp", "gachas", "gachaPickups.cardId", [][]any{{1}})
	if err != nil || !reflect.DeepEqual(fieldValues(pickups[0], "id"), []any{10.0}) {
		t.Fatalf("expected a scan to answer before the index exists, got %v, %v", pickups, err)
	}

	rebuilt, err := cache.EnsureEntityIndexes(ctx, "jp")
	if err != nil || !reflect.DeepEqual(rebuilt, []string{"gachas"}) {
		t.Fatalf("expected only gachas rebuilt, got %v, %v", rebuilt, err)
	}
	if exists, _ := cache.client.Exists(ctx, indexKey).Result(); exists != 1 {
		t.Fatal("expected ensure to write the gacha index")
	}
	if rebuilt, _ := cache.EnsureEntityIndexes(ctx, "jp"); len(rebuilt) != 0 {
		t.Fatalf("expected up-to-date indexes to be left alone, got %v", rebuilt)
	}
}

func TestStoreRegionBuildsMissingIndexesForUnchangedSources(t *testing.T) {
	cache := newStoreRegionTestCache(t, startTestMiniRedis(t))
	ctx := context.Background()
	payload := map[string]any{"gachas.json": []json.RawMessage{json.RawMessage(`{"id":10,"gachaPickups":[{"cardId":1}]}`)}}
	digests := map[string]string{"gachas.json": "gachas-digest"}
	if err := cache.StoreRegionWithSourceDigests(ctx, "jp", payload, digests); err != nil {
		t.Fatalf("seed payload: %v", err)
	}
	indexKey := cache.redisEntityIndexKey("jp", "gachas", "gachaPickups.cardId")
	if err := cache.client.Del(ctx, indexKey, cache.redisEntityIndexVersionKey("jp", "gachas")).Err(); err != nil {
		t.Fatalf("drop index: %v", err)
	}

	if err := cache.StoreRegionWithSourceDigests(ctx, "jp", payload, digests); err != nil {
		t.Fatalf("store unchanged payload: %v", err)
	}
	if exists, _ := cache.client.Exists(ctx, indexKey).Result(); exists != 1 {
		t.Fatal("expected an unchanged source to still get its missing index")
	}
}
