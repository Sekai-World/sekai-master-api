package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/klauspost/compress/zstd"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/domain/masterdata"
)

var redisSearchIndexBenchmarkSink *RedisMasterDataCache

func TestRedisEntityRecordCompressedRoundTrip(t *testing.T) {
	body := []byte(`{"id":1,"name":"compressed"}`)
	stored, err := marshalRedisEntityRecord(body)
	if err != nil {
		t.Fatalf("marshal compressed record: %v", err)
	}
	if !strings.HasPrefix(stored, redisEntityRecordZstdV1Prefix) {
		t.Fatalf("expected versioned compressed prefix, got %q", stored)
	}

	record, err := unmarshalRedisEntityRecord(stored, "jp", "cards", "1")
	if err != nil {
		t.Fatalf("unmarshal compressed record: %v", err)
	}
	if record["name"] != "compressed" {
		t.Fatalf("expected compressed record name, got %v", record["name"])
	}
}

func TestRedisEntityRecordCompressesRepetitiveMasterData(t *testing.T) {
	body := []byte(`{
			"id":100001,
			"name":"A recurring event title",
			"assetbundleName":"event_story_recurring_event_title",
			"bannerAssetbundleName":"event_story_recurring_event_title_banner",
			"logoAssetbundleName":"event_story_recurring_event_title_logo",
			"bgmAssetbundleName":"event_story_recurring_event_title_bgm",
			"eventStoryUnit":"recurring_event_title",
			"eventStoryUnitName":"Recurring Event Title",
			"description":"Recurring Event Title brings the recurring event title story to the recurring event title unit.",
			"summary":"Recurring Event Title brings the recurring event title story to the recurring event title unit.",
			"notice":"Recurring Event Title brings the recurring event title story to the recurring event title unit."
		}`)

	stored, err := marshalRedisEntityRecord(body)
	if err != nil {
		t.Fatalf("marshal repetitive record: %v", err)
	}
	if len(stored) >= len(body)*85/100 {
		t.Fatalf("expected compressed stored record (%d bytes including prefix) to be at least 15%% smaller than JSON body (%d bytes)", len(stored), len(body))
	}
}

func TestRedisEntityRecordSupportsLegacyPlainJSON(t *testing.T) {
	record, err := unmarshalRedisEntityRecord(`{"id":2,"name":"legacy"}`, "jp", "cards", "2")
	if err != nil {
		t.Fatalf("unmarshal legacy record: %v", err)
	}
	if record["name"] != "legacy" {
		t.Fatalf("expected legacy record name, got %v", record["name"])
	}
}

func TestRedisEntityRecordRejectsMalformedCompressedPayload(t *testing.T) {
	_, err := unmarshalRedisEntityRecord(redisEntityRecordZstdV1Prefix+"not-zstd", "jp", "cards", "3")
	if err == nil || !strings.Contains(err.Error(), "decode compressed record region jp entity cards id 3") {
		t.Fatalf("expected useful malformed compressed payload error, got %v", err)
	}
}

func TestRedisEntityRecordRejectsOversizedDecompressedPayload(t *testing.T) {
	stored, err := marshalRedisEntityRecord([]byte(strings.Repeat("x", maxRedisEntityRecordBodySize+1)))
	if err != nil {
		t.Fatalf("marshal oversized record: %v", err)
	}

	_, err = redisEntityRecordBody(stored, "jp", "cards", "4")
	if err == nil || !strings.Contains(err.Error(), "compressed record exceeds 67108864 byte limit region jp entity cards id 4") {
		t.Fatalf("expected contextual oversized record error, got %v", err)
	}
}

func TestRedisEntityRecordEncodingMatchesDefaultEncoder(t *testing.T) {
	// Stored bodies feed the entity revision digest, so the pooled encoder's
	// settings must not change the bytes it produces.
	defaultEncoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("new default encoder: %v", err)
	}
	defer defaultEncoder.Close()

	bodies := [][]byte{
		[]byte(`{"id":1,"name":"alpha"}`),
		[]byte(strings.Repeat(`{"resourceType":"jewel","resourceQuantity":100},`, 2000)),
	}
	for _, body := range bodies {
		stored, err := marshalRedisEntityRecord(body)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		want := redisEntityRecordZstdV1Prefix + string(defaultEncoder.EncodeAll(body, nil))
		if stored != want {
			t.Fatalf("expected pooled encoder output to match the default encoder for a %d-byte body", len(body))
		}
	}
}

func TestRedisEntityRecordDecodesConcurrently(t *testing.T) {
	const workers = 8
	stored := make([]string, workers)
	for index := range stored {
		body, err := marshalRedisEntityRecord([]byte(fmt.Sprintf(`{"id":%d,"name":"%s"}`, index, strings.Repeat("x", index*100))))
		if err != nil {
			t.Fatalf("marshal record %d: %v", index, err)
		}
		stored[index] = body
	}

	errs := make(chan error, workers)
	for index := range stored {
		go func() {
			for range 50 {
				record, err := unmarshalRedisEntityRecord(stored[index], "jp", "cards", fmt.Sprint(index))
				if err != nil {
					errs <- err
					return
				}
				if record["name"] != strings.Repeat("x", index*100) {
					errs <- fmt.Errorf("record %d decoded to %v", index, record["name"])
					return
				}
			}
			errs <- nil
		}()
	}
	for range workers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildRawRecordsSearchIndexMatchesDecodedRecords(t *testing.T) {
	// One searchable field per record keeps the text blob order deterministic.
	rawRecords := []json.RawMessage{
		json.RawMessage(`{"id":1,"name":"Alpha"}`),
		nil,
		json.RawMessage(`{}`),
		json.RawMessage(`not-json`),
		json.RawMessage(`{"name":"No ID"}`),
		json.RawMessage(`{"id":2,"name":"Alpha"}`),
		json.RawMessage(`{"id":1,"name":"Duplicate"}`),
		json.RawMessage(`{"id":3,"assetbundleName":"not searchable"}`),
	}
	decodedRecords := []map[string]any{
		{"id": float64(1), "name": "Alpha"},
		{"name": "No ID"},
		{"id": float64(2), "name": "Alpha"},
		{"id": float64(1), "name": "Duplicate"},
		{"id": float64(3), "assetbundleName": "not searchable"},
	}

	got := buildRawRecordsSearchIndex("skills", rawRecords)
	want := buildEntitySearchIndex("skills", decodedRecords)
	if want == nil || !equalEntitySearchIndex(got, want) {
		t.Fatalf("expected streamed index %+v to match decoded index %+v", got, want)
	}

	if index := buildRawRecordsSearchIndex("resourceBoxes", rawRecords); index != nil {
		t.Fatalf("expected no search index for composite-key entity, got %+v", index)
	}
	if index := buildRawRecordsSearchIndex("skills", []json.RawMessage{json.RawMessage(`{"id":1}`)}); index != nil {
		t.Fatalf("expected no search index without searchable fields, got %+v", index)
	}
}

func TestStoreRegionIncrementalUpdate(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                         miniRedis.Addr(),
		RedisDB:                           0,
		MasterDataRedisKeyPrefix:          "test:master-data:",
		MasterDataSearchIndexCacheEntries: 0,
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()

	initialPayload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
			map[string]any{"id": 2, "prefix": "beta"},
		},
		"skills.json": []any{
			map[string]any{"id": 100, "name": "focus"},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", initialPayload); err != nil {
		t.Fatalf("store initial payload: %v", err)
	}

	persisted, err := cache.client.HGet(ctx, cache.redisEntityKey("jp", "cards"), "1").Result()
	if err != nil {
		t.Fatalf("read persisted card: %v", err)
	}
	if !strings.HasPrefix(persisted, redisEntityRecordZstdV1Prefix) {
		t.Fatalf("expected StoreRegion to persist a versioned compressed record, got %q", persisted)
	}

	updatedPayload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha-updated"},
			map[string]any{"id": 3, "prefix": "gamma"},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", updatedPayload); err != nil {
		t.Fatalf("store updated payload: %v", err)
	}

	cardOne, found, err := cache.GetByID(ctx, "jp", "cards", "1")
	if err != nil {
		t.Fatalf("get card 1: %v", err)
	}
	if !found {
		t.Fatalf("expected card id=1 to exist")
	}
	if cardOne["prefix"] != "alpha-updated" {
		t.Fatalf("expected card id=1 prefix alpha-updated, got %v", cardOne["prefix"])
	}

	_, found, err = cache.GetByID(ctx, "jp", "cards", "2")
	if err != nil {
		t.Fatalf("get card 2: %v", err)
	}
	if found {
		t.Fatalf("expected card id=2 to be removed after incremental update")
	}

	_, found, err = cache.GetByID(ctx, "jp", "cards", "3")
	if err != nil {
		t.Fatalf("get card 3: %v", err)
	}
	if !found {
		t.Fatalf("expected card id=3 to exist")
	}

	cards, total, err := cache.ListByPage(ctx, "jp", "cards", 1, 10)
	if err != nil {
		t.Fatalf("list cards page: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected cards total=2, got %d", total)
	}
	if len(cards) != 2 {
		t.Fatalf("expected cards len=2, got %d", len(cards))
	}
	if cards[0]["id"] != float64(1) {
		t.Fatalf("expected first card id=1, got %v", cards[0]["id"])
	}
	if cards[1]["id"] != float64(3) {
		t.Fatalf("expected second card id=3, got %v", cards[1]["id"])
	}

	allCards, err := cache.ListAll(ctx, "jp", "cards")
	if err != nil {
		t.Fatalf("list all cards: %v", err)
	}
	if len(allCards) != 2 {
		t.Fatalf("expected list all cards len=2, got %d", len(allCards))
	}
	if allCards[0]["id"] != float64(1) {
		t.Fatalf("expected list all first card id=1, got %v", allCards[0]["id"])
	}
	if allCards[1]["id"] != float64(3) {
		t.Fatalf("expected list all second card id=3, got %v", allCards[1]["id"])
	}

	skillMatches, err := cache.Search(ctx, "jp", "skills", "focus", []string{"name"}, 10)
	if err != nil {
		t.Fatalf("search skills: %v", err)
	}
	if len(skillMatches) != 1 {
		t.Fatalf("expected untouched skills entity to stay searchable, got %d matches", len(skillMatches))
	}
}

func startTestMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(miniRedis.Close)
	return miniRedis
}

func newStoreRegionTestCache(t *testing.T, miniRedis *miniredis.Miniredis) *RedisMasterDataCache {
	t.Helper()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := cache.Close(); closeErr != nil {
			t.Errorf("close redis cache: %v", closeErr)
		}
	})
	return cache
}

func TestNewRedisMasterDataCacheWiresRedisTimeouts(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cfg := config.Config{
		RedisAddr:         miniRedis.Addr(),
		RedisDialTimeout:  11 * time.Second,
		RedisReadTimeout:  22 * time.Second,
		RedisWriteTimeout: 33 * time.Second,
		RedisPoolTimeout:  44 * time.Second,
	}

	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := cache.Close(); closeErr != nil {
			t.Errorf("close redis cache: %v", closeErr)
		}
	})

	options := cache.client.Options()
	if options.DialTimeout != cfg.RedisDialTimeout || options.ReadTimeout != cfg.RedisReadTimeout || options.WriteTimeout != cfg.RedisWriteTimeout || options.PoolTimeout != cfg.RedisPoolTimeout {
		t.Fatalf("unexpected Redis client timeouts: dial=%s read=%s write=%s pool=%s", options.DialTimeout, options.ReadTimeout, options.WriteTimeout, options.PoolTimeout)
	}
}

func TestStoreRegionPersistsCompactRawJSONRecords(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)

	ctx := context.Background()
	payload := map[string]any{
		"cards.json": []json.RawMessage{
			json.RawMessage(`{"id":1,"prefix":"alpha"}`),
			json.RawMessage(`{"id":2,"prefix":"beta","name":"beta card"}`),
			json.RawMessage(`{"prefix":"no-id"}`),
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store raw payload: %v", err)
	}

	autoSum := sha256.Sum256([]byte(`{"prefix":"no-id"}`))
	autoID := "auto:" + hex.EncodeToString(autoSum[:])

	fields, err := cache.client.HGetAll(ctx, cache.redisEntityKey("jp", "cards")).Result()
	if err != nil {
		t.Fatalf("read persisted cards: %v", err)
	}
	if len(fields) != 3 {
		t.Fatalf("expected 3 persisted records, got %d", len(fields))
	}

	cardOne, found, err := cache.GetByID(ctx, "jp", "cards", "1")
	if err != nil || !found || cardOne["prefix"] != "alpha" {
		t.Fatalf("expected card id=1 prefix alpha, got found=%v value=%v err=%v", found, cardOne, err)
	}

	noID, found, err := cache.GetByID(ctx, "jp", "cards", autoID)
	if err != nil || !found || noID["prefix"] != "no-id" {
		t.Fatalf("expected auto-id card prefix no-id, got found=%v value=%v err=%v", found, noID, err)
	}

	cards, total, err := cache.ListByPage(ctx, "jp", "cards", 1, 10)
	if err != nil || total != 3 || len(cards) != 3 {
		t.Fatalf("expected 3 cards, got total=%d len=%d err=%v", total, len(cards), err)
	}
	if cards[0]["id"] != float64(1) || cards[1]["id"] != float64(2) {
		t.Fatalf("unexpected card order: %v %v", cards[0]["id"], cards[1]["id"])
	}
}

