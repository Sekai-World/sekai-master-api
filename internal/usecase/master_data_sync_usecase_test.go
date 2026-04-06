package usecase

import (
	"context"
	"errors"
	"fmt"
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
	loadErrByZone  map[string]error
	loadCalls      int
}

func (loader *fakeSyncLoader) LoadRegion(_ context.Context, source masterdata.Source) (map[string]any, error) {
	loader.mu.Lock()
	defer loader.mu.Unlock()

	loader.loadCalls++
	if err, exists := loader.loadErrByZone[source.Region]; exists && err != nil {
		return nil, err
	}
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

type fakeCurrentEventCache struct {
	mu sync.Mutex

	events        []map[string]any
	currentEvents []map[string]any
	storeCalls    int
}

func (cache *fakeCurrentEventCache) StoreRegion(_ context.Context, _ string, payload map[string]any) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.storeCalls++
	currentPayload, ok := payload["currentEvents.json"]
	if !ok {
		cache.currentEvents = []map[string]any{}
		return nil
	}

	items, ok := currentPayload.([]any)
	if !ok {
		cache.currentEvents = []map[string]any{}
		return nil
	}

	next := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		next = append(next, record)
	}

	cache.currentEvents = next
	return nil
}

func (cache *fakeCurrentEventCache) GetByID(_ context.Context, _, _, _ string) (map[string]any, bool, error) {
	return nil, false, nil
}

func (cache *fakeCurrentEventCache) ListByPage(_ context.Context, _, entity string, page int, pageSize int) ([]map[string]any, int, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	var source []map[string]any
	switch strings.ToLower(strings.TrimSpace(entity)) {
	case "events":
		source = cache.events
	case "currentevents":
		source = cache.currentEvents
	default:
		return []map[string]any{}, 0, nil
	}

	total := len(source)
	if total == 0 {
		return []map[string]any{}, 0, nil
	}

	start := (page - 1) * pageSize
	if start >= total {
		return []map[string]any{}, total, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	items := make([]map[string]any, 0, end-start)
	for _, record := range source[start:end] {
		copied := make(map[string]any, len(record))
		for key, value := range record {
			copied[key] = value
		}
		items = append(items, copied)
	}

	return items, total, nil
}

func (cache *fakeCurrentEventCache) Search(_ context.Context, _, _, _ string, _ []string, _ int) ([]masterdata.SearchMatch, error) {
	return nil, nil
}

func (cache *fakeCurrentEventCache) StoreCallCount() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	return cache.storeCalls
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
	stableByZone  map[string]masterdata.SyncStatus
}

func newFakeSyncStatusStore(seed []masterdata.SyncStatus) *fakeSyncStatusStore {
	store := &fakeSyncStatusStore{
		byZone:        make(map[string]masterdata.SyncStatus),
		saved:         make([]masterdata.SyncStatus, 0),
		successByZone: make(map[string]masterdata.SyncStatus),
		stableByZone:  make(map[string]masterdata.SyncStatus),
	}
	for _, item := range seed {
		store.byZone[item.Region] = item
		if !strings.EqualFold(item.Status, "running") {
			store.stableByZone[item.Region] = item
		}
	}

	return store
}

