package storage

import (
	"container/list"
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
	"go.opentelemetry.io/otel/attribute"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/tracing"
)

type RedisMasterDataCache struct {
	client                  *redis.Client
	keyPrefix               string
	fileConcurrency         int
	searchIndexCacheEntries int

	mu           sync.RWMutex
	index        map[string]map[string]*entitySearchIndex
	indexOrder   *list.List
	indexEntries map[string]*list.Element
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

type cachedEntitySearchIndex struct {
	region  string
	entity  string
	version string
	index   *entitySearchIndex
}

var defaultSearchableFields = map[string]struct{}{
	"name": {},
}

var entitySearchableFields = map[string]map[string]struct{}{
	"cards": {
		"cardskillname": {},
		"prefix":        {},
	},
}

var relationshipSearchableFields = map[string]struct{}{
	"cardid":         {},
	"cardraritytype": {},
	"eventid":        {},
	"eventstoryid":   {},
	"musicid":        {},
	"unit":           {},
	"virtualliveid":  {},
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
	fileConcurrency := cfg.MasterDataFileConcurrency
	if fileConcurrency <= 0 {
		fileConcurrency = 8
	}

	return &RedisMasterDataCache{
		client:                  client,
		keyPrefix:               cfg.MasterDataRedisKeyPrefix,
		fileConcurrency:         fileConcurrency,
		searchIndexCacheEntries: cfg.EffectiveSearchIndexCacheEntries(),
		index:                   make(map[string]map[string]*entitySearchIndex),
		indexOrder:              list.New(),
		indexEntries:            make(map[string]*list.Element),
	}, nil
}

func (cache *RedisMasterDataCache) StoreRegion(ctx context.Context, region string, payload map[string]any) error {
	ctx, span := tracing.StartSpan(ctx, "redis.master_data.store_region", attribute.String("region", normalizeKey(region)), attribute.Int("file.count", len(payload)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	regionName := normalizeKey(region)
	if regionName == "" {
		err = errors.New("region is required")
		return err
	}

	filePaths := make([]string, 0, len(payload))
	for filePath := range payload {
		filePaths = append(filePaths, filePath)
	}
	sort.Strings(filePaths)
	progressReporter := masterdata.ProgressReporterFromContext(ctx)
	totalFiles := len(filePaths)
	processedFiles := 0

	entityLocks := make(map[string]*sync.Mutex)
	for _, filePath := range filePaths {
		if entity := entityNameFromPath(filePath); entity != "" {
			if _, exists := entityLocks[entity]; !exists {
				entityLocks[entity] = &sync.Mutex{}
			}
		}
	}

	progressMu := sync.Mutex{}
	reportProgress := func(filePath string) {
		progressMu.Lock()
		processedFiles++
		currentProcessedFiles := processedFiles
		reportCacheWriteProgress(progressReporter, region, filePath, currentProcessedFiles, totalFiles)
		progressMu.Unlock()
	}

	resultMu := sync.Mutex{}
	var firstErr error
	recordErr := func(err error) {
		resultMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		resultMu.Unlock()
	}

	processFile := func(filePath string) error {
		value := payload[filePath]
		entity := entityNameFromPath(filePath)
		if entity == "" {
			reportProgress(filePath)
			return nil
		}

		records, ok := value.([]any)
		if !ok {
			reportProgress(filePath)
			return nil
		}

		entityLock := entityLocks[entity]
		entityLock.Lock()
		defer entityLock.Unlock()

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
		var updatedIndex *entitySearchIndex
		updatedIndexVersion := ""
		if entityChanged {
			updatedIndex = buildEntitySearchIndex(entity, recordMaps)
		} else {
			found, err := cache.persistedEntitySearchIndexExists(ctx, regionName, entity)
			if err != nil {
				return fmt.Errorf("check persisted search index region %s entity %s: %w", regionName, entity, err)
			}
			if !found {
				updatedIndex = buildEntitySearchIndex(entity, recordMaps)
				persistIndexChanged = true
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
			updatedIndexVersion = cache.persistEntitySearchIndex(ctx, pipe, regionName, entity, updatedIndex)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("incremental update region %s entity %s: %w", regionName, entity, err)
		}
		if updatedIndex != nil {
			cache.setCachedEntityIndex(regionName, entity, updatedIndexVersion, updatedIndex)
		} else {
			cache.invalidateCachedEntityIndex(regionName, entity)
		}

		reportProgress(filePath)
		return nil
	}

	effectiveConcurrency := cache.fileConcurrency
	if effectiveConcurrency <= 0 {
		effectiveConcurrency = 1
	}
	if effectiveConcurrency > totalFiles && totalFiles > 0 {
		effectiveConcurrency = totalFiles
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, effectiveConcurrency)
	for _, filePath := range filePaths {
		semaphore <- struct{}{}
		filePath := filePath
		wg.Go(func() {
			defer func() {
				<-semaphore
			}()

			if err := processFile(filePath); err != nil {
				recordErr(err)
			}
		})
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}

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

func buildEntitySearchIndex(entity string, records []map[string]any) *entitySearchIndex {
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

		searchable := searchableFields(entity, recordMap)
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

func compactLegacySearchIndex(entity string, fields map[string][]legacySearchIndexItem) *entitySearchIndex {
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
		field = normalizeKey(field)
		if !isSearchableField(entity, field) {
			continue
		}
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

func compactPersistedSearchIndex(entity string, index legacyPersistedEntitySearchIndex) *entitySearchIndex {
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
		field = normalizeKey(field)
		if !isSearchableField(entity, field) {
			continue
		}
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

func (cache *RedisMasterDataCache) compactLegacyStringIDSearchIndex(
	ctx context.Context,
	region string,
	entity string,
	fields map[string][]string,
) (*entitySearchIndex, error) {
	if len(fields) == 0 {
		return nil, nil
	}

	ids := make([]string, 0)
	seenIDs := make(map[string]struct{})
	for field, fieldIDs := range fields {
		field = normalizeKey(field)
		if !isSearchableField(entity, field) {
			continue
		}

		for _, rawID := range fieldIDs {
			id := strings.TrimSpace(rawID)
			if id == "" {
				continue
			}
			if _, exists := seenIDs[id]; exists {
				continue
			}
			seenIDs[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}

	records, err := cache.getEntityRecordsByIDsMapped(ctx, region, entity, ids)
	if err != nil {
		return nil, fmt.Errorf("load legacy string id records region %s entity %s: %w", region, entity, err)
	}

	legacyFields := make(map[string][]legacySearchIndexItem, len(fields))
	for field, fieldIDs := range fields {
		field = normalizeKey(field)
		if !isSearchableField(entity, field) {
			continue
		}

		items := make([]legacySearchIndexItem, 0, len(fieldIDs))
		for _, rawID := range fieldIDs {
			id := strings.TrimSpace(rawID)
			if id == "" {
				continue
			}

			record, ok := records[id]
			if !ok {
				continue
			}
			searchable := searchableFields(entity, record)
			normalizedText := searchable[field]
			if normalizedText == "" {
				continue
			}

			items = append(items, legacySearchIndexItem{
				ID:             id,
				NormalizedText: normalizedText,
			})
		}
		if len(items) > 0 {
			legacyFields[field] = items
		}
	}

	return compactLegacySearchIndex(entity, legacyFields), nil
}

func compactCurrentSearchIndex(entity string, index entitySearchIndex) *entitySearchIndex {
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
		field = normalizeKey(field)
		if !isSearchableField(entity, field) || len(items) == 0 {
			continue
		}

		compactItems := make([]searchIndexItem, 0, len(items))
		for _, item := range items {
			if int(item.IDIndex) >= len(compact.IDs) {
				continue
			}

			text := index.textValue(item)
			if text == "" {
				continue
			}

			span, ok := textRefs[text]
			if !ok {
				offset := uint32(textBlob.Len())
				textBlob.WriteString(text)
				span = textSpan{Offset: offset, Length: uint32(len(text))}
				textRefs[text] = span
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
	ctx, span := tracing.StartSpan(ctx, "redis.master_data.list_by_page", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)), attribute.Int("page.size", pageSize))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

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

	span.SetAttributes(attribute.Int("result.count", len(items)), attribute.Int64("result.total", total))
	return items, int(total), nil
}

func (cache *RedisMasterDataCache) HasEntityRecords(ctx context.Context, region string, entity string) (bool, error) {
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return false, nil
	}

	count, err := cache.client.HLen(ctx, cache.redisEntityKey(regionName, entityName)).Result()
	if err != nil {
		return false, fmt.Errorf("hlen by-id region %s entity %s: %w", regionName, entityName, err)
	}

	return count > 0, nil
}

func (cache *RedisMasterDataCache) ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error) {
	ctx, span := tracing.StartSpan(ctx, "redis.master_data.list_all", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return []map[string]any{}, nil
	}

	orderKey := cache.redisEntityOrderKey(regionName, entityName)
	total, err := cache.client.LLen(ctx, orderKey).Result()
	if err != nil {
		return nil, fmt.Errorf("llen order ids region %s entity %s: %w", regionName, entityName, err)
	}

	byIDCount, err := cache.client.HLen(ctx, cache.redisEntityKey(regionName, entityName)).Result()
	if err != nil {
		return nil, fmt.Errorf("hlen by-id region %s entity %s: %w", regionName, entityName, err)
	}
	if byIDCount > total {
		rebuiltIDs, rebuildErr := cache.rebuildEntityOrderFromByID(ctx, regionName, entityName)
		if rebuildErr != nil {
			return nil, rebuildErr
		}
		total = int64(len(rebuiltIDs))
	}

	if total <= 0 {
		rebuiltIDs, rebuildErr := cache.rebuildEntityOrderFromByID(ctx, regionName, entityName)
		if rebuildErr != nil {
			return nil, rebuildErr
		}
		if len(rebuiltIDs) == 0 {
			return []map[string]any{}, nil
		}

		total = int64(len(rebuiltIDs))
	}

	ids, err := cache.client.LRange(ctx, orderKey, 0, total-1).Result()
	if err != nil {
		return nil, fmt.Errorf("lrange order ids region %s entity %s: %w", regionName, entityName, err)
	}

	items, err := cache.getEntityRecordsByIDs(ctx, regionName, entityName, ids)
	span.SetAttributes(attribute.Int("result.count", len(items)))
	return items, err
}

func (cache *RedisMasterDataCache) getEntityRecordsByIDs(ctx context.Context, region string, entity string, ids []string) ([]map[string]any, error) {
	if len(ids) == 0 {
		return []map[string]any{}, nil
	}

	const hmgetBatchSize = 500

	entityKey := cache.redisEntityKey(region, entity)
	items := make([]map[string]any, 0, len(ids))
	for start := 0; start < len(ids); start += hmgetBatchSize {
		end := start + hmgetBatchSize
		if end > len(ids) {
			end = len(ids)
		}

		batchIDs := ids[start:end]
		values, err := cache.client.HMGet(ctx, entityKey, batchIDs...).Result()
		if err != nil {
			return nil, fmt.Errorf("hmget region %s entity %s: %w", region, entity, err)
		}

		for index, raw := range values {
			if raw == nil {
				continue
			}

			record, err := unmarshalRedisEntityRecord(raw, region, entity, batchIDs[index])
			if err != nil {
				return nil, err
			}
			items = append(items, record)
		}
	}

	return items, nil
}

func (cache *RedisMasterDataCache) getEntityRecordsByIDsMapped(ctx context.Context, region string, entity string, ids []string) (map[string]map[string]any, error) {
	if len(ids) == 0 {
		return map[string]map[string]any{}, nil
	}

	const hmgetBatchSize = 500

	entityKey := cache.redisEntityKey(region, entity)
	result := make(map[string]map[string]any, len(ids))
	for start := 0; start < len(ids); start += hmgetBatchSize {
		end := start + hmgetBatchSize
		if end > len(ids) {
			end = len(ids)
		}

		batchIDs := ids[start:end]
		values, err := cache.client.HMGet(ctx, entityKey, batchIDs...).Result()
		if err != nil {
			return nil, fmt.Errorf("hmget region %s entity %s: %w", region, entity, err)
		}

		for index, raw := range values {
			if raw == nil {
				continue
			}

			record, err := unmarshalRedisEntityRecord(raw, region, entity, batchIDs[index])
			if err != nil {
				return nil, err
			}
			result[batchIDs[index]] = record
		}
	}

	return result, nil
}

func unmarshalRedisEntityRecord(raw any, region string, entity string, id string) (map[string]any, error) {
	var body []byte
	switch typed := raw.(type) {
	case string:
		body = []byte(typed)
	case []byte:
		body = typed
	default:
		return nil, fmt.Errorf("unexpected record type region %s entity %s id %s: %T", region, entity, id, raw)
	}

	var record map[string]any
	if err := json.Unmarshal(body, &record); err != nil {
		return nil, fmt.Errorf("unmarshal record region %s entity %s id %s: %w", region, entity, id, err)
	}

	return record, nil
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
	ctx, span := tracing.StartSpan(ctx, "redis.master_data.get_by_id", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	recordIDValue := strings.TrimSpace(id)
	if regionName == "" || entityName == "" || recordIDValue == "" {
		return nil, false, nil
	}

	body, err := cache.client.HGet(ctx, cache.redisEntityKey(regionName, entityName), recordIDValue).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			span.SetAttributes(attribute.Bool("cache.hit", false))
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("hget region %s entity %s id %s: %w", regionName, entityName, recordIDValue, err)
	}

	var record map[string]any
	if err := json.Unmarshal(body, &record); err != nil {
		return nil, false, fmt.Errorf("unmarshal record region %s entity %s id %s: %w", regionName, entityName, recordIDValue, err)
	}

	span.SetAttributes(attribute.Bool("cache.hit", true))
	return record, true, nil
}

func (cache *RedisMasterDataCache) Search(ctx context.Context, region string, entity string, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	ctx, span := tracing.StartSpan(ctx, "redis.master_data.search", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)), attribute.Int("search.field.count", len(fields)), attribute.Int("search.limit", limit))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	if limit <= 0 {
		limit = 20
	}

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	normalizedQuery := normalizeSearchText(query)
	if regionName == "" || entityName == "" || normalizedQuery == "" {
		return []masterdata.SearchMatch{}, nil
	}

	entityIndex, found, err := cache.cachedEntityIndex(ctx, regionName, entityName)
	if err != nil {
		return nil, err
	}
	if !found {
		var version string
		entityIndex, version, found, err = cache.readEntityIndexFromRedis(ctx, regionName, entityName)
		if err != nil {
			return nil, err
		}
		if found {
			cache.setCachedEntityIndex(regionName, entityName, version, entityIndex)
		}
	}

	if !found || entityIndex == nil || len(entityIndex.Fields) == 0 {
		var rebuildErr error
		entityIndex, rebuilt, rebuildErr := cache.rebuildEntityIndexFromRedis(ctx, regionName, entityName)
		if rebuildErr != nil {
			return nil, rebuildErr
		}
		if !rebuilt {
			return []masterdata.SearchMatch{}, nil
		}
		version, versionErr := cache.persistedEntitySearchIndexVersion(ctx, regionName, entityName)
		if versionErr != nil {
			return nil, versionErr
		}
		cache.setCachedEntityIndex(regionName, entityName, version, entityIndex)
		if entityIndex == nil || len(entityIndex.Fields) == 0 {
			return []masterdata.SearchMatch{}, nil
		}
	}

	selectedFields := normalizeFields(fields)
	if len(selectedFields) == 0 {
		selectedFields = []string{"name"}
	}

	// Short-circuit: single "id" field + query that normalizes to itself -> direct HGET.
	// Avoids full index scan for the common composite-handler pattern (field="id", query="41").
	if len(selectedFields) == 1 && selectedFields[0] == "id" && normalizedQuery == strings.ToLower(strings.TrimSpace(query)) {
		record, exists, fastErr := cache.GetByID(ctx, regionName, entityName, normalizedQuery)
		if fastErr != nil {
			return nil, fastErr
		}
		if !exists {
			return []masterdata.SearchMatch{}, nil
		}
		return []masterdata.SearchMatch{
			{
				Item:         record,
				MatchScore:   100,
				MatchType:    "exact",
				MatchedField: "id",
			},
		}, nil
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

	// Batch HMGET instead of per-record HGET loop.
	candidateIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		recordID := entityIndex.idsValue(candidate.IDIndex)
		if recordID == "" {
			continue
		}
		candidateIDs = append(candidateIDs, recordID)
	}

	if len(candidateIDs) == 0 {
		span.SetAttributes(attribute.Int("result.count", 0))
		return []masterdata.SearchMatch{}, nil
	}

	recordByID, batchErr := cache.getEntityRecordsByIDsMapped(ctx, regionName, entityName, candidateIDs)
	if batchErr != nil {
		return nil, batchErr
	}

	results := make([]masterdata.SearchMatch, 0, len(candidates))
	for _, candidate := range candidates {
		recordID := entityIndex.idsValue(candidate.IDIndex)
		record, found := recordByID[recordID]
		if !found {
			continue
		}

		results = append(results, masterdata.SearchMatch{
			Item:         record,
			MatchScore:   candidate.Score,
			MatchType:    candidate.MatchType,
			MatchedField: candidate.MatchedField,
		})
	}

	span.SetAttributes(attribute.Int("result.count", len(results)))
	return results, nil
}

func (cache *RedisMasterDataCache) rebuildEntityIndexFromRedis(
	ctx context.Context,
	region string,
	entity string,
) (*entitySearchIndex, bool, error) {
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return nil, false, nil
	}

	recordMap, err := cache.client.HGetAll(ctx, cache.redisEntityKey(regionName, entityName)).Result()
	if err != nil {
		return nil, false, fmt.Errorf("hgetall region %s entity %s: %w", regionName, entityName, err)
	}
	if len(recordMap) == 0 {
		if err := cache.removePersistedEntitySearchIndex(ctx, regionName, entityName); err != nil {
			return nil, false, err
		}
		cache.invalidateCachedEntityIndex(regionName, entityName)
		return nil, false, nil
	}

	records := make([]map[string]any, 0, len(recordMap))
	for _, raw := range recordMap {
		var record map[string]any
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, false, fmt.Errorf("decode redis record region %s entity %s: %w", regionName, entityName, err)
		}
		records = append(records, record)
	}

	entityIndex := buildEntitySearchIndex(entityName, records)
	pipe := cache.client.Pipeline()
	version := cache.persistEntitySearchIndex(ctx, pipe, regionName, entityName, entityIndex)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, false, fmt.Errorf("persist rebuilt search index region %s entity %s: %w", regionName, entityName, err)
	}
	cache.setCachedEntityIndex(regionName, entityName, version, entityIndex)

	return entityIndex, true, nil
}

func (cache *RedisMasterDataCache) LoadRegionIndexFromRedis(ctx context.Context, region string) (bool, error) {
	ctx, span := tracing.StartSpan(ctx, "redis.master_data.load_region_index", attribute.String("region", normalizeKey(region)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	regionName := normalizeKey(region)
	if regionName == "" {
		return false, nil
	}

	entities, err := cache.client.SMembers(ctx, cache.redisRegionSearchIndexEntitiesKey(regionName)).Result()
	if err != nil {
		return false, fmt.Errorf("list persisted search index entities region %s: %w", regionName, err)
	}
	if len(entities) == 0 {
		span.SetAttributes(attribute.Bool("index.loaded", false))
		return false, nil
	}

	loaded := false
	loadedCount := 0
	for _, entity := range entities {
		hasRecords, err := cache.HasEntityRecords(ctx, regionName, entity)
		if err != nil {
			return false, err
		}
		if !hasRecords {
			if err := cache.removePersistedEntitySearchIndex(ctx, regionName, entity); err != nil {
				return false, err
			}
			cache.invalidateCachedEntityIndex(regionName, entity)
			continue
		}

		entityIndex, version, found, err := cache.readEntityIndexFromRedis(ctx, regionName, entity)
		if err != nil {
			return false, err
		}
		if found {
			cache.setCachedEntityIndex(regionName, entity, version, entityIndex)
			loaded = true
			loadedCount++
		} else {
			if err := cache.removePersistedEntitySearchIndex(ctx, regionName, entity); err != nil {
				return false, err
			}
			cache.invalidateCachedEntityIndex(regionName, entity)
		}
	}

	if !loaded {
		span.SetAttributes(attribute.Bool("index.loaded", false))
		return false, nil
	}

	span.SetAttributes(attribute.Bool("index.loaded", true), attribute.Int("entity.count", loadedCount))
	return true, nil
}

func (cache *RedisMasterDataCache) HasRegionIndex(region string) bool {
	regionName := normalizeKey(region)
	if regionName == "" {
		return false
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	return len(cache.index[regionName]) > 0
}

func (cache *RedisMasterDataCache) RebuildRegionIndexFromRedis(ctx context.Context, region string) (bool, error) {
	regionName := normalizeKey(region)
	if regionName == "" {
		return false, nil
	}

	regionPrefix := cache.redisKey(regionName)
	pattern := regionPrefix + ":*:by-id"
	iterator := cache.client.Scan(ctx, 0, pattern, 0).Iterator()

	hasRecords := false
	nextEntities := make(map[string]struct{})

	existingEntities, err := cache.client.SMembers(ctx, cache.redisRegionSearchIndexEntitiesKey(regionName)).Result()
	if err != nil {
		return false, fmt.Errorf("list persisted search index entities before rebuild region %s: %w", regionName, err)
	}

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

			searchable := searchableFields(entity, record)
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
			nextEntities[entity] = struct{}{}

			pipe := cache.client.Pipeline()
			version := cache.persistEntitySearchIndex(ctx, pipe, regionName, entity, entityIndex)
			if _, err := pipe.Exec(ctx); err != nil {
				return false, fmt.Errorf("persist rebuilt search index region %s entity %s: %w", regionName, entity, err)
			}
			cache.setCachedEntityIndex(regionName, entity, version, entityIndex)
		} else {
			cache.invalidateCachedEntityIndex(regionName, entity)
		}
	}

	if err := iterator.Err(); err != nil {
		return false, fmt.Errorf("scan redis keys region %s: %w", regionName, err)
	}

	if !hasRecords {
		pipe := cache.client.Pipeline()
		for _, entity := range existingEntities {
			entityName := normalizeKey(entity)
			if entityName == "" {
				continue
			}
			pipe.Del(ctx, cache.redisEntitySearchIndexKey(regionName, entityName))
			pipe.Del(ctx, cache.redisEntitySearchIndexVersionKey(regionName, entityName))
		}
		pipe.Del(ctx, cache.redisRegionSearchIndexEntitiesKey(regionName))
		if _, err := pipe.Exec(ctx); err != nil {
			return false, fmt.Errorf("remove empty region search indexes region %s: %w", regionName, err)
		}
		cache.invalidateCachedRegionIndex(regionName)
		return false, nil
	}

	pipe := cache.client.Pipeline()
	for _, entity := range existingEntities {
		entityName := normalizeKey(entity)
		if entityName == "" {
			continue
		}
		if _, ok := nextEntities[entityName]; ok {
			continue
		}
		pipe.Del(ctx, cache.redisEntitySearchIndexKey(regionName, entityName))
		pipe.Del(ctx, cache.redisEntitySearchIndexVersionKey(regionName, entityName))
		pipe.SRem(ctx, cache.redisRegionSearchIndexEntitiesKey(regionName), entityName)
		cache.invalidateCachedEntityIndex(regionName, entityName)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return false, fmt.Errorf("remove stale rebuilt search indexes region %s: %w", regionName, err)
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

func (cache *RedisMasterDataCache) cachedEntityIndex(ctx context.Context, region string, entity string) (*entitySearchIndex, bool, error) {
	if cache == nil || cache.searchIndexCacheEntries <= 0 {
		return nil, false, nil
	}

	cache.mu.Lock()
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	entry, ok := cache.indexEntries[entityIndexCacheKey(regionName, entityName)]
	if !ok {
		cache.mu.Unlock()
		return nil, false, nil
	}
	cache.indexOrder.MoveToFront(entry)
	cached, ok := entry.Value.(cachedEntitySearchIndex)
	if !ok || cached.index == nil {
		cache.mu.Unlock()
		return nil, false, nil
	}
	cachedVersion := cached.version
	cachedIndex := cached.index
	cache.mu.Unlock()

	currentVersion, err := cache.persistedEntitySearchIndexVersion(ctx, regionName, entityName)
	if err != nil {
		return nil, false, err
	}
	if currentVersion == "" || cachedVersion != currentVersion {
		cache.invalidateCachedEntityIndex(regionName, entityName)
		return nil, false, nil
	}

	return cachedIndex, true, nil
}

func (cache *RedisMasterDataCache) setCachedEntityIndex(region string, entity string, version string, index *entitySearchIndex) {
	if cache == nil || cache.searchIndexCacheEntries <= 0 {
		return
	}
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" || version == "" || index == nil || len(index.IDs) == 0 || len(index.Fields) == 0 {
		cache.invalidateCachedEntityIndex(regionName, entityName)
		return
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.ensureIndexCacheLocked()

	key := entityIndexCacheKey(regionName, entityName)
	if entry, ok := cache.indexEntries[key]; ok {
		entry.Value = cachedEntitySearchIndex{region: regionName, entity: entityName, version: version, index: index}
		cache.indexOrder.MoveToFront(entry)
		cache.index[regionName][entityName] = index
		return
	}

	if _, ok := cache.index[regionName]; !ok {
		cache.index[regionName] = make(map[string]*entitySearchIndex)
	}
	cache.index[regionName][entityName] = index
	cache.indexEntries[key] = cache.indexOrder.PushFront(cachedEntitySearchIndex{region: regionName, entity: entityName, version: version, index: index})
	cache.evictCachedEntityIndexesLocked()
}

func (cache *RedisMasterDataCache) invalidateCachedEntityIndex(region string, entity string) {
	if cache == nil {
		return
	}
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.removeCachedEntityIndexLocked(regionName, entityName)
}

func (cache *RedisMasterDataCache) invalidateCachedRegionIndex(region string) {
	if cache == nil {
		return
	}
	regionName := normalizeKey(region)
	if regionName == "" {
		return
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	for entity := range cache.index[regionName] {
		cache.removeCachedEntityIndexLocked(regionName, entity)
	}
}

func (cache *RedisMasterDataCache) ensureIndexCacheLocked() {
	if cache.index == nil {
		cache.index = make(map[string]map[string]*entitySearchIndex)
	}
	if cache.indexOrder == nil {
		cache.indexOrder = list.New()
	}
	if cache.indexEntries == nil {
		cache.indexEntries = make(map[string]*list.Element)
	}
}

func (cache *RedisMasterDataCache) evictCachedEntityIndexesLocked() {
	for cache.searchIndexCacheEntries > 0 && cache.indexOrder.Len() > cache.searchIndexCacheEntries {
		entry := cache.indexOrder.Back()
		if entry == nil {
			return
		}
		cached, ok := entry.Value.(cachedEntitySearchIndex)
		if !ok {
			cache.indexOrder.Remove(entry)
			continue
		}
		cache.removeCachedEntityIndexLocked(cached.region, cached.entity)
	}
}

func (cache *RedisMasterDataCache) removeCachedEntityIndexLocked(region string, entity string) {
	key := entityIndexCacheKey(region, entity)
	if entry, ok := cache.indexEntries[key]; ok {
		cache.indexOrder.Remove(entry)
		delete(cache.indexEntries, key)
	}
	if entityIndexes, ok := cache.index[region]; ok {
		delete(entityIndexes, entity)
		if len(entityIndexes) == 0 {
			delete(cache.index, region)
		}
	}
}

func entityIndexCacheKey(region string, entity string) string {
	return normalizeKey(region) + ":" + normalizeKey(entity)
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

func (cache *RedisMasterDataCache) redisEntitySearchIndexVersionKey(region string, entity string) string {
	return cache.redisKey(region) + ":" + normalizeKey(entity) + ":search-index-version"
}

func (cache *RedisMasterDataCache) redisRegionSearchIndexEntitiesKey(region string) string {
	return cache.redisKey(region) + ":search-index:entities"
}

func (cache *RedisMasterDataCache) persistEntitySearchIndex(ctx context.Context, pipe redis.Pipeliner, region string, entity string, index *entitySearchIndex) string {
	entityName := normalizeKey(entity)
	if entityName == "" {
		return ""
	}

	indexKey := cache.redisEntitySearchIndexKey(region, entityName)
	versionKey := cache.redisEntitySearchIndexVersionKey(region, entityName)
	entitiesKey := cache.redisRegionSearchIndexEntitiesKey(region)
	if index == nil || len(index.IDs) == 0 || len(index.Fields) == 0 {
		pipe.Del(ctx, indexKey)
		pipe.Del(ctx, versionKey)
		pipe.SRem(ctx, entitiesKey, entityName)
		return ""
	}

	body, err := json.Marshal(index)
	if err != nil {
		return ""
	}
	version := entitySearchIndexVersion(body)

	pipe.Set(ctx, indexKey, body, 0)
	pipe.Set(ctx, versionKey, version, 0)
	pipe.SAdd(ctx, entitiesKey, entityName)
	return version
}

func (cache *RedisMasterDataCache) removePersistedEntitySearchIndex(ctx context.Context, region string, entity string) error {
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return nil
	}

	pipe := cache.client.Pipeline()
	pipe.Del(ctx, cache.redisEntitySearchIndexKey(regionName, entityName))
	pipe.Del(ctx, cache.redisEntitySearchIndexVersionKey(regionName, entityName))
	pipe.SRem(ctx, cache.redisRegionSearchIndexEntitiesKey(regionName), entityName)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("remove persisted search index region %s entity %s: %w", regionName, entityName, err)
	}

	return nil
}

func (cache *RedisMasterDataCache) persistedEntitySearchIndexExists(ctx context.Context, region string, entity string) (bool, error) {
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return false, nil
	}

	exists, err := cache.client.Exists(ctx, cache.redisEntitySearchIndexKey(regionName, entityName)).Result()
	if err != nil {
		return false, fmt.Errorf("exists persisted search index region %s entity %s: %w", regionName, entityName, err)
	}

	return exists > 0, nil
}

func (cache *RedisMasterDataCache) readEntityIndexFromRedis(ctx context.Context, region string, entity string) (*entitySearchIndex, string, bool, error) {
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return nil, "", false, nil
	}

	body, err := cache.client.Get(ctx, cache.redisEntitySearchIndexKey(regionName, entityName)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, "", false, nil
		}
		return nil, "", false, fmt.Errorf("get persisted search index region %s entity %s: %w", regionName, entityName, err)
	}
	version := entitySearchIndexVersion(body)
	decodeErrors := make([]error, 0, 4)

	var index entitySearchIndex
	if err := json.Unmarshal(body, &index); err == nil {
		compact := compactCurrentSearchIndex(entityName, index)
		if compact != nil {
			if !equalEntitySearchIndex(compact, &index) {
				version, err = cache.rewriteEntitySearchIndex(ctx, regionName, entityName, compact)
				if err != nil {
					return nil, "", false, err
				}
			} else {
				cache.ensureEntitySearchIndexVersion(ctx, regionName, entityName, version)
			}
			return compact, version, true, nil
		}
	} else {
		decodeErrors = append(decodeErrors, fmt.Errorf("current format: %w", err))
	}

	var legacyPersisted legacyPersistedEntitySearchIndex
	if err := json.Unmarshal(body, &legacyPersisted); err == nil {
		compact := compactPersistedSearchIndex(entityName, legacyPersisted)
		if compact != nil {
			version, err = cache.rewriteEntitySearchIndex(ctx, regionName, entityName, compact)
			if err != nil {
				return nil, "", false, err
			}
			return compact, version, true, nil
		}
	} else {
		decodeErrors = append(decodeErrors, fmt.Errorf("persisted legacy format: %w", err))
	}

	var legacyFields map[string][]legacySearchIndexItem
	if err := json.Unmarshal(body, &legacyFields); err == nil {
		legacyIndex := compactLegacySearchIndex(entityName, legacyFields)
		if legacyIndex == nil {
			return nil, "", false, nil
		}
		version, err = cache.rewriteEntitySearchIndex(ctx, regionName, entityName, legacyIndex)
		if err != nil {
			return nil, "", false, err
		}

		return legacyIndex, version, true, nil
	} else {
		decodeErrors = append(decodeErrors, fmt.Errorf("legacy item map format: %w", err))
		var legacyStringFields map[string][]string
		if err := json.Unmarshal(body, &legacyStringFields); err != nil {
			decodeErrors = append(decodeErrors, fmt.Errorf("legacy string-ID map format: %w", err))
			return nil, "", false, fmt.Errorf(
				"decode persisted search index region %s entity %s: %w",
				regionName,
				entityName,
				errors.Join(decodeErrors...),
			)
		}

		legacyIndex, err := cache.compactLegacyStringIDSearchIndex(ctx, regionName, entityName, legacyStringFields)
		if err != nil {
			return nil, "", false, err
		}
		if legacyIndex == nil {
			return nil, "", false, nil
		}
		version, err = cache.rewriteEntitySearchIndex(ctx, regionName, entityName, legacyIndex)
		if err != nil {
			return nil, "", false, err
		}

		return legacyIndex, version, true, nil
	}
}

func (cache *RedisMasterDataCache) rewriteEntitySearchIndex(
	ctx context.Context,
	region string,
	entity string,
	index *entitySearchIndex,
) (string, error) {
	pipe := cache.client.Pipeline()
	version := cache.persistEntitySearchIndex(ctx, pipe, region, entity, index)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("rewrite persisted search index region %s entity %s: %w", region, entity, err)
	}
	return version, nil
}

func (cache *RedisMasterDataCache) ensureEntitySearchIndexVersion(ctx context.Context, region string, entity string, version string) {
	if version == "" {
		return
	}
	_ = cache.client.SetNX(ctx, cache.redisEntitySearchIndexVersionKey(region, entity), version, 0).Err()
}

func (cache *RedisMasterDataCache) persistedEntitySearchIndexVersion(ctx context.Context, region string, entity string) (string, error) {
	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if regionName == "" || entityName == "" {
		return "", nil
	}

	version, err := cache.client.Get(ctx, cache.redisEntitySearchIndexVersionKey(regionName, entityName)).Result()
	if err == nil {
		return strings.TrimSpace(version), nil
	}
	if !errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("get persisted search index version region %s entity %s: %w", regionName, entityName, err)
	}

	body, err := cache.client.Get(ctx, cache.redisEntitySearchIndexKey(regionName, entityName)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil
		}
		return "", fmt.Errorf("get persisted search index for version region %s entity %s: %w", regionName, entityName, err)
	}
	version = entitySearchIndexVersion(body)
	cache.ensureEntitySearchIndexVersion(ctx, regionName, entityName, version)
	return version, nil
}

