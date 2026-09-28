package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
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

// fakeSyncCache is a minimal store: hasRegionData answers the region data
// check that sync readiness and the unchanged-commit shortcut ask.
type fakeSyncCache struct {
	mu              sync.Mutex
	storeCalls      int
	regionDataCalls int
	hasRegionData   bool
}

type fakeCurrentEventCache struct {
	mu sync.Mutex

	events     []map[string]any
	storeCalls int
}

func (cache *fakeCurrentEventCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.storeCalls++
	return nil
}

func (cache *fakeCurrentEventCache) GetByID(_ context.Context, _, entity, id string) (map[string]any, bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if strings.ToLower(strings.TrimSpace(entity)) != "events" {
		return nil, false, nil
	}
	for _, record := range cache.events {
		if key, _ := masterdata.CanonicalKeyPart(record["id"]); key == id {
			return record, true, nil
		}
	}
	return nil, false, nil
}

func (cache *fakeCurrentEventCache) ListAll(_ context.Context, _, entity string) ([]map[string]any, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	var source []map[string]any
	switch strings.ToLower(strings.TrimSpace(entity)) {
	case "events":
		source = cache.events
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

func (cache *fakeSyncCache) HasRegionData(_ context.Context, _ string) (bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.regionDataCalls++
	return cache.hasRegionData, nil
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
	// storedManifest is the version payload the store already holds; nil
	// leaves it missing.
	storedManifest    map[string]any
	storedManifestErr error
	cacheReady        bool
}

type manifestSyncTestFixture struct {
	source         masterdata.Source
	previousStatus masterdata.SyncStatus
	loader         *fakeSyncLoader
	cache          *fakeVersionSyncCache
	statusStore    *fakeSyncStatusStore
	publisher      *fakeSyncEventPublisher
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

	cache := &fakeVersionSyncCache{loadedVersions: map[string]any{}, loadReturnErr: options.storedManifestErr}
	cache.hasRegionData = options.cacheReady
	if options.storedManifest != nil {
		cache.loadedVersions[source.Region] = options.storedManifest
	}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})
	publisher := &fakeSyncEventPublisher{}

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, publisher, 1)

	return &manifestSyncTestFixture{
		source:         source,
		previousStatus: previousStatus,
		loader:         loader,
		cache:          cache,
		statusStore:    statusStore,
		publisher:      publisher,
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
	cache := &fakeSyncCache{hasRegionData: true}
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
	if cache.regionDataCalls != 2 {
		t.Fatalf("expected region data checks for readiness and the skip, got regionDataCalls=%d", cache.regionDataCalls)
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

func TestDashboardStatusKeepsSuccessfulStatusWithoutCheckingStore(t *testing.T) {
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
	cache := &fakeSyncCache{hasRegionData: false}

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
	if cache.regionDataCalls != 0 {
		t.Fatalf("expected dashboard status to avoid store readiness checks, got %d calls", cache.regionDataCalls)
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
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit:    "old-commit",
		previousFileCount: 99,
		resolvedCommit:    "new-commit",
		remoteManifest:    manifest,
		storedManifest:    manifest,
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
		t.Fatalf("expected stored data to be kept, got storeCalls=%d", fixture.cache.storeCalls)
	}
	if fixture.cache.storeCallCount() != 0 {
		t.Fatalf("expected stored version payload to be kept, got versionStoreCalls=%d", fixture.cache.storeCallCount())
	}

	latest, exists := fixture.statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest jp status")
	}
	if latest.Status != "success" || latest.SourceCommit != "new-commit" {
		t.Fatalf("expected successful status pinned to new-commit, got %#v", latest)
	}
	if latest.FileCount != 99 {
		t.Fatalf("expected file count to carry over the previous count 99, got %d", latest.FileCount)
	}
	if !containsSyncProgressEvent(fixture.publisher.listEvents(), "jp", "success", "compare", "versions manifest unchanged, kept stored data and skipped sync") {
		t.Fatalf("expected success compare event for the manifest skip")
	}
}

// derivedDataVersionSyncCache adds the derived-data rebuild to the version
// cache fake.
type derivedDataVersionSyncCache struct {
	*fakeVersionSyncCache
	ensureCalls int
	ensureErr   error
}

func (cache *derivedDataVersionSyncCache) EnsureDerivedEntityData(_ context.Context, _ string) ([]string, error) {
	cache.ensureCalls++
	return []string{"events"}, cache.ensureErr
}

func TestSyncAllManifestSkipRebuildsDerivedData(t *testing.T) {
	manifest := map[string]any{"dataVersion": "20260802"}
	for _, tt := range []struct {
		name      string
		ensureErr error
		wantLoads int
	}{
		{name: "rebuilt", wantLoads: 0},
		{name: "rebuild fails", ensureErr: errors.New("ensure failed"), wantLoads: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
				previousCommit:    "old-commit",
				previousFileCount: 99,
				resolvedCommit:    "new-commit",
				remoteManifest:    manifest,
				archivePayload:    manifestPayload(manifest, "from-archive"),
				storedManifest:    manifest,
				cacheReady:        true,
			})
			cache := &derivedDataVersionSyncCache{fakeVersionSyncCache: fixture.cache, ensureErr: tt.ensureErr}
			usecase := NewMasterDataSyncUsecase([]masterdata.Source{fixture.source}, fixture.loader, cache, fixture.statusStore, nil, 1)

			if err := usecase.SyncAll(context.Background()); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if cache.ensureCalls != 1 {
				t.Fatalf("expected one derived-data rebuild, got %d", cache.ensureCalls)
			}
			if fixture.loader.loadCalls != tt.wantLoads {
				t.Fatalf("expected %d archive loads, got %d", tt.wantLoads, fixture.loader.loadCalls)
			}
			if latest, _ := fixture.statusStore.latest("jp"); latest.Status != "success" || latest.SourceCommit != "new-commit" {
				t.Fatalf("expected success at new-commit, got %#v", latest)
			}
		})
	}
}

