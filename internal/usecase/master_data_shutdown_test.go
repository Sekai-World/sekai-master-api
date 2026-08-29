package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

// blockingSyncCache blocks StoreRegion until the context is done, simulating a
// cache write that is interrupted by graceful shutdown.
type blockingSyncCache struct {
	fakeSyncCache
}

func (c *blockingSyncCache) StoreRegion(ctx context.Context, _ string, _ map[string]any) error {
	c.mu.Lock()
	c.storeCalls++
	c.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

// TestMasterDataEventHubCloseIdempotent verifies the hub closes subscriber
// channels exactly once, is safe to call repeatedly, and that Publish after
// close never panics (no send on closed channel).
func TestMasterDataEventHubCloseIdempotent(t *testing.T) {
	hub := NewMasterDataEventHub()

	sub1, _ := hub.Subscribe()
	sub2, _ := hub.Subscribe()

	hub.PublishMasterDataUpdated(context.Background(), masterdata.SyncUpdatedEvent{Event: "master_data_updated", Status: "running"})

	select {
	case ev := <-sub1:
		if ev.Status != "running" {
			t.Fatalf("expected running event, got %s", ev.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber 1 did not receive event")
	}

	hub.Close()
	hub.Close() // idempotent, must not panic

	// Closed channels drain immediately and report not-ok once emptied.
	drain := func(ch <-chan masterdata.SyncUpdatedEvent) {
		for {
			if _, ok := <-ch; !ok {
				return
			}
		}
	}
	drain(sub1)
	drain(sub2)

	// Subscribe after close returns an already-closed channel.
	closedSub, _ := hub.Subscribe()
	drain(closedSub)

	// Publish after close must not panic.
	hub.PublishMasterDataUpdated(context.Background(), masterdata.SyncUpdatedEvent{Event: "master_data_updated", Status: "success"})
}

// TestSyncInterruptedDuringCacheStoreLeavesRecoverable proves the cache-store
// phase respects lifecycle cancellation: no terminal failed/success status is
// persisted and the region is left in its recoverable running state.
func TestSyncInterruptedDuringCacheStoreLeavesRecoverable(t *testing.T) {
	source := jpShutdownTestSource()
	loader := &fakeSyncLoader{
		payloadByZone: map[string]map[string]any{"jp": {"cards": []any{}}},
	}
	cache := &blockingSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)

	uc := newShutdownFixtureUsecase(t, source, loader, cache, statusStore)

	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	uc.SetLifecycleContext(lifecycleCtx)

	if err := uc.StartSync(context.Background(), "jp", false); err != nil {
		t.Fatalf("StartSync returned error: %v", err)
	}

	// Let the worker load successfully and begin the (blocking) cache store.
	time.Sleep(50 * time.Millisecond)

	// Graceful shutdown: cancel the app lifecycle context mid cache-store.
	cancelLifecycle()

	done := make(chan struct{})
	go func() {
		uc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sync worker did not stop after lifecycle cancellation")
	}

	for _, saved := range statusStore.saved {
		if saved.Region == "jp" && (saved.Status == "success" || saved.Status == "failed") {
			t.Fatalf("interrupted cache store must not persist terminal status, got %s", saved.Status)
		}
	}
	if !statusStore.hasSavedStatus("jp", "running") && !statusStore.hasSavedStatus("jp", "pending") {
		t.Fatalf("interrupted region status must remain recoverable (running/pending)")
	}
	if !cache.hasStoreCall("jp") {
		t.Fatalf("expected cache store to have been attempted")
	}
}

func (c *blockingSyncCache) hasStoreCall(_ string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.storeCalls > 0
}

// TestStartSyncRejectedAfterCloseAdmission verifies that once the lifecycle
// admission gate is closed (graceful shutdown), StartSync refuses new workers
// with ErrShutdownAdmission instead of admitting a sync that could outlive
// dependency teardown.
func TestStartSyncRejectedAfterCloseAdmission(t *testing.T) {
	source := jpShutdownTestSource()
	loader := newShutdownTestLoader()
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)
	uc := newShutdownFixtureUsecase(t, source, loader, cache, statusStore)

	uc.CloseAdmission()
	if err := uc.StartSync(context.Background(), "jp", false); !errors.Is(err, ErrShutdownAdmission) {
		t.Fatalf("expected ErrShutdownAdmission, got %v", err)
	}
}

// TestWaitClosesAdmissionAndDrainsInflight verifies Wait closes the admission gate
// (so subsequent sync starts are refused) and still drains the in-flight worker.
func TestWaitClosesAdmissionAndDrainsInflight(t *testing.T) {
	source := jpShutdownTestSource()
	loader := newShutdownTestLoader()
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)
	uc := newShutdownFixtureUsecase(t, source, loader, cache, statusStore)

	if err := uc.StartSync(context.Background(), "jp", false); err != nil {
		t.Fatalf("StartSync returned error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		uc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after in-flight worker finished")
	}

	if err := uc.StartSync(context.Background(), "jp", false); !errors.Is(err, ErrShutdownAdmission) {
		t.Fatalf("expected ErrShutdownAdmission after Wait closed admission, got %v", err)
	}
}

