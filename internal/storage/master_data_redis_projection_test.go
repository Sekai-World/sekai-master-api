package storage

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func gachaPayload(gachas ...string) map[string]any {
	records := make([]json.RawMessage, 0, len(gachas))
	for _, gacha := range gachas {
		records = append(records, json.RawMessage(gacha))
	}
	return map[string]any{"gachas.json": records}
}

func projectionNames(t *testing.T, cache *RedisMasterDataCache, region string) []any {
	t.Helper()
	projection, err := cache.LoadProjection(context.Background(), region, "gachas")
	if err != nil {
		t.Fatalf("load projection: %v", err)
	}
	names := make([]any, 0, projection.Len())
	for row := range projection.Len() {
		name, _ := projection.Value("name", row)
		names = append(names, name)
	}
	return names
}

func TestLoadProjectionReadsTheProjectionBuiltAtSync(t *testing.T) {
	cache := newStoreRegionTestCache(t, startTestMiniRedis(t))
	ctx := context.Background()
	if err := cache.StoreRegion(ctx, "jp", gachaPayload(
		`{"id":2,"name":"second","startAt":2000,"gachaDetails":[{"cardId":1}]}`,
		`{"id":1,"name":"first","startAt":1000}`,
	)); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	projection, err := cache.LoadProjection(ctx, "jp", "gachas")
	if err != nil {
		t.Fatalf("load projection: %v", err)
	}
	if got := projection.Rows(); !reflect.DeepEqual(got, []map[string]any{
		{"id": 2.0, "name": "second", "startAt": 2000.0},
		{"id": 1.0, "name": "first", "startAt": 1000.0},
	}) {
		t.Fatalf("expected the projected fields in stored order, got %v", got)
	}
	if !reflect.DeepEqual(projection.Keys, []string{"2", "1"}) {
		t.Fatalf("expected storage keys for batched reads, got %v", projection.Keys)
	}

	if err := cache.StoreRegion(ctx, "jp", gachaPayload(`{"id":1,"name":"renamed","startAt":1000}`)); err != nil {
		t.Fatalf("store changed payload: %v", err)
	}
	if got := projectionNames(t, cache, "jp"); !reflect.DeepEqual(got, []any{"renamed"}) {
		t.Fatalf("expected the projection to follow the changed records, got %v", got)
	}
	if _, err := cache.LoadProjection(ctx, "jp", "musics"); err == nil {
		t.Fatal("expected an entity without a projection to fail")
	}
}

func TestLoadProjectionBuildsFromRecordsUntilSyncWritesIt(t *testing.T) {
	cache := newStoreRegionTestCache(t, startTestMiniRedis(t))
	ctx := context.Background()
	if err := cache.StoreRegion(ctx, "jp", gachaPayload(`{"id":1,"name":"first"}`)); err != nil {
		t.Fatalf("store payload: %v", err)
	}
	// Data stored before projections existed has neither projection nor version.
	projectionKey := cache.redisEntityProjectionKey("jp", "gachas")
	if err := cache.client.Del(ctx, projectionKey, cache.redisEntityProjectionVersionKey("jp", "gachas")).Err(); err != nil {
		t.Fatalf("drop projection: %v", err)
	}

	if got := projectionNames(t, cache, "jp"); !reflect.DeepEqual(got, []any{"first"}) {
		t.Fatalf("expected the records to answer before the projection exists, got %v", got)
	}

	rebuilt, err := cache.EnsureDerivedEntityData(ctx, "jp")
	if err != nil || !reflect.DeepEqual(rebuilt, []string{"gachas"}) {
		t.Fatalf("expected only gachas rebuilt, got %v, %v", rebuilt, err)
	}
	if exists, _ := cache.client.Exists(ctx, projectionKey).Result(); exists != 1 {
		t.Fatal("expected ensure to write the gacha projection")
	}
}

func TestLoadProjectionIsEmptyForAnEntityTheRegionLacks(t *testing.T) {
	cache := newStoreRegionTestCache(t, startTestMiniRedis(t))
	core, warnings := observer.New(zap.WarnLevel)
	defer zap.ReplaceGlobals(zap.New(core))()

	projection, err := cache.LoadProjection(context.Background(), "jp", "costume3dgroups")
	if err != nil || projection.Len() != 0 {
		t.Fatalf("expected an empty projection, got %v, %v", projection, err)
	}
	if warnings.Len() != 0 {
		t.Fatalf("expected no warning for an entity the region lacks, got %v", warnings.All())
	}
}

func TestStoreRegionBuildsMissingProjectionsForUnchangedSources(t *testing.T) {
	cache := newStoreRegionTestCache(t, startTestMiniRedis(t))
	ctx := context.Background()
	payload := gachaPayload(`{"id":1,"name":"first"}`)
	digests := map[string]string{"gachas.json": "gachas-digest"}
	if err := cache.StoreRegionWithSourceDigests(ctx, "jp", payload, digests); err != nil {
		t.Fatalf("seed payload: %v", err)
	}
	projectionKey := cache.redisEntityProjectionKey("jp", "gachas")
	if err := cache.client.Del(ctx, projectionKey, cache.redisEntityProjectionVersionKey("jp", "gachas")).Err(); err != nil {
		t.Fatalf("drop projection: %v", err)
	}

	if err := cache.StoreRegionWithSourceDigests(ctx, "jp", payload, digests); err != nil {
		t.Fatalf("store unchanged payload: %v", err)
	}
	if exists, _ := cache.client.Exists(ctx, projectionKey).Result(); exists != 1 {
		t.Fatal("expected an unchanged source to still get its missing projection")
	}
}
