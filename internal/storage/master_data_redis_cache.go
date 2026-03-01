package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/redis/go-redis/v9"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/domain/masterdata"
)

type RedisMasterDataCache struct {
	client    *redis.Client
	keyPrefix string

	mu    sync.RWMutex
	index map[string]map[string]map[string][]searchIndexItem
}

type searchIndexItem struct {
	ID             string
	NormalizedText string
}

func NewRedisMasterDataCache(cfg config.Config) (*RedisMasterDataCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &RedisMasterDataCache{
		client:    client,
		keyPrefix: cfg.MasterDataRedisKeyPrefix,
		index:     make(map[string]map[string]map[string][]searchIndexItem),
	}, nil
}

func (cache *RedisMasterDataCache) StoreRegion(ctx context.Context, region string, payload map[string]any) error {
	regionName := normalizeKey(region)
	if regionName == "" {
		return errors.New("region is required")
	}

	nextIndex := make(map[string]map[string][]searchIndexItem)
	touchedEntities := make(map[string]struct{})
	for filePath, value := range payload {
		entity := entityNameFromPath(filePath)
		if entity == "" {
			continue
		}

		records, ok := value.([]any)
		if !ok {
			continue
		}

		touchedEntities[entity] = struct{}{}

		key := cache.redisEntityKey(regionName, entity)
		orderKey := cache.redisEntityOrderKey(regionName, entity)

		existingRecords, err := cache.client.HGetAll(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("hgetall redis key for region %s entity %s: %w", regionName, entity, err)
		}

		existingOrder, err := cache.client.LRange(ctx, orderKey, 0, -1).Result()
		if err != nil {
			return fmt.Errorf("lrange redis order key for region %s entity %s: %w", regionName, entity, err)
		}

		orderedIDs := make([]string, 0, len(records))
		nextRecords := make(map[string]string, len(records))

		for _, record := range records {
			recordMap, ok := record.(map[string]any)
			if !ok {
				continue
			}

			id := recordID(recordMap)
			if id == "" {
				continue
			}

			body, err := json.Marshal(recordMap)
			if err != nil {
				return fmt.Errorf("marshal record region %s entity %s id %s: %w", regionName, entity, id, err)
			}

			nextRecords[id] = string(body)
			orderedIDs = append(orderedIDs, id)

			searchable := searchableFields(recordMap)
			if len(searchable) == 0 {
				continue
			}

			if _, ok := nextIndex[entity]; !ok {
				nextIndex[entity] = make(map[string][]searchIndexItem)
			}

			for field, normalizedText := range searchable {
				nextIndex[entity][field] = append(nextIndex[entity][field], searchIndexItem{
					ID:             id,
					NormalizedText: normalizedText,
				})
			}
		}

		toUpsert := make(map[string]any)
		for id, body := range nextRecords {
			existingBody, exists := existingRecords[id]
			if !exists || existingBody != body {
				toUpsert[id] = body
			}
		}

		toDelete := make([]string, 0)
		for id := range existingRecords {
			if _, exists := nextRecords[id]; !exists {
				toDelete = append(toDelete, id)
			}
		}

		pipe := cache.client.Pipeline()
		if len(toUpsert) > 0 {
			pipe.HSet(ctx, key, toUpsert)
		}
		if len(toDelete) > 0 {
			pipe.HDel(ctx, key, toDelete...)
		}
		if !equalStringSlices(existingOrder, orderedIDs) {
			pipe.Del(ctx, orderKey)
			if len(orderedIDs) > 0 {
				values := make([]any, 0, len(orderedIDs))
				for _, id := range orderedIDs {
					values = append(values, id)
				}
				pipe.RPush(ctx, orderKey, values...)
			}
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("incremental update region %s entity %s: %w", regionName, entity, err)
		}
	}

	cache.mu.Lock()
	cache.index[regionName] = mergeRegionIndex(cache.index[regionName], nextIndex, touchedEntities)
	cache.mu.Unlock()

	return nil
}

func equalStringSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}

func mergeRegionIndex(
	existing map[string]map[string][]searchIndexItem,
	next map[string]map[string][]searchIndexItem,
	touched map[string]struct{},
) map[string]map[string][]searchIndexItem {
	merged := make(map[string]map[string][]searchIndexItem)
	for entity, fields := range existing {
		merged[entity] = fields
	}

	for entity := range touched {
		fields, ok := next[entity]
		if !ok || len(fields) == 0 {
			delete(merged, entity)
			continue
		}
		merged[entity] = fields
	}

	return merged
}

