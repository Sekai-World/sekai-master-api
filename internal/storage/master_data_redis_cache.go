package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"sekai-master-api/internal/config"
)

type RedisMasterDataCache struct {
	client    *redis.Client
	keyPrefix string
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
	}, nil
}

func (cache *RedisMasterDataCache) StoreRegion(ctx context.Context, region string, payload map[string]any) error {
	key := cache.redisKey(region)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal redis payload for region %s: %w", region, err)
	}

	if err := cache.client.Set(ctx, key, body, 0).Err(); err != nil {
		return fmt.Errorf("set redis key for region %s: %w", region, err)
	}

	return nil
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