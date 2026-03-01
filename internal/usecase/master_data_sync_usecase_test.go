package usecase

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/storage"
)

type fakeSyncLoader struct {
	mu             sync.Mutex
	resolvedByZone map[string]string
	payloadByZone  map[string]map[string]any
	loadCalls      int
}

func (loader *fakeSyncLoader) LoadRegion(_ context.Context, source masterdata.Source) (map[string]any, error) {
	loader.mu.Lock()
	defer loader.mu.Unlock()

	loader.loadCalls++
	if payload, exists := loader.payloadByZone[source.Region]; exists {
		return payload, nil
	}

	return map[string]any{}, nil
}

func (loader *fakeSyncLoader) ResolveRegionVersion(_ context.Context, source masterdata.Source) (string, error) {
	loader.mu.Lock()
	defer loader.mu.Unlock()

	return loader.resolvedByZone[source.Region], nil
}

type fakeSyncCache struct {
	mu         sync.Mutex
	storeCalls int
}

func (cache *fakeSyncCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.storeCalls++
	return nil
}

func (cache *fakeSyncCache) GetByID(_ context.Context, _, _, _ string) (map[string]any, bool, error) {
	return nil, false, nil
}

func (cache *fakeSyncCache) ListByPage(_ context.Context, _, _ string, _, _ int) ([]map[string]any, int, error) {
	return nil, 0, nil
}

func (cache *fakeSyncCache) Search(_ context.Context, _, _, _ string, _ []string, _ int) ([]masterdata.SearchMatch, error) {
	return nil, nil
}

type fakeSyncStatusStore struct {
	mu     sync.Mutex
	byZone map[string]masterdata.SyncStatus
	saved  []masterdata.SyncStatus
}

func newFakeSyncStatusStore(seed []masterdata.SyncStatus) *fakeSyncStatusStore {
	store := &fakeSyncStatusStore{
		byZone: make(map[string]masterdata.SyncStatus),
		saved:  make([]masterdata.SyncStatus, 0),
	}
	for _, item := range seed {
		store.byZone[item.Region] = item
	}

	return store
}

func (store *fakeSyncStatusStore) Save(_ context.Context, status masterdata.SyncStatus) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.byZone[status.Region] = status
	store.saved = append(store.saved, status)
	return nil
}

func (store *fakeSyncStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	items := make([]masterdata.SyncStatus, 0, len(store.byZone))
	for _, item := range store.byZone {
		items = append(items, item)
	}

	return items, nil
}

func (store *fakeSyncStatusStore) latest(region string) (masterdata.SyncStatus, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()

	item, exists := store.byZone[region]
	return item, exists
}

func (store *fakeSyncStatusStore) saveCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()

	return len(store.saved)
}

func TestSyncAllSkipsRegionWhenCommitUnchanged(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    42,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: "abc123",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "abc123"},
		payloadByZone:  map[string]map[string]any{"jp": {"cards.json": []any{map[string]any{"id": 1}}}},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 0 {
		t.Fatalf("expected load to be skipped, got loadCalls=%d", loader.loadCalls)
	}
	if cache.storeCalls != 0 {
		t.Fatalf("expected cache store to be skipped, got storeCalls=%d", cache.storeCalls)
	}
	if statusStore.saveCount() == 0 {
		t.Fatalf("expected status to be saved after skip")
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest jp status")
	}
	if latest.SourceCommit != "abc123" {
		t.Fatalf("expected source_commit to stay abc123, got %s", latest.SourceCommit)
	}
	if latest.Status != "success" {
		t.Fatalf("expected status to remain success, got %s", latest.Status)
	}
}

func TestSyncAllLoadsRegionWhenCommitChanged(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    10,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: "old-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "new-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "test"}},
			},
		},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 1 {
		t.Fatalf("expected one load call, got %d", loader.loadCalls)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected one cache store call, got %d", cache.storeCalls)
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest jp status")
	}
	if latest.SourceCommit != "new-commit" {
		t.Fatalf("expected source_commit to update to new-commit, got %s", latest.SourceCommit)
	}
	if latest.Status != "success" {
		t.Fatalf("expected status success, got %s", latest.Status)
	}
}

func TestSyncAllSkipDoesNotMutateRedisCache(t *testing.T) {
	miniRedis, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer miniRedis.Close()

	redisCache, err := storage.NewRedisMasterDataCache(config.Config{
		RedisAddr:                miniRedis.Addr(),
		RedisDB:                  0,
		MasterDataRedisKeyPrefix: "test:master-data:",
	})
	if err != nil {
		t.Fatalf("new redis cache: %v", err)
	}
	defer func() {
		_ = redisCache.Close()
	}()

	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}

	seedPayload := map[string]any{
		"cards.json": []any{map[string]any{"id": 1, "prefix": "stable"}},
	}
	if err := redisCache.StoreRegion(context.Background(), "jp", seedPayload); err != nil {
		t.Fatalf("seed redis cache: %v", err)
	}

	beforeRecord, found, err := redisCache.GetByID(context.Background(), "jp", "cards", "1")
	if err != nil {
		t.Fatalf("read seeded record: %v", err)
	}
	if !found {
		t.Fatalf("expected seeded record to exist")
	}

	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    1,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: "same-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "same-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "should-not-apply"}},
			},
		},
	}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, redisCache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 0 {
		t.Fatalf("expected loader not to run when commit unchanged, got %d", loader.loadCalls)
	}

	afterRecord, found, err := redisCache.GetByID(context.Background(), "jp", "cards", "1")
	if err != nil {
		t.Fatalf("read record after sync: %v", err)
	}
	if !found {
		t.Fatalf("expected record to remain after skip")
	}

	if beforeRecord["prefix"] != afterRecord["prefix"] {
		t.Fatalf("expected redis record unchanged, before=%v after=%v", beforeRecord["prefix"], afterRecord["prefix"])
	}
}