func (cache *RedisMasterDataCache) ListByPage(ctx context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return []map[string]any{}, 0, nil
	}

	orderKey := cache.redisEntityOrderKey(regionName, entityName)
	total, err := cache.client.LLen(ctx, orderKey).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("llen order ids region %s entity %s: %w", regionName, entityName, err)
	}
	if total <= 0 {
		return []map[string]any{}, 0, nil
	}

	start := int64((page - 1) * pageSize)
	if start >= total {
		return []map[string]any{}, int(total), nil
	}
	end := start + int64(pageSize) - 1

	ids, err := cache.client.LRange(ctx, orderKey, start, end).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("lrange order ids region %s entity %s: %w", regionName, entityName, err)
	}

	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		record, found, err := cache.GetByID(ctx, regionName, entityName, id)
		if err != nil {
			return nil, 0, err
		}
		if !found {
			continue
		}
		items = append(items, record)
	}

	return items, int(total), nil
}

func (cache *RedisMasterDataCache) GetByID(ctx context.Context, region string, entity string, id string) (map[string]any, bool, error) {
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	recordIDValue := strings.TrimSpace(id)
	if regionName == "" || entityName == "" || recordIDValue == "" {
		return nil, false, nil
	}

	body, err := cache.client.HGet(ctx, cache.redisEntityKey(regionName, entityName), recordIDValue).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("hget region %s entity %s id %s: %w", regionName, entityName, recordIDValue, err)
	}

	var record map[string]any
	if err := json.Unmarshal(body, &record); err != nil {
		return nil, false, fmt.Errorf("unmarshal record region %s entity %s id %s: %w", regionName, entityName, recordIDValue, err)
	}

	return record, true, nil
}

func (cache *RedisMasterDataCache) Search(ctx context.Context, region string, entity string, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	if limit <= 0 {
		limit = 20
	}

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	normalizedQuery := normalizeSearchText(query)
	if regionName == "" || entityName == "" || normalizedQuery == "" {
		return []masterdata.SearchMatch{}, nil
	}

	cache.mu.RLock()
	entityIndex := cache.index[regionName][entityName]
	cache.mu.RUnlock()

	if len(entityIndex) == 0 {
		return []masterdata.SearchMatch{}, nil
	}

	selectedFields := normalizeFields(fields)
	if len(selectedFields) == 0 {
		selectedFields = []string{"name"}
	}

	type scoredCandidate struct {
		ID           string
		Score        int
		MatchType    string
		MatchedField string
	}

	candidateMap := make(map[string]scoredCandidate)
	for _, field := range selectedFields {
		items := entityIndex[field]
		if len(items) == 0 {
			continue
		}

		for _, item := range items {
			score, matchType, matched := matchScore(item.NormalizedText, normalizedQuery)
			if !matched {
				continue
			}

			existing, exists := candidateMap[item.ID]
			if !exists || score > existing.Score {
				candidateMap[item.ID] = scoredCandidate{
					ID:           item.ID,
					Score:        score,
					MatchType:    matchType,
					MatchedField: field,
				}
			}
		}
	}

	candidates := make([]scoredCandidate, 0, len(candidateMap))
	for _, candidate := range candidateMap {
		candidates = append(candidates, candidate)
	}

	sort.Slice(candidates, func(i int, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].Score > candidates[j].Score
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	results := make([]masterdata.SearchMatch, 0, len(candidates))
	for _, candidate := range candidates {
		record, exists, err := cache.GetByID(ctx, regionName, entityName, candidate.ID)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}

		results = append(results, masterdata.SearchMatch{
			Item:         record,
			MatchScore:   candidate.Score,
			MatchType:    candidate.MatchType,
			MatchedField: candidate.MatchedField,
		})
	}

	return results, nil
}