func TestSyncAllLoadsRegionWhenManifestMatchesButStoreIsEmpty(t *testing.T) {
	manifest := map[string]any{"dataVersion": "20260802"}
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit:    "old-commit",
		previousFileCount: 99,
		resolvedCommit:    "new-commit",
		remoteManifest:    manifest,
		archivePayload:    manifestPayload(manifest, "from-archive"),
		storedManifest:    manifest,
	})

	if err := fixture.usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if fixture.loader.loadCalls != 1 {
		t.Fatalf("expected an empty store to take the archive load, got loadCalls=%d", fixture.loader.loadCalls)
	}
	if fixture.cache.storeCalls != 1 {
		t.Fatalf("expected exactly one store write, got storeCalls=%d", fixture.cache.storeCalls)
	}
	if !fixture.statusStore.hasSavedStatus("jp", "pending") {
		t.Fatalf("expected pending status while the store is empty")
	}

	latest, exists := fixture.statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest jp status")
	}
	if latest.Status != "success" || latest.SourceCommit != "new-commit" {
		t.Fatalf("expected successful status pinned to new-commit, got %#v", latest)
	}
}

func TestSyncAllLoadsRegionWhenStoredVersionPayloadCannotBeRead(t *testing.T) {
	manifest := map[string]any{"dataVersion": "20260802"}
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit:    "old-commit",
		previousFileCount: 2,
		resolvedCommit:    "new-commit",
		remoteManifest:    manifest,
		archiveLoadErr:    errors.New("archive fallback"),
		storedManifestErr: errors.New("store read failed"),
		cacheReady:        true,
	})

	if err := fixture.usecase.SyncAll(context.Background()); err == nil {
		t.Fatal("expected archive fallback load failure")
	}
	if fixture.loader.loadCalls != 1 {
		t.Fatalf("expected exactly one archive fallback load, got loadCalls=%d", fixture.loader.loadCalls)
	}
	if fixture.loader.manifestCalls != 0 {
		t.Fatalf("expected the remote manifest not to load without a stored payload, got manifestCalls=%d", fixture.loader.manifestCalls)
	}

	for _, saved := range fixture.statusStore.savedByRegion("jp") {
		if strings.EqualFold(saved.Status, "success") && saved.SourceCommit == "new-commit" {
			t.Fatalf("did not expect the manifest skip to advance a success status to new-commit: %#v", saved)
		}
	}
}

func TestSyncAllLoadsRegionWhenChangedCommitManifestDiffers(t *testing.T) {
	fixture := newManifestSyncTestFixture(t, manifestSyncTestOptions{
		previousCommit:    "old-commit",
		previousFileCount: 1,
		resolvedCommit:    "new-commit",
		remoteManifest:    map[string]any{"dataVersion": "new"},
		archivePayload:    map[string]any{"cards.json": []any{map[string]any{"id": 1}}},
		storedManifest:    map[string]any{"dataVersion": "old"},
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
		storedManifest:    map[string]any{"dataVersion": "old"},
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
		storedManifest: map[string]any{"dataVersion": "same"},
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

func TestSyncAllFallsBackToFullSyncWhenCommitUnchangedButStoreIsEmpty(t *testing.T) {
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
	cache := &fakeSyncCache{hasRegionData: false}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 1 {
		t.Fatalf("expected fallback to full sync when the store is empty, got loadCalls=%d", loader.loadCalls)
	}
	if cache.storeCalls != 1 {
		t.Fatalf("expected cache to be built from github payload, got storeCalls=%d", cache.storeCalls)
	}
}

func TestSyncAllSetsPendingWhenStoreHasNoRegionData(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "new-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "pending-check"}},
			},
		},
	}
	cache := &fakeSyncCache{hasRegionData: false}
	statusStore := newFakeSyncStatusStore(nil)

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !statusStore.hasSavedStatus("jp", "pending") {
		t.Fatalf("expected pending status to be saved when the store has no region data")
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected latest jp status")
	}
	if latest.Status != "success" {
		t.Fatalf("expected final status success, got %s", latest.Status)
	}
}