// IDs of a million or more (bondsHonors, bondsHonorWords) used to be stored
// under their float exponent form ("1.010201e+06"), so lookups by the decimal
// ID found nothing.
func TestStoreRegionKeysLargeIntegerIDsInDecimal(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)

	ctx := context.Background()
	payload := map[string]any{
		"bondsHonors.json": []json.RawMessage{
			json.RawMessage(`{"id":1010201,"name":"bonds low"}`),
			json.RawMessage(`{"id":12122010,"name":"bonds high"}`),
		},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	fields, err := cache.client.HKeys(ctx, cache.redisEntityKey("jp", "bondsHonors")).Result()
	if err != nil {
		t.Fatalf("read persisted keys: %v", err)
	}
	sort.Strings(fields)
	if strings.Join(fields, ",") != "1010201,12122010" {
		t.Fatalf("expected decimal hash fields, got %v", fields)
	}

	for id, name := range map[string]string{"1010201": "bonds low", "12122010": "bonds high"} {
		record, found, err := cache.GetByID(ctx, "jp", "bondsHonors", id)
		if err != nil || !found || record["name"] != name {
			t.Fatalf("expected id=%s name %q, got found=%v value=%v err=%v", id, name, found, record, err)
		}
	}

	matches, err := cache.Search(ctx, "jp", "bondsHonors", "bonds high", []string{"name"}, 10)
	if err != nil || len(matches) != 1 || matches[0].Item["id"] != float64(12122010) {
		t.Fatalf("expected search to resolve id 12122010, got %v err=%v", matches, err)
	}
}

// Redis written before decimal ids holds exponent keys under a source digest
// that still matches, so the layout version must force one rewrite.
func TestStoreRegionRewritesExponentKeysForUnchangedSources(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)

	ctx := context.Background()
	body := `{"id":1010201,"name":"bonds low"}`
	payload := map[string]any{"bondsHonors.json": []json.RawMessage{json.RawMessage(body)}}
	fileDigests := map[string]string{"bondsHonors.json": "bonds-digest"}

	if err := cache.StoreRegionWithSourceDigests(ctx, "jp", payload, fileDigests); err != nil {
		t.Fatalf("seed payload: %v", err)
	}
	// Roll the stored state back to the old layout, keeping the search index.
	entityKey := cache.redisEntityKey("jp", "bondsHonors")
	orderKey := cache.redisEntityOrderKey("jp", "bondsHonors")
	pipe := cache.client.TxPipeline()
	pipe.Del(ctx, entityKey, orderKey)
	pipe.HSet(ctx, entityKey, "1.010201e+06", body)
	pipe.RPush(ctx, orderKey, "1.010201e+06")
	pipe.Set(ctx, cache.redisEntityRevisionKey("jp", "bondsHonors"), "legacy-revision", 0)
	pipe.Set(ctx, cache.redisEntitySourceDigestKey("jp", "bondsHonors"), "bonds-digest", 0)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed legacy layout: %v", err)
	}

	if err := cache.StoreRegionWithSourceDigests(ctx, "jp", payload, fileDigests); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	fields, err := cache.client.HKeys(ctx, entityKey).Result()
	if err != nil || strings.Join(fields, ",") != "1010201" {
		t.Fatalf("expected only the decimal field, got %v err=%v", fields, err)
	}
	order, err := cache.client.LRange(ctx, orderKey, 0, -1).Result()
	if err != nil || strings.Join(order, ",") != "1010201" {
		t.Fatalf("expected decimal order, got %v err=%v", order, err)
	}
	if record, found, err := cache.GetByID(ctx, "jp", "bondsHonors", "1010201"); err != nil || !found || record["name"] != "bonds low" {
		t.Fatalf("expected id=1010201 after rewrite, got found=%v value=%v err=%v", found, record, err)
	}
}

func TestStoreRegionUsesCompositeKeysForResourceBoxesAndDetails(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)
	ctx := context.Background()

	firstBox := json.RawMessage(`{"id":7001,"resourceBoxPurpose":"event_ranking_reward","resourceBoxType":"event","details":[]}`)
	secondBox := json.RawMessage(`{"id":7001,"resourceBoxPurpose":"virtual_live_reward","resourceBoxType":"live","details":[]}`)
	firstDetail := json.RawMessage(`{"id":11,"resourceBoxId":7001,"resourceBoxPurpose":"event_ranking_reward","seq":1,"resourceType":"jewel"}`)
	secondDetail := json.RawMessage(`{"id":11,"resourceBoxId":7001,"resourceBoxPurpose":"event_ranking_reward","seq":2,"resourceType":"coin"}`)
	payload := map[string]any{
		"resourceboxes.json":      []json.RawMessage{firstBox, secondBox},
		"resourceboxdetails.json": []json.RawMessage{firstDetail, secondDetail},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store composite-key payload: %v", err)
	}

	assertCompositeResourceBoxList(t, ctx, cache)
	assertCompositeResourceBoxStorage(t, ctx, cache, [2]json.RawMessage{firstBox, secondBox})
	assertCompositeResourceBoxDetailList(t, ctx, cache)
	assertCompositeResourceBoxDetailStorage(t, ctx, cache, [2]json.RawMessage{firstDetail, secondDetail})
	assertCompositeResourceBoxDetailPage(t, ctx, cache)
	assertCompositeBareIDLookupsUnavailable(t, ctx, cache)
}

func TestStoreRegionUsesCompositeKeysForCharacterMissionV2ParameterGroups(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)
	ctx := context.Background()
	entity := "charactermissionv2parametergroups"
	first := json.RawMessage(`{"id":42,"seq":1,"requirement":1000}`)
	second := json.RawMessage(`{"id":42,"seq":2,"requirement":2000}`)

	if !usesCompositeStorageKey("characterMissionV2ParameterGroups") {
		t.Fatal("expected character mission V2 parameter groups to use composite storage keys")
	}
	fields := compositeStorageKeyFields("characterMissionV2ParameterGroups")
	if len(fields) != 2 || fields[0] != "id" || fields[1] != "seq" {
		t.Fatalf("expected character mission V2 parameter group key fields [id seq], got %v", fields)
	}

	firstKey, ok := compositeRecordStorageKeyFromRaw("characterMissionV2ParameterGroups", first)
	if !ok {
		t.Fatal("expected first character mission V2 parameter group to have a composite storage key")
	}
	secondKey, ok := compositeRecordStorageKeyFromRaw("characterMissionV2ParameterGroups", second)
	if !ok {
		t.Fatal("expected second character mission V2 parameter group to have a composite storage key")
	}
	expectedFirstKey := encodeCompositeStorageKey(entity, []string{"id", "seq"}, []string{"42", "1"})
	if firstKey != expectedFirstKey {
		t.Fatalf("expected composite key %q, got %q", expectedFirstKey, firstKey)
	}
	if firstKey == secondKey {
		t.Fatalf("expected different sequences to produce distinct composite keys, got %q", firstKey)
	}

	payload := map[string]any{
		"characterMissionV2ParameterGroups.json": []json.RawMessage{first, second},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store character mission V2 parameter groups: %v", err)
	}

	groups, err := cache.ListAll(ctx, "jp", entity)
	if err != nil {
		t.Fatalf("list character mission V2 parameter groups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected both character mission V2 parameter group levels, got %v", groups)
	}
	if groups[0]["id"] != float64(42) || groups[0]["seq"] != float64(1) || groups[0]["requirement"] != float64(1000) {
		t.Fatalf("expected first parameter group to preserve original fields, got %v", groups[0])
	}
	if groups[1]["id"] != float64(42) || groups[1]["seq"] != float64(2) || groups[1]["requirement"] != float64(2000) {
		t.Fatalf("expected second parameter group to preserve original fields, got %v", groups[1])
	}

	stored, err := cache.client.HGetAll(ctx, cache.redisEntityKey("jp", entity)).Result()
	if err != nil {
		t.Fatalf("read character mission V2 parameter group hash: %v", err)
	}
	if len(stored) != 2 || stored[firstKey] == "" || stored[secondKey] == "" {
		t.Fatalf("expected distinct composite parameter group hash fields, got %v", stored)
	}
}

func assertCompositeResourceBoxList(t *testing.T, ctx context.Context, cache *RedisMasterDataCache) {
	t.Helper()

	boxes, err := cache.ListAll(ctx, "jp", "resourceboxes")
	if err != nil {
		t.Fatalf("list resourceboxes: %v", err)
	}
	if len(boxes) != 2 {
		t.Fatalf("expected both same-id resourceboxes, got %d", len(boxes))
	}
	if boxes[0]["resourceBoxPurpose"] != "event_ranking_reward" || boxes[1]["resourceBoxPurpose"] != "virtual_live_reward" {
		t.Fatalf("resourceboxes did not preserve source order and purpose: %v", boxes)
	}
	if boxes[0]["id"] != float64(7001) || boxes[1]["id"] != float64(7001) {
		t.Fatalf("expected original resourcebox IDs to remain in the bodies: %v", boxes)
	}
}

func assertCompositeResourceBoxStorage(
	t *testing.T,
	ctx context.Context,
	cache *RedisMasterDataCache,
	boxes [2]json.RawMessage,
) {
	t.Helper()

	boxKeyOne, ok := compositeRecordStorageKeyFromRaw("resourceboxes", boxes[0])
	if !ok {
		t.Fatal("expected first resourcebox to have a composite storage key")
	}
	boxKeyTwo, ok := compositeRecordStorageKeyFromRaw("resourceboxes", boxes[1])
	if !ok {
		t.Fatal("expected second resourcebox to have a composite storage key")
	}
	boxFields, err := cache.client.HGetAll(ctx, cache.redisEntityKey("jp", "resourceboxes")).Result()
	if err != nil {
		t.Fatalf("read resourcebox hash: %v", err)
	}
	if len(boxFields) != 2 || boxFields[boxKeyOne] == "" || boxFields[boxKeyTwo] == "" {
		t.Fatalf("expected distinct composite resourcebox hash fields, got %v", boxFields)
	}
	boxOrder, err := cache.client.LRange(ctx, cache.redisEntityOrderKey("jp", "resourceboxes"), 0, -1).Result()
	if err != nil {
		t.Fatalf("read resourcebox order: %v", err)
	}
	if len(boxOrder) != 2 || boxOrder[0] != boxKeyOne || boxOrder[1] != boxKeyTwo {
		t.Fatalf("expected composite keys in source order, got %v", boxOrder)
	}
}

func assertCompositeResourceBoxDetailList(t *testing.T, ctx context.Context, cache *RedisMasterDataCache) {
	t.Helper()

	details, err := cache.ListAll(ctx, "jp", "resourceboxdetails")
	if err != nil {
		t.Fatalf("list resourceboxdetails: %v", err)
	}
	if len(details) != 2 || details[0]["seq"] != float64(1) || details[1]["seq"] != float64(2) {
		t.Fatalf("expected both detail sequences in source order, got %v", details)
	}
	if details[0]["id"] != float64(11) || details[1]["id"] != float64(11) || details[0]["resourceBoxId"] != float64(7001) {
		t.Fatalf("expected original detail IDs and parent relation in bodies, got %v", details)
	}
}

func assertCompositeResourceBoxDetailStorage(
	t *testing.T,
	ctx context.Context,
	cache *RedisMasterDataCache,
	details [2]json.RawMessage,
) {
	t.Helper()

	detailKeyOne, ok := compositeRecordStorageKeyFromRaw("resourceboxdetails", details[0])
	if !ok {
		t.Fatal("expected first detail to have a composite storage key")
	}
	detailKeyTwo, ok := compositeRecordStorageKeyFromRaw("resourceboxdetails", details[1])
	if !ok {
		t.Fatal("expected second detail to have a composite storage key")
	}
	detailFields, err := cache.client.HGetAll(ctx, cache.redisEntityKey("jp", "resourceboxdetails")).Result()
	if err != nil {
		t.Fatalf("read resourceboxdetail hash: %v", err)
	}
	if len(detailFields) != 2 || detailFields[detailKeyOne] == "" || detailFields[detailKeyTwo] == "" {
		t.Fatalf("expected distinct composite detail hash fields, got %v", detailFields)
	}
}

func assertCompositeResourceBoxDetailPage(t *testing.T, ctx context.Context, cache *RedisMasterDataCache) {
	t.Helper()

	page, total, err := cache.ListByPage(ctx, "jp", "resourceboxdetails", 1, 10)
	if err != nil {
		t.Fatalf("list resourceboxdetails page: %v", err)
	}
	if total != 2 || len(page) != 2 || page[0]["seq"] != float64(1) || page[1]["seq"] != float64(2) {
		t.Fatalf("expected paginated composite details in order, total=%d items=%v", total, page)
	}
}

func assertCompositeBareIDLookupsUnavailable(t *testing.T, ctx context.Context, cache *RedisMasterDataCache) {
	t.Helper()

	for _, lookup := range []struct {
		entity string
		id     string
	}{{"resourceboxes", "7001"}, {"resourceboxdetails", "11"}} {
		if _, found, err := cache.GetByID(ctx, "jp", lookup.entity, lookup.id); err != nil || found {
			t.Fatalf("expected bare GetByID lookup to be unsupported for %s, found=%v err=%v", lookup.entity, found, err)
		}
	}
}

