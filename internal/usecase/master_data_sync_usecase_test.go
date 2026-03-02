package usecase

import (
	"context"
	"errors"
	"strings"
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
	mu                 sync.Mutex
	storeCalls         int
	rebuildCalls       int
	rebuildFromRedisOK bool
	hasRegionIndex     bool
	hasRegionIndexSet  bool
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

func (cache *fakeSyncCache) RebuildRegionIndexFromRedis(_ context.Context, _ string) (bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.rebuildCalls++
	if cache.rebuildFromRedisOK {
		return true, nil
	}

	return false, nil
}

func (cache *fakeSyncCache) HasRegionIndex(_ string) bool {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if !cache.hasRegionIndexSet {
		return true
	}

	return cache.hasRegionIndex
}

type fakeSyncStatusStore struct {
	mu            sync.Mutex
	byZone        map[string]masterdata.SyncStatus
	saved         []masterdata.SyncStatus
	successByZone map[string]masterdata.SyncStatus
}

func newFakeSyncStatusStore(seed []masterdata.SyncStatus) *fakeSyncStatusStore {
	store := &fakeSyncStatusStore{
		byZone:        make(map[string]masterdata.SyncStatus),
		saved:         make([]masterdata.SyncStatus, 0),
		successByZone: make(map[string]masterdata.SyncStatus),
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

func (store *fakeSyncStatusStore) ListLatestSuccess(_ context.Context) ([]masterdata.SyncStatus, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	items := make([]masterdata.SyncStatus, 0, len(store.successByZone))
	for _, item := range store.successByZone {
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

func (store *fakeSyncStatusStore) hasSavedStatus(region string, status string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()

	for _, item := range store.saved {
		if item.Region == region && strings.EqualFold(item.Status, status) {
			return true
		}
	}

	return false
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
	cache.rebuildFromRedisOK = true
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
	if cache.rebuildCalls != 1 {
		t.Fatalf("expected redis index rebuild call on skip, got rebuildCalls=%d", cache.rebuildCalls)
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

func TestSyncAllForceLoadsWhenCommitUnchanged(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    10,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: "same-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "same-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "forced"}},
			},
		},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAllForce(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 1 {
		t.Fatalf("expected one load call for force sync, got %d", loader.loadCalls)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected one cache store call for force sync, got %d", cache.storeCalls)
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest jp status")
	}
	if latest.Status != "success" {
		t.Fatalf("expected status success, got %s", latest.Status)
	}
	if latest.SourceCommit != "same-commit" {
		t.Fatalf("expected source_commit same-commit, got %s", latest.SourceCommit)
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

func TestSyncAllSkipsByRestoringFromLocalBackupWhenRedisMissing(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    2,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: "same-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "same-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "from-github"}},
			},
		},
	}
	cache := &fakeSyncCache{}
	cache.rebuildFromRedisOK = false
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)
	backupStore := NewFileMasterDataPayloadBackupStore(t.TempDir())
	if err := backupStore.SaveRegionPayload(context.Background(), source, "same-commit", map[string]any{
		"cards.json": []any{map[string]any{"id": 99, "prefix": "from-local"}},
	}); err != nil {
		t.Fatalf("save local backup: %v", err)
	}
	usecase.backupStore = backupStore

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 0 {
		t.Fatalf("expected full sync to be skipped using local backup, got loadCalls=%d", loader.loadCalls)
	}
	if cache.rebuildCalls != 1 {
		t.Fatalf("expected one redis rebuild attempt, got %d", cache.rebuildCalls)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected one cache store from local backup, got %d", cache.storeCalls)
	}
}

func TestSyncAllFallsBackToFullSyncWhenRedisAndLocalBackupMissing(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    2,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: "same-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "same-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "from-github"}},
			},
		},
	}
	cache := &fakeSyncCache{}
	cache.rebuildFromRedisOK = false
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)
	usecase.backupStore = NewFileMasterDataPayloadBackupStore(t.TempDir())

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 1 {
		t.Fatalf("expected fallback to full sync when local backup missing, got loadCalls=%d", loader.loadCalls)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected cache to be built from github payload, got storeCalls=%d", cache.storeCalls)
	}
}

func TestSyncAllSetsPendingWhenRegionIndexMissing(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "new-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "pending-check"}},
			},
		},
	}
	cache := &fakeSyncCache{
		hasRegionIndexSet: true,
		hasRegionIndex:    false,
	}
	statusStore := newFakeSyncStatusStore(nil)

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !statusStore.hasSavedStatus("jp", "pending") {
		t.Fatalf("expected pending status to be saved when region index is missing")
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest jp status")
	}
	if latest.Status != "success" {
		t.Fatalf("expected final status success, got %s", latest.Status)
	}
}

func TestSyncAllUsesLatestSuccessWhenLatestStatusIsPending(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}

	pendingStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "pending",
		FileCount:    0,
		LastSyncedAt: time.Now().UTC().Add(-time.Minute),
		SourceCommit: "",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Minute),
	}

	latestSuccess := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    12,
		LastSyncedAt: time.Now().UTC().Add(-2 * time.Hour),
		SourceCommit: "same-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-2 * time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "same-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {"cards.json": []any{map[string]any{"id": 1, "prefix": "should-not-load"}}},
		},
	}
	cache := &fakeSyncCache{rebuildFromRedisOK: true}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{pendingStatus})
	statusStore.successByZone["jp"] = latestSuccess

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 0 {
		t.Fatalf("expected load to be skipped using latest success status, got loadCalls=%d", loader.loadCalls)
	}
	if cache.rebuildCalls != 1 {
		t.Fatalf("expected redis index rebuild on skip, got rebuildCalls=%d", cache.rebuildCalls)
	}
}

func TestSyncRegionOnlyRunsTargetRegion(t *testing.T) {
	sourceJP := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo-jp", Ref: "main", Path: "data"}
	sourceEN := masterdata.Source{Region: "en", Owner: "owner", Repo: "repo-en", Ref: "main", Path: "data"}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "commit-jp", "en": "commit-en"},
		payloadByZone: map[string]map[string]any{
			"jp": {"cards.json": []any{map[string]any{"id": 1}}},
			"en": {"cards.json": []any{map[string]any{"id": 2}}},
		},
	}
	statusStore := newFakeSyncStatusStore(nil)
	cache := &fakeSyncCache{}

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{sourceJP, sourceEN}, loader, cache, statusStore, nil, 2)

	if err := usecase.SyncRegion(context.Background(), "jp"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 1 {
		t.Fatalf("expected only one region load call, got %d", loader.loadCalls)
	}

	if _, exists := statusStore.latest("jp"); !exists {
		t.Fatalf("expected jp status saved")
	}
	if _, exists := statusStore.latest("en"); exists {
		t.Fatalf("did not expect en status to be updated")
	}
}

func TestSyncRegionReturnsNotFoundForUnknownRegion(t *testing.T) {
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{{Region: "jp"}}, &fakeSyncLoader{}, &fakeSyncCache{}, newFakeSyncStatusStore(nil), nil, 1)

	err := usecase.SyncRegion(context.Background(), "unknown")
	if !errors.Is(err, ErrRegionNotFound) {
		t.Fatalf("expected ErrRegionNotFound, got %v", err)
	}
}
