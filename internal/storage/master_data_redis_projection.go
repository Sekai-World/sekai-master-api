package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/logging"
	"sekai-master-api/internal/tracing"
)

// List projections hold the fields list endpoints filter, sort, and return
// (see masterdata.ProjectionFields) for every record of an entity, by column,
// in one gob-encoded, zstd-compressed value. They are written with the records
// they describe, and :projection-version records which definition they follow.

// ErrUnknownProjection reports a read of a projection the entity does not define.
var ErrUnknownProjection = errors.New("master data projection is not defined for entity")

func (cache *RedisMasterDataCache) redisEntityProjectionKey(region, entity string) string {
	return cache.redisKey(region) + ":" + normalizeKey(entity) + ":projection"
}

func (cache *RedisMasterDataCache) redisEntityProjectionVersionKey(region, entity string) string {
	return cache.redisKey(region) + ":" + normalizeKey(entity) + ":projection-version"
}

// writeEntityProjection queues entity's projection and version on pipe, or
// their removal when the entity has no projection.
func (cache *RedisMasterDataCache) writeEntityProjection(ctx context.Context, pipe redis.Pipeliner, region, entity string, builder *masterdata.ProjectionBuilder) error {
	projectionKey := cache.redisEntityProjectionKey(region, entity)
	versionKey := cache.redisEntityProjectionVersionKey(region, entity)
	if builder == nil {
		pipe.Del(ctx, projectionKey, versionKey)
		return nil
	}

	body, err := masterdata.EncodeProjection(builder.Build())
	if err != nil {
		return fmt.Errorf("projection region %s entity %s: %w", region, entity, err)
	}
	stored, err := marshalRedisEntityRecord(body)
	if err != nil {
		return fmt.Errorf("compress projection region %s entity %s: %w", region, entity, err)
	}
	pipe.Set(ctx, projectionKey, stored, 0)
	pipe.Set(ctx, versionKey, masterdata.ProjectionVersion(entity), 0)
	return nil
}

// LoadProjection returns entity's list projection. A region without the
// entity gets an empty projection. Until sync has built the projection, it is
// built from the entity's records so results stay correct.
func (cache *RedisMasterDataCache) LoadProjection(ctx context.Context, region string, entity string) (*masterdata.Projection, error) {
	ctx, span := tracing.StartSpan(ctx, "redis.master_data.load_projection", attribute.String("region", normalizeKey(region)), attribute.String("entity", normalizeKey(entity)))
	var err error
	defer func() {
		tracing.EndSpan(span, err)
	}()

	regionName := normalizeKey(region)
	entityName := normalizeKey(entity)
	if masterdata.ProjectionVersion(entityName) == "" {
		err = fmt.Errorf("%w: %s", ErrUnknownProjection, entityName)
		return nil, err
	}

	pipe := cache.client.Pipeline()
	bodyCmd := pipe.Get(ctx, cache.redisEntityProjectionKey(regionName, entityName))
	versionCmd := pipe.Get(ctx, cache.redisEntityProjectionVersionKey(regionName, entityName))
	storedCmd := pipe.Exists(ctx, cache.redisEntityKey(regionName, entityName))
	_, _ = pipe.Exec(ctx)
	for _, cmdErr := range []error{bodyCmd.Err(), versionCmd.Err(), storedCmd.Err()} {
		if cmdErr != nil && !errors.Is(cmdErr, redis.Nil) {
			err = fmt.Errorf("read projection region %s entity %s: %w", regionName, entityName, cmdErr)
			return nil, err
		}
	}

	if storedCmd.Val() == 0 {
		return masterdata.BuildProjection(entityName, nil, nil), nil
	}
	if version, _ := versionCmd.Result(); version != masterdata.ProjectionVersion(entityName) || bodyCmd.Err() != nil {
		logging.FromContext(ctx).Warnw("master data projection not built; building from records", "region", regionName, "entity", entityName)
		span.SetAttributes(attribute.Bool("projection.scan", true))
		return cache.buildProjectionFromRecords(ctx, regionName, entityName)
	}

	body, err := redisEntityRecordBody(bodyCmd.Val(), regionName, entityName, "projection")
	if err != nil {
		return nil, err
	}
	projection, err := masterdata.DecodeProjection(body)
	if err != nil {
		err = fmt.Errorf("projection region %s entity %s: %w", regionName, entityName, err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("result.count", projection.Len()))
	return projection, nil
}

func (cache *RedisMasterDataCache) buildProjectionFromRecords(ctx context.Context, region, entity string) (*masterdata.Projection, error) {
	order, err := cache.client.LRange(ctx, cache.redisEntityOrderKey(region, entity), 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("lrange order region %s entity %s: %w", region, entity, err)
	}
	records, err := cache.fetchEntityRecords(ctx, region, entity, order)
	if err != nil {
		return nil, err
	}
	return masterdata.BuildProjection(entity, order, records), nil
}
