package usecase

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

// systemOfRecordCache stands in for the PostgreSQL store: it inspects region
// data and prunes entities.
type systemOfRecordCache struct {
	fakeSyncCache
	mu            sync.Mutex
	hasRegionData bool
	storeTokens   []int64
	pruneTokens   []int64
	pruneKeeps    [][]string
	pruneErr      error
}

// systemOfRecordStore exposes only the store interfaces below, so the embedded
// fake's own region data check does not leak into the usecase.
type systemOfRecordStore struct {
	cache *systemOfRecordCache
}

func (store systemOfRecordStore) StoreRegion(ctx context.Context, region string, payload map[string]any) error {
	store.cache.mu.Lock()
	store.cache.storeTokens = append(store.cache.storeTokens, masterdata.FencingTokenFromContext(ctx))
	store.cache.mu.Unlock()
	return store.cache.fakeSyncCache.StoreRegion(ctx, region, payload)
}

func (store systemOfRecordStore) GetByID(ctx context.Context, region, entity, id string) (map[string]any, bool, error) {
	return store.cache.fakeSyncCache.GetByID(ctx, region, entity, id)
}

func (store systemOfRecordStore) ListAll(ctx context.Context, region, entity string) ([]map[string]any, error) {
	return store.cache.fakeSyncCache.ListAll(ctx, region, entity)
}

func (store systemOfRecordStore) ListByPage(ctx context.Context, region, entity string, page, pageSize int) ([]map[string]any, int, error) {
	return store.cache.fakeSyncCache.ListByPage(ctx, region, entity, page, pageSize)
}

func (store systemOfRecordStore) HasRegionData(_ context.Context, _ string) (bool, error) {
	store.cache.mu.Lock()
	defer store.cache.mu.Unlock()
	return store.cache.hasRegionData, nil
}

func (store systemOfRecordStore) PruneRegionEntities(ctx context.Context, _ string, keep []string) ([]string, error) {
	store.cache.mu.Lock()
	defer store.cache.mu.Unlock()
	sorted := append([]string(nil), keep...)
	sort.Strings(sorted)
	store.cache.pruneKeeps = append(store.cache.pruneKeeps, sorted)
	store.cache.pruneTokens = append(store.cache.pruneTokens, masterdata.FencingTokenFromContext(ctx))
	return []string{"dropped"}, store.cache.pruneErr
}

var (
	_ MasterDataCacheRegionDataInspector = systemOfRecordStore{}
	_ MasterDataCacheEntityPruner        = systemOfRecordStore{}
)

func newSystemOfRecordUsecase(t *testing.T, loader *fakeSyncLoader, statusStore *fakeSyncStatusStore, cache *systemOfRecordCache) *MasterDataSyncUsecase {
	t.Helper()
	source := masterdata.Source{Region: "jp", Owner: "Sekai-World", Repo: "masterdata-dump", Ref: "main"}
	return NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, systemOfRecordStore{cache: cache}, statusStore, nil, 1)
}

func TestSyncCarriesLeaseTokenToStoreWrites(t *testing.T) {
	loader := &fakeSyncLoader{payloadByZone: map[string]map[string]any{
		"jp": {"cards.json": []any{map[string]any{"id": 1}}, "gachas.json": []any{}},
	}}
	cache := &systemOfRecordCache{}
	usecase := newSystemOfRecordUsecase(t, loader, newFakeSyncStatusStore(nil), cache)
	usecase.SetLeaseCoordinator(&fakeLeaseCoordinator{}, time.Second)

	if err := usecase.SyncAllForce(context.Background()); err != nil {
		t.Fatalf("SyncAllForce: %v", err)
	}
	if len(cache.storeTokens) != 1 || cache.storeTokens[0] != 1 {
		t.Fatalf("store write tokens = %v, want [1]", cache.storeTokens)
	}
	if len(cache.pruneTokens) != 1 || cache.pruneTokens[0] != 1 {
		t.Fatalf("prune tokens = %v, want [1]", cache.pruneTokens)
	}
}

