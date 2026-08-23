package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	mu                 sync.Mutex
	resolvedByZone     map[string]string
	payloadByZone      map[string]map[string]any
	loadErrByZone      map[string]error
	manifestByZone     map[string]map[string]any
	manifestErrByZone  map[string]error
	manifestRefsByZone map[string]string
	loadCalls          int
	resolveCalls       int
	manifestCalls      int
}

type timedSyncLoader struct {
	mu                 sync.Mutex
	payloadByZone      map[string]map[string]any
	loadDelayByZone    map[string]time.Duration
	loadCallsByZone    map[string]int
	activeLoads        int
	maxConcurrentLoads int
	canceledLoads      int
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

	loader.resolveCalls++
	return loader.resolvedByZone[source.Region], nil
}

func (loader *fakeSyncLoader) LoadVersionManifest(_ context.Context, source masterdata.Source) (map[string]any, bool, error) {
	loader.mu.Lock()
	defer loader.mu.Unlock()

	loader.manifestCalls++
	if loader.manifestRefsByZone == nil {
		loader.manifestRefsByZone = make(map[string]string)
	}
	loader.manifestRefsByZone[source.Region] = source.Ref
	if err, exists := loader.manifestErrByZone[source.Region]; exists && err != nil {
		return nil, false, err
	}
	manifest, found := loader.manifestByZone[source.Region]
	return manifest, found, nil
}

func (loader *timedSyncLoader) LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error) {
	loader.mu.Lock()
	if loader.loadCallsByZone == nil {
		loader.loadCallsByZone = make(map[string]int)
	}
	loader.loadCallsByZone[source.Region]++
	loader.activeLoads++
	if loader.activeLoads > loader.maxConcurrentLoads {
		loader.maxConcurrentLoads = loader.activeLoads
	}
	delay := loader.loadDelayByZone[source.Region]
	payload := loader.payloadByZone[source.Region]
	loader.mu.Unlock()

	defer func() {
		loader.mu.Lock()
		loader.activeLoads--
		loader.mu.Unlock()
	}()

	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			loader.mu.Lock()
			loader.canceledLoads++
			loader.mu.Unlock()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	if payload == nil {
		return map[string]any{}, nil
	}

	return payload, nil
}

func (loader *timedSyncLoader) MaxConcurrentLoads() int {
	loader.mu.Lock()
	defer loader.mu.Unlock()

	return loader.maxConcurrentLoads
}

func (loader *timedSyncLoader) CanceledLoads() int {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	return loader.canceledLoads
}