func TestStoreRegionCompositeFallbackKeysPreserveIncompleteRecords(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)
	ctx := context.Background()

	missingPurpose := map[string]any{"id": 8001, "resourceBoxType": "material"}
	payload := map[string]any{
		"resourceboxes.json": []any{
			missingPurpose,
			map[string]any{"id": 8001, "resourceBoxType": "material"},
			map[string]any{"resourceBoxPurpose": "virtual_live_reward", "resourceBoxType": "material"},
		},
		"resourceboxdetails.json": []any{
			map[string]any{"id": 81, "resourceBoxId": 8001, "resourceBoxPurpose": "event_ranking_reward", "resourceType": "jewel"},
			map[string]any{"id": 81, "resourceBoxId": 8001, "resourceBoxPurpose": "event_ranking_reward", "resourceType": "jewel"},
			map[string]any{"resourceBoxPurpose": "event_ranking_reward", "seq": 3, "resourceType": "coin"},
			map[string]any{"resourceBoxId": 8001, "seq": 4, "resourceType": "coin"},
		},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store incomplete composite records: %v", err)
	}

	boxes, err := cache.ListAll(ctx, "jp", "resourceboxes")
	if err != nil {
		t.Fatalf("list incomplete resourceboxes: %v", err)
	}
	if len(boxes) != 3 || boxes[0]["id"] != float64(8001) {
		t.Fatalf("expected all incomplete resourceboxes with original fields, got %v", boxes)
	}
	if _, exists := boxes[0]["resourceBoxPurpose"]; exists {
		t.Fatalf("fallback storage key must not add a purpose to the record body: %v", boxes[0])
	}
	boxFields, err := cache.client.HGetAll(ctx, cache.redisEntityKey("jp", "resourceboxes")).Result()
	if err != nil {
		t.Fatalf("read incomplete resourcebox hash: %v", err)
	}
	if len(boxFields) != 3 {
		t.Fatalf("expected identical incomplete boxes not to overwrite, got %d hash fields", len(boxFields))
	}

	details, err := cache.ListAll(ctx, "jp", "resourceboxdetails")
	if err != nil {
		t.Fatalf("list incomplete resourceboxdetails: %v", err)
	}
	if len(details) != 4 || details[0]["id"] != float64(81) || details[1]["id"] != float64(81) {
		t.Fatalf("expected all incomplete details with original fields, got %v", details)
	}
	detailFields, err := cache.client.HGetAll(ctx, cache.redisEntityKey("jp", "resourceboxdetails")).Result()
	if err != nil {
		t.Fatalf("read incomplete detail hash: %v", err)
	}
	if len(detailFields) != 4 {
		t.Fatalf("expected incomplete details not to overwrite, got %d hash fields", len(detailFields))
	}
}

func TestStoreRegionKeepsOrdinaryDuplicateIDBehavior(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)
	ctx := context.Background()

	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "name": "first"},
			map[string]any{"id": 1, "name": "second"},
		},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store duplicate ordinary IDs: %v", err)
	}

	fields, err := cache.client.HGetAll(ctx, cache.redisEntityKey("jp", "cards")).Result()
	if err != nil {
		t.Fatalf("read card hash: %v", err)
	}
	if len(fields) != 1 || fields["1"] == "" {
		t.Fatalf("ordinary entity duplicate IDs should keep using the bare ID field, got %v", fields)
	}
	card, found, err := cache.GetByID(ctx, "jp", "cards", "1")
	if err != nil || !found || card["name"] != "second" {
		t.Fatalf("expected last ordinary duplicate to remain addressable by bare ID, record=%v found=%v err=%v", card, found, err)
	}
	allCards, err := cache.ListAll(ctx, "jp", "cards")
	if err != nil {
		t.Fatalf("list duplicate cards: %v", err)
	}
	if len(allCards) != 2 || allCards[0]["name"] != "second" || allCards[1]["name"] != "second" {
		t.Fatalf("ordinary duplicate list behavior changed, got %v", allCards)
	}
}

type legacyCompositeEntitySeed struct {
	entity     string
	legacyID   string
	fileDigest string
}

func TestStoreRegionReplacesLegacyCompositeKeysAndSearchArtifacts(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)
	ctx := context.Background()

	legacySeeds := []legacyCompositeEntitySeed{
		{entity: "resourceboxes", legacyID: "9001", fileDigest: "boxes-digest"},
		{entity: "resourceboxdetails", legacyID: "41", fileDigest: "details-digest"},
	}
	for _, seed := range legacySeeds {
		seedLegacyCompositeEntity(t, ctx, cache, seed)
	}

	boxes := []json.RawMessage{
		json.RawMessage(`{"id":9001,"resourceBoxPurpose":"event_ranking_reward","details":[]}`),
		json.RawMessage(`{"id":9001,"resourceBoxPurpose":"virtual_live_reward","details":[]}`),
	}
	details := []json.RawMessage{
		json.RawMessage(`{"id":41,"resourceBoxId":9001,"resourceBoxPurpose":"event_ranking_reward","seq":1}`),
		json.RawMessage(`{"id":41,"resourceBoxId":9001,"resourceBoxPurpose":"event_ranking_reward","seq":2}`),
	}
	payload := map[string]any{
		"resourceboxes.json":      boxes,
		"resourceboxdetails.json": details,
	}
	fileDigests := map[string]string{
		"resourceboxes.json":      "boxes-digest",
		"resourceboxdetails.json": "details-digest",
	}
	if err := cache.StoreRegionWithSourceDigests(ctx, "jp", payload, fileDigests); err != nil {
		t.Fatalf("store migrated composite-key payload: %v", err)
	}

	for _, seed := range legacySeeds {
		assertLegacyCompositeEntityMigration(t, ctx, cache, seed)
	}
	resourceBoxes, err := cache.ListAll(ctx, "jp", "resourceboxes")
	if err != nil || len(resourceBoxes) != 2 {
		t.Fatalf("expected region rewrite to return both independent resourceboxes, got %v err=%v", resourceBoxes, err)
	}

	for _, seed := range legacySeeds {
		seedForcedLegacyCompositeEntity(t, ctx, cache, seed)
	}
	if err := cache.StoreRegion(masterdata.WithForceFullStore(ctx), "jp", payload); err != nil {
		t.Fatalf("force-store composite-key payload: %v", err)
	}
	for _, seed := range legacySeeds {
		assertForcedLegacyCompositeEntityRemoved(t, ctx, cache, seed)
	}
}

func seedLegacyCompositeEntity(
	t *testing.T,
	ctx context.Context,
	cache *RedisMasterDataCache,
	seed legacyCompositeEntitySeed,
) {
	t.Helper()

	if err := cache.client.HSet(ctx, cache.redisEntityKey("jp", seed.entity), seed.legacyID, `{"id":9001,"resourceBoxPurpose":"event_ranking_reward"}`).Err(); err != nil {
		t.Fatalf("seed legacy %s record: %v", seed.entity, err)
	}
	if err := cache.client.RPush(ctx, cache.redisEntityOrderKey("jp", seed.entity), seed.legacyID).Err(); err != nil {
		t.Fatalf("seed legacy %s order: %v", seed.entity, err)
	}
	if err := cache.client.Set(ctx, cache.redisEntityRevisionKey("jp", seed.entity), "legacy-revision", 0).Err(); err != nil {
		t.Fatalf("seed legacy %s revision: %v", seed.entity, err)
	}
	if err := cache.client.Set(ctx, cache.redisEntitySourceDigestKey("jp", seed.entity), seed.fileDigest, 0).Err(); err != nil {
		t.Fatalf("seed legacy %s source digest: %v", seed.entity, err)
	}
	if err := cache.client.Set(ctx, cache.redisEntitySearchIndexKey("jp", seed.entity), "legacy-index", 0).Err(); err != nil {
		t.Fatalf("seed legacy %s search index: %v", seed.entity, err)
	}
	if err := cache.client.Set(ctx, cache.redisEntitySearchIndexVersionKey("jp", seed.entity), "legacy-version", 0).Err(); err != nil {
		t.Fatalf("seed legacy %s search index version: %v", seed.entity, err)
	}
	if err := cache.client.SAdd(ctx, cache.redisRegionSearchIndexEntitiesKey("jp"), seed.entity).Err(); err != nil {
		t.Fatalf("seed legacy %s search-index membership: %v", seed.entity, err)
	}
}

func assertLegacyCompositeEntityMigration(
	t *testing.T,
	ctx context.Context,
	cache *RedisMasterDataCache,
	seed legacyCompositeEntitySeed,
) {
	t.Helper()

	entity := seed.entity
	fields, err := cache.client.HGetAll(ctx, cache.redisEntityKey("jp", entity)).Result()
	if err != nil {
		t.Fatalf("read migrated %s hash: %v", entity, err)
	}
	if len(fields) != 2 {
		t.Fatalf("expected both migrated %s records, got %v", entity, fields)
	}
	for field := range fields {
		if field == "9001" || field == "41" {
			t.Fatalf("legacy bare field %q remains in %s hash: %v", field, entity, fields)
		}
	}
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", entity))
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", entity))
	assertRedisSetExcludes(t, ctx, cache, cache.redisRegionSearchIndexEntitiesKey("jp"), entity)
	order, err := cache.client.LRange(ctx, cache.redisEntityOrderKey("jp", entity), 0, -1).Result()
	if err != nil {
		t.Fatalf("read migrated %s order: %v", entity, err)
	}
	if len(order) != 2 || order[0] == "9001" || order[0] == "41" || order[1] == "9001" || order[1] == "41" {
		t.Fatalf("legacy bare IDs remain in %s order: %v", entity, order)
	}
}

func seedForcedLegacyCompositeEntity(
	t *testing.T,
	ctx context.Context,
	cache *RedisMasterDataCache,
	seed legacyCompositeEntitySeed,
) {
	t.Helper()

	legacyID := "forced-legacy:" + seed.legacyID
	if err := cache.client.HSet(ctx, cache.redisEntityKey("jp", seed.entity), legacyID, `{"legacy":true}`).Err(); err != nil {
		t.Fatalf("seed forced-full-store legacy %s field: %v", seed.entity, err)
	}
	if err := cache.client.RPush(ctx, cache.redisEntityOrderKey("jp", seed.entity), legacyID).Err(); err != nil {
		t.Fatalf("seed forced-full-store legacy %s order: %v", seed.entity, err)
	}
}

func assertForcedLegacyCompositeEntityRemoved(
	t *testing.T,
	ctx context.Context,
	cache *RedisMasterDataCache,
	seed legacyCompositeEntitySeed,
) {
	t.Helper()

	if exists, err := cache.client.HExists(ctx, cache.redisEntityKey("jp", seed.entity), "forced-legacy:"+seed.legacyID).Result(); err != nil || exists {
		t.Fatalf("forced full store retained legacy %s hash field, exists=%v err=%v", seed.entity, exists, err)
	}
	order, err := cache.client.LRange(ctx, cache.redisEntityOrderKey("jp", seed.entity), 0, -1).Result()
	if err != nil {
		t.Fatalf("read forced full store %s order: %v", seed.entity, err)
	}
	for _, storageID := range order {
		if strings.HasPrefix(storageID, "forced-legacy:") {
			t.Fatalf("forced full store retained legacy %s order entry: %v", seed.entity, order)
		}
	}
}

func TestCompositeEntitiesDoNotSearchOrBuildIndexes(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)
	ctx := context.Background()
	payload := map[string]any{
		"resourceboxes.json": []any{
			map[string]any{"id": 9001, "resourceBoxPurpose": "event_ranking_reward", "name": "searchable box"},
		},
		"resourceboxdetails.json": []any{
			map[string]any{"resourceBoxId": 9001, "resourceBoxPurpose": "event_ranking_reward", "seq": 1, "name": "searchable detail"},
		},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store composite records: %v", err)
	}

	for _, entity := range []string{"resourceboxes", "resourceboxdetails"} {
		assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", entity))
		assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", entity))
		matches, err := cache.Search(ctx, "jp", entity, "searchable", []string{"name"}, 10)
		if err != nil || len(matches) != 0 {
			t.Fatalf("expected search to be disabled for %s, matches=%v err=%v", entity, matches, err)
		}
		assertNoRetainedEntityIndex(t, cache, "jp", entity)
	}

	// A legacy malformed index must not be decoded or repaired by Search for a
	// composite-key entity. Explicit index loading is responsible for cleanup.
	legacyIndex := []byte("not-json")
	if err := cache.client.Set(ctx, cache.redisEntitySearchIndexKey("jp", "resourceboxes"), legacyIndex, 0).Err(); err != nil {
		t.Fatalf("seed malformed legacy search index: %v", err)
	}
	if err := cache.client.Set(ctx, cache.redisEntitySearchIndexVersionKey("jp", "resourceboxes"), "legacy", 0).Err(); err != nil {
		t.Fatalf("seed legacy search index version: %v", err)
	}
	if err := cache.client.SAdd(ctx, cache.redisRegionSearchIndexEntitiesKey("jp"), "resourceboxes").Err(); err != nil {
		t.Fatalf("seed legacy search-index membership: %v", err)
	}
	if matches, err := cache.Search(ctx, "jp", "resourceboxes", "searchable", []string{"name"}, 10); err != nil || len(matches) != 0 {
		t.Fatalf("expected disabled composite search to ignore malformed index, matches=%v err=%v", matches, err)
	}
	storedIndex, err := cache.client.Get(ctx, cache.redisEntitySearchIndexKey("jp", "resourceboxes")).Bytes()
	if err != nil || string(storedIndex) != string(legacyIndex) {
		t.Fatalf("Search should leave stale composite index untouched, body=%q err=%v", storedIndex, err)
	}

	if _, err := cache.LoadRegionIndexFromRedis(ctx, "jp"); err != nil {
		t.Fatalf("load indexes and clean legacy composite artifact: %v", err)
	}
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", "resourceboxes"))
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", "resourceboxes"))
	assertRedisSetExcludes(t, ctx, cache, cache.redisRegionSearchIndexEntitiesKey("jp"), "resourceboxes")

	if err := cache.client.Set(ctx, cache.redisEntitySearchIndexKey("jp", "resourceboxdetails"), legacyIndex, 0).Err(); err != nil {
		t.Fatalf("seed malformed detail search index: %v", err)
	}
	if err := cache.client.Set(ctx, cache.redisEntitySearchIndexVersionKey("jp", "resourceboxdetails"), "legacy", 0).Err(); err != nil {
		t.Fatalf("seed detail search index version: %v", err)
	}
	if err := cache.client.SAdd(ctx, cache.redisRegionSearchIndexEntitiesKey("jp"), "resourceboxdetails").Err(); err != nil {
		t.Fatalf("seed detail search-index membership: %v", err)
	}
	if _, err := cache.RebuildRegionIndexFromRedis(ctx, "jp"); err != nil {
		t.Fatalf("rebuild indexes and clean legacy composite artifact: %v", err)
	}
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", "resourceboxdetails"))
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", "resourceboxdetails"))
	assertRedisSetExcludes(t, ctx, cache, cache.redisRegionSearchIndexEntitiesKey("jp"), "resourceboxdetails")
}