func TestSyncAllDoesNotSetPendingWhenStoreHasRegionData(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "new-commit"},
		payloadByZone: map[string]map[string]any{
			"jp": {
				"cards.json": []any{map[string]any{"id": 1, "prefix": "persisted-index"}},
			},
		},
	}
	cache := &fakeSyncCache{hasRegionData: true}
	statusStore := newFakeSyncStatusStore(nil)

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if statusStore.hasSavedStatus("jp", "pending") {
		t.Fatalf("did not expect pending status when the store has region data")
	}
	if cache.regionDataCalls == 0 {
		t.Fatalf("expected the region data check to run")
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
	cache := &fakeSyncCache{hasRegionData: true}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{pendingStatus})
	statusStore.successByZone["jp"] = latestSuccess

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if loader.loadCalls != 0 {
		t.Fatalf("expected load to be skipped using latest success status, got loadCalls=%d", loader.loadCalls)
	}
	if cache.regionDataCalls != 2 {
		t.Fatalf("expected region data checks for readiness and the skip, got regionDataCalls=%d", cache.regionDataCalls)
	}
}

func TestSyncAllSkipDoesNotWritePendingWhenStoreHasRegionData(t *testing.T) {
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
	cache := &fakeSyncCache{hasRegionData: true}
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

// The rate-limit fallback re-saves the previous success over the stored data,
// so it must not apply when the store is empty (for example before the first
// sync into a new store) or when the last run failed and may have left a mix
// of two commits.
func TestSyncAllRateLimitFailsWhenStoredDataIsNotThePreviousSuccess(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousSuccess := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    8,
		LastSyncedAt: time.Now().UTC().Add(-2 * time.Hour),
		SourceCommit: "prev-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-2 * time.Hour),
	}
	lastFailed := previousSuccess
	lastFailed.Status = "failed"
	lastFailed.SourceCommit = "failed-commit"
	lastFailed.UpdatedAt = time.Now().UTC().Add(-time.Hour)

	for _, tt := range []struct {
		name          string
		hasRegionData bool
		latest        masterdata.SyncStatus
	}{
		{name: "empty store", hasRegionData: false, latest: previousSuccess},
		{name: "last run failed", hasRegionData: true, latest: lastFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			loader := &fakeSyncLoader{
				resolvedByZone: map[string]string{"jp": "next-commit"},
				loadErrByZone:  map[string]error{"jp": errors.New("api rate limit exceeded")},
			}
			statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{tt.latest})
			statusStore.successByZone["jp"] = previousSuccess
			usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, &fakeSyncCache{hasRegionData: tt.hasRegionData}, statusStore, nil, 1)

			if err := usecase.SyncAll(context.Background()); err == nil {
				t.Fatal("expected the rate limit to fail the region")
			}
			latest, exists := statusStore.latest("jp")
			if !exists || !strings.EqualFold(latest.Status, "failed") {
				t.Fatalf("expected failed status, got %#v", latest)
			}
		})
	}
}

