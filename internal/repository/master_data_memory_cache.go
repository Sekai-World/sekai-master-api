package repository

import (
	"context"
	"sync"
)

type MemoryMasterDataCache struct {
	mu    sync.RWMutex
	store map[string]map[string]any
}

func NewMemoryMasterDataCache() *MemoryMasterDataCache {
	return &MemoryMasterDataCache{
		store: make(map[string]map[string]any),
	}
}

func (cache *MemoryMasterDataCache) StoreRegion(_ context.Context, region string, payload map[string]any) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.store[region] = payload
	return nil
}

func (cache *MemoryMasterDataCache) Region(region string) map[string]any {
	cache.mu.RLock()
	defer cache.mu.RUnlock()

	return cache.store[region]
}
