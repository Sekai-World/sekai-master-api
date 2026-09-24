package storage

import (
	"context"
	"testing"
)

func TestEntityRevisionTracksStoredEntityContent(t *testing.T) {
	cache := newStoreRegionTestCache(t, startTestMiniRedis(t))
	ctx := context.Background()

	missing, err := cache.EntityRevision(ctx, "jp", "resourceBoxes")
	if err != nil {
		t.Fatalf("read missing revision: %v", err)
	}
	if missing != "" {
		t.Fatalf("expected empty revision before sync, got %q", missing)
	}

	payload := map[string]any{
		"resourceBoxes.json": []any{
			map[string]any{"id": 1, "resourceBoxPurpose": "story_mission"},
		},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store initial payload: %v", err)
	}
	initial, err := cache.EntityRevision(ctx, "JP", "resourceboxes")
	if err != nil {
		t.Fatalf("read initial revision: %v", err)
	}
	if initial == "" {
		t.Fatal("expected a revision after storing the entity")
	}

	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store unchanged payload: %v", err)
	}
	unchanged, err := cache.EntityRevision(ctx, "jp", "resourceboxes")
	if err != nil {
		t.Fatalf("read unchanged revision: %v", err)
	}
	if unchanged != initial {
		t.Fatalf("expected unchanged content to keep revision %q, got %q", initial, unchanged)
	}

	payload["resourceBoxes.json"] = []any{
		map[string]any{"id": 1, "resourceBoxPurpose": "story_mission"},
		map[string]any{"id": 2, "resourceBoxPurpose": "normal_mission"},
	}
	if err := cache.StoreRegion(ctx, "jp", payload); err != nil {
		t.Fatalf("store changed payload: %v", err)
	}
	changed, err := cache.EntityRevision(ctx, "jp", "resourceboxes")
	if err != nil {
		t.Fatalf("read changed revision: %v", err)
	}
	if changed == "" || changed == initial {
		t.Fatalf("expected changed content to produce a new revision, got %q (initial %q)", changed, initial)
	}
}
