package repository

import (
	"context"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

func TestStatusRepositoryReportsLatestRowsPerRegion(t *testing.T) {
	db := newPostgresTestDB(t)
	repository := NewMasterDataSyncStatusRepository(db, "")
	ctx := context.Background()

	sources := []masterdata.Source{
		{Region: "jp", Owner: "Sekai-World", Repo: "sekai-master-db-diff", Ref: "main"},
		{Region: "en", Owner: "Sekai-World", Repo: "sekai-master-db-en-diff", Ref: "main", Path: "master"},
	}
	if err := repository.SeedPending(ctx, sources); err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	save := func(source masterdata.Source, status string, commit string, errorMessage string) {
		t.Helper()
		now := time.Now().UTC()
		if err := repository.Save(ctx, masterdata.SyncStatus{
			Region:         source.Region,
			Status:         status,
			FileCount:      419,
			SyncDurationMS: 1234,
			LastSyncedAt:   now,
			SourceCommit:   commit,
			ErrorMessage:   errorMessage,
			Source:         source,
			UpdatedAt:      now,
		}); err != nil {
			t.Fatalf("save %s %s: %v", source.Region, status, err)
		}
	}
	// jp: a successful sync followed by a new one still running.
	save(sources[0], "running", "", "")
	save(sources[0], "success", "commit-1", "")
	save(sources[0], "running", "commit-2", "")
	// en: a failed sync.
	save(sources[1], "running", "", "")
	save(sources[1], "failed", "", "fetch failed")

	latest := statusesByRegion(t, repository.List)
	assertStatus(t, "latest jp", latest["jp"], "running", "commit-2", "")
	assertStatus(t, "latest en", latest["en"], "failed", "", "fetch failed")
	if latest["en"].Source != sources[1] || latest["jp"].Source != sources[0] {
		t.Fatalf("sources = %+v / %+v, want %+v / %+v", latest["jp"].Source, latest["en"].Source, sources[0], sources[1])
	}
	if latest["jp"].FileCount != 419 || latest["jp"].SyncDurationMS != 1234 || latest["jp"].UpdatedAt.IsZero() {
		t.Fatalf("latest jp = %+v, want file count, duration and update time", latest["jp"])
	}

	success := statusesByRegion(t, repository.ListLatestSuccess)
	if len(success) != 1 {
		t.Fatalf("latest success = %+v, want only jp", success)
	}
	assertStatus(t, "latest success jp", success["jp"], "success", "commit-1", "")

	stable := statusesByRegion(t, repository.ListLatestStable)
	assertStatus(t, "latest stable jp", stable["jp"], "success", "commit-1", "")
	assertStatus(t, "latest stable en", stable["en"], "failed", "", "fetch failed")
}

func TestStatusRepositoryWithoutDatabaseIsEmpty(t *testing.T) {
	repository := NewMasterDataSyncStatusRepository(nil, "master-data-sync")
	if err := repository.Save(context.Background(), masterdata.SyncStatus{Region: "jp"}); err != nil {
		t.Fatalf("save without database: %v", err)
	}
	statuses, err := repository.List(context.Background())
	if err != nil || len(statuses) != 0 {
		t.Fatalf("list without database = %+v, %v; want empty", statuses, err)
	}
}

func statusesByRegion(t *testing.T, list func(context.Context) ([]masterdata.SyncStatus, error)) map[string]masterdata.SyncStatus {
	t.Helper()

	statuses, err := list(context.Background())
	if err != nil {
		t.Fatalf("list statuses: %v", err)
	}
	byRegion := make(map[string]masterdata.SyncStatus, len(statuses))
	for _, status := range statuses {
		if _, duplicate := byRegion[status.Region]; duplicate {
			t.Fatalf("region %s listed twice in %+v", status.Region, statuses)
		}
		byRegion[status.Region] = status
	}
	return byRegion
}

func assertStatus(t *testing.T, label string, got masterdata.SyncStatus, status string, commit string, errorMessage string) {
	t.Helper()
	if got.Status != status || got.SourceCommit != commit || got.ErrorMessage != errorMessage {
		t.Fatalf("%s = status %q commit %q error %q, want %q %q %q", label, got.Status, got.SourceCommit, got.ErrorMessage, status, commit, errorMessage)
	}
}