type fakeSyncCache struct {
	mu                 sync.Mutex
	storeCalls         int
	rebuildCalls       int
	loadCalls          int
	rebuildFromRedisOK bool
	loadFromRedisOK    bool
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

func (cache *fakeCurrentEventCache) ListAll(_ context.Context, _, entity string) ([]map[string]any, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	var source []map[string]any
	switch strings.ToLower(strings.TrimSpace(entity)) {
	case "events":
		source = cache.events
	case "currentevents":
		source = cache.currentEvents
	default:
		return []map[string]any{}, nil
	}

	items := make([]map[string]any, 0, len(source))
	for _, record := range source {
		copied := make(map[string]any, len(record))
		for key, value := range record {
			copied[key] = value
		}
		items = append(items, copied)
	}

	return items, nil
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

func (cache *fakeSyncCache) ListAll(_ context.Context, _, _ string) ([]map[string]any, error) {
	return nil, nil
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

func (cache *fakeSyncCache) LoadRegionIndexFromRedis(_ context.Context, _ string) (bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.loadCalls++
	if cache.loadFromRedisOK {
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

type fakeSyncEventPublisher struct {
	mu     sync.Mutex
	events []masterdata.SyncUpdatedEvent
}

func (publisher *fakeSyncEventPublisher) PublishMasterDataUpdated(_ context.Context, event masterdata.SyncUpdatedEvent) error {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()

	publisher.events = append(publisher.events, event)
	return nil
}

func (publisher *fakeSyncEventPublisher) listEvents() []masterdata.SyncUpdatedEvent {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()

	items := make([]masterdata.SyncUpdatedEvent, 0, len(publisher.events))
	items = append(items, publisher.events...)
	return items
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

type manifestSyncTestOptions struct {
	previousCommit    string
	previousFileCount int
	resolvedCommit    string
	remoteManifest    map[string]any
	remoteManifestErr error
	archivePayload    map[string]any
	archiveLoadErr    error
	backupCommit      string
	backupPayload     map[string]any
	cacheReady        bool
}

type manifestSyncTestFixture struct {
	source         masterdata.Source
	previousStatus masterdata.SyncStatus
	loader         *fakeSyncLoader
	cache          *fakeSyncCache
	statusStore    *fakeSyncStatusStore
	backupStore    MasterDataPayloadBackupStore
	usecase        *MasterDataSyncUsecase
}

func newManifestSyncTestFixture(t *testing.T, options manifestSyncTestOptions) *manifestSyncTestFixture {
	t.Helper()

	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       source.Region,
		Status:       "success",
		FileCount:    options.previousFileCount,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: options.previousCommit,
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}
	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{source.Region: options.resolvedCommit},
		manifestByZone: map[string]map[string]any{
			source.Region: options.remoteManifest,
		},
		payloadByZone: map[string]map[string]any{
			source.Region: options.archivePayload,
		},
	}
	if options.remoteManifestErr != nil {
		loader.manifestErrByZone = map[string]error{source.Region: options.remoteManifestErr}
	}
	if options.archiveLoadErr != nil {
		loader.loadErrByZone = map[string]error{source.Region: options.archiveLoadErr}
	}

	cache := &fakeSyncCache{}
	if options.cacheReady {
		cache.loadFromRedisOK = true
	}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})
	backupStore := NewFileMasterDataPayloadBackupStore(t.TempDir())
	if options.backupPayload != nil {
		if err := backupStore.SaveRegionPayload(context.Background(), source, options.backupCommit, options.backupPayload); err != nil {
			t.Fatalf("save local backup: %v", err)
		}
	}

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)
	usecase.SetBackupStore(backupStore)

	return &manifestSyncTestFixture{
		source:         source,
		previousStatus: previousStatus,
		loader:         loader,
		cache:          cache,
		statusStore:    statusStore,
		backupStore:    backupStore,
		usecase:        usecase,
	}
}

func manifestPayload(manifest map[string]any, prefix string) map[string]any {
	return map[string]any{
		"data/versions.json": manifest,
		"cards.json":         []any{map[string]any{"id": 1, "prefix": prefix}},
	}
}

func containsSyncProgressEvent(events []masterdata.SyncUpdatedEvent, region string, status string, phase string, message string) bool {
	for _, event := range events {
		if strings.TrimSpace(event.Event) != "master_data_sync_progress" {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(event.Region), strings.TrimSpace(region)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(event.Status), strings.TrimSpace(status)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(event.Phase), strings.TrimSpace(phase)) {
			continue
		}
		if strings.TrimSpace(event.Message) != strings.TrimSpace(message) {
			continue
		}
		return true
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
	if cache.rebuildCalls != 2 {
		t.Fatalf("expected redis index rebuild calls on skip and readiness annotation, got rebuildCalls=%d", cache.rebuildCalls)
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

func TestSyncAllLocalBackupRestoreOnCommitUnchangedPublishesIntermediateProgress(t *testing.T) {
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
	cache := &fakeSyncCache{rebuildFromRedisOK: false}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})
	publisher := &fakeSyncEventPublisher{}

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, publisher, 1)
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

	events := publisher.listEvents()
	if !containsSyncProgressEvent(events, "jp", "running", "cache", "restoring cache from local backup") {
		t.Fatalf("expected running cache progress event for local backup restore")
	}
	if !containsSyncProgressEvent(events, "jp", "success", "compare", "commit unchanged, restored cache from local backup and skipped sync") {
		t.Fatalf("expected success compare event for local backup restore")
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

func TestDashboardStatusKeepsSuccessfulStatusWhenRuntimeIndexMissing(t *testing.T) {
	now := time.Now().UTC()
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{
			Region:       "jp",
			Status:       "success",
			FileCount:    12,
			LastSyncedAt: now.Add(-time.Minute),
			UpdatedAt:    now.Add(-time.Minute),
		},
	})
	cache := &fakeSyncCache{
		hasRegionIndexSet: true,
		hasRegionIndex:    false,
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)

	statuses, err := usecase.DashboardStatus(context.Background())
	if err != nil {
		t.Fatalf("expected dashboard status success, got %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected one status item, got %d", len(statuses))
	}
	if statuses[0].Status != "success" {
		t.Fatalf("expected persisted success to stay successful, got %s", statuses[0].Status)
	}
	if statuses[0].ErrorMessage != "" {
		t.Fatalf("expected persisted success to avoid cache readiness message, got %q", statuses[0].ErrorMessage)
	}
	if cache.loadCalls != 0 {
		t.Fatalf("expected dashboard status to avoid redis index load, got %d calls", cache.loadCalls)
	}
	if cache.rebuildCalls != 0 {
		t.Fatalf("expected dashboard status to avoid redis index rebuild, got %d calls", cache.rebuildCalls)
	}
}

func TestRuntimeSearchIndexReadyRegionsSkipsSuccessfulStatusWhenRedisCacheMissing(t *testing.T) {
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{Region: "jp", Status: "success", UpdatedAt: time.Now().UTC()},
	})
	cache := &fakeSyncCache{
		hasRegionIndexSet: true,
		hasRegionIndex:    false,
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)

	regions, err := usecase.RuntimeSearchIndexReadyRegions(context.Background())
	if err != nil {
		t.Fatalf("expected ready regions success, got %v", err)
	}
	if len(regions) != 0 {
		t.Fatalf("expected no ready regions when redis cache is missing, got %v", regions)
	}
	if cache.loadCalls != 0 {
		t.Fatalf("expected ready regions to avoid redis index load, got %d calls", cache.loadCalls)
	}
	if cache.rebuildCalls != 0 {
		t.Fatalf("expected ready regions to avoid redis index rebuild, got %d calls", cache.rebuildCalls)
	}
}