func (store *fakeSyncStatusStore) Save(_ context.Context, status masterdata.SyncStatus) error {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.byZone[status.Region] = status
	store.saved = append(store.saved, status)
	if !strings.EqualFold(status.Status, "running") {
		store.stableByZone[status.Region] = status
	}
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

func (store *fakeSyncStatusStore) ListLatestStable(_ context.Context) ([]masterdata.SyncStatus, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	items := make([]masterdata.SyncStatus, 0, len(store.stableByZone))
	for _, item := range store.stableByZone {
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

func (store *fakeSyncStatusStore) savedByRegion(region string) []masterdata.SyncStatus {
	store.mu.Lock()
	defer store.mu.Unlock()

	items := make([]masterdata.SyncStatus, 0)
	for _, item := range store.saved {
		if item.Region == region {
			items = append(items, item)
		}
	}

	return items
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

func TestDashboardStatusFallsBackFromStaleRunningWhenSyncCompleted(t *testing.T) {
	now := time.Now().UTC()
	statusStore := newFakeSyncStatusStore(nil)
	statusStore.byZone["jp"] = masterdata.SyncStatus{
		Region:    "jp",
		Status:    "running",
		UpdatedAt: now,
	}
	statusStore.stableByZone["jp"] = masterdata.SyncStatus{
		Region:         "jp",
		Status:         "success",
		FileCount:      12,
		SourceCommit:   "commit-1",
		LastSyncedAt:   now.Add(-time.Minute),
		SyncDurationMS: 1234,
		UpdatedAt:      now.Add(-time.Minute),
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, nil, statusStore, nil, 1)

	statuses, err := usecase.DashboardStatus(context.Background())
	if err != nil {
		t.Fatalf("expected dashboard status success, got %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected one status item, got %d", len(statuses))
	}
	if statuses[0].Status != "success" {
		t.Fatalf("expected stale running status to fall back to success, got %s", statuses[0].Status)
	}
}

func TestDashboardStatusKeepsRunningWhileSyncActive(t *testing.T) {
	now := time.Now().UTC()
	statusStore := newFakeSyncStatusStore(nil)
	statusStore.byZone["jp"] = masterdata.SyncStatus{
		Region:    "jp",
		Status:    "running",
		UpdatedAt: now,
	}
	statusStore.stableByZone["jp"] = masterdata.SyncStatus{
		Region:    "jp",
		Status:    "success",
		UpdatedAt: now.Add(-time.Minute),
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, nil, statusStore, nil, 1)
	usecase.syncRunning.Store(true)

	statuses, err := usecase.DashboardStatus(context.Background())
	if err != nil {
		t.Fatalf("expected dashboard status success, got %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected one status item, got %d", len(statuses))
	}
	if statuses[0].Status != "running" {
		t.Fatalf("expected running status while sync is active, got %s", statuses[0].Status)
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

func TestSyncAllSkipWritesSuccessAfterPendingWhenRegionIndexMissing(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    7,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: "same-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "same-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {"cards.json": []any{map[string]any{"id": 1, "prefix": "should-not-load"}}},
		},
	}
	cache := &fakeSyncCache{rebuildFromRedisOK: true, hasRegionIndexSet: true, hasRegionIndex: false}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	saved := statusStore.savedByRegion("jp")
	if len(saved) < 2 {
		t.Fatalf("expected at least pending and success statuses, got %d", len(saved))
	}

	if !strings.EqualFold(saved[0].Status, "pending") {
		t.Fatalf("expected first status pending, got %s", saved[0].Status)
	}

	if !strings.EqualFold(saved[len(saved)-1].Status, "success") {
		t.Fatalf("expected final status success, got %s", saved[len(saved)-1].Status)
	}

	if saved[len(saved)-1].UpdatedAt.Equal(saved[0].UpdatedAt) {
		t.Fatalf("expected success updated_at to differ from pending updated_at to avoid latest-status tie")
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

func TestSyncAllFallsBackToPreviousStateOnRateLimit(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    8,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: "prev-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "next-commit"},
		loadErrByZone:  map[string]error{"jp": errors.New("api rate limit exceeded")},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error on rate limit fallback, got %v", err)
	}

	if loader.loadCalls != 1 {
		t.Fatalf("expected one load attempt, got %d", loader.loadCalls)
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected fallback status for jp")
	}
	if !strings.EqualFold(latest.Status, "success") {
		t.Fatalf("expected fallback status success, got %s", latest.Status)
	}
	if latest.SourceCommit != "prev-commit" {
		t.Fatalf("expected fallback commit prev-commit, got %s", latest.SourceCommit)
	}
}

func TestSyncAllFallsBackAndRestoresBackupOnRateLimit(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    3,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: "prev-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "next-commit"},
		loadErrByZone:  map[string]error{"jp": errors.New("too many requests")},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)
	backupStore := NewFileMasterDataPayloadBackupStore(t.TempDir())
	if err := backupStore.SaveRegionPayload(context.Background(), source, "prev-commit", map[string]any{
		"cards.json": []any{map[string]any{"id": 1, "prefix": "from-backup"}},
	}); err != nil {
		t.Fatalf("save backup payload: %v", err)
	}
	usecase.backupStore = backupStore

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error on rate limit fallback with backup, got %v", err)
	}

	if cache.storeCalls != 1 {
		t.Fatalf("expected one cache restore call from backup, got %d", cache.storeCalls)
	}
}

func TestSyncAllRateLimitWithoutPreviousStatusFails(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "next-commit"},
		loadErrByZone:  map[string]error{"jp": errors.New("api rate limit exceeded")},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	err := usecase.SyncAll(context.Background())
	if err == nil {
		t.Fatalf("expected error when rate limit occurs without previous status")
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected failed status for jp")
	}
	if !strings.EqualFold(latest.Status, "failed") {
		t.Fatalf("expected failed status, got %s", latest.Status)
	}
}

func TestCurrentEventConcurrentRequestsOnlyStoreCacheOnce(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	nowMillis := now.UnixMilli()

	cache := &fakeCurrentEventCache{
		events: []map[string]any{
			{
				"id":       1,
				"name":     "current-event",
				"startAt":  nowMillis - 60_000,
				"closedAt": nowMillis + 60_000,
			},
		},
		currentEvents: []map[string]any{},
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)

	const workers = 32
	start := make(chan struct{})
	errCh := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			record, found, err := usecase.CurrentEvent(context.Background(), "jp", now)
			if err != nil {
				errCh <- err
				return
			}
			if !found {
				errCh <- errors.New("expected current event found")
				return
			}
			if fmt.Sprintf("%v", record["id"]) != "1" {
				errCh <- fmt.Errorf("expected id=1, got %v", record["id"])
				return
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent current event query failed: %v", err)
		}
	}

	if cache.StoreCallCount() != 1 {
		t.Fatalf("expected current event cache to be stored once, got %d", cache.StoreCallCount())
	}
}

func TestCurrentEventSupportsSecondBasedTimestamps(t *testing.T) {
	now := time.Unix(1_772_438_533, 0).UTC()
	nowSeconds := now.Unix()

	cache := &fakeCurrentEventCache{
		events: []map[string]any{
			{
				"id":       777,
				"name":     "second-based-event",
				"startAt":  nowSeconds - 120,
				"closedAt": nowSeconds + 120,
			},
		},
		currentEvents: []map[string]any{},
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)

	record, found, err := usecase.CurrentEvent(context.Background(), "jp", now)
	if err != nil {
		t.Fatalf("current event query error: %v", err)
	}
	if !found {
		t.Fatalf("expected current event to be found for second-based timestamps")
	}
	if fmt.Sprintf("%v", record["id"]) != "777" {
		t.Fatalf("expected id=777, got %v", record["id"])
	}
}

func TestCurrentEventIgnoresZeroClosedAtUsesAggregateAt(t *testing.T) {
	now := time.UnixMilli(1_772_438_533_000).UTC()
	nowMillis := now.UnixMilli()

	cache := &fakeCurrentEventCache{
		events: []map[string]any{
			{
				"id":          888,
				"name":        "zero-closed-at-event",
				"startAt":     nowMillis - 60_000,
				"closedAt":    0,
				"aggregateAt": nowMillis + 60_000,
			},
		},
		currentEvents: []map[string]any{},
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)

	record, found, err := usecase.CurrentEvent(context.Background(), "jp", now)
	if err != nil {
		t.Fatalf("current event query error: %v", err)
	}
	if !found {
		t.Fatalf("expected current event to be found when closedAt is zero")
	}
	if fmt.Sprintf("%v", record["id"]) != "888" {
		t.Fatalf("expected id=888, got %v", record["id"])
	}
}

func TestCurrentEventSupportsRFC3339Timestamps(t *testing.T) {
	now := time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Minute).Format(time.RFC3339)
	end := now.Add(2 * time.Minute).Format(time.RFC3339)

	cache := &fakeCurrentEventCache{
		events: []map[string]any{
			{
				"id":       889,
				"name":     "rfc3339-event",
				"startAt":  start,
				"closedAt": end,
			},
		},
		currentEvents: []map[string]any{},
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)

	record, found, err := usecase.CurrentEvent(context.Background(), "jp", now)
	if err != nil {
		t.Fatalf("current event query error: %v", err)
	}
	if !found {
		t.Fatalf("expected current event to be found for RFC3339 timestamps")
	}
	if fmt.Sprintf("%v", record["id"]) != "889" {
		t.Fatalf("expected id=889, got %v", record["id"])
	}
}

