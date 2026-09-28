package usecase

import (
	"context"
	"fmt"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

func TestCurrentEventUntilReportsTheNextChange(t *testing.T) {
	now := time.UnixMilli(1_772_438_533_000).UTC()
	nowMillis := now.UnixMilli()
	for _, tt := range []struct {
		name      string
		events    []map[string]any
		wantID    string
		wantUntil int64
	}{
		{
			name: "current event closes before the next starts",
			events: []map[string]any{
				{"id": 1, "startAt": nowMillis - 60_000, "closedAt": nowMillis + 60_000},
				{"id": 2, "startAt": nowMillis + 120_000, "closedAt": nowMillis + 300_000},
			},
			wantID: "1", wantUntil: nowMillis + 60_001,
		},
		{
			name: "next event starts first",
			events: []map[string]any{
				{"id": 1, "startAt": nowMillis - 60_000, "closedAt": nowMillis + 600_000},
				{"id": 2, "startAt": nowMillis + 120_000, "closedAt": nowMillis + 300_000},
			},
			wantID: "1", wantUntil: nowMillis + 120_000,
		},
		{
			name:   "between events",
			events: []map[string]any{{"id": 1, "startAt": nowMillis - 600_000, "closedAt": nowMillis - 60_000}, {"id": 2, "startAt": nowMillis + 90_000, "closedAt": nowMillis + 300_000}},
			wantID: "", wantUntil: nowMillis + 90_000,
		},
		{
			name:   "no future change",
			events: []map[string]any{{"id": 1, "startAt": nowMillis - 600_000, "closedAt": nowMillis - 60_000}},
			wantID: "", wantUntil: 0,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usecase := NewMasterDataSyncUsecase(nil, nil, &fakeCurrentEventCache{events: tt.events}, nil, nil, 1)
			record, found, until, err := usecase.CurrentEventUntil(context.Background(), "jp", now)
			if err != nil {
				t.Fatal(err)
			}
			gotID := ""
			if found {
				gotID = fmt.Sprint(record["id"])
			}
			if gotID != tt.wantID {
				t.Fatalf("current event %q, want %q", gotID, tt.wantID)
			}
			gotUntil := int64(0)
			if !until.IsZero() {
				gotUntil = until.UnixMilli()
			}
			if gotUntil != tt.wantUntil {
				t.Fatalf("until %d, want %d", gotUntil, tt.wantUntil)
			}
		})
	}
}

func TestRegionCacheRevisionFollowsTheLatestStatus(t *testing.T) {
	updatedAt := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	statusStore := newFakeSyncStatusStore([]masterdata.SyncStatus{
		{Region: "jp", Status: "success", SourceCommit: "c1", UpdatedAt: updatedAt},
		{Region: "en", Status: "running", SourceCommit: "c2", UpdatedAt: updatedAt},
	})
	usecase := NewMasterDataSyncUsecase(nil, nil, nil, statusStore, nil, 1)
	ctx := context.Background()

	revision, ok, err := usecase.RegionCacheRevision(ctx, " JP ")
	if err != nil || !ok || revision != "c1@2026-09-28T12:00:00Z" {
		t.Fatalf("jp revision %q %t %v", revision, ok, err)
	}
	if _, ok, _ := usecase.RegionCacheRevision(ctx, "en"); ok {
		t.Fatal("a running sync must bypass the cache")
	}
	if _, ok, _ := usecase.RegionCacheRevision(ctx, "tw"); ok {
		t.Fatal("a region without status must bypass the cache")
	}

	// A new sync of the same commit still moves the revision.
	statusStore.byZone["jp"] = masterdata.SyncStatus{Region: "jp", Status: "success", SourceCommit: "c1", UpdatedAt: updatedAt.Add(time.Minute)}
	next, ok, _ := usecase.RegionCacheRevision(ctx, "jp")
	if !ok || next == revision {
		t.Fatalf("revision after a re-sync = %q %t, want a new value", next, ok)
	}
}