func TestSuccessfulSyncRegionsNormalizesDeduplicatesAndSorts(t *testing.T) {
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{Region: " JP ", Status: "SUCCESS"},
		{Region: "en", Status: "failed"},
		{Region: "jp", Status: "success"},
		{Region: " EN ", Status: " success "},
		{Region: "  ", Status: "success"},
	})

	usecase := NewMasterDataSyncUsecase(nil, nil, nil, statusStore, nil, 1)

	regions, err := usecase.SuccessfulSyncRegions(context.Background())
	if err != nil {
		t.Fatalf("expected successful sync regions, got %v", err)
	}
	if len(regions) != 2 || regions[0] != "en" || regions[1] != "jp" {
		t.Fatalf("expected normalized, unique, sorted successful regions, got %v", regions)
	}
}

func TestRuntimeSearchIndexReadyRegionsSkipsSuccessfulStatusEvenWhenRedisIndexCanLoad(t *testing.T) {
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{Region: "jp", Status: "success", UpdatedAt: time.Now().UTC()},
	})
	cache := &fakeSyncCache{
		hasRegionIndexSet: true,
		hasRegionIndex:    false,
		loadFromRedisOK:   true,
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)

	regions, err := usecase.RuntimeSearchIndexReadyRegions(context.Background())
	if err != nil {
		t.Fatalf("expected ready regions success, got %v", err)
	}
	if len(regions) != 0 {
		t.Fatalf("expected no ready regions without retained runtime index, got %v", regions)
	}
	if cache.loadCalls != 0 {
		t.Fatalf("expected ready regions to avoid redis index load, got %d calls", cache.loadCalls)
	}
}

func TestRuntimeSearchIndexReadyRegionsSkipsSuccessfulStatusEvenWhenRedisIndexCanRebuild(t *testing.T) {
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{Region: "jp", Status: "success", UpdatedAt: time.Now().UTC()},
	})
	cache := &fakeSyncCache{
		hasRegionIndexSet:  true,
		hasRegionIndex:     false,
		rebuildFromRedisOK: true,
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)

	regions, err := usecase.RuntimeSearchIndexReadyRegions(context.Background())
	if err != nil {
		t.Fatalf("expected ready regions success, got %v", err)
	}
	if len(regions) != 0 {
		t.Fatalf("expected no ready regions without retained runtime index, got %v", regions)
	}
	if cache.rebuildCalls != 0 {
		t.Fatalf("expected ready regions to avoid redis index rebuild, got %d calls", cache.rebuildCalls)
	}
}

func TestRuntimeSearchIndexReadyRegionsIncludesSuccessfulStatusWhenRuntimeIndexIsRetained(t *testing.T) {
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{Region: "jp", Status: "success", UpdatedAt: time.Now().UTC()},
	})
	cache := &fakeSyncCache{
		hasRegionIndexSet: true,
		hasRegionIndex:    true,
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)

	regions, err := usecase.RuntimeSearchIndexReadyRegions(context.Background())
	if err != nil {
		t.Fatalf("expected ready regions success, got %v", err)
	}
	if len(regions) != 1 || regions[0] != "jp" {
		t.Fatalf("expected jp ready from retained runtime index, got %v", regions)
	}
	if cache.loadCalls != 0 {
		t.Fatalf("expected ready regions to avoid redis index load, got %d calls", cache.loadCalls)
	}
	if cache.rebuildCalls != 0 {
		t.Fatalf("expected ready regions to avoid redis index rebuild, got %d calls", cache.rebuildCalls)
	}
}

func TestInterruptedRegionsReturnsConfiguredRunningAndPendingRegions(t *testing.T) {
	now := time.Now().UTC()
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{Region: "jp", Status: "running", UpdatedAt: now},
		{Region: "en", Status: "pending", UpdatedAt: now},
		{Region: "tw", Status: "success", UpdatedAt: now},
		{Region: "orphan", Status: "running", UpdatedAt: now},
	})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp"},
		{Region: "en"},
		{Region: "tw"},
	}, nil, nil, statusStore, nil, 1)

	regions, err := usecase.InterruptedRegions(context.Background())
	if err != nil {
		t.Fatalf("InterruptedRegions() error = %v", err)
	}

	if len(regions) != 2 || regions[0] != "en" || regions[1] != "jp" {
		t.Fatalf("InterruptedRegions() = %v, want [en jp]", regions)
	}
}

func TestRecoverInterruptedSyncOnlyRetriesInterruptedRegions(t *testing.T) {
	sourceJP := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	sourceEN := masterdata.Source{Region: "en", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}

	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{Region: "jp", Status: "running", UpdatedAt: time.Now().UTC()},
		{Region: "en", Status: "success", UpdatedAt: time.Now().UTC()},
	})

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{
			"jp": "commit-jp",
			"en": "commit-en",
		},
		payloadByZone: map[string]map[string]any{
			"jp": {"cards.json": []any{map[string]any{"id": 1}}},
			"en": {"cards.json": []any{map[string]any{"id": 2}}},
		},
	}
	cache := &fakeSyncCache{}

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{sourceJP, sourceEN}, loader, cache, statusStore, nil, 1)

	regions, err := usecase.RecoverInterruptedSync(context.Background())
	if err != nil {
		t.Fatalf("RecoverInterruptedSync() error = %v", err)
	}

	if len(regions) != 1 || regions[0] != "jp" {
		t.Fatalf("RecoverInterruptedSync() regions = %v, want [jp]", regions)
	}
	if loader.loadCalls != 1 {
		t.Fatalf("expected one load call for interrupted region recovery, got %d", loader.loadCalls)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected one cache store call for interrupted region recovery, got %d", cache.storeCalls)
	}
}