func TestStoreRegionRevisionTracksDataAndOrderChanges(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)
	ctx := context.Background()
	first := map[string]any{"cards.json": []any{map[string]any{"id": 1, "name": "one"}, map[string]any{"id": 2, "name": "two"}}}
	if err := cache.StoreRegion(ctx, "jp", first); err != nil {
		t.Fatal(err)
	}
	revisionKey := cache.redisEntityRevisionKey("jp", "cards")
	firstRevision, err := cache.client.Get(ctx, revisionKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreRegion(ctx, "jp", first); err != nil {
		t.Fatal(err)
	}
	unchangedRevision, _ := cache.client.Get(ctx, revisionKey).Result()
	if unchangedRevision != firstRevision {
		t.Fatalf("identical store changed revision from %q to %q", firstRevision, unchangedRevision)
	}

	orderOnly := map[string]any{"cards.json": []any{map[string]any{"id": 2, "name": "two"}, map[string]any{"id": 1, "name": "one"}}}
	if err := cache.StoreRegion(ctx, "jp", orderOnly); err != nil {
		t.Fatal(err)
	}
	orderRevision, _ := cache.client.Get(ctx, revisionKey).Result()
	if orderRevision == firstRevision {
		t.Fatal("order-only change did not change revision")
	}
	ids, err := cache.client.LRange(ctx, cache.redisEntityOrderKey("jp", "cards"), 0, -1).Result()
	if err != nil || strings.Join(ids, ",") != "2,1" {
		t.Fatalf("expected persisted order 2,1, got %v, err=%v", ids, err)
	}

	changed := map[string]any{"cards.json": []any{map[string]any{"id": 2, "name": "two-updated"}, map[string]any{"id": 1, "name": "one"}}}
	if err := cache.StoreRegion(ctx, "jp", changed); err != nil {
		t.Fatal(err)
	}
	changedRevision, _ := cache.client.Get(ctx, revisionKey).Result()
	if changedRevision == orderRevision {
		t.Fatal("record change did not change revision")
	}
	record, found, err := cache.GetByID(ctx, "jp", "cards", "2")
	if err != nil || !found || record["name"] != "two-updated" {
		t.Fatalf("changed record not persisted: record=%v found=%t err=%v", record, found, err)
	}
}

func TestStoreRegionRepairsMissingSearchIndexOnRevisionMatch(t *testing.T) {
	miniRedis := startTestMiniRedis(t)
	cache := newStoreRegionTestCache(t, miniRedis)
	ctx := context.Background()
	payload := map[string]any{"cards.json": []any{map[string]any{"id": 1, "name": "search me"}}}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatal(err)
	}
	indexKey := cache.redisEntitySearchIndexKey("jp", "cards")
	if err := cache.client.Del(ctx, indexKey, cache.redisEntitySearchIndexVersionKey("jp", "cards")).Err(); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatal(err)
	}
	if exists, _ := cache.client.Exists(ctx, indexKey).Result(); exists != 1 {
		t.Fatal("expected identical store to repair missing persisted search index")
	}
	matches, err := cache.Search(ctx, "jp", "cards", "search me", nil, 10)
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected repaired search index match, got %d matches err=%v", len(matches), err)
	}
}

func TestSearchIndexPersistsOnlySearchableFields(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                         miniRedis.Addr(),
		RedisDB:                           0,
		MasterDataRedisKeyPrefix:          "test:master-data:",
		MasterDataSearchIndexCacheEntries: 0,
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()
	payload := map[string]any{
		"costume3dModelAvailablePatterns.json": []any{
			map[string]any{
				"id":                  1,
				"name":                "searchable costume",
				"assetbundleName":     "very-large-unused-string",
				"costume3dId":         10,
				"colorId":             20,
				"availablePatternIds": []any{1, 2, 3},
			},
		},
		"eventCards.json": []any{
			map[string]any{"id": 10, "eventId": 100, "cardId": 200, "bonusRate": 50},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	costumeIndex := readPersistedSearchIndex(t, ctx, cache, "jp", "costume3dmodelavailablepatterns")
	assertPersistedSearchIndex(t, costumeIndex, 1, []string{"name"})

	eventCardsIndex := readPersistedSearchIndex(t, ctx, cache, "jp", "eventcards")
	assertPersistedSearchIndex(t, eventCardsIndex, 1, []string{"cardid", "eventid"})
	assertNoRetainedRegionIndex(t, cache, "jp")

	matches, err := cache.Search(ctx, "jp", "costume3dmodelavailablepatterns", "searchable", nil, 10)
	if err != nil {
		t.Fatalf("search default name field: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected default name search to match, got %d", len(matches))
	}
	assertNoRetainedRegionIndex(t, cache, "jp")

	unusedMatches, err := cache.Search(ctx, "jp", "costume3dmodelavailablepatterns", "very-large", []string{"assetbundleName"}, 10)
	if err != nil {
		t.Fatalf("search unused field: %v", err)
	}
	if len(unusedMatches) != 0 {
		t.Fatalf("expected unused assetbundleName field to be omitted, got %d matches", len(unusedMatches))
	}

	eventMatches, err := cache.Search(ctx, "jp", "eventcards", "100", []string{"eventId"}, 10)
	if err != nil {
		t.Fatalf("search eventcards by eventId: %v", err)
	}
	if len(eventMatches) != 1 {
		t.Fatalf("expected eventId relationship field to remain searchable, got %d matches", len(eventMatches))
	}
	assertNoRetainedRegionIndex(t, cache, "jp")
}

func TestSearchIndexRequiredCurrentSearchesStillWork(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                         miniRedis.Addr(),
		RedisDB:                           0,
		MasterDataRedisKeyPrefix:          "test:master-data:",
		MasterDataSearchIndexCacheEntries: 0,
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()
	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "card-prefix", "name": "card-name", "cardSkillName": "skill-name"},
		},
		"cardEpisodes.json": []any{
			map[string]any{"id": 10, "cardId": 1, "title": "episode-title"},
		},
		"eventCards.json": []any{
			map[string]any{"id": 20, "eventId": 100, "cardId": 1},
		},
		"cardRarities.json": []any{
			map[string]any{"cardRarityType": "rarity_4", "maxLevel": 60},
		},
		"cardParameters.json": []any{
			map[string]any{"id": 30, "cardId": 1, "power": 9000},
		},
		"musicVocals.json": []any{
			map[string]any{"id": 40, "musicId": 200, "caption": "vocal-caption"},
		},
		"eventStories.json": []any{
			map[string]any{"id": 50, "eventId": 100, "eventStoryId": 500},
		},
		"eventStoryUnits.json": []any{
			map[string]any{"id": 60, "eventStoryId": 500, "unit": "mmj"},
		},
		"unitProfiles.json": []any{
			map[string]any{"unit": "theme_park", "unitName": "Wonderlands x Showtime"},
		},
		"virtualLiveCharacters.json": []any{
			map[string]any{"id": 70, "virtualLiveId": 700, "characterId": 3},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	tests := []struct {
		name   string
		entity string
		query  string
		field  string
	}{
		{name: "default name", entity: "cards", query: "card-name", field: "name"},
		{name: "cards prefix", entity: "cards", query: "card-prefix", field: "prefix"},
		{name: "cards skill name", entity: "cards", query: "skill-name", field: "cardSkillName"},
		{name: "cardepisodes card id", entity: "cardepisodes", query: "1", field: "cardId"},
		{name: "eventcards card id", entity: "eventcards", query: "1", field: "cardId"},
		{name: "eventcards event id", entity: "eventcards", query: "100", field: "eventId"},
		{name: "cardrarities rarity type", entity: "cardrarities", query: "rarity_4", field: "cardRarityType"},
		{name: "cardparameters card id", entity: "cardparameters", query: "1", field: "cardId"},
		{name: "musicvocals music id", entity: "musicvocals", query: "200", field: "musicId"},
		{name: "eventstories event id", entity: "eventstories", query: "100", field: "eventId"},
		{name: "eventstories event story id", entity: "eventstories", query: "500", field: "eventStoryId"},
		{name: "eventstoryunits event story id", entity: "eventstoryunits", query: "500", field: "eventStoryId"},
		{name: "unitprofiles unit", entity: "unitprofiles", query: "theme_park", field: "unit"},
		{name: "virtual live id", entity: "virtuallivecharacters", query: "700", field: "virtualLiveId"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches, err := cache.Search(ctx, "jp", tt.entity, tt.query, []string{tt.field}, 10)
			if err != nil {
				t.Fatalf("search %s by %s: %v", tt.entity, tt.field, err)
			}
			if len(matches) != 1 {
				t.Fatalf("expected 1 match for %s.%s=%q, got %d", tt.entity, tt.field, tt.query, len(matches))
			}
		})
	}
}

func TestStoreRegionOmitsUnusedScalarFieldsFromPersistedIndex(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                         miniRedis.Addr(),
		RedisDB:                           0,
		MasterDataRedisKeyPrefix:          "test:master-data:",
		MasterDataSearchIndexCacheEntries: 0,
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()
	payload := map[string]any{
		"cards.json": []any{
			map[string]any{
				"id":              1,
				"prefix":          "alpha",
				"assetbundleName": "unused-card-scalar",
			},
		},
		"skills.json": []any{
			map[string]any{
				"id":            10,
				"name":          "focus",
				"cardSkillName": "card-only-field",
			},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	cardsIndex := readPersistedSearchIndex(t, ctx, cache, "jp", "cards")
	assertPersistedSearchIndex(t, cardsIndex, 1, []string{"prefix"})

	skillsIndex := readPersistedSearchIndex(t, ctx, cache, "jp", "skills")
	assertPersistedSearchIndex(t, skillsIndex, 1, []string{"name"})

	assertNoRetainedRegionIndex(t, cache, "jp")
}

func TestLoadRegionIndexFromRedisFiltersCurrentPersistedFieldsAfterRestart(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	writerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new writer redis cache: %v", err)
	}
	defer func() {
		_ = writerCache.Close()
	}()

	ctx := context.Background()
	if err := writerCache.client.HSet(
		ctx,
		writerCache.redisEntityKey("jp", "cards"),
		"1",
		`{"id":1,"prefix":"alpha","assetbundleName":"legacy-bundle"}`,
	).Err(); err != nil {
		t.Fatalf("seed by-id record: %v", err)
	}
	staleIndex := entitySearchIndex{
		IDs:      []string{"1"},
		TextBlob: "alphalegacy-bundle",
		Fields: map[string][]searchIndexItem{
			"prefix": {
				{IDIndex: 0, TextOffset: 0, TextLength: 5},
			},
			"assetbundlename": {
				{IDIndex: 0, TextOffset: 5, TextLength: 13},
			},
		},
	}
	staleBody, err := json.Marshal(staleIndex)
	if err != nil {
		t.Fatalf("marshal stale index: %v", err)
	}
	if err := writerCache.client.Set(ctx, writerCache.redisEntitySearchIndexKey("jp", "cards"), staleBody, 0).Err(); err != nil {
		t.Fatalf("seed stale search index: %v", err)
	}
	if err := writerCache.client.SAdd(ctx, writerCache.redisRegionSearchIndexEntitiesKey("jp"), "cards").Err(); err != nil {
		t.Fatalf("seed region index set: %v", err)
	}

	readerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new reader redis cache: %v", err)
	}
	defer func() {
		_ = readerCache.Close()
	}()

	loaded, err := readerCache.LoadRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("load region index: %v", err)
	}
	if !loaded {
		t.Fatalf("expected current persisted index to load")
	}

	filteredIndex := readPersistedSearchIndex(t, ctx, readerCache, "jp", "cards")
	assertPersistedSearchIndex(t, filteredIndex, 1, []string{"prefix"})
	if filteredIndex.TextBlob != "alpha" {
		t.Fatalf("expected filtered text blob to be compacted to allowed text, got %q", filteredIndex.TextBlob)
	}
	assertNoRetainedRegionIndex(t, readerCache, "jp")

	matches, err := readerCache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search allowed field after load: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected allowed field to remain searchable after load, got %d matches", len(matches))
	}
	assertNoRetainedRegionIndex(t, readerCache, "jp")
}

