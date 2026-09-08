package repository

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

// newSQLiteTestDB opens an in-memory SQLite database with the coordination
// schema. The DDL mirrors the Goose migrations; production fencing runs on
// PostgreSQL, but the SQLite path keeps development parity testable.
func newSQLiteTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
CREATE TABLE master_data_sync_leases (
  name TEXT PRIMARY KEY,
  holder TEXT NOT NULL,
  fencing_token BIGINT NOT NULL,
  acquired_at TIMESTAMP NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  last_heartbeat_at TIMESTAMP NOT NULL
)`)
	if err != nil {
		t.Fatalf("create leases table: %v", err)
	}

	_, err = db.Exec(`
CREATE TABLE master_data_sync_status (
  region TEXT NOT NULL,
  status TEXT NOT NULL,
  file_count INTEGER NOT NULL,
  sync_duration_ms INTEGER NOT NULL DEFAULT 0,
  last_synced_at TIMESTAMP NOT NULL,
  source_commit TEXT,
  error_message TEXT,
  source_owner TEXT NOT NULL,
  source_repo TEXT NOT NULL,
  source_ref TEXT NOT NULL,
  source_path TEXT,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  fencing_token BIGINT NOT NULL DEFAULT 0
)`)
	if err != nil {
		t.Fatalf("create status table: %v", err)
	}

	return db
}

func TestLeaseAcquireIssuesFirstToken(t *testing.T) {
	db := newSQLiteTestDB(t)
	repository := NewMasterDataSyncLeaseRepository(db, "sqlite")

	record, acquired, err := repository.Acquire(context.Background(), "master-data-sync", "holder-a", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("first acquire failed: acquired=%v err=%v", acquired, err)
	}
	if record.FencingToken != 1 {
		t.Fatalf("first acquire token = %d, want 1", record.FencingToken)
	}
	if record.Holder != "holder-a" {
		t.Fatalf("holder = %q, want holder-a", record.Holder)
	}
}

func TestLeaseAcquireBusyWhileUnexpired(t *testing.T) {
	db := newSQLiteTestDB(t)
	repository := NewMasterDataSyncLeaseRepository(db, "sqlite")

	if _, acquired, err := repository.Acquire(context.Background(), "master-data-sync", "holder-a", time.Minute); err != nil || !acquired {
		t.Fatalf("seed acquire failed: acquired=%v err=%v", acquired, err)
	}

	_, acquired, err := repository.Acquire(context.Background(), "master-data-sync", "holder-b", time.Minute)
	if err != nil {
		t.Fatalf("second acquire errored: %v", err)
	}
	if acquired {
		t.Fatal("second acquire unexpectedly succeeded while lease unexpired")
	}
}

func TestLeaseTakeoverAfterExpiryBumpsToken(t *testing.T) {
	db := newSQLiteTestDB(t)
	repository := NewMasterDataSyncLeaseRepository(db, "sqlite")

	if _, acquired, err := repository.Acquire(context.Background(), "master-data-sync", "holder-a", time.Millisecond); err != nil || !acquired {
		t.Fatalf("seed acquire failed: acquired=%v err=%v", acquired, err)
	}
	time.Sleep(5 * time.Millisecond)

	record, acquired, err := repository.Acquire(context.Background(), "master-data-sync", "holder-b", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("takeover acquire failed: acquired=%v err=%v", acquired, err)
	}
	if record.FencingToken != 2 {
		t.Fatalf("takeover token = %d, want 2", record.FencingToken)
	}
	if record.Holder != "holder-b" {
		t.Fatalf("takeover holder = %q, want holder-b", record.Holder)
	}
}

func TestLeaseRenewKeepsHolderButFailsAfterTakeover(t *testing.T) {
	db := newSQLiteTestDB(t)
	repository := NewMasterDataSyncLeaseRepository(db, "sqlite")

	if _, acquired, err := repository.Acquire(context.Background(), "master-data-sync", "holder-a", 100*time.Millisecond); err != nil || !acquired {
		t.Fatalf("seed acquire failed: acquired=%v err=%v", acquired, err)
	}

	renewed, err := repository.Renew(context.Background(), "master-data-sync", "holder-a", time.Minute)
	if err != nil || !renewed {
		t.Fatalf("holder renewal failed: renewed=%v err=%v", renewed, err)
	}

	// Release expires the lease in place; holder-b takes over, and the stale
	// holder must fail to renew afterwards.
	if err := repository.Release(context.Background(), "master-data-sync", "holder-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, acquired, err := repository.Acquire(context.Background(), "master-data-sync", "holder-b", time.Minute); err != nil || !acquired {
		t.Fatalf("takeover failed: acquired=%v err=%v", acquired, err)
	}

	renewed, err = repository.Renew(context.Background(), "master-data-sync", "holder-a", time.Minute)
	if err != nil {
		t.Fatalf("stale renewal errored: %v", err)
	}
	if renewed {
		t.Fatal("stale holder renewal unexpectedly succeeded after takeover")
	}
}

func TestLeaseReleaseEnablesImmediateAcquireWithNextToken(t *testing.T) {
	db := newSQLiteTestDB(t)
	repository := NewMasterDataSyncLeaseRepository(db, "sqlite")

	if _, acquired, err := repository.Acquire(context.Background(), "master-data-sync", "holder-a", time.Minute); err != nil || !acquired {
		t.Fatalf("seed acquire failed: acquired=%v err=%v", acquired, err)
	}
	if err := repository.Release(context.Background(), "master-data-sync", "holder-a"); err != nil {
		t.Fatalf("release: %v", err)
	}

	record, acquired, err := repository.Acquire(context.Background(), "master-data-sync", "holder-b", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("post-release acquire failed: acquired=%v err=%v", acquired, err)
	}
	if record.FencingToken != 2 {
		t.Fatalf("post-release token = %d, want 2 (monotonic across cycles)", record.FencingToken)
	}
}

func TestLeaseCoordinatorMapsRepositoryOutcomes(t *testing.T) {
	db := newSQLiteTestDB(t)
	repository := NewMasterDataSyncLeaseRepository(db, "sqlite")

	coordinatorA := NewMasterDataSyncLeaseCoordinator(repository, "master-data-sync", "holder-a", time.Minute)
	claim, err := coordinatorA.Acquire(context.Background())
	if err != nil || claim.Token != 1 {
		t.Fatalf("coordinator acquire: claim=%+v err=%v", claim, err)
	}

	coordinatorB := NewMasterDataSyncLeaseCoordinator(repository, "master-data-sync", "holder-b", time.Minute)
	if _, err := coordinatorB.Acquire(context.Background()); !errors.Is(err, masterdata.ErrSyncLeaseHeld) {
		t.Fatalf("busy acquire error = %v, want ErrSyncLeaseHeld", err)
	}

	if err := coordinatorA.Renew(context.Background(), claim); err != nil {
		t.Fatalf("renew: %v", err)
	}

	state, err := coordinatorB.State(context.Background())
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !state.Held || state.Holder != "holder-a" || state.Token != 1 {
		t.Fatalf("state = %+v, want held by holder-a token 1", state)
	}

	if err := coordinatorA.Release(context.Background(), claim); err != nil {
		t.Fatalf("release: %v", err)
	}
	state, err = coordinatorB.State(context.Background())
	if err != nil {
		t.Fatalf("state after release: %v", err)
	}
	if state.Held {
		t.Fatalf("state after release = %+v, want unheld", state)
	}
}

func TestFencedStatusSaveRejectsStaleToken(t *testing.T) {
	db := newSQLiteTestDB(t)
	leaseRepository := NewMasterDataSyncLeaseRepository(db, "sqlite")
	statusRepository := NewMasterDataSyncStatusRepository(db, "sqlite", "master-data-sync")

	if _, acquired, err := leaseRepository.Acquire(context.Background(), "master-data-sync", "holder-a", time.Millisecond); err != nil || !acquired {
		t.Fatalf("seed acquire failed: acquired=%v err=%v", acquired, err)
	}

	status := masterdata.SyncStatus{
		Region:       "jp",
		Status:       "success",
		SourceCommit: "commit-1",
		FencingToken: 1,
		LastSyncedAt: time.Now().UTC(),
		Source:       masterdata.Source{Region: "jp", Owner: "Sekai-World", Repo: "masterdata-dump", Ref: "main"},
		UpdatedAt:    time.Now().UTC(),
	}
	if err := statusRepository.Save(context.Background(), status); err != nil {
		t.Fatalf("current-token save failed: %v", err)
	}

	// Let holder-a's lease expire, then take over as holder-b (token 2); the
	// stale owner's token-1 write must be fenced out.
	time.Sleep(5 * time.Millisecond)
	if _, acquired, err := leaseRepository.Acquire(context.Background(), "master-data-sync", "holder-b", time.Minute); err != nil || !acquired {
		t.Fatalf("takeover failed: acquired=%v err=%v", acquired, err)
	}

	stale := status
	stale.Status = "failed"
	stale.ErrorMessage = "stale owner write"
	if err := statusRepository.Save(context.Background(), stale); !errors.Is(err, masterdata.ErrFencedOut) {
		t.Fatalf("stale save error = %v, want ErrFencedOut", err)
	}

	// Token-zero writes (unleased legacy paths) always pass.
	legacy := status
	legacy.FencingToken = 0
	if err := statusRepository.Save(context.Background(), legacy); err != nil {
		t.Fatalf("legacy token-zero save failed: %v", err)
	}

	// The new owner's token-2 write passes and persists the token.
	fresh := status
	fresh.FencingToken = 2
	if err := statusRepository.Save(context.Background(), fresh); err != nil {
		t.Fatalf("new-owner save failed: %v", err)
	}
	var persisted int64
	if err := db.QueryRow(`SELECT fencing_token FROM master_data_sync_status WHERE region = 'jp' ORDER BY created_at DESC, rowid DESC LIMIT 1`).Scan(&persisted); err != nil {
		t.Fatalf("query persisted token: %v", err)
	}
	if persisted != 2 {
		t.Fatalf("persisted fencing_token = %d, want 2", persisted)
	}
}
