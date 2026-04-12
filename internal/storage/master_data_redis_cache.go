package storage

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
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
	index map[string]map[string]*entitySearchIndex
}

type RegionIndexStats struct {
	Region          string
	Loaded          bool
	EntityCount     int
	RecordCount     int
	FieldCount      int
	EntryCount      int
	TextBlobBytes   int
	ApproxSizeBytes int
}

type RedisUsageStats struct {
	UsedMemoryBytes    int64
	UsedMemoryRSSBytes int64
	PeakMemoryBytes    int64
	KeyCount           int64
}

type entitySearchIndex struct {
	IDs      []string                     `json:"ids"`
	TextBlob string                       `json:"b"`
	Fields   map[string][]searchIndexItem `json:"fields"`
}

type searchIndexItem struct {
	IDIndex    uint32 `json:"i"`
	TextOffset uint32 `json:"o"`
	TextLength uint32 `json:"l"`
}

type legacySearchIndexItem struct {
	ID             string `json:"id"`
	NormalizedText string `json:"normalized_text"`
}

type legacyPersistedEntitySearchIndex struct {
	IDs    []string                                    `json:"ids"`
	Fields map[string][]legacyPersistedSearchIndexItem `json:"fields"`
}

type legacyPersistedSearchIndexItem struct {
	IDIndex        uint32 `json:"i"`
	NormalizedText string `json:"t"`
}

type textSpan struct {
	Offset uint32
	Length uint32
}

func (index *entitySearchIndex) idsValue(idIndex uint32) string {
	if index == nil {
		return ""
	}

	position := int(idIndex)
	if position < 0 || position >= len(index.IDs) {
		return ""
	}

	return index.IDs[position]
}

func (index *entitySearchIndex) textValue(item searchIndexItem) string {
	if index == nil {
		return ""
	}

	start := int(item.TextOffset)
	if start < 0 || start > len(index.TextBlob) {
		return ""
	}

	end := start + int(item.TextLength)
	if end < start || end > len(index.TextBlob) {
		return ""
	}

	return index.TextBlob[start:end]
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
		index:     make(map[string]map[string]*entitySearchIndex),
	}, nil
}

