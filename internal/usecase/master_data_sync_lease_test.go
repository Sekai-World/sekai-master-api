package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

type fakeLeaseCoordinator struct {
	mu         sync.Mutex
	acquireErr error
	renewErr   error
	acquired   []masterdata.SyncLeaseClaim
	renewals   int
	released   []masterdata.SyncLeaseClaim
	state      masterdata.SyncLeaseState
	stateErr   error
}

func (coordinator *fakeLeaseCoordinator) Acquire(ctx context.Context) (masterdata.SyncLeaseClaim, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.acquireErr != nil {
		return masterdata.SyncLeaseClaim{}, coordinator.acquireErr
	}
	claim := masterdata.SyncLeaseClaim{Holder: "holder-test", Token: int64(len(coordinator.acquired)) + 1}
	coordinator.acquired = append(coordinator.acquired, claim)
	return claim, nil
}

func (coordinator *fakeLeaseCoordinator) Renew(ctx context.Context, claim masterdata.SyncLeaseClaim) error {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.renewals++
	return coordinator.renewErr
}

func (coordinator *fakeLeaseCoordinator) Release(ctx context.Context, claim masterdata.SyncLeaseClaim) error {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.released = append(coordinator.released, claim)
	return nil
}

func (coordinator *fakeLeaseCoordinator) State(ctx context.Context) (masterdata.SyncLeaseState, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	return coordinator.state, coordinator.stateErr
}

type leaseBlockingLoader struct {
	mu    sync.Mutex
	calls int
}

func (loader *leaseBlockingLoader) LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error) {
	loader.mu.Lock()
	loader.calls++
	loader.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func newLeaseTestUsecase(t *testing.T, loader MasterDataSourceLoader, statusStore MasterDataSyncStatusStore) *MasterDataSyncUsecase {
	t.Helper()
	source := masterdata.Source{Region: "jp", Owner: "Sekai-World", Repo: "masterdata-dump", Ref: "main"}
	usecase := NewMasterDataSyncUsecase([]masterdata.Source{source}, loader, &fakeSyncCache{}, statusStore, nil, 1)
	return usecase
}

func TestSyncSkipsWhenLeaseHeldElsewhere(t *testing.T) {
	loader := &fakeSyncLoader{}
	statusStore := newFakeSyncStatusStore(nil)
	usecase := newLeaseTestUsecase(t, loader, statusStore)
	coordinator := &fakeLeaseCoordinator{acquireErr: masterdata.ErrSyncLeaseHeld}
	usecase.SetLeaseCoordinator(coordinator, time.Second)

	err := usecase.SyncAllForce(context.Background())
	if !errors.Is(err, masterdata.ErrSyncLeaseHeld) {
		t.Fatalf("SyncAllForce error = %v, want ErrSyncLeaseHeld", err)
	}
	if loader.loadCalls != 0 {
		t.Fatalf("loader called %d times, want 0", loader.loadCalls)
	}
	if len(coordinator.released) != 0 {
		t.Fatalf("release called for a lease that was never acquired: %+v", coordinator.released)
	}
}

func TestSyncAcquireInfrastructureErrorFailsJob(t *testing.T) {
	loader := &fakeSyncLoader{}
	usecase := newLeaseTestUsecase(t, loader, newFakeSyncStatusStore(nil))
	coordinator := &fakeLeaseCoordinator{acquireErr: errors.New("pg unreachable")}
	usecase.SetLeaseCoordinator(coordinator, time.Second)

	err := usecase.SyncAllForce(context.Background())
	if err == nil || errors.Is(err, masterdata.ErrSyncLeaseHeld) {
		t.Fatalf("SyncAllForce error = %v, want a wrapped infrastructure error", err)
	}
	if loader.loadCalls != 0 {
		t.Fatalf("loader called %d times, want 0", loader.loadCalls)
	}
}

func TestSyncStampsLeaseTokenOnStatusWritesAndReleases(t *testing.T) {
	loader := &fakeSyncLoader{
		payloadByZone: map[string]map[string]any{
			"jp": {"cards.json": []any{map[string]any{"id": 1, "prefix": "初音"}}},
		},
	}
	statusStore := newFakeSyncStatusStore(nil)
	usecase := newLeaseTestUsecase(t, loader, statusStore)
	coordinator := &fakeLeaseCoordinator{}
	usecase.SetLeaseCoordinator(coordinator, time.Second)

	if err := usecase.SyncAllForce(context.Background()); err != nil {
		t.Fatalf("SyncAllForce failed: %v", err)
	}

	latest, exists := statusStore.latest("jp")
	if !exists || latest.Status != "success" {
		t.Fatalf("latest status = %+v exists=%v, want success", latest, exists)
	}
	if latest.FencingToken != 1 {
		t.Fatalf("status fencing token = %d, want 1 (stamped from the lease claim)", latest.FencingToken)
	}
	if len(coordinator.released) != 1 || coordinator.released[0].Token != 1 {
		t.Fatalf("released claims = %+v, want exactly the token-1 claim", coordinator.released)
	}
}

func TestSyncHeartbeatLossCancelsJobAndRecoversLease(t *testing.T) {
	loader := &leaseBlockingLoader{}
	statusStore := newFakeSyncStatusStore(nil)
	usecase := newLeaseTestUsecase(t, loader, statusStore)
	coordinator := &fakeLeaseCoordinator{renewErr: masterdata.ErrSyncLeaseLost}
	usecase.SetLeaseCoordinator(coordinator, 2*time.Millisecond)

	err := usecase.SyncAllForce(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncAllForce error = %v, want context.Canceled (lease-loss self-fencing)", err)
	}
	if loader.calls == 0 {
		t.Fatal("loader was never called; job cancelled before it started")
	}
	if coordinator.renewals == 0 {
		t.Fatal("heartbeat never renewed; lease loss was not detected")
	}
	if len(coordinator.released) != 1 {
		t.Fatalf("released claims = %+v, want the acquired claim released after cancellation", coordinator.released)
	}

	// A cancelled (interrupted) job must not leave a terminal status behind.
	latest, exists := statusStore.latest("jp")
	if exists && (latest.Status == "success" || latest.Status == "failed") {
		t.Fatalf("latest status after lease loss = %q, want recoverable (running/pending)", latest.Status)
	}
}

// ctxValidatingLeaseCoordinator mimics a real database coordinator: a Release
// call whose context is already expired fails instead of silently succeeding.
type ctxValidatingLeaseCoordinator struct {
	fakeLeaseCoordinator
	releaseCtxErrs []error
}

func (coordinator *ctxValidatingLeaseCoordinator) Release(ctx context.Context, claim masterdata.SyncLeaseClaim) error {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if err := ctx.Err(); err != nil {
		coordinator.releaseCtxErrs = append(coordinator.releaseCtxErrs, err)
		return err
	}
	coordinator.released = append(coordinator.released, claim)
	return nil
}

type leaseSlowLoader struct {
	delay time.Duration
}

func (loader *leaseSlowLoader) LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(loader.delay):
		return map[string]any{"cards.json": []any{map[string]any{"id": 1, "prefix": "初音"}}}, nil
	}
}