func TestRecoverInterruptedSyncRespectsConfiguredConcurrency(t *testing.T) {
	sources := []masterdata.Source{
		{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "en", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "tw", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
	}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{Region: "jp", Status: "running", UpdatedAt: time.Now().UTC()},
		{Region: "en", Status: "pending", UpdatedAt: time.Now().UTC()},
		{Region: "tw", Status: "running", UpdatedAt: time.Now().UTC()},
	})
	loader := &timedSyncLoader{
		payloadByZone: map[string]map[string]any{
			"jp": {"cards.json": []any{map[string]any{"id": 1}}},
			"en": {"cards.json": []any{map[string]any{"id": 2}}},
			"tw": {"cards.json": []any{map[string]any{"id": 3}}},
		},
		loadDelayByZone: map[string]time.Duration{
			"jp": 40 * time.Millisecond,
			"en": 40 * time.Millisecond,
			"tw": 40 * time.Millisecond,
		},
	}
	cache := &fakeSyncCache{}

	usecase := NewMasterDataSyncUsecase(sources, loader, cache, statusStore, nil, 2)

	regions, err := usecase.RecoverInterruptedSync(context.Background())
	if err != nil {
		t.Fatalf("RecoverInterruptedSync() error = %v", err)
	}

	if len(regions) != 3 {
		t.Fatalf("RecoverInterruptedSync() regions = %v, want all 3 interrupted regions", regions)
	}
	if loader.MaxConcurrentLoads() != 2 {
		t.Fatalf("expected interrupted recovery to respect concurrency 2, got max concurrent loads %d", loader.MaxConcurrentLoads())
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

func TestSyncAllSkipsChangedCommitWhenVersionsManifestIsUnchanged(t *testing.T) {
	manifest := map[string]any{"dataVersion": "20260802"}
	latestPayload := manifestPayload(manifest, "from-backup")
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit:    "old-commit",
		previousFileCount: 99,
		resolvedCommit:    "new-commit",
		remoteManifest:    manifest,
		backupCommit:      "old-commit",
		backupPayload:     latestPayload,
		cacheReady:        true,
	})

	if err := fixture.usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if fixture.loader.loadCalls != 0 {
		t.Fatalf("expected archive load to be skipped, got loadCalls=%d", fixture.loader.loadCalls)
	}
	if fixture.loader.manifestCalls != 1 {
		t.Fatalf("expected one manifest load, got manifestCalls=%d", fixture.loader.manifestCalls)
	}
	if fixture.loader.manifestRefsByZone["jp"] != "new-commit" {
		t.Fatalf("expected manifest ref to be pinned to new-commit, got %q", fixture.loader.manifestRefsByZone["jp"])
	}
	if fixture.cache.storeCalls != 0 {
		t.Fatalf("expected ready cache not to be restored, got storeCalls=%d", fixture.cache.storeCalls)
	}

	latest, exists := fixture.statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest jp status")
	}
	if latest.Status != "success" || latest.SourceCommit != "new-commit" {
		t.Fatalf("expected successful status pinned to new-commit, got %#v", latest)
	}
	if latest.FileCount != len(latestPayload) {
		t.Fatalf("expected file count to equal reusable payload count %d, got %d", len(latestPayload), latest.FileCount)
	}

	_, rebasedCommit, _, found, err := fixture.backupStore.LoadLatestRegionPayload(context.Background(), fixture.source)
	if err != nil {
		t.Fatalf("load rebased local backup: %v", err)
	}
	if !found || rebasedCommit != "new-commit" {
		t.Fatalf("expected local backup to be rebased to new-commit, found=%t commit=%q", found, rebasedCommit)
	}
}

func TestSyncAllRestoresManifestMatchToCacheWhenCacheIsNotReady(t *testing.T) {
	manifest := map[string]any{"dataVersion": "20260802"}
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit:    "old-commit",
		previousFileCount: 99,
		resolvedCommit:    "new-commit",
		remoteManifest:    manifest,
		backupCommit:      "old-commit",
		backupPayload:     manifestPayload(manifest, "from-backup"),
	})

	if err := fixture.usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if fixture.loader.loadCalls != 0 {
		t.Fatalf("expected archive load to be skipped, got loadCalls=%d", fixture.loader.loadCalls)
	}
	if fixture.cache.storeCalls != 1 {
		t.Fatalf("expected exactly one cache restore, got storeCalls=%d", fixture.cache.storeCalls)
	}

	latest, exists := fixture.statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest jp status")
	}
	if latest.Status != "success" || latest.SourceCommit != "new-commit" {
		t.Fatalf("expected successful status pinned to new-commit, got %#v", latest)
	}
}