func (cache *RedisMasterDataCache) StoreRegion(ctx context.Context, region string, payload map[string]any) error {
	regionName := normalizeKey(region)
	if regionName == "" {
		return errors.New("region is required")
	}

	filePaths := make([]string, 0, len(payload))
	for filePath := range payload {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths)
	progressReporter := masterdata.ProgressReporterFromContext(ctx)
	totalFiles := len(filePaths)
	processedFiles := 0

	nextIndex := make(map[string]*entitySearchIndex)
	touchedEntities := make(map[string]struct{})
	for _, filePath := range filePaths {
		value := payload[filePath]
		entity := entityNameFromPath(filePath)
		if entity == "" {
			processedFiles++
			reportCacheWriteProgress(progressReporter, region, filePath, processedFiles, totalFiles)
			continue
		}

		records, ok := value.([]any)
		if !ok {
			processedFiles++
			reportCacheWriteProgress(progressReporter, region, filePath, processedFiles, totalFiles)
			continue
		}

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
		recordMaps := make([]map[string]any, 0, len(records))

		for _, record := range records {
			recordMap, ok := record.(map[string]any)
			if !ok {
				continue
			}

			body, err := json.Marshal(recordMap)
			if err != nil {
				return fmt.Errorf("marshal record region %s entity %s: %w", regionName, entity, err)
			}

			id := recordStorageID(recordMap, body)
			if id == "" {
				continue
			}

			nextRecords[id] = string(body)
			orderedIDs = append(orderedIDs, id)
			recordMaps = append(recordMaps, recordMap)
		}

		recordsChanged := !equalStringMaps(existingRecords, nextRecords)
		orderChanged := !equalStringSlices(existingOrder, orderedIDs)
		entityChanged := recordsChanged || orderChanged
		persistIndexChanged := entityChanged
		if entityChanged {
			touchedEntities[entity] = struct{}{}
			nextIndex[entity] = buildEntitySearchIndex(recordMaps)
		} else {
			cache.mu.RLock()
			regionIndex := cache.index[regionName]
			_, hasEntityIndex := regionIndex[entity]
			cache.mu.RUnlock()
			if !hasEntityIndex {
				fields, found, err := cache.readEntityIndexFromRedis(ctx, regionName, entity)
				if err != nil {
					return fmt.Errorf("load persisted search index region %s entity %s: %w", regionName, entity, err)
				}

				touchedEntities[entity] = struct{}{}
				if found {
					nextIndex[entity] = fields
				} else {
					nextIndex[entity] = buildEntitySearchIndex(recordMaps)
					persistIndexChanged = true
				}
			}
		}

		toUpsert := make(map[string]any)
		if recordsChanged {
			for id, body := range nextRecords {
				existingBody, exists := existingRecords[id]
				if !exists || existingBody != body {
					toUpsert[id] = body
				}
			}
		}

		toDelete := make([]string, 0)
		if recordsChanged {
			for id := range existingRecords {
				if _, exists := nextRecords[id]; !exists {
					toDelete = append(toDelete, id)
				}
			}
		}

		pipe := cache.client.Pipeline()
		if len(toUpsert) > 0 {
			pipe.HSet(ctx, key, toUpsert)
		}
		if len(toDelete) > 0 {
			pipe.HDel(ctx, key, toDelete...)
		}
		if orderChanged {
			pipe.Del(ctx, orderKey)
			if len(orderedIDs) > 0 {
				values := make([]any, 0, len(orderedIDs))
				for _, id := range orderedIDs {
					values = append(values, id)
				}
				pipe.RPush(ctx, orderKey, values...)
			}
		}
		if persistIndexChanged {
			cache.persistEntitySearchIndex(ctx, pipe, regionName, entity, nextIndex[entity])
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("incremental update region %s entity %s: %w", regionName, entity, err)
		}

		processedFiles++
		reportCacheWriteProgress(progressReporter, region, filePath, processedFiles, totalFiles)
	}

	cache.mu.Lock()
	cache.index[regionName] = mergeRegionIndex(cache.index[regionName], nextIndex, touchedEntities)
	cache.mu.Unlock()

	return nil
}

func equalStringMaps(left map[string]string, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}

	for key, leftValue := range left {
		if rightValue, ok := right[key]; !ok || leftValue != rightValue {
			return false
		}
	}

	return true
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

func reportCacheWriteProgress(reporter masterdata.ProgressReporter, region string, filePath string, processedFiles int, totalFiles int) {
	if reporter == nil {
		return
	}

	reporter(masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         strings.TrimSpace(region),
		Phase:          "cache",
		Message:        "writing cache",
		FilePath:       strings.TrimSpace(filePath),
		FileCount:      totalFiles,
		ProcessedFiles: processedFiles,
		TotalFiles:     totalFiles,
		UpdatedAt:      time.Now().UTC(),
	})
}

func mergeRegionIndex(
	existing map[string]*entitySearchIndex,
	next map[string]*entitySearchIndex,
	touched map[string]struct{},
) map[string]*entitySearchIndex {
	merged := make(map[string]*entitySearchIndex)
	for entity, fields := range existing {
		merged[entity] = fields
	}

	for entity := range touched {
		fields, ok := next[entity]
		if !ok || fields == nil || len(fields.Fields) == 0 {
			delete(merged, entity)
			continue
		}
		merged[entity] = fields
	}

	return merged
}

