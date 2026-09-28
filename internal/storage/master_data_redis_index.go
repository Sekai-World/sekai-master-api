package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/logging"
	"sekai-master-api/internal/tracing"
)

// Relation indexes answer "which records have this field value" without
// decoding the entity. Each index (see masterdata.EntityIndexes) is a Redis
// hash from an index key to a JSON array of storage keys in stored order. They
// are written with the records they describe, and :index-version records which
// definitions they were built from.

// ErrUnknownIndex reports a lookup on an index the entity does not define.
var ErrUnknownIndex = errors.New("master data index is not defined for entity")

func (cache *RedisMasterDataCache) redisEntityIndexKey(region, entity, index string) string {
	return cache.redisKey(region) + ":" + normalizeKey(entity) + ":index:" + index
}

func (cache *RedisMasterDataCache) redisEntityIndexVersionKey(region, entity string) string {
	return cache.redisKey(region) + ":" + normalizeKey(entity) + ":index-version"
}

func newEntityIndexes(entity string) map[string]map[string][]string {
	names := masterdata.EntityIndexes(entity)
	if len(names) == 0 {
		return nil
	}
	indexes := make(map[string]map[string][]string, len(names))
	for _, name := range names {
		indexes[name] = make(map[string][]string)
	}
	return indexes
}

func addToEntityIndexes(indexes map[string]map[string][]string, storageKey string, record map[string]any) {
	if record == nil {
		return
	}
	for name, index := range indexes {
		for _, key := range masterdata.IndexKeys(record, name) {
			index[key] = append(index[key], storageKey)
		}
	}
}

func (cache *RedisMasterDataCache) storedEntityIndexVersion(ctx context.Context, versionKey string) (string, error) {
	version, err := cache.client.Get(ctx, versionKey).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get index version %s: %w", versionKey, err)
	}
	return version, nil
}

// writeEntityIndexes queues a full rewrite of entity's relation indexes on
// pipe: indexes named by staleVersion or currently defined are deleted, the
// built ones written, and the version updated.
func (cache *RedisMasterDataCache) writeEntityIndexes(ctx context.Context, pipe redis.Pipeliner, region, entity, staleVersion string, indexes map[string]map[string][]string) error {
	for _, name := range append(masterdata.IndexNamesFromVersion(staleVersion), masterdata.EntityIndexes(entity)...) {
		pipe.Del(ctx, cache.redisEntityIndexKey(region, entity, name))
	}
	for name, index := range indexes {
		if len(index) == 0 {
			continue
		}
		values := make(map[string]any, len(index))
		for key, storageKeys := range index {
			body, err := json.Marshal(storageKeys)
			if err != nil {
				return fmt.Errorf("marshal index %s region %s entity %s: %w", name, region, entity, err)
			}
			values[key] = body
		}
		pipe.HSet(ctx, cache.redisEntityIndexKey(region, entity, name), values)
	}

	versionKey := cache.redisEntityIndexVersionKey(region, entity)
	if version := masterdata.IndexVersion(entity); version != "" {
		pipe.Set(ctx, versionKey, version, 0)
	} else {
		pipe.Del(ctx, versionKey)
	}
	return nil
}

// EnsureDerivedEntityData builds the relation indexes and list projections of
// region's entities whose stored version is out of date, reading their records
// from Redis. Sync calls it when it skips an unchanged commit, so new
// definitions reach data that no store will rewrite. It returns the entities
// it rebuilt anything for.
func (cache *RedisMasterDataCache) EnsureDerivedEntityData(ctx context.Context, region string) ([]string, error) {
	regionName := normalizeKey(region)
	rebuilt := make([]string, 0)
	for _, entity := range derivedDataEntities() {
		indexVersion, err := cache.storedEntityIndexVersion(ctx, cache.redisEntityIndexVersionKey(regionName, entity))
		if err != nil {
			return rebuilt, err
		}
		projectionVersion, err := cache.storedEntityIndexVersion(ctx, cache.redisEntityProjectionVersionKey(regionName, entity))
		if err != nil {
			return rebuilt, err
		}
		indexesStale := indexVersion != masterdata.IndexVersion(entity)
		projectionStale := projectionVersion != masterdata.ProjectionVersion(entity)
		if !indexesStale && !projectionStale {
			continue
		}
		stored, err := cache.client.Exists(ctx, cache.redisEntityKey(regionName, entity)).Result()
		if err != nil {
			return rebuilt, fmt.Errorf("check records region %s entity %s: %w", regionName, entity, err)
		}
		if stored == 0 {
			continue
		}

		order, err := cache.client.LRange(ctx, cache.redisEntityOrderKey(regionName, entity), 0, -1).Result()
		if err != nil {
			return rebuilt, fmt.Errorf("lrange order region %s entity %s: %w", regionName, entity, err)
		}
		records, err := cache.fetchEntityRecords(ctx, regionName, entity, order)
		if err != nil {
			return rebuilt, err
		}

		pipe := cache.client.TxPipeline()
		if indexesStale {
			indexes := newEntityIndexes(entity)
			for position, record := range records {
				addToEntityIndexes(indexes, order[position], record)
			}
			if err := cache.writeEntityIndexes(ctx, pipe, regionName, entity, indexVersion, indexes); err != nil {
				return rebuilt, err
			}
		}
		if projectionStale {
			builder := masterdata.NewProjectionBuilder(entity)
			if builder != nil {
				for position, record := range records {
					if record != nil {
						builder.Add(order[position], record)
					}
				}
			}
			if err := cache.writeEntityProjection(ctx, pipe, regionName, entity, builder); err != nil {
				return rebuilt, err
			}
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return rebuilt, fmt.Errorf("write derived data region %s entity %s: %w", regionName, entity, err)
		}
		rebuilt = append(rebuilt, entity)
	}
	return rebuilt, nil
}