func TestSyncAllRateLimitFallbackKeepsStoredDataAndResavesPreviousSuccess(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
	previousStatus := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		FileCount:    3,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
		SourceCommit: "prev-commit",
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{"jp": "next-commit"},
		loadErrByZone:  map[string]error{"jp": errors.New("too many requests")},
	}
	cache := &fakeSyncCache{hasRegionData: true}
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, nil, 1)

	if err := usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error on rate limit fallback, got %v", err)
	}

	if cache.storeCalls != 0 {
		t.Fatalf("expected the fallback to keep stored data without writing, got %d store calls", cache.storeCalls)
	}

	latest, exists := statusStore.latest("jp")
	if !exists {
		t.Fatalf("expected fallback status for jp")
	}
	if !strings.EqualFold(latest.Status, "success") || latest.SourceCommit != "prev-commit" || latest.ErrorMessage != "" {
		t.Fatalf("expected previous success status re-saved, got %#v", latest)
	}
	if latest.FileCount != previousStatus.FileCount || !latest.LastSyncedAt.Equal(previousStatus.LastSyncedAt) {
		t.Fatalf("expected file count %d and last synced %v carried over, got %#v", previousStatus.FileCount, previousStatus.LastSyncedAt, latest)
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

func TestCurrentEventConcurrentRequestsNeverWrite(t *testing.T) {
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

	if cache.StoreCallCount() != 0 {
		t.Fatalf("expected current event reads not to write, got %d writes", cache.StoreCallCount())
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

func TestCurrentEventPrefersLatestStartAndReturnsFullRecord(t *testing.T) {
	now := time.UnixMilli(1_772_438_533_000).UTC()
	nowMillis := now.UnixMilli()

	cache := &fakeCurrentEventCache{
		events: []map[string]any{
			{"id": 10, "name": "older-overlap", "startAt": nowMillis - 120_000, "closedAt": nowMillis + 120_000},
			{"id": 11, "name": "newer-overlap", "startAt": nowMillis - 60_000, "closedAt": nowMillis + 60_000, "eventType": "cheerful_carnival"},
			{"id": 12, "name": "upcoming", "startAt": nowMillis + 60_000, "closedAt": nowMillis + 120_000},
		},
	}

	usecase := NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)

	record, found, err := usecase.CurrentEvent(context.Background(), "jp", now)
	if err != nil || !found {
		t.Fatalf("expected current event, got found=%t err=%v", found, err)
	}
	if fmt.Sprintf("%v", record["id"]) != "11" || record["eventType"] != "cheerful_carnival" {
		t.Fatalf("expected the full record of event 11, got %v", record)
	}
	if cache.StoreCallCount() != 0 {
		t.Fatalf("expected no writes, got %d", cache.StoreCallCount())
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
		events: events,
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

type fakeVersionSyncCache struct {
	fakeSyncCache
	mu                sync.Mutex
	versionStoreCalls int
	versionLoadCalls  int
	loadedVersions    map[string]any
	loadReturnErr     error
	storeReturnErr    error
}

func (cache *fakeVersionSyncCache) StoreRegionVersionPayload(_ context.Context, _ string, version any) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.versionStoreCalls++
	if cache.storeReturnErr != nil {
		return cache.storeReturnErr
	}
	return nil
}

func (cache *fakeVersionSyncCache) LoadRegionVersionPayload(_ context.Context, region string) (any, bool, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	cache.versionLoadCalls++
	if cache.loadReturnErr != nil {
		return nil, false, cache.loadReturnErr
	}
	if cache.loadedVersions != nil {
		if version, ok := cache.loadedVersions[region]; ok {
			return version, true, nil
		}
	}
	return nil, false, nil
}

func (cache *fakeVersionSyncCache) storeCallCount() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.versionStoreCalls
}

func versionCacheSource() masterdata.Source {
	return masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
}

type versionSyncTestFixture struct {
	source      masterdata.Source
	loader      *fakeSyncLoader
	cache       *fakeVersionSyncCache
	statusStore *fakeSyncStatusStore
	publisher   *fakeSyncEventPublisher
	usecase     *MasterDataSyncUsecase
}

type versionSyncTestOptions struct {
	previousCommit    string
	previousFileCount int
	resolvedCommit    string
	manifest          map[string]any
	archivePayload    map[string]any
	cacheReady        bool
	storeReturnErr    error
	loadReturnErr     error
	loadedVersions    map[string]any
	previousStatus    string
	publisher         *fakeSyncEventPublisher
}

func newVersionSyncTestFixture(t *testing.T, opts versionSyncTestOptions) *versionSyncTestFixture {
	t.Helper()

	source := versionCacheSource()
	if opts.previousStatus == "" {
		opts.previousStatus = "success"
	}
	if opts.previousCommit == "" {
		opts.previousCommit = "abc123"
	}
	if opts.previousFileCount == 0 {
		opts.previousFileCount = 5
	}

	previousStatus := masterdata.SyncStatus{
		Region:       source.Region,
		Status:       opts.previousStatus,
		FileCount:    opts.previousFileCount,
		LastSyncedAt: time.Now().UTC().Add(-time.Hour),
		SourceCommit: opts.previousCommit,
		Source:       source,
		UpdatedAt:    time.Now().UTC().Add(-time.Hour),
	}

	loader := &fakeSyncLoader{
		resolvedByZone: map[string]string{source.Region: opts.resolvedCommit},
		manifestByZone: map[string]map[string]any{source.Region: opts.manifest},
		payloadByZone:  map[string]map[string]any{source.Region: opts.archivePayload},
	}

	if opts.loadedVersions == nil {
		opts.loadedVersions = map[string]any{}
	}
	cache := &fakeVersionSyncCache{loadedVersions: opts.loadedVersions}
	cache.hasRegionData = opts.cacheReady
	cache.storeReturnErr = opts.storeReturnErr
	cache.loadReturnErr = opts.loadReturnErr

	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus})
	publisher := opts.publisher
	if publisher == nil {
		publisher = &fakeSyncEventPublisher{}
	}

	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, publisher, 1)

	return &versionSyncTestFixture{
		source:      source,
		loader:      loader,
		cache:       cache,
		statusStore: statusStore,
		publisher:   publisher,
		usecase:     usecase,
	}
}