func buildEntitySearchIndex(records []map[string]any) *entitySearchIndex {
	index := &entitySearchIndex{
		IDs:    make([]string, 0, len(records)),
		Fields: make(map[string][]searchIndexItem),
	}
	idIndexes := make(map[string]uint32, len(records))
	textRefs := make(map[string]textSpan)
	textBlob := strings.Builder{}

	for _, recordMap := range records {
		if recordMap == nil {
			continue
		}

		id := recordID(recordMap)
		if id == "" {
			body, err := json.Marshal(recordMap)
			if err != nil {
				continue
			}
			id = recordStorageID(recordMap, body)
			if id == "" {
				continue
			}
		}

		idIndex, exists := idIndexes[id]
		if !exists {
			idIndex = uint32(len(index.IDs))
			index.IDs = append(index.IDs, id)
			idIndexes[id] = idIndex
		}

		searchable := searchableFields(recordMap)
		for field, normalizedText := range searchable {
			span, ok := textRefs[normalizedText]
			if !ok {
				offset := uint32(textBlob.Len())
				textBlob.WriteString(normalizedText)
				span = textSpan{
					Offset: offset,
					Length: uint32(len(normalizedText)),
				}
				textRefs[normalizedText] = span
			}

			index.Fields[field] = append(index.Fields[field], searchIndexItem{
				IDIndex:    idIndex,
				TextOffset: span.Offset,
				TextLength: span.Length,
			})
		}
	}

	if len(index.IDs) == 0 || len(index.Fields) == 0 {
		return nil
	}
	index.TextBlob = textBlob.String()

	return index
}

func compactLegacySearchIndex(fields map[string][]legacySearchIndexItem) *entitySearchIndex {
	if len(fields) == 0 {
		return nil
	}

	index := &entitySearchIndex{
		IDs:    make([]string, 0),
		Fields: make(map[string][]searchIndexItem, len(fields)),
	}
	idIndexes := make(map[string]uint32)
	textRefs := make(map[string]textSpan)
	textBlob := strings.Builder{}

	for field, items := range fields {
		if len(items) == 0 {
			continue
		}

		compactItems := make([]searchIndexItem, 0, len(items))
		for _, item := range items {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}

			idIndex, exists := idIndexes[id]
			if !exists {
				idIndex = uint32(len(index.IDs))
				index.IDs = append(index.IDs, id)
				idIndexes[id] = idIndex
			}

			span, ok := textRefs[item.NormalizedText]
			if !ok {
				offset := uint32(textBlob.Len())
				textBlob.WriteString(item.NormalizedText)
				span = textSpan{
					Offset: offset,
					Length: uint32(len(item.NormalizedText)),
				}
				textRefs[item.NormalizedText] = span
			}

			compactItems = append(compactItems, searchIndexItem{
				IDIndex:    idIndex,
				TextOffset: span.Offset,
				TextLength: span.Length,
			})
		}
		if len(compactItems) > 0 {
			index.Fields[field] = compactItems
		}
	}

	if len(index.IDs) == 0 || len(index.Fields) == 0 {
		return nil
	}
	index.TextBlob = textBlob.String()

	return index
}

func compactPersistedSearchIndex(index legacyPersistedEntitySearchIndex) *entitySearchIndex {
	if len(index.IDs) == 0 || len(index.Fields) == 0 {
		return nil
	}

	compact := &entitySearchIndex{
		IDs:    append([]string(nil), index.IDs...),
		Fields: make(map[string][]searchIndexItem, len(index.Fields)),
	}
	textRefs := make(map[string]textSpan)
	textBlob := strings.Builder{}

	for field, items := range index.Fields {
		if len(items) == 0 {
			continue
		}

		compactItems := make([]searchIndexItem, 0, len(items))
		for _, item := range items {
			if int(item.IDIndex) >= len(compact.IDs) {
				continue
			}

			span, ok := textRefs[item.NormalizedText]
			if !ok {
				offset := uint32(textBlob.Len())
				textBlob.WriteString(item.NormalizedText)
				span = textSpan{
					Offset: offset,
					Length: uint32(len(item.NormalizedText)),
				}
				textRefs[item.NormalizedText] = span
			}

			compactItems = append(compactItems, searchIndexItem{
				IDIndex:    item.IDIndex,
				TextOffset: span.Offset,
				TextLength: span.Length,
			})
		}
		if len(compactItems) > 0 {
			compact.Fields[field] = compactItems
		}
	}

	if len(compact.Fields) == 0 {
		return nil
	}

	compact.TextBlob = textBlob.String()
	return compact
}