func TestSearchCurrentPersistedIndexDoesNotRewrite(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()
	if err := cache.client.HSet(ctx, cache.redisEntityKey("jp", "cards"), "1", `{"id":1,"prefix":"alpha"}`).Err(); err != nil {
		t.Fatalf("seed by-id record: %v", err)
	}

	currentIndex := entitySearchIndex{
		IDs:      []string{"1"},
		TextBlob: "alpha",
		Fields: map[string][]searchIndexItem{
			"prefix": {
				{IDIndex: 0, TextOffset: 0, TextLength: 5},
			},
		},
	}
	currentBody, err := json.MarshalIndent(currentIndex, "", "  ")
	if err != nil {
		t.Fatalf("marshal current index: %v", err)
	}
	indexKey := cache.redisEntitySearchIndexKey("jp", "cards")
	if err := cache.client.Set(ctx, indexKey, currentBody, time.Minute).Err(); err != nil {
		t.Fatalf("seed current search index: %v", err)
	}
	if err := cache.client.SAdd(ctx, cache.redisRegionSearchIndexEntitiesKey("jp"), "cards").Err(); err != nil {
		t.Fatalf("seed region index set: %v", err)
	}

	matches, err := cache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search current index: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected current index to remain searchable, got %d matches", len(matches))
	}

	storedBody, err := cache.client.Get(ctx, indexKey).Bytes()
	if err != nil {
		t.Fatalf("get current search index after search: %v", err)
	}
	if string(storedBody) != string(currentBody) {
		t.Fatalf("expected current search index bytes to remain unchanged after search")
	}

	ttl, err := cache.client.TTL(ctx, indexKey).Result()
	if err != nil {
		t.Fatalf("get current search index ttl after search: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("expected current search index ttl to remain positive after search, got %s", ttl)
	}
	assertNoRetainedRegionIndex(t, cache, "jp")
}

func TestReadEntityIndexFromRedisReportsAllDecodeFormats(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() { _ = cache.Close() }()

	ctx := context.Background()
	if err := cache.client.Set(ctx, cache.redisEntitySearchIndexKey("jp", "cards"), []byte("not-json"), 0).Err(); err != nil {
		t.Fatalf("seed malformed index: %v", err)
	}

	_, _, found, err := cache.readEntityIndexFromRedis(ctx, "jp", "cards")
	if err == nil {
		t.Fatalf("expected malformed persisted index to return an error")
	}
	if found {
		t.Fatalf("expected malformed persisted index not to be reported as loaded")
	}
	for _, label := range []string{
		"current format",
		"persisted legacy format",
		"legacy item map format",
		"legacy string-ID map format",
	} {
		if !strings.Contains(err.Error(), label) {
			t.Fatalf("expected decode error to include %q, got %v", label, err)
		}
	}
}

func TestRewriteEntitySearchIndexReturnsPipelineError(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("close redis cache: %v", err)
	}

	_, err = cache.rewriteEntitySearchIndex(context.Background(), "jp", "cards", &entitySearchIndex{
		IDs:      []string{"1"},
		TextBlob: "alpha",
		Fields: map[string][]searchIndexItem{
			"prefix": {{IDIndex: 0, TextOffset: 0, TextLength: 5}},
		},
	})
	if err == nil {
		t.Fatalf("expected rewrite pipeline failure to be returned")
	}
	if !strings.Contains(err.Error(), "rewrite persisted search index region jp entity cards") {
		t.Fatalf("expected contextual rewrite error, got %v", err)
	}
}

func TestLoadRegionIndexFromRedisFiltersLegacyPersistedFields(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()
	if err := cache.client.HSet(ctx, cache.redisEntityKey("jp", "cards"), "1", `{"id":1,"prefix":"alpha","assetbundleName":"legacy-bundle"}`).Err(); err != nil {
		t.Fatalf("seed by-id record: %v", err)
	}
	legacy := map[string][]legacySearchIndexItem{
		"prefix":          {{ID: "1", NormalizedText: "alpha"}},
		"assetbundlename": {{ID: "1", NormalizedText: "legacy-bundle"}},
	}
	legacyBody, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy index: %v", err)
	}
	legacyIndexKey := cache.redisEntitySearchIndexKey("jp", "cards")
	if err := cache.client.Set(ctx, legacyIndexKey, legacyBody, time.Minute).Err(); err != nil {
		t.Fatalf("seed legacy search index: %v", err)
	}
	if err := cache.client.SAdd(ctx, cache.redisRegionSearchIndexEntitiesKey("jp"), "cards").Err(); err != nil {
		t.Fatalf("seed region index set: %v", err)
	}

	loaded, err := cache.LoadRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("load region index: %v", err)
	}
	if !loaded {
		t.Fatalf("expected legacy persisted index to load")
	}

	filteredIndex := readPersistedSearchIndex(t, ctx, cache, "jp", "cards")
	assertPersistedSearchIndex(t, filteredIndex, 1, []string{"prefix"})
	legacyTTL, err := cache.client.TTL(ctx, legacyIndexKey).Result()
	if err != nil {
		t.Fatalf("get legacy search index ttl after migration: %v", err)
	}
	if legacyTTL >= 0 {
		t.Fatalf("expected legacy search index migration to rewrite without ttl, got ttl %s", legacyTTL)
	}
	assertNoRetainedRegionIndex(t, cache, "jp")

	legacyMatches, err := cache.Search(ctx, "jp", "cards", "legacy", []string{"assetbundleName"}, 10)
	if err != nil {
		t.Fatalf("search filtered legacy field: %v", err)
	}
	if len(legacyMatches) != 0 {
		t.Fatalf("expected filtered legacy field to be omitted, got %d matches", len(legacyMatches))
	}

	prefixMatches, err := cache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search allowed legacy field: %v", err)
	}
	if len(prefixMatches) != 1 {
		t.Fatalf("expected allowed legacy field to remain searchable, got %d matches", len(prefixMatches))
	}
	assertNoRetainedRegionIndex(t, cache, "jp")
}

func TestLoadRegionIndexFromRedisSupportsLegacyStringIDFields(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()
	if err := cache.client.HSet(ctx, cache.redisEntityKey("jp", "cards"), "1", `{"id":1,"prefix":"alpha","assetbundleName":"legacy-bundle"}`).Err(); err != nil {
		t.Fatalf("seed by-id record: %v", err)
	}
	legacy := map[string][]string{
		"prefix":          {"1"},
		"assetbundlename": {"1"},
	}
	legacyBody, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy string id index: %v", err)
	}
	legacyIndexKey := cache.redisEntitySearchIndexKey("jp", "cards")
	if err := cache.client.Set(ctx, legacyIndexKey, legacyBody, time.Minute).Err(); err != nil {
		t.Fatalf("seed legacy string id search index: %v", err)
	}
	if err := cache.client.SAdd(ctx, cache.redisRegionSearchIndexEntitiesKey("jp"), "cards").Err(); err != nil {
		t.Fatalf("seed region index set: %v", err)
	}

	loaded, err := cache.LoadRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("load region index: %v", err)
	}
	if !loaded {
		t.Fatalf("expected legacy string id persisted index to load")
	}

	filteredIndex := readPersistedSearchIndex(t, ctx, cache, "jp", "cards")
	assertPersistedSearchIndex(t, filteredIndex, 1, []string{"prefix"})
	if filteredIndex.TextBlob != "alpha" {
		t.Fatalf("expected filtered text blob to be compacted from by-id record, got %q", filteredIndex.TextBlob)
	}
	legacyTTL, err := cache.client.TTL(ctx, legacyIndexKey).Result()
	if err != nil {
		t.Fatalf("get legacy string id search index ttl after migration: %v", err)
	}
	if legacyTTL >= 0 {
		t.Fatalf("expected legacy string id search index migration to rewrite without ttl, got ttl %s", legacyTTL)
	}
	assertNoRetainedRegionIndex(t, cache, "jp")

	legacyMatches, err := cache.Search(ctx, "jp", "cards", "legacy", []string{"assetbundleName"}, 10)
	if err != nil {
		t.Fatalf("search filtered legacy string id field: %v", err)
	}
	if len(legacyMatches) != 0 {
		t.Fatalf("expected filtered legacy string id field to be omitted, got %d matches", len(legacyMatches))
	}

	prefixMatches, err := cache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search allowed legacy string id field: %v", err)
	}
	if len(prefixMatches) != 1 {
		t.Fatalf("expected allowed legacy string id field to remain searchable, got %d matches", len(prefixMatches))
	}
	assertNoRetainedRegionIndex(t, cache, "jp")
}

func TestLoadRegionIndexFromRedisFiltersWrapperLegacyPersistedFields(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()
	if err := cache.client.HSet(ctx, cache.redisEntityKey("jp", "cards"), "1", `{"id":1,"prefix":"alpha","assetbundleName":"legacy-bundle"}`).Err(); err != nil {
		t.Fatalf("seed by-id record: %v", err)
	}
	legacy := legacyPersistedEntitySearchIndex{
		IDs: []string{"1"},
		Fields: map[string][]legacyPersistedSearchIndexItem{
			"prefix": {
				{IDIndex: 0, NormalizedText: "alpha"},
			},
			"assetbundlename": {
				{IDIndex: 0, NormalizedText: "legacy-bundle"},
			},
		},
	}
	legacyBody, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal wrapper legacy index: %v", err)
	}
	legacyIndexKey := cache.redisEntitySearchIndexKey("jp", "cards")
	if err := cache.client.Set(ctx, legacyIndexKey, legacyBody, time.Minute).Err(); err != nil {
		t.Fatalf("seed wrapper legacy search index: %v", err)
	}
	if err := cache.client.SAdd(ctx, cache.redisRegionSearchIndexEntitiesKey("jp"), "cards").Err(); err != nil {
		t.Fatalf("seed region index set: %v", err)
	}

	loaded, err := cache.LoadRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("load region index: %v", err)
	}
	if !loaded {
		t.Fatalf("expected wrapper legacy persisted index to load")
	}

	filteredIndex := readPersistedSearchIndex(t, ctx, cache, "jp", "cards")
	assertPersistedSearchIndex(t, filteredIndex, 1, []string{"prefix"})
	if filteredIndex.TextBlob != "alpha" {
		t.Fatalf("expected filtered text blob to be compacted to allowed text, got %q", filteredIndex.TextBlob)
	}
	legacyTTL, err := cache.client.TTL(ctx, legacyIndexKey).Result()
	if err != nil {
		t.Fatalf("get wrapper legacy search index ttl after migration: %v", err)
	}
	if legacyTTL >= 0 {
		t.Fatalf("expected wrapper legacy search index migration to rewrite without ttl, got ttl %s", legacyTTL)
	}
	assertNoRetainedRegionIndex(t, cache, "jp")

	legacyMatches, err := cache.Search(ctx, "jp", "cards", "legacy", []string{"assetbundleName"}, 10)
	if err != nil {
		t.Fatalf("search filtered wrapper legacy field: %v", err)
	}
	if len(legacyMatches) != 0 {
		t.Fatalf("expected filtered wrapper legacy field to be omitted, got %d matches", len(legacyMatches))
	}

	prefixMatches, err := cache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search allowed wrapper legacy field: %v", err)
	}
	if len(prefixMatches) != 1 {
		t.Fatalf("expected allowed wrapper legacy field to remain searchable, got %d matches", len(prefixMatches))
	}
	assertNoRetainedRegionIndex(t, cache, "jp")
}

func TestStoreRegionReportsCacheWriteProgress(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	events := make([]masterdata.SyncUpdatedEvent, 0)
	ctx := masterdata.WithProgressReporter(context.Background(), func(event masterdata.SyncUpdatedEvent) {
		if event.Phase != "cache" {
			return
		}
		events = append(events, event)
	})

	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
		"skills.json": []any{
			map[string]any{"id": 2, "name": "focus"},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 cache progress events, got %d", len(events))
	}
	if events[0].ProcessedFiles != 1 || events[0].TotalFiles != 2 {
		t.Fatalf("expected first cache progress 1/2, got %d/%d", events[0].ProcessedFiles, events[0].TotalFiles)
	}
	if events[1].ProcessedFiles != 2 || events[1].TotalFiles != 2 {
		t.Fatalf("expected second cache progress 2/2, got %d/%d", events[1].ProcessedFiles, events[1].TotalFiles)
	}
}

func TestRegionIndexStatsReportsNoRetainedIndexAfterStoreRegion(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()
	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha", "cardSkillName": "score up"},
			map[string]any{"id": 2, "prefix": "beta", "cardSkillName": "life recover"},
		},
		"skills.json": []any{
			map[string]any{"id": 100, "name": "focus"},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	cardsIndex := readPersistedSearchIndex(t, ctx, cache, "jp", "cards")
	assertPersistedSearchIndex(t, cardsIndex, 2, []string{"cardskillname", "prefix"})
	skillsIndex := readPersistedSearchIndex(t, ctx, cache, "jp", "skills")
	assertPersistedSearchIndex(t, skillsIndex, 1, []string{"name"})
	assertNoRetainedRegionIndex(t, cache, "jp")
}