func TestSyncAllFallsBackToArchiveWhenManifestBackupCommitDiffers(t *testing.T) {
	manifest := map[string]any{"dataVersion": "20260802"}
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit:    "old-commit",
		previousFileCount: 2,
		resolvedCommit:    "new-commit",
		remoteManifest:    manifest,
		archiveLoadErr:    errors.New("archive fallback"),
		backupCommit:      "other-commit",
		backupPayload:     manifestPayload(manifest, "from-backup"),
	})

	if err := fixture.usecase.SyncAll(context.Background()); err == nil {
		t.Fatal("expected archive fallback load failure")
	}
	if fixture.loader.loadCalls != 1 {
		t.Fatalf("expected exactly one archive fallback load, got loadCalls=%d", fixture.loader.loadCalls)
	}
	if fixture.cache.storeCalls != 0 {
		t.Fatalf("expected manifest reuse not to restore the cache, got storeCalls=%d", fixture.cache.storeCalls)
	}

	for _, saved := range fixture.statusStore.savedByRegion("jp") {
		if strings.EqualFold(saved.Status, "success") && saved.SourceCommit == "new-commit" {
			t.Fatalf("did not expect manifest reuse to advance a success status to new-commit: %#v", saved)
		}
	}

	_, backupCommit, _, found, err := fixture.backupStore.LoadLatestRegionPayload(context.Background(), fixture.source)
	if err != nil {
		t.Fatalf("load local backup after fallback: %v", err)
	}
	if !found || backupCommit != "other-commit" {
		t.Fatalf("expected local backup to remain pinned to other-commit, found=%t commit=%q", found, backupCommit)
	}
}

func TestSyncAllLoadsRegionWhenChangedCommitManifestDiffers(t *testing.T) {
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit:    "old-commit",
		previousFileCount: 1,
		resolvedCommit:    "new-commit",
		remoteManifest:    map[string]any{"dataVersion": "new"},
		archivePayload:    map[string]any{"cards.json": []any{map[string]any{"id": 1}}},
		backupCommit:      "old-commit",
		backupPayload:     map[string]any{"data/versions.json": map[string]any{"dataVersion": "old"}},
		cacheReady:        true,
	})

	if err := fixture.usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if fixture.loader.loadCalls != 1 {
		t.Fatalf("expected changed manifest to use archive load, got loadCalls=%d", fixture.loader.loadCalls)
	}
}

func TestSyncAllLoadsRegionWhenChangedCommitManifestLoadFails(t *testing.T) {
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit:    "old-commit",
		resolvedCommit:    "new-commit",
		remoteManifestErr: errors.New("manifest unavailable"),
		archivePayload:    map[string]any{"cards.json": []any{map[string]any{"id": 1}}},
		backupCommit:      "old-commit",
		backupPayload:     map[string]any{"data/versions.json": map[string]any{"dataVersion": "old"}},
		cacheReady:        true,
	})

	if err := fixture.usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if fixture.loader.loadCalls != 1 {
		t.Fatalf("expected manifest failure to use archive load, got loadCalls=%d", fixture.loader.loadCalls)
	}
}