func isValidEntitySearchIndex(index *entitySearchIndex) bool {
	if index == nil || len(index.IDs) == 0 || len(index.Fields) == 0 {
		return false
	}

	for _, items := range index.Fields {
		if len(items) == 0 {
			continue
		}
		for _, item := range items {
			if int(item.IDIndex) >= len(index.IDs) {
				return false
			}
			start := int(item.TextOffset)
			end := start + int(item.TextLength)
			if start < 0 || end < start || end > len(index.TextBlob) {
				return false
			}
		}
	}

	return true
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

	byIDCount, err := cache.client.HLen(ctx, cache.redisEntityKey(regionName, entityName)).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("hlen by-id region %s entity %s: %w", regionName, entityName, err)
	}
	if byIDCount > total {
		rebuiltIDs, rebuildErr := cache.rebuildEntityOrderFromByID(ctx, regionName, entityName)
		if rebuildErr != nil {
			return nil, 0, rebuildErr
		}
		total = int64(len(rebuiltIDs))
	}

	if total <= 0 {
		rebuiltIDs, rebuildErr := cache.rebuildEntityOrderFromByID(ctx, regionName, entityName)
		if rebuildErr != nil {
			return nil, 0, rebuildErr
		}
		if len(rebuiltIDs) == 0 {
			return []map[string]any{}, 0, nil
		}

		total = int64(len(rebuiltIDs))
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

func (cache *RedisMasterDataCache) rebuildEntityOrderFromByID(ctx context.Context, region string, entity string) ([]string, error) {
	recordMap, err := cache.client.HGetAll(ctx, cache.redisEntityKey(region, entity)).Result()
	if err != nil {
		return nil, fmt.Errorf("hgetall by-id region %s entity %s: %w", region, entity, err)
	}
	if len(recordMap) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(recordMap))
	for id := range recordMap {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		ids = append(ids, trimmed)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	sort.Slice(ids, func(i int, j int) bool {
		leftInt, leftErr := strconv.ParseInt(ids[i], 10, 64)
		rightInt, rightErr := strconv.ParseInt(ids[j], 10, 64)
		if leftErr == nil && rightErr == nil {
			return leftInt < rightInt
		}

		return ids[i] < ids[j]
	})

	orderKey := cache.redisEntityOrderKey(region, entity)
	pipe := cache.client.Pipeline()
	pipe.Del(ctx, orderKey)
	values := make([]any, 0, len(ids))
	for _, id := range ids {
		values = append(values, id)
	}
	pipe.RPush(ctx, orderKey, values...)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("rebuild order region %s entity %s: %w", region, entity, err)
	}

	return ids, nil
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

	if entityIndex == nil || len(entityIndex.Fields) == 0 {
		loaded, err := cache.loadEntityIndexFromRedis(ctx, regionName, entityName)
		if err != nil {
			return nil, err
		}
		if loaded {
			cache.mu.RLock()
			entityIndex = cache.index[regionName][entityName]
			cache.mu.RUnlock()
		}
	}

	if entityIndex == nil || len(entityIndex.Fields) == 0 {
		rebuilt, err := cache.RebuildRegionIndexFromRedis(ctx, regionName)
		if err != nil {
			return nil, err
		}
		if !rebuilt {
			return []masterdata.SearchMatch{}, nil
		}

		cache.mu.RLock()
		entityIndex = cache.index[regionName][entityName]
		cache.mu.RUnlock()
		if entityIndex == nil || len(entityIndex.Fields) == 0 {
			return []masterdata.SearchMatch{}, nil
		}
	}

	selectedFields := normalizeFields(fields)
	if len(selectedFields) == 0 {
		selectedFields = []string{"name"}
	}

	type scoredCandidate struct {
		IDIndex      uint32
		Score        int
		MatchType    string
		MatchedField string
	}

	candidateMap := make(map[uint32]scoredCandidate)
	for _, field := range selectedFields {
		items := entityIndex.Fields[field]
		if len(items) == 0 {
			continue
		}

		for _, item := range items {
			score, matchType, matched := matchScore(entityIndex.textValue(item), normalizedQuery)
			if !matched {
				continue
			}

			existing, exists := candidateMap[item.IDIndex]
			if !exists || score > existing.Score {
				candidateMap[item.IDIndex] = scoredCandidate{
					IDIndex:      item.IDIndex,
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
			return entityIndex.idsValue(candidates[i].IDIndex) < entityIndex.idsValue(candidates[j].IDIndex)
		}
		return candidates[i].Score > candidates[j].Score
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	results := make([]masterdata.SearchMatch, 0, len(candidates))
	for _, candidate := range candidates {
		recordID := entityIndex.idsValue(candidate.IDIndex)
		if recordID == "" {
			continue
		}

		record, exists, err := cache.GetByID(ctx, regionName, entityName, recordID)
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

func (cache *RedisMasterDataCache) LoadRegionIndexFromRedis(ctx context.Context, region string) (bool, error) {
	regionName := normalizeKey(region)
	if regionName == "" {
		return false, nil
	}

	entities, err := cache.client.SMembers(ctx, cache.redisRegionSearchIndexEntitiesKey(regionName)).Result()
	if err != nil {
		return false, fmt.Errorf("list persisted search index entities region %s: %w", regionName, err)
	}
	if len(entities) == 0 {
		return false, nil
	}

	nextIndex := make(map[string]*entitySearchIndex, len(entities))
	loaded := false
	for _, entity := range entities {
		fields, found, err := cache.readEntityIndexFromRedis(ctx, regionName, entity)
		if err != nil {
			return false, err
		}
		if !found {
			continue
		}
		nextIndex[entity] = fields
		loaded = true
	}

	if !loaded {
		return false, nil
	}

	cache.mu.Lock()
	cache.index[regionName] = nextIndex
	cache.mu.Unlock()

	return true, nil
}

func (cache *RedisMasterDataCache) HasRegionIndex(region string) bool {
	regionName := normalizeKey(region)
	if regionName == "" {
		return false
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	entityIndex := cache.index[regionName]
	if len(entityIndex) == 0 {
		return false
	}

	for _, fields := range entityIndex {
		if fields == nil {
			continue
		}
		for _, items := range fields.Fields {
			if len(items) > 0 {
				return true
			}
		}
	}

	return false
}

func (cache *RedisMasterDataCache) RebuildRegionIndexFromRedis(ctx context.Context, region string) (bool, error) {
	regionName := normalizeKey(region)
	if regionName == "" {
		return false, nil
	}

	regionPrefix := cache.redisKey(regionName)
	pattern := regionPrefix + ":*:by-id"
	iterator := cache.client.Scan(ctx, 0, pattern, 0).Iterator()

	nextIndex := make(map[string]*entitySearchIndex)
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
		entityIndex := &entitySearchIndex{
			IDs:    make([]string, 0, len(recordMap)),
			Fields: make(map[string][]searchIndexItem),
		}
		idIndexes := make(map[string]uint32, len(recordMap))
		textRefs := make(map[string]textSpan)
		textBlob := strings.Builder{}
		for id, raw := range recordMap {
			if strings.TrimSpace(id) == "" {
				continue
			}

			var record map[string]any
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				return false, fmt.Errorf("decode redis record region %s entity %s: %w", regionName, entity, err)
			}

			searchable := searchableFields(record)
			if len(searchable) == 0 {
				continue
			}

			idIndex, exists := idIndexes[id]
			if !exists {
				idIndex = uint32(len(entityIndex.IDs))
				entityIndex.IDs = append(entityIndex.IDs, id)
				idIndexes[id] = idIndex
			}

			for field, normalizedText := range searchable {
				span, ok := textRefs[normalizedText]
				if !ok {
					offset := uint32(textBlob.Len())
					textBlob.WriteString(normalizedText)
					span = textSpan{
						Offset: offset,
						Length: uint32(len(normalizedText)),
					}
					textRefs[normalizedText] = span
				}

				entityIndex.Fields[field] = append(entityIndex.Fields[field], searchIndexItem{
					IDIndex:    idIndex,
					TextOffset: span.Offset,
					TextLength: span.Length,
				})
			}
		}
		if len(entityIndex.IDs) > 0 && len(entityIndex.Fields) > 0 {
			entityIndex.TextBlob = textBlob.String()
			nextIndex[entity] = entityIndex
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

	existingEntities, err := cache.client.SMembers(ctx, cache.redisRegionSearchIndexEntitiesKey(regionName)).Result()
	if err != nil {
		return false, fmt.Errorf("list persisted search index entities before rebuild region %s: %w", regionName, err)
	}

	pipe := cache.client.Pipeline()
	pipe.Del(ctx, cache.redisRegionSearchIndexEntitiesKey(regionName))

	nextEntities := make(map[string]struct{}, len(nextIndex))
	for entity, fields := range nextIndex {
		nextEntities[entity] = struct{}{}
		cache.persistEntitySearchIndex(ctx, pipe, regionName, entity, fields)
	}
	for _, entity := range existingEntities {
		if _, ok := nextEntities[normalizeKey(entity)]; ok {
			continue
		}
		pipe.Del(ctx, cache.redisEntitySearchIndexKey(regionName, entity))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("persist rebuilt search indexes region %s: %w", regionName, err)
	}

	return true, nil
}

func (cache *RedisMasterDataCache) Close() error {
	if cache.client == nil {
		return nil
	}

	return cache.client.Close()
}

func (cache *RedisMasterDataCache) RegionIndexStats() []RegionIndexStats {
	if cache == nil {
		return nil
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	regions := make([]string, 0, len(cache.index))
	for region := range cache.index {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	stats := make([]RegionIndexStats, 0, len(regions))
	for _, region := range regions {
		entityIndexes := cache.index[region]
		stat := RegionIndexStats{
			Region: region,
			Loaded: len(entityIndexes) > 0,
		}

		for entityName, index := range entityIndexes {
			if index == nil {
				continue
			}

			stat.EntityCount++
			stat.RecordCount += len(index.IDs)
			stat.FieldCount += len(index.Fields)
			stat.TextBlobBytes += len(index.TextBlob)
			stat.ApproxSizeBytes += len(entityName) + len(index.TextBlob)

			for _, id := range index.IDs {
				stat.ApproxSizeBytes += len(id)
			}

			for field, items := range index.Fields {
				stat.EntryCount += len(items)
				stat.ApproxSizeBytes += len(field) + len(items)*12
			}
		}

		stats = append(stats, stat)
	}

	return stats
}

func (cache *RedisMasterDataCache) RedisUsageStats(ctx context.Context) (RedisUsageStats, error) {
	if cache == nil || cache.client == nil {
		return RedisUsageStats{}, nil
	}

	info, err := cache.client.Info(ctx, "memory").Result()
	if err != nil {
		return RedisUsageStats{}, err
	}

	keyCount, err := cache.client.DBSize(ctx).Result()
	if err != nil {
		return RedisUsageStats{}, err
	}

	return RedisUsageStats{
		UsedMemoryBytes:    parseRedisInfoInt64(info, "used_memory"),
		UsedMemoryRSSBytes: parseRedisInfoInt64(info, "used_memory_rss"),
		PeakMemoryBytes:    parseRedisInfoInt64(info, "used_memory_peak"),
		KeyCount:           keyCount,
	}, nil
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

func (cache *RedisMasterDataCache) redisEntitySearchIndexKey(region string, entity string) string {
	return cache.redisKey(region) + ":" + normalizeKey(entity) + ":search-index"
}

func (cache *RedisMasterDataCache) redisRegionSearchIndexEntitiesKey(region string) string {
	return cache.redisKey(region) + ":search-index:entities"
}

func (cache *RedisMasterDataCache) persistEntitySearchIndex(ctx context.Context, pipe redis.Pipeliner, region string, entity string, index *entitySearchIndex) {
	entityName := normalizeKey(entity)
	if entityName == "" {
		return
	}

	indexKey := cache.redisEntitySearchIndexKey(region, entityName)
	entitiesKey := cache.redisRegionSearchIndexEntitiesKey(region)
	if index == nil || len(index.IDs) == 0 || len(index.Fields) == 0 {
		pipe.Del(ctx, indexKey)
		pipe.SRem(ctx, entitiesKey, entityName)
		return
	}

	body, err := json.Marshal(index)
	if err != nil {
		return
	}

	pipe.Set(ctx, indexKey, body, 0)
	pipe.SAdd(ctx, entitiesKey, entityName)
}

func (cache *RedisMasterDataCache) loadEntityIndexFromRedis(ctx context.Context, region string, entity string) (bool, error) {
	index, found, err := cache.readEntityIndexFromRedis(ctx, region, entity)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	cache.mu.Lock()
	if _, ok := cache.index[region]; !ok {
		cache.index[region] = make(map[string]*entitySearchIndex)
	}
	cache.index[region][normalizeKey(entity)] = index
	cache.mu.Unlock()

	return true, nil
}

func (cache *RedisMasterDataCache) readEntityIndexFromRedis(ctx context.Context, region string, entity string) (*entitySearchIndex, bool, error) {
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return nil, false, nil
	}

	body, err := cache.client.Get(ctx, cache.redisEntitySearchIndexKey(regionName, entityName)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get persisted search index region %s entity %s: %w", regionName, entityName, err)
	}

	var index entitySearchIndex
	if err := json.Unmarshal(body, &index); err == nil {
		if isValidEntitySearchIndex(&index) {
			return &index, true, nil
		}
	}

	var legacyPersisted legacyPersistedEntitySearchIndex
	if err := json.Unmarshal(body, &legacyPersisted); err == nil {
		compact := compactPersistedSearchIndex(legacyPersisted)
		if compact != nil {
			return compact, true, nil
		}
	}

	var legacyFields map[string][]legacySearchIndexItem
	if err := json.Unmarshal(body, &legacyFields); err != nil {
		return nil, false, fmt.Errorf("decode persisted search index region %s entity %s: %w", regionName, entityName, err)
	}

	legacyIndex := compactLegacySearchIndex(legacyFields)
	if legacyIndex == nil {
		return nil, false, nil
	}

	return legacyIndex, true, nil
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

func recordStorageID(record map[string]any, body []byte) string {
	id := recordID(record)
	if id != "" {
		return id
	}

	if len(body) == 0 {
		return ""
	}

	sum := sha1.Sum(body)
	return "auto:" + hex.EncodeToString(sum[:])
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

func parseRedisInfoInt64(info string, key string) int64 {
	for _, line := range strings.Split(info, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		left, right, found := strings.Cut(trimmed, ":")
		if !found || strings.TrimSpace(left) != key {
			continue
		}

		value, err := strconv.ParseInt(strings.TrimSpace(right), 10, 64)
		if err != nil {
			return 0
		}

		return value
	}

	return 0
}

func searchableFields(record map[string]any) map[string]string {
	result := make(map[string]string)
	for key, value := range record {
		field := normalizeKey(key)
		if field == "" {
			continue
		}

		switch typed := value.(type) {
		case []any:
			parts := make([]string, 0, len(typed))
			for _, item := range typed {
				normalized, ok := normalizeSearchValue(item)
				if !ok {
					continue
				}
				parts = append(parts, normalized)
			}
			if len(parts) > 0 {
				result[field] = strings.Join(parts, " ")
			}
		default:
			normalized, ok := normalizeSearchValue(value)
			if ok {
				result[field] = normalized
			}
		}
	}

	return result
}

func normalizeSearchValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		normalized := normalizeSearchText(typed)
		if normalized == "" {
			return "", false
		}
		return normalized, true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return "", false
		}
		if typed == math.Trunc(typed) {
			return strconv.FormatInt(int64(typed), 10), true
		}
		return normalizeSearchText(strconv.FormatFloat(typed, 'f', -1, 64)), true
	case float32:
		floatValue := float64(typed)
		if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) {
			return "", false
		}
		if floatValue == math.Trunc(floatValue) {
			return strconv.FormatInt(int64(floatValue), 10), true
		}
		return normalizeSearchText(strconv.FormatFloat(floatValue, 'f', -1, 32)), true
	case int:
		return strconv.Itoa(typed), true
	case int8:
		return strconv.FormatInt(int64(typed), 10), true
	case int16:
		return strconv.FormatInt(int64(typed), 10), true
	case int32:
		return strconv.FormatInt(int64(typed), 10), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case uint:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint64:
		return strconv.FormatUint(typed, 10), true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
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