func TestFullSyncPrunesEntitiesTheSourceDropped(t *testing.T) {
	loader := &fakeSyncLoader{payloadByZone: map[string]map[string]any{
		"jp": {"cards.json": []any{map[string]any{"id": 1}}, "versions.json": map[string]any{"dataVersion": "1"}},
	}}
	cache := &systemOfRecordCache{}
	statusStore := newFakeSyncStatusStore(nil)
	usecase := newSystemOfRecordUsecase(t, loader, statusStore, cache)

	if err := usecase.SyncAllForce(context.Background()); err != nil {
		t.Fatalf("SyncAllForce: %v", err)
	}
	if len(cache.pruneKeeps) != 1 || strings.Join(cache.pruneKeeps[0], ",") != "cards.json,versions.json" {
		t.Fatalf("prune keep lists = %v", cache.pruneKeeps)
	}
	if latest, _ := statusStore.latest("jp"); latest.Status != "success" {
		t.Fatalf("status = %+v, want success", latest)
	}
}

func TestFullSyncFailsRegionWhenPruneFails(t *testing.T) {
	loader := &fakeSyncLoader{payloadByZone: map[string]map[string]any{
		"jp": {"cards.json": []any{map[string]any{"id": 1}}},
	}}
	cache := &systemOfRecordCache{pruneErr: errors.New("prune failed")}
	statusStore := newFakeSyncStatusStore(nil)
	usecase := newSystemOfRecordUsecase(t, loader, statusStore, cache)

	if err := usecase.SyncAllForce(context.Background()); err == nil {
		t.Fatal("SyncAllForce succeeded although pruning failed")
	}
	if latest, _ := statusStore.latest("jp"); latest.Status != "failed" || !strings.Contains(latest.ErrorMessage, "prune failed") {
		t.Fatalf("status = %+v, want failed with the prune error", latest)
	}
}

func TestUnchangedCommitSkipsWhenStoreHasRegionData(t *testing.T) {
	for _, hasData := range []bool{true, false} {
		loader := &fakeSyncLoader{
			resolvedByZone: map[string]string{"jp": "commit-1"},
			payloadByZone:  map[string]map[string]any{"jp": {"cards.json": []any{map[string]any{"id": 1}}}},
		}
		statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{{
			Region:       "jp",
			Status:       "success",
			SourceCommit: "commit-1",
			Source:       masterdata.Source{Region: "jp", Owner: "Sekai-World", Repo: "masterdata-dump", Ref: "main"},
		}})
		cache := &systemOfRecordCache{hasRegionData: hasData}
		usecase := newSystemOfRecordUsecase(t, loader, statusStore, cache)
		publisher := &fakeSyncEventPublisher{}
		usecase.publisher = publisher

		if err := usecase.SyncAll(context.Background()); err != nil {
			t.Fatalf("SyncAll (region data %t): %v", hasData, err)
		}
		wantLoads := 1
		if hasData {
			wantLoads = 0
		}
		if loader.loadCalls != wantLoads {
			t.Fatalf("region data %t: loader called %d times, want %d", hasData, loader.loadCalls, wantLoads)
		}
		if latest, _ := statusStore.latest("jp"); latest.Status != "success" || latest.SourceCommit != "commit-1" {
			t.Fatalf("region data %t: status = %+v", hasData, latest)
		}
		// Progress names the region data check the store ran.
		wantStatus, wantMessage := "running", "commit unchanged but the store has no data for the region, fallback to full sync"
		if hasData {
			wantStatus, wantMessage = "success", "commit unchanged, stored region data present and skipped sync"
		}
		if !containsSyncProgressEvent(publisher.listEvents(), "jp", wantStatus, "compare", wantMessage) {
			t.Fatalf("region data %t: missing %s compare progress %q", hasData, wantStatus, wantMessage)
		}
	}
}

func TestRegionCacheReadyAsksStoreForRegionData(t *testing.T) {
	cache := &systemOfRecordCache{}
	usecase := newSystemOfRecordUsecase(t, &fakeSyncLoader{}, newFakeSyncStatusStore(nil), cache)

	if ready, err := usecase.regionCacheReady(context.Background(), "jp"); err != nil || ready {
		t.Fatalf("empty store ready = %v %v", ready, err)
	}
	cache.hasRegionData = true
	if ready, err := usecase.regionCacheReady(context.Background(), "jp"); err != nil || !ready {
		t.Fatalf("populated store ready = %v %v", ready, err)
	}
}