func TestCurrentEventSupportsMicrosecondEpoch(t *testing.T) {
	now := time.UnixMilli(1_772_438_533_000).UTC()
	nowMicros := now.UnixMicro()

	cache := &fakeCurrentEventCache{
		events: []map[string]any{
			{
				"id":       890,
				"name":     "microsecond-event",
				"startAt":  nowMicros - 120_000_000,
				"closedAt": nowMicros + 120_000_000,
			},
		},
		currentEvents: []map[string]any{},
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)

	record, found, err := usecase.CurrentEvent(context.Background(), "jp", now)
	if err != nil {
		t.Fatalf("current event query error: %v", err)
	}
	if !found {
		t.Fatalf("expected current event to be found for microsecond epoch")
	}
	if fmt.Sprintf("%v", record["id"]) != "890" {
		t.Fatalf("expected id=890, got %v", record["id"])
	}
}

func TestCurrentEventSupportsDecimalNumericStringTimestamp(t *testing.T) {
	now := time.UnixMilli(1_772_438_533_000).UTC()
	nowMillis := now.UnixMilli()

	cache := &fakeCurrentEventCache{
		events: []map[string]any{
			{
				"id":       891,
				"name":     "decimal-string-event",
				"startAt":  fmt.Sprintf("%d.0", nowMillis-120_000),
				"closedAt": fmt.Sprintf("%d.0", nowMillis+120_000),
			},
		},
		currentEvents: []map[string]any{},
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)

	record, found, err := usecase.CurrentEvent(context.Background(), "jp", now)
	if err != nil {
		t.Fatalf("current event query error: %v", err)
	}
	if !found {
		t.Fatalf("expected current event to be found for decimal numeric string timestamps")
	}
	if fmt.Sprintf("%v", record["id"]) != "891" {
		t.Fatalf("expected id=891, got %v", record["id"])
	}
}

func TestCurrentEventScansBeyondFirstHundredRecords(t *testing.T) {
	now := time.UnixMilli(1_772_438_533_000).UTC()
	nowMillis := now.UnixMilli()

	events := make([]map[string]any, 0, 120)
	for i := 1; i <= 119; i++ {
		events = append(events, map[string]any{
			"id":       i,
			"name":     fmt.Sprintf("past-event-%d", i),
			"startAt":  nowMillis - 1_000_000,
			"closedAt": nowMillis - 500_000,
		})
	}
	events = append(events, map[string]any{
		"id":       120,
		"name":     "current-event-on-second-page",
		"startAt":  nowMillis - 60_000,
		"closedAt": nowMillis + 60_000,
	})

	cache := &fakeCurrentEventCache{
		events:        events,
		currentEvents: []map[string]any{},
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)

	record, found, err := usecase.CurrentEvent(context.Background(), "jp", now)
	if err != nil {
		t.Fatalf("current event query error: %v", err)
	}
	if !found {
		t.Fatalf("expected current event found beyond first 100 records")
	}
	if fmt.Sprintf("%v", record["id"]) != "120" {
		t.Fatalf("expected id=120, got %v", record["id"])
	}
}
