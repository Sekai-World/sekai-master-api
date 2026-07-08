package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/domain/masterdata"
)

var redisSearchIndexBenchmarkSink *RedisMasterDataCache

func TestStoreRegionIncrementalUpdate(t *testing.T) {
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

func TestSearchIndexPersistsOnlySearchableFields(t *testing.T) {
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