func TestSyncAllAppliesTimeoutPerRegion(t *testing.T) {
	sources := []masterdata.Source{
		{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "en", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
	}
	loader := &timedSyncLoader{
		payloadByZone: map[string]map[string]any{
			"jp": {"cards.json": []any{map[string]any{"id": 1}}},
			"en": {"cards.json": []any{map[string]any{"id": 2}}},
		},
		loadDelayByZone: map[string]time.Duration{
			"jp": 80 * time.Millisecond,
			"en": 10 * time.Millisecond,
		},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)

	usecase := NewMasterDataSyncUsecase(sources, loader, cache, statusStore, nil, 1)
	usecase.SetRegionTimeout(40 * time.Millisecond)

	err := usecase.SyncAll(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected sync error to include context deadline exceeded, got %v", err)
	}

	jpStatus, ok := statusStore.latest("jp")
	if !ok || !strings.EqualFold(jpStatus.Status, "failed") {
		t.Fatalf("expected jp timeout to be persisted as failed, got %#v exists=%t", jpStatus, ok)
	}
	enStatus, ok := statusStore.latest("en")
	if !ok || !strings.EqualFold(enStatus.Status, "success") {
		t.Fatalf("expected en to still complete successfully after jp timeout, got %#v exists=%t", enStatus, ok)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected only one successful cache store after per-region timeout handling, got %d", cache.storeCalls)
	}
}

func TestSyncAllAppliesSeparateJobTimeout(t *testing.T) {
	source := masterdata.Source{Region: "en", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	loader := &timedSyncLoader{
		payloadByZone:   map[string]map[string]any{"en": {"cards.json": []any{map[string]any{"id": 1}}}},
		loadDelayByZone: map[string]time.Duration{"en": 100 * time.Millisecond},
	}
	statusStore := newFakeSyncStatusStore(nil)
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, &fakeSyncCache{}, statusStore, nil, 1)
	usecase.SetRegionTimeout(time.Second)
	usecase.SetJobTimeout(20 * time.Millisecond)

	err := usecase.SyncAll(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected job timeout error, got %v", err)
	}
	if usecase.IsSyncRunning() {
		t.Fatal("expected running state to be released after job timeout")
	}
	if loader.CanceledLoads() != 1 {
		t.Fatalf("expected loader cancellation at job deadline, got %d cancellations", loader.CanceledLoads())
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

func TestSyncAllForceLoadsWhenChangedCommitManifestIsUnchanged(t *testing.T) {
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit: "old-commit",
		resolvedCommit: "new-commit",
		remoteManifest: map[string]any{"dataVersion": "same"},
		archivePayload: map[string]any{"cards.json": []any{map[string]any{"id": 1}}},
		backupCommit:   "old-commit",
		backupPayload:  map[string]any{"data/versions.json": map[string]any{"dataVersion": "same"}},
		cacheReady:     true,
	})

	if err := fixture.usecase.SyncAllForce(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if fixture.loader.loadCalls != 1 {
		t.Fatalf("expected force sync to use archive load, got loadCalls=%d", fixture.loader.loadCalls)
	}
	if fixture.loader.manifestCalls != 0 {
		t.Fatalf("expected force sync not to load manifest, got manifestCalls=%d", fixture.loader.manifestCalls)
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
	if cache.rebuildCalls != 2 {
		t.Fatalf("expected redis rebuild attempts during skip and readiness annotation, got %d", cache.rebuildCalls)
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

func TestSyncAllRestoresFromLocalBackupWhenStatusMissingInDevelopment(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "remote-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"data/versions.json": map[string]any{
					"dataVersion": "20260421",
				},
				"cards.json": []any{map[string]any{"id": 1, "prefix": "from-github"}},
			},
		},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)
	usecase.EnableDevelopmentBackupBootstrap(true)

	backupStore := NewFileMasterDataPayloadBackupStore(t.TempDir())
	if err := backupStore.SaveRegionPayload(context.Background(), source, "local-commit", map[string]any{
		"cards.json":  []any{map[string]any{"id": 99, "prefix": "from-local"}},
		"skills.json": []any{map[string]any{"id": 100, "name": "from-local-skill"}},
		"data/versions.json": map[string]any{
			"dataVersion": "20260421",
		},
	}); err != nil {
		t.Fatalf("save local backup: %v", err)
	}
	usecase.backupStore = backupStore

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.resolveCalls != 0 {
		t.Fatalf("expected remote compare to be skipped, got resolveCalls=%d", loader.resolveCalls)
	}
	if loader.loadCalls != 1 {
		t.Fatalf("expected one remote versions compare load, got loadCalls=%d", loader.loadCalls)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected one cache store from local backup, got %d", cache.storeCalls)
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected restored status for jp")
	}
	if latest.Status != "success" {
		t.Fatalf("expected success status, got %s", latest.Status)
	}
	if latest.SourceCommit != "local-commit" {
		t.Fatalf("expected restored commit local-commit, got %s", latest.SourceCommit)
	}
	if latest.FileCount != 3 {
		t.Fatalf("expected restored file_count=3, got %d", latest.FileCount)
	}
}

func TestSyncAllFallsBackToRemoteSyncWhenLocalBackupVersionsMismatchInDevelopment(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "remote-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"data/versions.json": map[string]any{
					"dataVersion": "20260422",
				},
				"cards.json": []any{map[string]any{"id": 1, "prefix": "from-github"}},
			},
		},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)
	usecase.EnableDevelopmentBackupBootstrap(true)

	backupStore := NewFileMasterDataPayloadBackupStore(t.TempDir())
	if err := backupStore.SaveRegionPayload(context.Background(), source, "local-commit", map[string]any{
		"data/versions.json": map[string]any{
			"dataVersion": "20260421",
		},
		"cards.json": []any{map[string]any{"id": 99, "prefix": "from-local"}},
	}); err != nil {
		t.Fatalf("save local backup: %v", err)
	}
	usecase.backupStore = backupStore

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.resolveCalls != 1 {
		t.Fatalf("expected remote compare to run after mismatch, got resolveCalls=%d", loader.resolveCalls)
	}
	if loader.loadCalls != 2 {
		t.Fatalf("expected one versions compare load + one remote sync load, got loadCalls=%d", loader.loadCalls)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected one cache store from remote payload, got %d", cache.storeCalls)
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest status for jp")
	}
	if latest.SourceCommit != "remote-commit" {
		t.Fatalf("expected remote commit remote-commit, got %s", latest.SourceCommit)
	}
}

func TestSyncAllBootstrapLocalBackupRestorePublishesIntermediateProgress(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "remote-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"data/versions.json": map[string]any{
					"dataVersion": "20260421",
				},
			},
		},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)
	publisher := &fakeSyncEventPublisher{}

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, publisher, 1)
	usecase.EnableDevelopmentBackupBootstrap(true)
	backupStore := NewFileMasterDataPayloadBackupStore(t.TempDir())
	if err := backupStore.SaveRegionPayload(context.Background(), source, "local-commit", map[string]any{
		"data/versions.json": map[string]any{
			"dataVersion": "20260421",
		},
		"cards.json": []any{map[string]any{"id": 99, "prefix": "from-local"}},
	}); err != nil {
		t.Fatalf("save local backup: %v", err)
	}
	usecase.backupStore = backupStore

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	events := publisher.listEvents()
	if !containsSyncProgressEvent(events, "jp", "running", "bootstrap", "comparing local versions.json with remote") {
		t.Fatalf("expected bootstrap compare progress event")
	}
	if !containsSyncProgressEvent(events, "jp", "running", "cache", "restoring cache from local backup") {
		t.Fatalf("expected running cache progress event")
	}
	if !containsSyncProgressEvent(events, "jp", "success", "bootstrap", "database status missing, restored cache from local backup") {
		t.Fatalf("expected bootstrap success event")
	}
}