func entitySearchIndexVersion(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sum := sha1.Sum(body)
	return hex.EncodeToString(sum[:])
}

func equalEntitySearchIndex(left *entitySearchIndex, right *entitySearchIndex) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.TextBlob != right.TextBlob || len(left.IDs) != len(right.IDs) || len(left.Fields) != len(right.Fields) {
		return false
	}
	for index, id := range left.IDs {
		if right.IDs[index] != id {
			return false
		}
	}
	for field, leftItems := range left.Fields {
		rightItems, ok := right.Fields[field]
		if !ok || len(leftItems) != len(rightItems) {
			return false
		}
		for index, leftItem := range leftItems {
			if rightItems[index] != leftItem {
				return false
			}
		}
	}

	return true
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

func searchableFields(entity string, record map[string]any) map[string]string {
	result := make(map[string]string)
	for key, value := range record {
		field := normalizeKey(key)
		if field == "" || !isSearchableField(entity, field) {
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

func isSearchableField(entity string, field string) bool {
	field = normalizeKey(field)
	if field == "" {
		return false
	}

	if _, ok := defaultSearchableFields[field]; ok {
		return true
	}

	if entityFields := entitySearchableFields[normalizeKey(entity)]; entityFields != nil {
		if _, ok := entityFields[field]; ok {
			return true
		}
	}

	_, ok := relationshipSearchableFields[field]
	return ok
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
