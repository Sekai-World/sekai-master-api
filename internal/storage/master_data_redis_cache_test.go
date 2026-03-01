package storage

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"sekai-master-api/internal/config"
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