// TestSyncWGAdmissionNoAddWaitRace exercises the admission primitive under
// concurrency to prove there is no syncWG.Add/Wait race once the gate is closed.
// Run with -race to detect any data race on the admission flag or WaitGroup.
func TestSyncWGAdmissionNoAddWaitRace(t *testing.T) {
	uc := NewMasterDataSyncUsecase(nil, nil, nil, nil, nil, 1)

	var admitters sync.WaitGroup
	for i := 0; i < 50; i++ {
		admitters.Add(1)
		go func() {
			defer admitters.Done()
			for j := 0; j < 200; j++ {
				if uc.tryAdmitSyncWorker() {
					uc.syncWG.Done()
				}
			}
		}()
	}

	closed := make(chan struct{})
	go func() {
		time.Sleep(2 * time.Millisecond)
		uc.CloseAdmission()
		uc.syncWG.Wait()
		close(closed)
	}()

	admitters.Wait()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("closer did not finish Wait after admission closed")
	}
}

// TestSyncReturnsContextCanceledOnInterruption verifies the run itself reports
// interruption (context.Canceled) rather than a success/failure error.
func TestSyncReturnsContextCanceledOnInterruption(t *testing.T) {
	source := jpShutdownTestSource()
	loader := &timedSyncLoader{
		payloadByZone:   map[string]map[string]any{"jp": {"cards": []any{}}},
		loadDelayByZone: map[string]time.Duration{"jp": 5 * time.Second},
	}
	cache := &fakeSyncCache{}
	statusStore := newFakeSyncStatusStore(nil)
	uc := newShutdownFixtureUsecase(t, source, loader, cache, statusStore)

	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	uc.SetLifecycleContext(lifecycleCtx)

	runErrCh := make(chan error, 1)
	// The startup path calls SyncAll(appCtx); cancellation propagates through the
	// parent context, not a separate lifecycle wiring.
	go func() { runErrCh <- uc.SyncAll(lifecycleCtx) }()

	time.Sleep(50 * time.Millisecond)
	cancelLifecycle()

	select {
	case err := <-runErrCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled on interruption, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SyncAll did not return after lifecycle cancellation")
	}
}

// jpShutdownTestSource and newShutdownTestLoader collapse the repeated fixture
// literals shared across the shutdown tests so they are defined once.
func jpShutdownTestSource() masterdata.Source {
	return masterdata.Source{Region: "jp", Owner: "Sekai-World", Repo: "sekai-master-data-jp", Ref: "main"}
}

func newShutdownTestLoader() *fakeSyncLoader {
	return &fakeSyncLoader{payloadByZone: map[string]map[string]any{"jp": {"cards": []any{}}}}
}

// newShutdownFixtureUsecase builds a MasterDataSyncUsecase for shutdown tests with
// a nil backup store and a background lifecycle context, collapsing the repeated
// constructor/setup boilerplate shared across these tests.
func newShutdownFixtureUsecase(t *testing.T, source masterdata.Source, loader MasterDataSourceLoader, cache MasterDataCache, statusStore MasterDataSyncStatusStore) *MasterDataSyncUsecase {
	t.Helper()
	uc := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, cache, statusStore, &fakeSyncEventPublisher{}, 1)
	uc.SetBackupStore(nil)
	uc.SetLifecycleContext(context.Background())
	return uc
}