func TestSyncSuccessPersistsVersionPayloadToCache(t *testing.T) {
	manifest := map[string]any{"data/versions.json": map[string]any{"appVersion": "4.0.0"}}
	payload := manifestPayload(manifest, "new")
	fixture := newVersionSyncTestFixture(t, versionSyncTestOptions{
		resolvedCommit: "def456",
		manifest:       manifest,
		archivePayload: payload,
	})

	if err := fixture.usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected sync success, got %v", err)
	}
	if fixture.cache.storeCallCount() != 1 {
		t.Fatalf("expected 1 version store call, got %d", fixture.cache.storeCallCount())
	}
	if !fixture.statusStore.hasSavedStatus("jp", "success") {
		t.Fatalf("expected success status saved for jp")
	}
}

func TestSyncFailsWhenVersionCacheStoreErrors(t *testing.T) {
	manifest := map[string]any{"data/versions.json": map[string]any{"appVersion": "4.0.0"}}
	payload := manifestPayload(manifest, "new")
	fixture := newVersionSyncTestFixture(t, versionSyncTestOptions{
		resolvedCommit: "def456",
		manifest:       manifest,
		archivePayload: payload,
		storeReturnErr: errors.New("store connection refused"),
	})

	if err := fixture.usecase.SyncAll(context.Background()); err == nil {
		t.Fatalf("expected sync failure from version-cache store error")
	}
	if !fixture.statusStore.hasSavedStatus("jp", "failed") {
		t.Fatalf("expected failed status saved for jp")
	}
	events := fixture.publisher.listEvents()
	if !containsSyncProgressEvent(events, "jp", "failed", "version-cache", "store connection refused") {
		t.Fatalf("expected version-cache failed event published, got %v", events)
	}
}

func TestVersionByRegionReadsStoredVersionPayload(t *testing.T) {
	source := versionCacheSource()
	tests := []struct {
		name           string
		loadedVersions map[string]any
		loadReturnErr  error
		wantErr        bool
		wantFound      bool
		wantAppVersion string
	}{
		{
			name:           "returns_stored_payload",
			loadedVersions: map[string]any{"jp": map[string]any{"appVersion": "stored-version"}},
			wantFound:      true,
			wantAppVersion: "stored-version",
		},
		{
			name:           "reports_missing_payload",
			loadedVersions: map[string]any{},
		},
		{
			name:          "returns_load_error",
			loadReturnErr: errors.New("store connection refused"),
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &fakeVersionSyncCache{
				loadedVersions: tt.loadedVersions,
				loadReturnErr:  tt.loadReturnErr,
			}
			usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, nil, cache, nil, nil, 1)

			version, found, err := usecase.VersionByRegion(context.Background(), "jp")
			if tt.wantErr {
				if err == nil || !errors.Is(err, tt.loadReturnErr) {
					t.Fatalf("expected wrapped load error, got %v", err)
				}
				if found || version != nil {
					t.Fatalf("expected no version on load error, got found=%v version=%v", found, version)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if found != tt.wantFound {
				t.Fatalf("expected found=%v, got %v", tt.wantFound, found)
			}
			if tt.wantFound {
				versionMap := version.(map[string]any)
				if versionMap["appVersion"] != tt.wantAppVersion {
					t.Fatalf("expected appVersion=%q, got %v", tt.wantAppVersion, versionMap["appVersion"])
				}
			}
		})
	}
}

func TestVersionByRegionWithoutVersionLoaderOrSource(t *testing.T) {
	source := versionCacheSource()
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, nil, &fakeSyncCache{}, nil, nil, 1)

	if version, found, err := usecase.VersionByRegion(context.Background(), source.Region); err != nil || found || version != nil {
		t.Fatalf("expected no version from a store without version payloads, got version=%v found=%v err=%v", version, found, err)
	}

	versioned := NewMasterDataSyncUsecase([]masterdata.Source{source}, nil, &fakeVersionSyncCache{
		loadedVersions: map[string]any{"jp": map[string]any{"appVersion": "stored-version"}},
	}, nil, nil, 1)
	if version, found, err := versioned.VersionByRegion(context.Background(), "unknown"); err != nil || found || version != nil {
		t.Fatalf("expected no version for an unconfigured region, got version=%v found=%v err=%v", version, found, err)
	}
}