func TestSearchUsesCardPrefixField(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()

	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha", "name": "totally unrelated"},
			map[string]any{"id": 2, "prefix": "zzz", "name": "alpha in name only"},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	prefixMatches, err := cache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search cards by prefix field: %v", err)
	}
	if len(prefixMatches) != 1 {
		t.Fatalf("expected 1 prefix match, got %d", len(prefixMatches))
	}
	if prefixMatches[0].Item["id"] != float64(1) {
		t.Fatalf("expected prefix match id=1, got %v", prefixMatches[0].Item["id"])
	}

	nameMatches, err := cache.Search(ctx, "jp", "cards", "alpha", []string{"name"}, 10)
	if err != nil {
		t.Fatalf("search cards by name field: %v", err)
	}
	if len(nameMatches) != 1 {
		t.Fatalf("expected 1 name match, got %d", len(nameMatches))
	}
	if nameMatches[0].Item["id"] != float64(2) {
		t.Fatalf("expected name match id=2, got %v", nameMatches[0].Item["id"])
	}
}

func TestSearchUsesCardSkillNameField(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()

	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha", "cardSkillName": "score up"},
			map[string]any{"id": 2, "prefix": "beta", "cardSkillName": "life recover"},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	matches, err := cache.Search(ctx, "jp", "cards", "score", []string{"cardSkillName"}, 10)
	if err != nil {
		t.Fatalf("search cards by cardSkillName field: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 cardSkillName match, got %d", len(matches))
	}
	if matches[0].Item["id"] != float64(1) {
		t.Fatalf("expected match id=1, got %v", matches[0].Item["id"])
	}
}

func TestSearchCardRaritiesWithoutID(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()

	payload := map[string]any{
		"cardRarities.json": []any{
			map[string]any{"cardRarityType": "rarity_1", "maxLevel": 20},
			map[string]any{"cardRarityType": "rarity_2", "maxLevel": 30},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	matches, err := cache.Search(ctx, "jp", "cardrarities", "rarity_1", []string{"cardRarityType"}, 10)
	if err != nil {
		t.Fatalf("search card rarities by cardRarityType: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 card rarity match, got %d", len(matches))
	}
	if matches[0].Item["cardRarityType"] != "rarity_1" {
		t.Fatalf("expected cardRarityType rarity_1, got %v", matches[0].Item["cardRarityType"])
	}
}

func TestSearchCardEpisodesByNumericCardID(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()

	payload := map[string]any{
		"cardEpisodes.json": []any{
			map[string]any{"id": 1, "cardId": 1001, "episodeNo": 1},
			map[string]any{"id": 2, "cardId": 2002, "episodeNo": 1},
		},
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	matches, err := cache.Search(ctx, "jp", "cardepisodes", "1001", []string{"cardId"}, 10)
	if err != nil {
		t.Fatalf("search card episodes by cardId: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 card episode match, got %d", len(matches))
	}
	if matches[0].Item["cardId"] != float64(1001) {
		t.Fatalf("expected cardId 1001, got %v", matches[0].Item["cardId"])
	}
}

func TestSearchIndexRebuildsFromRedisAfterRestart(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}

	writerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new writer redis cache: %v", err)
	}
	defer func() {
		_ = writerCache.Close()
	}()

	ctx := context.Background()
	payload := map[string]any{
		"unitProfiles.json": []any{
			map[string]any{"unit": "theme_park", "unitName": "Wonderlands x Showtime"},
		},
	}

	if err := writerCache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}
	storedIndex := readPersistedSearchIndex(t, ctx, writerCache, "jp", "unitprofiles")
	assertPersistedSearchIndex(t, storedIndex, 1, []string{"unit"})
	assertNoRetainedRegionIndex(t, writerCache, "jp")

	readerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new reader redis cache: %v", err)
	}
	defer func() {
		_ = readerCache.Close()
	}()

	matches, err := readerCache.Search(ctx, "jp", "unitprofiles", "theme_park", []string{"unit"}, 10)
	if err != nil {
		t.Fatalf("search unitprofiles after restart: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 unitprofiles match after restart, got %d", len(matches))
	}
	if matches[0].Item["unit"] != "theme_park" {
		t.Fatalf("expected unit theme_park, got %v", matches[0].Item["unit"])
	}
	assertNoRetainedRegionIndex(t, readerCache, "jp")
}

func TestLoadRegionIndexFromRedisPreloadsPersistedIndexes(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}

	writerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new writer redis cache: %v", err)
	}
	defer func() {
		_ = writerCache.Close()
	}()

	ctx := context.Background()
	payload := map[string]any{
		"unitProfiles.json": []any{
			map[string]any{"unit": "theme_park", "unitName": "Wonderlands x Showtime"},
		},
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
	}

	if err := writerCache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	readerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new reader redis cache: %v", err)
	}
	defer func() {
		_ = readerCache.Close()
	}()

	if readerCache.HasRegionIndex("jp") {
		t.Fatalf("expected empty in-memory index before preload")
	}

	loaded, err := readerCache.LoadRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("load persisted region index: %v", err)
	}
	if !loaded {
		t.Fatalf("expected persisted region index to load")
	}
	persistedUnitProfilesIndex := readPersistedSearchIndex(t, ctx, readerCache, "jp", "unitprofiles")
	assertPersistedSearchIndex(t, persistedUnitProfilesIndex, 1, []string{"unit"})
	persistedCardsIndex := readPersistedSearchIndex(t, ctx, readerCache, "jp", "cards")
	assertPersistedSearchIndex(t, persistedCardsIndex, 1, []string{"prefix"})
	assertNoRetainedRegionIndex(t, readerCache, "jp")

	matches, err := readerCache.Search(ctx, "jp", "unitprofiles", "theme_park", []string{"unit"}, 10)
	if err != nil {
		t.Fatalf("search unitprofiles after preload: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 unitprofiles match after preload, got %d", len(matches))
	}
	assertNoRetainedRegionIndex(t, readerCache, "jp")
}

func TestRegionIndexStoreRegionReusesPersistedIndexWhenEntityIsUnchanged(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}

	payload := map[string]any{
		"unitProfiles.json": []any{
			map[string]any{"unit": "theme_park", "unitName": "Wonderlands x Showtime"},
		},
	}

	writerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new writer redis cache: %v", err)
	}
	if err := writerCache.StoreRegion(context.Background(), "jp", payload); err != nil {
		t.Fatalf("seed payload: %v", err)
	}
	_ = writerCache.Close()

	readerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new reader redis cache: %v", err)
	}
	defer func() {
		_ = readerCache.Close()
	}()

	if err := readerCache.StoreRegion(context.Background(), "jp", payload); err != nil {
		t.Fatalf("store unchanged payload: %v", err)
	}
	persistedIndex := readPersistedSearchIndex(t, context.Background(), readerCache, "jp", "unitprofiles")
	assertPersistedSearchIndex(t, persistedIndex, 1, []string{"unit"})
	assertNoRetainedRegionIndex(t, readerCache, "jp")

	matches, err := readerCache.Search(context.Background(), "jp", "unitprofiles", "theme_park", []string{"unit"}, 10)
	if err != nil {
		t.Fatalf("search after unchanged store: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 unitprofiles match after unchanged store, got %d", len(matches))
	}
	assertNoRetainedRegionIndex(t, readerCache, "jp")
}

func TestSearchRetainsDecodedIndexWhenLRUCacheEnabled(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                         miniRedis.Addr(),
		RedisDB:                           0,
		MasterDataRedisKeyPrefix:          "test:master-data:",
		MasterDataSearchIndexCacheEntries: 2,
	}
	ctx := context.Background()
	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
	}

	writerCache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new writer redis cache: %v", err)
	}
	if err := writerCache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}
	_ = writerCache.Close()

	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	if cache.HasRegionIndex("jp") {
		t.Fatalf("expected empty decoded index cache before search")
	}
	matches, err := cache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search cards: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 search match, got %d", len(matches))
	}
	assertRetainedEntityIndex(t, cache, "jp", "cards")
}

func TestSearchDecodedIndexLRUEvictsLeastRecentlyUsed(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                         miniRedis.Addr(),
		RedisDB:                           0,
		MasterDataRedisKeyPrefix:          "test:master-data:",
		MasterDataSearchIndexCacheEntries: 2,
	}
	ctx := context.Background()
	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
		"skills.json": []any{
			map[string]any{"id": 10, "name": "focus"},
		},
		"unitProfiles.json": []any{
			map[string]any{"unit": "theme_park"},
		},
	}

	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	if _, err := cache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10); err != nil {
		t.Fatalf("search cards: %v", err)
	}
	if _, err := cache.Search(ctx, "jp", "skills", "focus", []string{"name"}, 10); err != nil {
		t.Fatalf("search skills: %v", err)
	}
	if _, err := cache.Search(ctx, "jp", "unitprofiles", "theme_park", []string{"unit"}, 10); err != nil {
		t.Fatalf("search unitprofiles: %v", err)
	}

	assertNoRetainedEntityIndex(t, cache, "jp", "cards")
	assertRetainedEntityIndex(t, cache, "jp", "skills")
	assertRetainedEntityIndex(t, cache, "jp", "unitprofiles")
}

func TestStoreRegionInvalidatesUnchangedCachedIndex(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                         miniRedis.Addr(),
		RedisDB:                           0,
		MasterDataRedisKeyPrefix:          "test:master-data:",
		MasterDataSearchIndexCacheEntries: 1,
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()
	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}
	staleIndex := buildEntitySearchIndex("cards", []map[string]any{{"id": 1, "prefix": "beta"}})
	version, err := cache.persistedEntitySearchIndexVersion(ctx, "jp", "cards")
	if err != nil {
		t.Fatalf("get persisted index version: %v", err)
	}
	cache.setCachedEntityIndex("jp", "cards", version, staleIndex)

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store unchanged payload: %v", err)
	}
	assertNoRetainedEntityIndex(t, cache, "jp", "cards")

	matches, err := cache.Search(ctx, "jp", "cards", "beta", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search stale beta prefix: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected stale beta prefix to miss after unchanged store invalidation, got %d", len(matches))
	}
}

func TestSearchLRUValidatesRedisVersionAcrossCacheInstances(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                         miniRedis.Addr(),
		RedisDB:                           0,
		MasterDataRedisKeyPrefix:          "test:master-data:",
		MasterDataSearchIndexCacheEntries: 1,
	}
	ctx := context.Background()
	initialPayload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
	}
	updatedPayload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "beta"},
		},
	}

	writerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new writer cache: %v", err)
	}
	defer func() {
		_ = writerCache.Close()
	}()
	readerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new reader cache: %v", err)
	}
	defer func() {
		_ = readerCache.Close()
	}()

	if err := writerCache.StoreRegion(ctx, "jp", initialPayload); err != nil {
		t.Fatalf("store initial payload: %v", err)
	}
	alphaMatches, err := readerCache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search alpha before update: %v", err)
	}
	if len(alphaMatches) != 1 {
		t.Fatalf("expected alpha to match before update, got %d", len(alphaMatches))
	}
	assertRetainedEntityIndex(t, readerCache, "jp", "cards")

	if err := writerCache.StoreRegion(ctx, "jp", updatedPayload); err != nil {
		t.Fatalf("store updated payload: %v", err)
	}
	alphaMatches, err = readerCache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search alpha after update: %v", err)
	}
	if len(alphaMatches) != 0 {
		t.Fatalf("expected alpha to miss after Redis update from another cache, got %d", len(alphaMatches))
	}
	betaMatches, err := readerCache.Search(ctx, "jp", "cards", "beta", []string{"prefix"}, 10)
	if err != nil {
		t.Fatalf("search beta after update: %v", err)
	}
	if len(betaMatches) != 1 {
		t.Fatalf("expected beta to match after Redis update, got %d", len(betaMatches))
	}
}

func TestListByPageRebuildsOrderWhenMissing(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()

	byIDKey := cache.redisEntityKey("jp", "events")
	if err := cache.client.HSet(ctx, byIDKey,
		"10", `{"id":10,"name":"event-10"}`,
		"2", `{"id":2,"name":"event-2"}`,
	).Err(); err != nil {
		t.Fatalf("seed by-id hash: %v", err)
	}

	items, total, err := cache.ListByPage(ctx, "jp", "events", 1, 10)
	if err != nil {
		t.Fatalf("list by page with missing order: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total=2, got %d", total)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0]["id"] != float64(2) {
		t.Fatalf("expected first id=2 after rebuild, got %v", items[0]["id"])
	}
	if items[1]["id"] != float64(10) {
		t.Fatalf("expected second id=10 after rebuild, got %v", items[1]["id"])
	}
}

func TestListByPageRebuildsOrderWhenTruncated(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	ctx := context.Background()

	byIDKey := cache.redisEntityKey("jp", "events")
	if err := cache.client.HSet(ctx, byIDKey,
		"1", `{"id":1,"name":"event-1"}`,
		"2", `{"id":2,"name":"event-2"}`,
		"3", `{"id":3,"name":"event-3"}`,
	).Err(); err != nil {
		t.Fatalf("seed by-id hash: %v", err)
	}

	orderKey := cache.redisEntityOrderKey("jp", "events")
	if err := cache.client.RPush(ctx, orderKey, "1").Err(); err != nil {
		t.Fatalf("seed truncated order: %v", err)
	}

	items, total, err := cache.ListByPage(ctx, "jp", "events", 1, 10)
	if err != nil {
		t.Fatalf("list by page with truncated order: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected total=3 after rebuild, got %d", total)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items after rebuild, got %d", len(items))
	}

	if items[0]["id"] != float64(1) || items[1]["id"] != float64(2) || items[2]["id"] != float64(3) {
		t.Fatalf("expected rebuilt order [1,2,3], got [%v,%v,%v]", items[0]["id"], items[1]["id"], items[2]["id"])
	}
}

