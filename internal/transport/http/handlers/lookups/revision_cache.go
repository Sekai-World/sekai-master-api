package lookups

import (
	"context"
	"sync"
)

// revisionCache keeps one derived, read-only value per region and reuses it
// while the source entity's persisted revision is unchanged. Sync may run in a
// different process, so every read revalidates against the revision the caller
// read from Redis instead of relying on in-process invalidation.
type revisionCache[T any] struct {
	mu      sync.Mutex
	entries map[string]*revisionCacheEntry[T]
}

type revisionCacheEntry[T any] struct {
	// mu serializes rebuilds for one region so concurrent cold requests wait
	// for a single load instead of each decoding the full entity.
	mu       sync.Mutex
	revision string
	value    T
}

// load returns the cached value for region when it was built from revision,
// otherwise it builds, stores, and returns a new value. An empty revision means
// the source cannot be validated, so the value is built and never cached.
// Cached values are shared across requests and must not be mutated.
func (cache *revisionCache[T]) load(ctx context.Context, region string, revision string, build func(context.Context) (T, error)) (T, error) {
	if revision == "" {
		return build(ctx)
	}

	entry := cache.entry(region)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.revision == revision {
		return entry.value, nil
	}

	value, err := build(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	entry.revision = revision
	entry.value = value
	return value, nil
}

func (cache *revisionCache[T]) entry(region string) *revisionCacheEntry[T] {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[string]*revisionCacheEntry[T])
	}
	entry, ok := cache.entries[region]
	if !ok {
		entry = &revisionCacheEntry[T]{}
		cache.entries[region] = entry
	}
	return entry
}