func TestSyncSkipFallsBackToFullSyncWhenVersionsPayloadMissing(t *testing.T) {
	manifest := map[string]any{"dataVersion": "20260802"}
	payloadWithoutVersions := map[string]any{
		"cards.json": []any{map[string]any{"id": 1, "prefix": "from-archive"}},
	}
	fixture := newVersionSyncTestFixture(t, versionSyncTestOptions{
		previousCommit:    "old-commit",
		previousFileCount: 2,
		resolvedCommit:    "new-commit",
		manifest:          manifest,
		archivePayload:    payloadWithoutVersions,
		cacheReady:        true,
	})

	if err := fixture.usecase.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if fixture.cache.versionStoreCalls != 0 {
		t.Fatalf("expected version cache not to be populated, got versionStoreCalls=%d", fixture.cache.versionStoreCalls)
	}
	if fixture.loader.loadCalls != 1 {
		t.Fatalf("expected full sync fallback, got loadCalls=%d", fixture.loader.loadCalls)
	}
}

func TestSyncAllVersionCacheShortcutScenarios(t *testing.T) {
	storedVersion := map[string]any{"appVersion": "3.2.1"}
	tests := []struct {
		name              string
		hasRegionData     bool
		loadedVersions    map[string]any
		loadReturnErr     error
		wantLoadCalls     int
		wantVersionStores int
	}{
		{
			name:              "skips_when_region_data_and_version_payload_are_stored",
			hasRegionData:     true,
			loadedVersions:    map[string]any{"jp": storedVersion},
			wantLoadCalls:     0,
			wantVersionStores: 0,
		},
		{
			name:              "falls_back_when_version_payload_is_missing",
			hasRegionData:     true,
			wantLoadCalls:     1,
			wantVersionStores: 1,
		},
		{
			name:              "falls_back_when_version_payload_load_fails",
			hasRegionData:     true,
			loadReturnErr:     errors.New("store read failed"),
			wantLoadCalls:     1,
			wantVersionStores: 1,
		},
		{
			name:              "falls_back_when_store_has_no_region_data",
			loadedVersions:    map[string]any{"jp": storedVersion},
			wantLoadCalls:     1,
			wantVersionStores: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newVersionSyncTestFixture(t, versionSyncTestOptions{
				resolvedCommit: "abc123",
				cacheReady:     tt.hasRegionData,
				loadedVersions: tt.loadedVersions,
				loadReturnErr:  tt.loadReturnErr,
				archivePayload: map[string]any{
					"data/versions.json": storedVersion,
					"cards.json":         []any{map[string]any{"id": 1}},
				},
			})

			if err := fixture.usecase.SyncAll(context.Background()); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if fixture.loader.loadCalls != tt.wantLoadCalls {
				t.Fatalf("expected loadCalls=%d, got %d", tt.wantLoadCalls, fixture.loader.loadCalls)
			}
			if fixture.cache.storeCallCount() != tt.wantVersionStores {
				t.Fatalf("expected version store calls=%d, got %d", tt.wantVersionStores, fixture.cache.storeCallCount())
			}
			if !fixture.statusStore.hasSavedStatus("jp", "success") {
				t.Fatalf("expected success status saved for jp")
			}
			latest, _ := fixture.statusStore.latest("jp")
			if latest.SourceCommit != "abc123" {
				t.Fatalf("expected status pinned to abc123, got %#v", latest)
			}
		})
	}
}

// fakeReadinessCache is a minimal MasterDataCache used to exercise the
// version-metadata readiness helper without a real store.
type fakeReadinessCache struct {
	versionByRegion map[string]bool
	versionErr      error
}