func TestRedisMasterDataCacheSearchIndexRetentionCharacterization(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	ctx := context.Background()
	payload := redisSearchIndexBenchmarkPayload(64)

	writerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new writer redis cache: %v", err)
	}
	defer func() {
		_ = writerCache.Close()
	}()

	if err := writerCache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store benchmark payload: %v", err)
	}
	storedIndex := readPersistedSearchIndex(t, ctx, writerCache, "jp", "cards")
	assertPersistedSearchIndex(t, storedIndex, 64, []string{"cardskillname", "name", "prefix"})
	assertNoRetainedRegionIndex(t, writerCache, "jp")

	searchCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new search redis cache: %v", err)
	}
	defer func() {
		_ = searchCache.Close()
	}()

	if searchCache.HasRegionIndex("jp") {
		t.Fatalf("expected fresh cache to start without decoded in-process index")
	}
	searchMatches, err := searchCache.Search(ctx, "jp", "cards", "benchmarkprefix000010", []string{"prefix"}, 5)
	if err != nil {
		t.Fatalf("search after restart: %v", err)
	}
	if len(searchMatches) != 1 {
		t.Fatalf("expected search after restart to match 1 card, got %d", len(searchMatches))
	}
	searchedIndex := readPersistedSearchIndex(t, ctx, searchCache, "jp", "cards")
	assertPersistedSearchIndex(t, searchedIndex, 64, []string{"cardskillname", "name", "prefix"})
	assertNoRetainedRegionIndex(t, searchCache, "jp")

	loadCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new load redis cache: %v", err)
	}
	defer func() {
		_ = loadCache.Close()
	}()
	loaded, err := loadCache.LoadRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("load region index from redis: %v", err)
	}
	if !loaded {
		t.Fatalf("expected LoadRegionIndexFromRedis to load persisted index")
	}
	loadedIndex := readPersistedSearchIndex(t, ctx, loadCache, "jp", "cards")
	assertPersistedSearchIndex(t, loadedIndex, 64, []string{"cardskillname", "name", "prefix"})
	assertNoRetainedRegionIndex(t, loadCache, "jp")

	if err := writerCache.client.Del(ctx, writerCache.redisEntitySearchIndexKey("jp", "cards"), writerCache.redisRegionSearchIndexEntitiesKey("jp")).Err(); err != nil {
		t.Fatalf("delete persisted search index before rebuild: %v", err)
	}
	rebuildCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new rebuild redis cache: %v", err)
	}
	defer func() {
		_ = rebuildCache.Close()
	}()
	rebuilt, err := rebuildCache.RebuildRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("rebuild region index from redis: %v", err)
	}
	if !rebuilt {
		t.Fatalf("expected RebuildRegionIndexFromRedis to rebuild index from by-id records")
	}
	rebuiltIndex := readPersistedSearchIndex(t, ctx, rebuildCache, "jp", "cards")
	assertPersistedSearchIndex(t, rebuiltIndex, 64, []string{"cardskillname", "name", "prefix"})
	assertNoRetainedRegionIndex(t, rebuildCache, "jp")
}

func TestRebuildRegionIndexFromRedisRemovesStaleSearchIndexVersionKeys(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	ctx := context.Background()
	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
		"skills.json": []any{
			map[string]any{"id": 10, "name": "focus"},
		},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}
	assertRedisKeyExists(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", "cards"))
	assertRedisKeyExists(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", "cards"))
	assertRedisKeyExists(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", "skills"))
	assertRedisKeyExists(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", "skills"))

	if err := cache.client.Del(ctx, cache.redisEntityKey("jp", "skills")).Err(); err != nil {
		t.Fatalf("delete stale entity by-id hash: %v", err)
	}

	rebuilt, err := cache.RebuildRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("rebuild region index: %v", err)
	}
	if !rebuilt {
		t.Fatalf("expected region index rebuild to keep remaining entity")
	}

	assertRedisKeyExists(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", "cards"))
	assertRedisKeyExists(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", "cards"))
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", "skills"))
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", "skills"))
	assertRedisSetExcludes(t, ctx, cache, cache.redisRegionSearchIndexEntitiesKey("jp"), "skills")
}

func TestRebuildRegionIndexFromRedisClearsSearchIndexArtifactsWhenRegionHasNoRecords(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	ctx := context.Background()
	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = cache.Close()
	}()

	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
		"skills.json": []any{
			map[string]any{"id": 10, "name": "focus"},
		},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}
	assertRedisKeyExists(t, ctx, cache, cache.redisRegionSearchIndexEntitiesKey("jp"))

	if err := cache.client.Del(ctx, cache.redisEntityKey("jp", "cards"), cache.redisEntityKey("jp", "skills")).Err(); err != nil {
		t.Fatalf("delete region by-id hashes: %v", err)
	}

	rebuilt, err := cache.RebuildRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("rebuild empty region index: %v", err)
	}
	if rebuilt {
		t.Fatalf("expected empty region rebuild to report no rebuilt index")
	}

	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", "cards"))
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", "cards"))
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexKey("jp", "skills"))
	assertRedisKeyMissing(t, ctx, cache, cache.redisEntitySearchIndexVersionKey("jp", "skills"))
	assertRedisKeyMissing(t, ctx, cache, cache.redisRegionSearchIndexEntitiesKey("jp"))
}

func TestLoadRegionIndexFromRedisRemovesStaleEntityArtifacts(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	ctx := context.Background()
	writerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new writer redis cache: %v", err)
	}
	defer func() {
		_ = writerCache.Close()
	}()

	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
		"skills.json": []any{
			map[string]any{"id": 10, "name": "focus"},
		},
	}
	if err := writerCache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	if err := writerCache.client.Del(ctx, writerCache.redisEntityKey("jp", "skills")).Err(); err != nil {
		t.Fatalf("delete stale skills by-id hash: %v", err)
	}

	loadCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new load redis cache: %v", err)
	}
	defer func() {
		_ = loadCache.Close()
	}()

	loaded, err := loadCache.LoadRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		t.Fatalf("load region index from redis: %v", err)
	}
	if !loaded {
		t.Fatalf("expected load region index from redis to keep remaining entity")
	}

	assertRedisKeyExists(t, ctx, loadCache, loadCache.redisEntitySearchIndexKey("jp", "cards"))
	assertRedisKeyExists(t, ctx, loadCache, loadCache.redisEntitySearchIndexVersionKey("jp", "cards"))
	assertRedisKeyMissing(t, ctx, loadCache, loadCache.redisEntitySearchIndexKey("jp", "skills"))
	assertRedisKeyMissing(t, ctx, loadCache, loadCache.redisEntitySearchIndexVersionKey("jp", "skills"))
	assertRedisSetExcludes(t, ctx, loadCache, loadCache.redisRegionSearchIndexEntitiesKey("jp"), "skills")
	assertNoRetainedEntityIndex(t, loadCache, "jp", "skills")
}

func TestSearchRebuildRemovesStaleEntityArtifactsWhenByIDHashIsGone(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	ctx := context.Background()
	writerCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new writer redis cache: %v", err)
	}
	defer func() {
		_ = writerCache.Close()
	}()

	payload := map[string]any{
		"cards.json": []any{
			map[string]any{"id": 1, "prefix": "alpha"},
		},
	}
	if err := writerCache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store payload: %v", err)
	}

	if err := writerCache.client.Del(
		ctx,
		writerCache.redisEntityKey("jp", "cards"),
		writerCache.redisEntitySearchIndexKey("jp", "cards"),
	).Err(); err != nil {
		t.Fatalf("delete cards by-id hash and persisted index: %v", err)
	}

	searchCache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		t.Fatalf("new search redis cache: %v", err)
	}
	defer func() {
		_ = searchCache.Close()
	}()

	matches, err := searchCache.Search(ctx, "jp", "cards", "alpha", []string{"prefix"}, 5)
	if err != nil {
		t.Fatalf("search missing by-id hash: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected search with missing by-id hash to return no matches, got %d", len(matches))
	}

	assertRedisKeyMissing(t, ctx, searchCache, searchCache.redisEntitySearchIndexKey("jp", "cards"))
	assertRedisKeyMissing(t, ctx, searchCache, searchCache.redisEntitySearchIndexVersionKey("jp", "cards"))
	assertRedisSetExcludes(t, ctx, searchCache, searchCache.redisRegionSearchIndexEntitiesKey("jp"), "cards")
	assertNoRetainedEntityIndex(t, searchCache, "jp", "cards")
}

func BenchmarkRedisMasterDataCacheSearchIndexRetention(b *testing.B) {
	scenarios := []struct {
		name string
		run  func(context.Context, config.Config, map[string]any) (*RedisMasterDataCache, error)
	}{
		{name: "StoreRegion", run: benchmarkRetainedCacheAfterStoreRegion},
		{name: "SearchAfterRestart", run: benchmarkRetainedCacheAfterSearchRestart},
		{name: "LoadRegionIndexFromRedis", run: benchmarkRetainedCacheAfterLoadRegionIndex},
		{name: "RebuildRegionIndexFromRedis", run: benchmarkRetainedCacheAfterRebuildRegionIndex},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			for b.Loop() {
				cache := benchmarkRedisSearchIndexRetentionOnce(b, scenario.run)
				if redisSearchIndexBenchmarkSink != nil {
					_ = redisSearchIndexBenchmarkSink.Close()
				}
				redisSearchIndexBenchmarkSink = cache
			}

			runtime.GC()
			stat := retainedSearchIndexStat(b, redisSearchIndexBenchmarkSink, "jp", "cards")
			b.ReportMetric(float64(stat.RecordCount), "retained_records")
			b.ReportMetric(float64(stat.FieldCount), "retained_fields")
			b.ReportMetric(float64(stat.EntryCount), "retained_entries")
			b.ReportMetric(float64(stat.TextBlobBytes), "retained_text_blob_bytes")
			b.ReportMetric(float64(stat.ApproxSizeBytes), "retained_index_approx_bytes")
		})
	}
}

func BenchmarkRedisMasterDataCacheDecodedSearchIndexCache(b *testing.B) {
	scenarios := []struct {
		name         string
		cacheEntries int
		beforeSearch func(*RedisMasterDataCache)
	}{
		{name: "Disabled", cacheEntries: 0},
		{name: "ColdLRU", cacheEntries: 1, beforeSearch: func(cache *RedisMasterDataCache) {
			cache.invalidateCachedEntityIndex("jp", "cards")
		}},
		{name: "HotLRU", cacheEntries: 1},
	}

	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			cache := benchmarkDecodedSearchIndexCacheSetup(b, scenario.cacheEntries)
			defer func() {
				_ = cache.Close()
			}()
			ctx := context.Background()
			if scenario.name == "HotLRU" {
				if _, err := cache.Search(ctx, "jp", "cards", "benchmarkprefix000100", []string{"prefix"}, 10); err != nil {
					b.Fatalf("warm search cache: %v", err)
				}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if scenario.beforeSearch != nil {
					scenario.beforeSearch(cache)
				}
				matches, err := cache.Search(ctx, "jp", "cards", "benchmarkprefix000100", []string{"prefix"}, 10)
				if err != nil {
					b.Fatalf("search cards: %v", err)
				}
				if len(matches) != 1 {
					b.Fatalf("expected 1 search match, got %d", len(matches))
				}
			}
		})
	}
}

func readPersistedSearchIndex(
	t *testing.T,
	ctx context.Context,
	cache *RedisMasterDataCache,
	region string,
	entity string,
) entitySearchIndex {
	t.Helper()

	body, err := cache.client.Get(ctx, cache.redisEntitySearchIndexKey(region, entity)).Bytes()
	if err != nil {
		t.Fatalf("get persisted search index %s/%s: %v", region, entity, err)
	}

	var index entitySearchIndex
	if err := json.Unmarshal(body, &index); err != nil {
		t.Fatalf("decode persisted search index %s/%s: %v", region, entity, err)
	}

	return index
}

func assertIndexedFields(t *testing.T, index entitySearchIndex, wantFields []string) {
	t.Helper()

	if len(index.Fields) != len(wantFields) {
		t.Fatalf("indexed fields = %v, want %v", indexedFieldNames(index), wantFields)
	}
	for _, field := range wantFields {
		if _, ok := index.Fields[field]; !ok {
			t.Fatalf("indexed fields = %v, want field %q", indexedFieldNames(index), field)
		}
	}
}

func assertPersistedSearchIndex(t *testing.T, index entitySearchIndex, wantRecords int, wantFields []string) {
	t.Helper()

	if len(index.IDs) != wantRecords {
		t.Fatalf("persisted index ids = %d, want %d", len(index.IDs), wantRecords)
	}
	assertIndexedFields(t, index, wantFields)
	if len(index.TextBlob) == 0 {
		t.Fatalf("expected persisted index text blob")
	}
}