func (cache *RedisMasterDataCache) RebuildRegionIndexFromRedis(ctx context.Context, region string) (bool, error) {
	regionName := normalizeKey(region)
	if regionName == "" {
		return false, nil
	}

	regionPrefix := cache.redisKey(regionName)
	pattern := regionPrefix + ":*:by-id"
	iterator := cache.client.Scan(ctx, 0, pattern, 0).Iterator()

	nextIndex := make(map[string]map[string][]searchIndexItem)
	hasRecords := false

	for iterator.Next(ctx) {
		key := iterator.Val()
		entity := entityNameFromByIDKey(regionPrefix, key)
		if entity == "" {
			continue
		}

		recordMap, err := cache.client.HGetAll(ctx, key).Result()
		if err != nil {
			return false, fmt.Errorf("hgetall region %s entity %s: %w", regionName, entity, err)
		}
		if len(recordMap) == 0 {
			continue
		}

		hasRecords = true
		for _, raw := range recordMap {
			var record map[string]any
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				return false, fmt.Errorf("decode redis record region %s entity %s: %w", regionName, entity, err)
			}

			searchable := searchableFields(record)
			if len(searchable) == 0 {
				continue
			}

			id := recordID(record)
			if id == "" {
				continue
			}

			if _, ok := nextIndex[entity]; !ok {
				nextIndex[entity] = make(map[string][]searchIndexItem)
			}

			for field, normalizedText := range searchable {
				nextIndex[entity][field] = append(nextIndex[entity][field], searchIndexItem{
					ID:             id,
					NormalizedText: normalizedText,
				})
			}
		}
	}

	if err := iterator.Err(); err != nil {
		return false, fmt.Errorf("scan redis keys region %s: %w", regionName, err)
	}

	if !hasRecords {
		return false, nil
	}

	cache.mu.Lock()
	cache.index[regionName] = nextIndex
	cache.mu.Unlock()

	return true, nil
}

func (cache *RedisMasterDataCache) Close() error {
	if cache.client == nil {
		return nil
	}

	return cache.client.Close()
}

func (cache *RedisMasterDataCache) redisKey(region string) string {
	cleanPrefix := strings.TrimSpace(cache.keyPrefix)
	if cleanPrefix == "" {
		cleanPrefix = "sekai:master-data:"
	}
	if !strings.HasSuffix(cleanPrefix, ":") {
		cleanPrefix += ":"
	}

	return cleanPrefix + strings.ToLower(strings.TrimSpace(region))
}

func (cache *RedisMasterDataCache) redisEntityKey(region string, entity string) string {
	return cache.redisKey(region) + ":" + normalizeKey(entity) + ":by-id"
}

func (cache *RedisMasterDataCache) redisEntityOrderKey(region string, entity string) string {
	return cache.redisKey(region) + ":" + normalizeKey(entity) + ":order"
}

func entityNameFromByIDKey(regionPrefix string, key string) string {
	prefix := regionPrefix + ":"
	suffix := ":by-id"

	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
		return ""
	}

	entity := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
	return normalizeKey(entity)
}

func normalizeKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func entityNameFromPath(filePath string) string {
	base := filepath.Base(strings.TrimSpace(filePath))
	if base == "" {
		return ""
	}
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return normalizeKey(name)
}

func recordID(record map[string]any) string {
	idValue, ok := record["id"]
	if !ok || idValue == nil {
		return ""
	}

	return strings.TrimSpace(fmt.Sprintf("%v", idValue))
}

func normalizeSearchText(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	builder := strings.Builder{}
	builder.Grow(len(trimmed))
	for _, r := range strings.ToLower(trimmed) {
		if unicode.IsSpace(r) {
			continue
		}
		builder.WriteRune(r)
	}

	return builder.String()
}

func searchableFields(record map[string]any) map[string]string {
	result := make(map[string]string)
	for key, value := range record {
		field := normalizeKey(key)
		if field == "" {
			continue
		}

		switch typed := value.(type) {
		case string:
			normalized := normalizeSearchText(typed)
			if normalized != "" {
				result[field] = normalized
			}
		case []any:
			parts := make([]string, 0, len(typed))
			for _, item := range typed {
				stringValue, ok := item.(string)
				if !ok {
					continue
				}
				normalized := normalizeSearchText(stringValue)
				if normalized == "" {
					continue
				}
				parts = append(parts, normalized)
			}
			if len(parts) > 0 {
				result[field] = strings.Join(parts, " ")
			}
		}
	}

	return result
}

func normalizeFields(fields []string) []string {
	if len(fields) == 0 {
		return nil
	}

	result := make([]string, 0, len(fields))
	seen := make(map[string]struct{})
	for _, field := range fields {
		normalized := normalizeKey(field)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}

	return result
}

func matchScore(candidate string, query string) (int, string, bool) {
	if candidate == "" || query == "" {
		return 0, "", false
	}

	if candidate == query {
		return 100, "exact", true
	}

	if strings.HasPrefix(candidate, query) {
		return 80, "prefix", true
	}

	index := strings.Index(candidate, query)
	if index < 0 {
		return 0, "", false
	}

	score := 60 - index
	if score < 30 {
		score = 30
	}
	return score, "contains", true
}