func (c *fakeReadinessCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (c *fakeReadinessCache) GetByID(_ context.Context, _, _, _ string) (map[string]any, bool, error) {
	return nil, false, nil
}

func (c *fakeReadinessCache) ListAll(_ context.Context, _, _ string) ([]map[string]any, error) {
	return nil, nil
}

func (c *fakeReadinessCache) ListByPage(_ context.Context, _, _ string, _, _ int) ([]map[string]any, int, error) {
	return nil, 0, nil
}

func (c *fakeReadinessCache) LoadRegionVersionPayload(_ context.Context, region string) (any, bool, error) {
	if c.versionErr != nil {
		return nil, false, c.versionErr
	}
	if v, ok := c.versionByRegion[strings.ToLower(strings.TrimSpace(region))]; ok {
		if !v {
			return map[string]any{}, false, nil
		}
		return map[string]any{"appVersion": "stub"}, true, nil
	}
	return map[string]any{"appVersion": "stub"}, true, nil
}

func TestRegionVersionReadyReflectsVersionAvailability(t *testing.T) {
	uc := &MasterDataSyncUsecase{cache: &fakeReadinessCache{versionByRegion: map[string]bool{"jp": false}}}
	ready, err := uc.RegionVersionReady(context.Background(), "jp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ready {
		t.Fatalf("expected jp version not ready")
	}

	uc.cache = &fakeReadinessCache{versionByRegion: map[string]bool{"jp": true}}
	ready, err = uc.RegionVersionReady(context.Background(), "jp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ready {
		t.Fatalf("expected jp version ready")
	}

	// A region without an explicit version mapping defaults to available so a
	// non-version-aware cache does not block readiness.
	uc.cache = &fakeReadinessCache{}
	ready, err = uc.RegionVersionReady(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ready {
		t.Fatalf("expected default version ready for unknown region")
	}
}

func TestRegionVersionReadyCorruptPayloadReportsNotReadyWithoutError(t *testing.T) {
	// A corrupt/malformed stored version payload is a data problem (surfaced
	// as master_data by the readiness probe), not a store failure, so it must
	// not propagate an error to the caller.
	var corruptSink any
	corrupt := json.Unmarshal([]byte("{not-valid-json"), &corruptSink)
	if corrupt == nil {
		t.Fatalf("expected a corrupt json unmarshal error fixture")
	}
	uc := &MasterDataSyncUsecase{cache: &fakeReadinessCache{versionErr: corrupt}}
	ready, err := uc.RegionVersionReady(context.Background(), "jp")
	if err != nil {
		t.Fatalf("corrupt payload must not propagate a store error, got %v", err)
	}
	if ready {
		t.Fatalf("expected jp version not ready for corrupt payload")
	}
}

func TestRegionVersionReadyStoreReadErrorPropagates(t *testing.T) {
	// A genuine store read failure must propagate so the readiness probe
	// reports the database dependency rather than master_data.
	uc := &MasterDataSyncUsecase{cache: &fakeReadinessCache{versionErr: errors.New("database: connection pool timeout")}}
	ready, err := uc.RegionVersionReady(context.Background(), "jp")
	if err == nil {
		t.Fatalf("expected store read error to propagate")
	}
	if ready {
		t.Fatalf("expected jp not ready when the store read fails")
	}
}

// recordCountingSyncCache is a store that counts its records per region.
type recordCountingSyncCache struct {
	fakeSyncCache
	counts   map[string]int64
	countErr error
}

func (cache *recordCountingSyncCache) RegionRecordCounts(_ context.Context) (map[string]int64, error) {
	return cache.counts, cache.countErr
}

func TestRegionRecordCountsReadsStoreCounter(t *testing.T) {
	counting := &recordCountingSyncCache{counts: map[string]int64{"jp": 12, "en": 3}}
	counts, err := NewMasterDataSyncUsecase(nil, nil, counting, nil, nil, 1).RegionRecordCounts(context.Background())
	if err != nil {
		t.Fatalf("RegionRecordCounts() error = %v", err)
	}
	if len(counts) != 2 || counts["jp"] != 12 || counts["en"] != 3 {
		t.Fatalf("RegionRecordCounts() = %v, want jp=12 en=3", counts)
	}
	if counting.storeCalls != 0 || counting.regionDataCalls != 0 {
		t.Fatalf("expected record counts to only read the counter, got storeCalls=%d regionDataCalls=%d", counting.storeCalls, counting.regionDataCalls)
	}

	countErr := errors.New("count failed")
	failing := &recordCountingSyncCache{countErr: countErr}
	if _, err := NewMasterDataSyncUsecase(nil, nil, failing, nil, nil, 1).RegionRecordCounts(context.Background()); !errors.Is(err, countErr) {
		t.Fatalf("RegionRecordCounts() error = %v, want %v", err, countErr)
	}

	if counts, err := NewMasterDataSyncUsecase(nil, nil, &fakeSyncCache{}, nil, nil, 1).RegionRecordCounts(context.Background()); err != nil || counts != nil {
		t.Fatalf("expected nil counts from a store without a counter, got %v %v", counts, err)
	}

	var nilUsecase *MasterDataSyncUsecase
	if counts, err := nilUsecase.RegionRecordCounts(context.Background()); err != nil || counts != nil {
		t.Fatalf("expected nil counts from a nil usecase, got %v %v", counts, err)
	}
}

// TestStartSyncInterruptedByLifecycleCancellation verifies that an admin
// StartSync background worker is cancelled by the application lifecycle context
// during graceful shutdown, that Wait() returns once it stops, and that the
// in-flight region status is left recoverable (not marked failed) so
// interrupted-sync recovery can resume it. Cancellation must not be treated as
// a successful sync.
func TestStartSyncInterruptedByLifecycleCancellation(t *testing.T) {
	source := masterdata.Source{Region: "jp", Owner: "Sekai-World", Repo: "sekai-master-data-jp", Ref: "main"}
	loader := &timedSyncLoader{
		payloadByZone:   map[string]map[string]any{"jp": {"cards": []any{}}},
		loadDelayByZone: map[string]time.Duration{"jp": 5 * time.Second},
	}
	cache := &fakeSyncCache{}
	statusStore := &fakeSyncStatusStore{
		byZone:        make(map[string]masterdata.SyncStatus),
		saved:         make([]masterdata.SyncStatus, 0),
		successByZone: make(map[string]masterdata.SyncStatus),
		stableByZone:  make(map[string]masterdata.SyncStatus),
	}
	publisher := &fakeSyncEventPublisher{}

	uc := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, publisher, 1)

	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	uc.SetLifecycleContext(lifecycleCtx)

	if err := uc.StartSync(context.Background(), "jp", false); err != nil {
		t.Fatalf("StartSync returned error: %v", err)
	}
	if !uc.IsSyncRunning() {
		t.Fatalf("expected sync to be running after StartSync")
	}

	// Let the worker begin and reach the in-flight load phase.
	time.Sleep(50 * time.Millisecond)

	// Simulate graceful shutdown: cancel the app lifecycle context.
	cancelLifecycle()

	done := make(chan struct{})
	go func() {
		uc.Wait()
		close(done)
	}()

	select {
	case <-done:
		// worker stopped within the bounded window.
	case <-time.After(2 * time.Second):
		t.Fatal("StartSync worker did not stop after lifecycle cancellation")
	}

	if uc.IsSyncRunning() {
		t.Fatalf("expected sync running flag to be cleared after cancellation")
	}
	if statusStore.hasSavedStatus("jp", "failed") {
		t.Fatalf("interrupted region must not be marked failed; status must stay recoverable")
	}
	if !statusStore.hasSavedStatus("jp", "running") && !statusStore.hasSavedStatus("jp", "pending") {
		t.Fatalf("interrupted region status must remain recoverable (running/pending)")
	}

	foundInterrupted := false
	for _, event := range publisher.listEvents() {
		if event.Event == "master_data_updated" && event.Status == "interrupted" {
			foundInterrupted = true
		}
	}
	if !foundInterrupted {
		t.Fatalf("expected interrupted sync completion event")
	}
}

type indexEnsuringSyncCache struct {
	*fakeSyncCache
	ensureErr   error
	ensureCalls int
}

func (cache *indexEnsuringSyncCache) EnsureDerivedEntityData(_ context.Context, _ string) ([]string, error) {
	cache.ensureCalls++
	return []string{"gachas"}, cache.ensureErr
}

func TestSyncAllBuildsRelationIndexesWhenSkippingUnchangedCommit(t *testing.T) {
	for _, test := range []struct {
		name       string
		ensureErr  error
		wantLoaded bool
	}{
		{name: "indexes built, sync skipped"},
		{name: "index build failed, full sync", ensureErr: errors.New("store down"), wantLoaded: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := masterdata.Source{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"}
			previousStatus := masterdata.SyncStatus{Region: "jp", Status: "success", SourceCommit: "abc123", Source: source}
			loader := &fakeSyncLoader{
				resolvedByZone: map[string]string{"jp": "abc123"},
				payloadByZone:  map[string]map[string]any{"jp": {"cards.json": []any{map[string]any{"id": 1}}}},
			}
			cache := &indexEnsuringSyncCache{fakeSyncCache: &fakeSyncCache{hasRegionData: true}, ensureErr: test.ensureErr}
			usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, newFakeSyncStatusStore([]masterdata.SyncStatus{previousStatus}), nil, 1)

			if err := usecase.SyncAll(context.Background()); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if cache.ensureCalls != 1 {
				t.Fatalf("expected one relation index check, got %d", cache.ensureCalls)
			}
			if loaded := loader.loadCalls > 0; loaded != test.wantLoaded {
				t.Fatalf("expected full sync=%v, got loadCalls=%d", test.wantLoaded, loader.loadCalls)
			}
		})
	}
}
