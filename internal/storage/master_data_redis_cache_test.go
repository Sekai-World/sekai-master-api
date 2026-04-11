package storage

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/domain/masterdata"
)

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

	skillMatches, err := cache.Search(ctx, "jp", "skills", "focus", []string{"name"}, 10)
	if err != nil {
		t.Fatalf("search skills: %v", err)
	}
	if len(skillMatches) != 1 {
		t.Fatalf("expected untouched skills entity to stay searchable, got %d matches", len(skillMatches))
	}
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

func TestSearchRebuildsIndexFromRedisAfterRestart(t *testing.T) {
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
	if !readerCache.HasRegionIndex("jp") {
		t.Fatalf("expected in-memory index after preload")
	}

	matches, err := readerCache.Search(ctx, "jp", "unitprofiles", "theme_park", []string{"unit"}, 10)
	if err != nil {
		t.Fatalf("search unitprofiles after preload: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 unitprofiles match after preload, got %d", len(matches))
	}
}

func TestStoreRegionReusesPersistedIndexWhenEntityIsUnchanged(t *testing.T) {
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
	if !readerCache.HasRegionIndex("jp") {
		t.Fatalf("expected unchanged store to restore in-memory index")
	}

	matches, err := readerCache.Search(context.Background(), "jp", "unitprofiles", "theme_park", []string{"unit"}, 10)
	if err != nil {
		t.Fatalf("search after unchanged store: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 unitprofiles match after unchanged store, got %d", len(matches))
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