func indexedFieldNames(index entitySearchIndex) []string {
	fields := make([]string, 0, len(index.Fields))
	for field := range index.Fields {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	return fields
}

func redisSearchIndexBenchmarkPayload(recordCount int) map[string]any {
	records := make([]any, 0, recordCount)
	for id := range recordCount {
		records = append(records, map[string]any{
			"id":            id + 1,
			"name":          fmt.Sprintf("Benchmark Card Name %06d", id),
			"prefix":        fmt.Sprintf("Benchmark Prefix %06d", id),
			"cardSkillName": fmt.Sprintf("Benchmark Skill %06d", id),
		})
	}

	return map[string]any{
		"cards.json": records,
	}
}

func assertNoRetainedRegionIndex(t *testing.T, cache *RedisMasterDataCache, region string) {
	t.Helper()

	if cache.HasRegionIndex(region) {
		t.Fatalf("expected no retained decoded search index for region %s", region)
	}
	stats := cache.RegionIndexStats()
	for _, stat := range stats {
		if stat.Region == normalizeKey(region) {
			t.Fatalf("expected no retained region index stats for %s, got %+v", region, stat)
		}
	}
}

func assertRedisKeyExists(t *testing.T, ctx context.Context, cache *RedisMasterDataCache, key string) {
	t.Helper()

	exists, err := cache.client.Exists(ctx, key).Result()
	if err != nil {
		t.Fatalf("check redis key %s exists: %v", key, err)
	}
	if exists == 0 {
		t.Fatalf("expected redis key %s to exist", key)
	}
}

func assertRedisKeyMissing(t *testing.T, ctx context.Context, cache *RedisMasterDataCache, key string) {
	t.Helper()

	exists, err := cache.client.Exists(ctx, key).Result()
	if err != nil {
		t.Fatalf("check redis key %s missing: %v", key, err)
	}
	if exists != 0 {
		t.Fatalf("expected redis key %s to be missing", key)
	}
}

func assertRedisSetExcludes(t *testing.T, ctx context.Context, cache *RedisMasterDataCache, key string, member string) {
	t.Helper()

	isMember, err := cache.client.SIsMember(ctx, key, member).Result()
	if err != nil {
		t.Fatalf("check redis set %s excludes %s: %v", key, member, err)
	}
	if isMember {
		t.Fatalf("expected redis set %s to exclude %s", key, member)
	}
}

func assertRetainedEntityIndex(t *testing.T, cache *RedisMasterDataCache, region string, entity string) {
	t.Helper()

	_, found, err := cache.cachedEntityIndex(context.Background(), region, entity)
	if err != nil {
		t.Fatalf("read cached entity index: %v", err)
	}
	if !found {
		t.Fatalf("expected retained decoded search index for %s/%s", region, entity)
	}
}

func assertNoRetainedEntityIndex(t *testing.T, cache *RedisMasterDataCache, region string, entity string) {
	t.Helper()

	_, found, err := cache.cachedEntityIndex(context.Background(), region, entity)
	if err != nil {
		t.Fatalf("read cached entity index: %v", err)
	}
	if found {
		t.Fatalf("expected no retained decoded search index for %s/%s", region, entity)
	}
}

func benchmarkRedisSearchIndexRetentionOnce(
	b *testing.B,
	run func(context.Context, config.Config, map[string]any) (*RedisMasterDataCache, error),
) *RedisMasterDataCache {
	b.Helper()

	miniRedis, err := miniredis.Run()
	if err != nil {
		b.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cfg := config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "bench:master-data:",
	}
	cache, err := run(context.Background(), cfg, redisSearchIndexBenchmarkPayload(4096))
	if err != nil {
		b.Fatal(err)
	}

	return cache
}

func benchmarkDecodedSearchIndexCacheSetup(b *testing.B, cacheEntries int) *RedisMasterDataCache {
	b.Helper()

	miniRedis, err := miniredis.Run()
	if err != nil {
		b.Fatalf("start miniredis: %v", err)
	}
	b.Cleanup(miniRedis.Close)

	cfg := config.Config{
		RedisAddr:                         miniRedis.Addr(),
		RedisDB:                           0,
		MasterDataRedisKeyPrefix:          "bench:master-data:",
		MasterDataSearchIndexCacheEntries: cacheEntries,
	}
	ctx := context.Background()
	writerCache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "bench:master-data:",
	})
	if err != nil {
		b.Fatalf("new writer cache: %v", err)
	}
	if err := writerCache.StoreRegion(ctx, "jp", redisSearchIndexBenchmarkPayload(4096)); err != nil {
		_ = writerCache.Close()
		b.Fatalf("store region: %v", err)
	}
	if err := writerCache.Close(); err != nil {
		b.Fatalf("close writer cache: %v", err)
	}

	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		b.Fatalf("new search cache: %v", err)
	}
	return cache
}

func benchmarkRetainedCacheAfterStoreRegion(ctx context.Context, cfg config.Config, payload map[string]any) (*RedisMasterDataCache, error) {
	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		return nil, fmt.Errorf("new redis cache: %w", err)
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		_ = cache.Close()
		return nil, fmt.Errorf("store region: %w", err)
	}

	return cache, nil
}

func benchmarkRetainedCacheAfterSearchRestart(ctx context.Context, cfg config.Config, payload map[string]any) (*RedisMasterDataCache, error) {
	writerCache, err := benchmarkRetainedCacheAfterStoreRegion(ctx, cfg, payload)
	if err != nil {
		return nil, err
	}
	if err := writerCache.Close(); err != nil {
		return nil, fmt.Errorf("close writer cache: %w", err)
	}

	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		return nil, fmt.Errorf("new reader cache: %w", err)
	}
	if _, err := cache.Search(ctx, "jp", "cards", "benchmarkprefix000100", []string{"prefix"}, 10); err != nil {
		_ = cache.Close()
		return nil, fmt.Errorf("search after restart: %w", err)
	}

	return cache, nil
}

func benchmarkRetainedCacheAfterLoadRegionIndex(ctx context.Context, cfg config.Config, payload map[string]any) (*RedisMasterDataCache, error) {
	writerCache, err := benchmarkRetainedCacheAfterStoreRegion(ctx, cfg, payload)
	if err != nil {
		return nil, err
	}
	if err := writerCache.Close(); err != nil {
		return nil, fmt.Errorf("close writer cache: %w", err)
	}

	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		return nil, fmt.Errorf("new reader cache: %w", err)
	}
	loaded, err := cache.LoadRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		_ = cache.Close()
		return nil, fmt.Errorf("load region index from redis: %w", err)
	}
	if !loaded {
		_ = cache.Close()
		return nil, fmt.Errorf("load region index from redis: no index loaded")
	}

	return cache, nil
}

func benchmarkRetainedCacheAfterRebuildRegionIndex(ctx context.Context, cfg config.Config, payload map[string]any) (*RedisMasterDataCache, error) {
	writerCache, err := benchmarkRetainedCacheAfterStoreRegion(ctx, cfg, payload)
	if err != nil {
		return nil, err
	}
	if err := writerCache.client.Del(ctx, writerCache.redisEntitySearchIndexKey("jp", "cards"), writerCache.redisRegionSearchIndexEntitiesKey("jp")).Err(); err != nil {
		_ = writerCache.Close()
		return nil, fmt.Errorf("delete persisted search index: %w", err)
	}
	if err := writerCache.Close(); err != nil {
		return nil, fmt.Errorf("close writer cache: %w", err)
	}

	cache, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		return nil, fmt.Errorf("new rebuild cache: %w", err)
	}
	rebuilt, err := cache.RebuildRegionIndexFromRedis(ctx, "jp")
	if err != nil {
		_ = cache.Close()
		return nil, fmt.Errorf("rebuild region index from redis: %w", err)
	}
	if !rebuilt {
		_ = cache.Close()
		return nil, fmt.Errorf("rebuild region index from redis: no index rebuilt")
	}

	return cache, nil
}

func retainedSearchIndexStat(tb testing.TB, cache *RedisMasterDataCache, region string, entity string) RegionIndexStats {
	tb.Helper()

	if cache == nil {
		tb.Fatalf("expected retained cache")
	}
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)

	cache.mu.RLock()
	entityIndex := cache.index[regionName][entityName]
	cache.mu.RUnlock()

	if entityIndex == nil {
		return RegionIndexStats{Region: regionName}
	}

	stat := RegionIndexStats{
		Region:          regionName,
		Loaded:          true,
		EntityCount:     1,
		RecordCount:     len(entityIndex.IDs),
		FieldCount:      len(entityIndex.Fields),
		TextBlobBytes:   len(entityIndex.TextBlob),
		ApproxSizeBytes: len(entityName) + len(entityIndex.TextBlob),
	}
	for _, id := range entityIndex.IDs {
		stat.ApproxSizeBytes += len(id)
	}
	for field, items := range entityIndex.Fields {
		stat.EntryCount += len(items)
		stat.ApproxSizeBytes += len(field) + len(items)*12
	}

	return stat
}

func newVersionPayloadTestCache(tb testing.TB) (context.Context, *RedisMasterDataCache) {
	tb.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		tb.Fatalf("start miniredis: %v", err)
	}
	tb.Cleanup(mr.Close)

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                mr.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		tb.Fatalf("new redis cache: %v", err)
	}
	tb.Cleanup(func() { _ = cache.Close() })

	return context.Background(), cache
}

func newVersionPayloadTestCachePair(tb testing.TB) (context.Context, config.Config, *RedisMasterDataCache, *RedisMasterDataCache) {
	tb.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		tb.Fatalf("start miniredis: %v", err)
	}
	tb.Cleanup(mr.Close)

	cfg := config.Config{
		RedisAddr:                mr.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	}
	writer, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		tb.Fatalf("new writer cache: %v", err)
	}
	tb.Cleanup(func() { _ = writer.Close() })

	reader, err := NewRedisMasterDataCache(cfg)
	if err != nil {
		tb.Fatalf("new reader cache: %v", err)
	}
	tb.Cleanup(func() { _ = reader.Close() })

	return context.Background(), cfg, writer, reader
}

func TestStoreAndLoadRegionVersionPayload(t *testing.T) {
	ctx, cache := newVersionPayloadTestCache(t)

	version := map[string]any{
		"appVersion":   "3.2.1",
		"assetVersion": "3.2.1.10",
		"dataVersion":  "3.2.1.10",
		"cdnVersion":   1,
	}
	if err := cache.StoreRegionVersionPayload(ctx, "jp", version); err != nil {
		t.Fatalf("store version payload: %v", err)
	}

	loaded, found, err := cache.LoadRegionVersionPayload(ctx, "jp")
	if err != nil {
		t.Fatalf("load version payload: %v", err)
	}
	if !found {
		t.Fatalf("expected version payload to be found")
	}

	loadedMap, ok := loaded.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", loaded)
	}
	if loadedMap["appVersion"] != "3.2.1" {
		t.Fatalf("expected appVersion=3.2.1, got %v", loadedMap["appVersion"])
	}
	if loadedMap["cdnVersion"] != float64(1) {
		t.Fatalf("expected cdnVersion=1, got %v", loadedMap["cdnVersion"])
	}
}

func TestLoadRegionVersionPayloadMissingReturnsFalse(t *testing.T) {
	ctx, cache := newVersionPayloadTestCache(t)

	loaded, found, err := cache.LoadRegionVersionPayload(ctx, "en")
	if err != nil {
		t.Fatalf("expected no error on missing key, got %v", err)
	}
	if found {
		t.Fatalf("expected found=false for missing region version")
	}
	if loaded != nil {
		t.Fatalf("expected nil payload for missing region version, got %v", loaded)
	}
}

func TestVersionPayloadEmptyRegionReturnsErrorAndNothing(t *testing.T) {
	_, cache := newVersionPayloadTestCache(t)

	if err := cache.StoreRegionVersionPayload(context.Background(), "", map[string]any{"k": "v"}); err == nil {
		t.Fatalf("expected error for empty region")
	}

	loaded, found, err := cache.LoadRegionVersionPayload(context.Background(), "")
	if err != nil {
		t.Fatalf("expected no error on empty region, got %v", err)
	}
	if found {
		t.Fatalf("expected found=false for empty region")
	}
	if loaded != nil {
		t.Fatalf("expected nil payload for empty region")
	}
}

func TestStoreAndLoadVersionPayloadCrossInstance(t *testing.T) {
	ctx, _, writer, reader := newVersionPayloadTestCachePair(t)

	version := map[string]any{"appVersion": "1.0.0"}
	if err := writer.StoreRegionVersionPayload(ctx, "jp", version); err != nil {
		t.Fatalf("writer store version: %v", err)
	}

	loaded, found, err := reader.LoadRegionVersionPayload(ctx, "jp")
	if err != nil {
		t.Fatalf("reader load version: %v", err)
	}
	if !found {
		t.Fatalf("expected version to be found in reader")
	}
	loadedMap := loaded.(map[string]any)
	if loadedMap["appVersion"] != "1.0.0" {
		t.Fatalf("expected appVersion=1.0.0, got %v", loadedMap["appVersion"])
	}
}

func TestRedisMasterDataCachePing(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	cache, err := NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() { _ = cache.Close() }()

	if err := cache.Ping(context.Background()); err != nil {
		t.Fatalf("expected ping ok while redis is up, got %v", err)
	}

	// Simulate Redis becoming unreachable and ensure Ping reports the failure
	// so the readiness probe can mark the pod not ready.
	miniRedis.Close()
	if err := cache.Ping(context.Background()); err == nil {
		t.Fatalf("expected ping error after redis closed")
	}
}