// derivedDataEntities returns the entities with relation indexes or a list
// projection.
func derivedDataEntities() []string {
	entities := append(masterdata.IndexedEntities(), masterdata.ProjectedEntities()...)
	slices.Sort(entities)
	return slices.Compact(entities)
}

// ListByIndex returns, for each lookup, the records whose index fields equal
// the lookup's values (in the index's field order), in stored order. All
// lookups share one index read and one record read. Until sync has built the
// index, the entity is scanned instead so results stay correct.
func (cache *RedisMasterDataCache) ListByIndex(ctx context.Context, region string, entity string, index string, lookups [][]any) ([][]map[string]any, error) {
	ctx, span := tracing.StartSpan(ctx, "redis.master_data.list_by_index", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)), attribute.String("index", index), attribute.Int("request.count", len(lookups)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if !masterdata.HasIndex(entityName, index) {
		err = fmt.Errorf("%w: %s on %s", ErrUnknownIndex, index, entityName)
		return nil, err
	}

	results := make([][]map[string]any, len(lookups))
	keys := make([]string, 0, len(lookups))
	positions := make([]int, 0, len(lookups))
	for position, lookup := range lookups {
		if key, ok := masterdata.IndexLookupKey(lookup...); ok {
			keys = append(keys, key)
			positions = append(positions, position)
		}
	}
	if len(keys) == 0 {
		return results, nil
	}

	pipe := cache.client.Pipeline()
	versionCmd := pipe.Get(ctx, cache.redisEntityIndexVersionKey(regionName, entityName))
	valuesCmd := pipe.HMGet(ctx, cache.redisEntityIndexKey(regionName, entityName, index), keys...)
	storedCmd := pipe.Exists(ctx, cache.redisEntityKey(regionName, entityName))
	_, _ = pipe.Exec(ctx)
	for _, cmdErr := range []error{versionCmd.Err(), valuesCmd.Err(), storedCmd.Err()} {
		if cmdErr != nil && !errors.Is(cmdErr, redis.Nil) {
			err = fmt.Errorf("read index %s region %s entity %s: %w", index, regionName, entityName, cmdErr)
			return nil, err
		}
	}
	if storedCmd.Val() == 0 {
		// A region without this entity (TW has no musiccategories) has no
		// records and so no index to build.
		for _, position := range positions {
			results[position] = make([]map[string]any, 0)
		}
		return results, nil
	}
	if version, _ := versionCmd.Result(); version != masterdata.IndexVersion(entityName) {
		logging.FromContext(ctx).Warnw("master data index not built; scanning entity", "region", regionName, "entity", entityName, "index", index)
		span.SetAttributes(attribute.Bool("index.scan", true))
		return cache.scanByIndex(ctx, regionName, entityName, index, keys, positions, results)
	}

	storageKeyLists := make([][]string, len(keys))
	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{})
	for position, raw := range valuesCmd.Val() {
		body, ok := raw.(string)
		if !ok {
			continue
		}
		if err = json.Unmarshal([]byte(body), &storageKeyLists[position]); err != nil {
			err = fmt.Errorf("decode index %s region %s entity %s: %w", index, regionName, entityName, err)
			return nil, err
		}
		for _, storageKey := range storageKeyLists[position] {
			if _, duplicate := seen[storageKey]; !duplicate {
				seen[storageKey] = struct{}{}
				unique = append(unique, storageKey)
			}
		}
	}

	records, err := cache.getEntityRecordsByIDsMapped(ctx, regionName, entityName, unique)
	if err != nil {
		return nil, err
	}
	for position, storageKeys := range storageKeyLists {
		matched := make([]map[string]any, 0, len(storageKeys))
		for _, storageKey := range storageKeys {
			if record := records[storageKey]; record != nil {
				matched = append(matched, record)
			}
		}
		results[positions[position]] = matched
	}
	span.SetAttributes(attribute.Int("result.records", len(records)))
	return results, nil
}

func (cache *RedisMasterDataCache) scanByIndex(ctx context.Context, region, entity, index string, keys []string, positions []int, results [][]map[string]any) ([][]map[string]any, error) {
	records, err := cache.ListAll(ctx, region, entity)
	if err != nil {
		return nil, err
	}

	wanted := make(map[string][]int, len(keys))
	for position, key := range keys {
		wanted[key] = append(wanted[key], positions[position])
	}
	for position := range keys {
		results[positions[position]] = make([]map[string]any, 0)
	}
	for _, record := range records {
		for _, key := range masterdata.IndexKeys(record, index) {
			for _, position := range wanted[key] {
				results[position] = append(results[position], record)
			}
		}
	}
	return results, nil
}