func TestSyncAllDoesNotRestoreFromLocalBackupWhenStatusMissingOutsideDevelopment(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "remote-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "from-github"}},
			},
		},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	backupStore := NewFileMasterDataPayloadBackupStore(t.TempDir())
	if err := backupStore.SaveRegionPayload(context.Background(), source, "local-commit", map[string]any{
		"cards.json": []any{map[string]any{"id": 99, "prefix": "from-local"}},
	}); err != nil {
		t.Fatalf("save local backup: %v", err)
	}
	usecase.backupStore = backupStore

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.resolveCalls != 1 {
		t.Fatalf("expected remote compare to run outside development bootstrap, got resolveCalls=%d", loader.resolveCalls)
	}
	if loader.loadCalls != 1 {
		t.Fatalf("expected remote load to run outside development bootstrap, got loadCalls=%d", loader.loadCalls)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected one cache store from remote payload, got %d", cache.storeCalls)
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest status for jp")
	}
	if latest.SourceCommit != "remote-commit" {
		t.Fatalf("expected remote commit remote-commit, got %s", latest.SourceCommit)
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

func TestSyncAllDoesNotSetPendingWhenPersistedRegionIndexLoads(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "new-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "persisted-index"}},
			},
		},
	}
	cache := &fakeSyncCache{
		hasRegionIndexSet: true,
		hasRegionIndex:    false,
		loadFromRedisOK:   true,
	}
	statusStore := newFakeSyncStatusStore(nil)

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if statusStore.hasSavedStatus("jp", "pending") {
		t.Fatalf("did not expect pending status when persisted region index loads")
	}
	if cache.loadCalls == 0 {
		t.Fatalf("expected persisted index load to be attempted")
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
	if cache.rebuildCalls != 2 {
		t.Fatalf("expected redis index rebuild calls on skip and readiness annotation, got rebuildCalls=%d", cache.rebuildCalls)
	}
}

func TestSyncAllSkipDoesNotWritePendingWhenRegionIndexCanRebuild(t *testing.T) {
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
	if len(saved) != 1 {
		t.Fatalf("expected only unchanged success status, got %d", len(saved))
	}

	if !strings.EqualFold(saved[0].Status, "success") {
		t.Fatalf("expected unchanged success status, got %s", saved[0].Status)
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

func TestStartSyncDetachesFromCanceledRequestContext(t *testing.T) {
	loader := &timedSyncLoader{
		payloadByZone:   map[string]map[string]any{"jp": {}},
		loadDelayByZone: map[string]time.Duration{"jp": 20 * time.Millisecond},
	}
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{{Region: "jp"}}, loader, &fakeSyncCache{}, newFakeSyncStatusStore(nil), nil, 1)

	ctx, cancel := context.WithCancel(context.Background())
	if err := usecase.StartSync(ctx, "jp", false); err != nil {
		t.Fatalf("StartSync() error = %v", err)
	}
	cancel()

	deadline := time.Now().Add(time.Second)
	for usecase.IsSyncRunning() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if usecase.IsSyncRunning() {
		t.Fatal("detached worker did not complete after request cancellation")
	}
}

func TestStartSyncAdmitsExactlyOneConcurrentJob(t *testing.T) {
	loader := &timedSyncLoader{
		payloadByZone:   map[string]map[string]any{"jp": {}},
		loadDelayByZone: map[string]time.Duration{"jp": 50 * time.Millisecond},
	}
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{{Region: "jp"}}, loader, &fakeSyncCache{}, newFakeSyncStatusStore(nil), nil, 1)

	const callers = 8
	results := make(chan error, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- usecase.StartSync(context.Background(), "jp", false)
		}()
	}
	group.Wait()
	close(results)

	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrSyncInProgress) {
			t.Errorf("StartSync() unexpected error = %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted jobs = %d, want 1", accepted)
	}

	deadline := time.Now().Add(time.Second)
	for usecase.IsSyncRunning() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if usecase.IsSyncRunning() {
		t.Fatal("worker did not release running state")
	}
}