// Regression: the release context must be created when the release runs, not
// when the lease is acquired. A real sync outlasts any fixed release budget,
// so a budget created up front is always expired by release time (observed as
// "release sync lease: context deadline exceeded" against real PostgreSQL).
func TestSyncReleaseContextIsFreshAfterLongSync(t *testing.T) {
	statusStore := newFakeSyncStatusStore(nil)
	usecase := newLeaseTestUsecase(t, &leaseSlowLoader{delay: 50 * time.Millisecond}, statusStore)
	coordinator := &ctxValidatingLeaseCoordinator{}
	usecase.SetLeaseCoordinator(coordinator, time.Second)
	usecase.leaseReleaseTimeout = 10 * time.Millisecond

	if err := usecase.SyncAllForce(context.Background()); err != nil {
		t.Fatalf("SyncAllForce failed: %v", err)
	}

	if len(coordinator.releaseCtxErrs) != 0 {
		t.Fatalf("release ran with an expired context: %v", coordinator.releaseCtxErrs)
	}
	if len(coordinator.released) != 1 || coordinator.released[0].Token != 1 {
		t.Fatalf("released claims = %+v, want exactly the token-1 claim", coordinator.released)
	}
}

func TestSyncLeaseStatePassesThroughCoordinator(t *testing.T) {
	usecase := newLeaseTestUsecase(t, &fakeSyncLoader{}, newFakeSyncStatusStore(nil))

	state, err := usecase.SyncLeaseState(context.Background())
	if err != nil || state.Held {
		t.Fatalf("nil coordinator state = %+v err=%v, want unheld without error", state, err)
	}

	want := masterdata.SyncLeaseState{Held: true, Holder: "other", Token: 7, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	coordinator := &fakeLeaseCoordinator{state: want}
	usecase.SetLeaseCoordinator(coordinator, time.Second)

	state, err = usecase.SyncLeaseState(context.Background())
	if err != nil || state != want {
		t.Fatalf("state = %+v err=%v, want %+v", state, err, want)
	}
}