func TestStartSyncReleasesRunningStateAfterFailure(t *testing.T) {
	loader := &fakeSyncLoader{loadErrByZone: map[string]error{"jp": errors.New("load failed")}}
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{{Region: "jp"}}, loader, &fakeSyncCache{}, newFakeSyncStatusStore(nil), nil, 1)

	if err := usecase.StartSync(context.Background(), "jp", false); err != nil {
		t.Fatalf("StartSync() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for usecase.IsSyncRunning() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if usecase.IsSyncRunning() {
		t.Fatal("failed worker did not release running state")
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

func TestVersionByRegionReadsVersionPayloadWithoutLoadingWholeBackup(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	backupStore := NewFileMasterDataPayloadBackupStore(t.TempDir())

	if err := backupStore.SaveRegionPayload(context.Background(), source, "commit-1", map[string]any{
		"data/versions.json": map[string]any{
			"appVersion":  "3.2.1",
			"dataVersion": "20260423",
		},
		"cards.json": []any{map[string]any{"id": 1}},
	}); err != nil {
		t.Fatalf("save backup payload: %v", err)
	}

	store, ok := backupStore.(*fileMasterDataPayloadBackupStore)
	if !ok {
		t.Fatalf("expected file backup store")
	}

	corruptedPath := filepath.Join(store.regionDir(source.Region), "latest", "cards.json")
	if err := os.WriteFile(corruptedPath, []byte("{"), 0o644); err != nil {
		t.Fatalf("corrupt non-version backup file: %v", err)
	}

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, nil, nil, nil, nil, 1)
	usecase.SetBackupStore(backupStore)

	version, found, err := usecase.VersionByRegion(context.Background(), "jp")
	if err != nil {
		t.Fatalf("expected version lookup success, got %v", err)
	}
	if !found {
		t.Fatalf("expected version to be found")
	}

	versionMap, ok := version.(map[string]any)
	if !ok {
		t.Fatalf("expected version payload map, got %T", version)
	}
	if versionMap["appVersion"] != "3.2.1" {
		t.Fatalf("expected appVersion 3.2.1, got %v", versionMap["appVersion"])
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

func TestCurrentEventRequiresClosedAt(t *testing.T) {
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
	if found {
		t.Fatalf("expected current event to be missing when closedAt is zero")
	}
	if record != nil {
		t.Fatalf("expected no record, got %v", record)
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

func TestCurrentEventDoesNotExtendWindowWithDisplayOrDistributionTimes(t *testing.T) {
	now := time.UnixMilli(1_776_625_626_773).UTC()

	cache := &fakeCurrentEventCache{
		events: []map[string]any{
			{
				"id":                               201,
				"name":                             "Show",
				"startAt":                          float64(1_775_714_400_000),
				"eventOnlyComponentDisplayStartAt": float64(1_775_703_600_000),
				"distributionStartAt":              float64(1_776_391_199_000),
				"closedAt":                         float64(1_776_509_999_000),
				"aggregateAt":                      float64(1_776_340_799_000),
				"distributionEndAt":                float64(1_777_647_599_000),
				"eventOnlyComponentDisplayEndAt":   float64(1_776_481_199_000),
			},
		},
		currentEvents: []map[string]any{},
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)

	_, found, err := usecase.CurrentEvent(context.Background(), "jp", now)
	if err != nil {
		t.Fatalf("current event query error: %v", err)
	}
	if found {
		t.Fatalf("expected event to be expired when only distribution/display windows remain")
	}
}

func TestWarmConfiguredRegionIndexesLoadsPersistedIndexes(t *testing.T) {
	cache := &fakeSyncCache{
		loadFromRedisOK: true,
	}
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp"},
		{Region: "en"},
	}, nil, cache, nil, nil, 1)

	regions, err := usecase.WarmConfiguredRegionIndexes(context.Background())
	if err != nil {
		t.Fatalf("warm configured region indexes: %v", err)
	}
	if len(regions) != 2 {
		t.Fatalf("expected 2 warmed regions, got %d", len(regions))
	}
	if cache.loadCalls != 2 {
		t.Fatalf("expected 2 persisted index load calls, got %d", cache.loadCalls)
	}
}

func TestEnsureConfiguredRegionIndexesRebuildsMissingIndexes(t *testing.T) {
	cache := &fakeSyncCache{
		hasRegionIndexSet:  true,
		hasRegionIndex:     false,
		loadFromRedisOK:    false,
		rebuildFromRedisOK: true,
	}
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp"},
		{Region: "en"},
	}, nil, cache, nil, nil, 1)

	loadedRegions, rebuiltRegions, err := usecase.EnsureConfiguredRegionIndexes(context.Background())
	if err != nil {
		t.Fatalf("ensure configured region indexes: %v", err)
	}
	if len(loadedRegions) != 0 {
		t.Fatalf("expected no loaded regions, got %v", loadedRegions)
	}
	if len(rebuiltRegions) != 2 {
		t.Fatalf("expected 2 rebuilt regions, got %d", len(rebuiltRegions))
	}
	if cache.loadCalls != 2 {
		t.Fatalf("expected 2 persisted index load calls, got %d", cache.loadCalls)
	}
	if cache.rebuildCalls != 2 {
		t.Fatalf("expected 2 index rebuild calls, got %d", cache.rebuildCalls)
	}
}

func TestEnsureConfiguredRegionIndexesValidatesRedisWhenDecodedIndexIsRetained(t *testing.T) {
	cache := &fakeSyncCache{
		hasRegionIndexSet: true,
		hasRegionIndex:    true,
		loadFromRedisOK:   true,
	}
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp"},
	}, nil, cache, nil, nil, 1)

	loadedRegions, rebuiltRegions, err := usecase.EnsureConfiguredRegionIndexes(context.Background())
	if err != nil {
		t.Fatalf("ensure configured region indexes: %v", err)
	}
	if len(loadedRegions) != 1 || loadedRegions[0] != "jp" {
		t.Fatalf("expected jp to load from persisted Redis indexes, got %v", loadedRegions)
	}
	if len(rebuiltRegions) != 0 {
		t.Fatalf("expected no rebuilt regions, got %v", rebuiltRegions)
	}
	if cache.loadCalls != 1 {
		t.Fatalf("expected 1 persisted index load call, got %d", cache.loadCalls)
	}
	if cache.rebuildCalls != 0 {
		t.Fatalf("expected no rebuild calls, got %d", cache.rebuildCalls)
	}
}
