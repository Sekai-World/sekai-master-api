package shared

import (
	"context"
	"testing"
	"time"

	"sekai-master-api/internal/transport/http/cachehint"
)

func TestFilterSpoilerItemsContextReportsTheFirstReveal(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	items := []map[string]any{
		{"id": 1, "releaseAt": now.Add(-time.Hour).UnixMilli()},
		// Hidden until both of its future times pass.
		{"id": 2, "startAt": now.Add(time.Hour).UnixMilli(), "publishedAt": now.Add(3 * time.Hour).UnixMilli()},
		{"id": 3, "releaseAt": now.Add(2 * time.Hour).UnixMilli()},
	}

	ctx, hint := cachehint.WithHint(context.Background())
	filtered := FilterSpoilerItemsContext(ctx, items, now)

	if len(filtered) != 1 || filtered[0]["id"] != 1 {
		t.Fatalf("filtered = %v, want only id 1", filtered)
	}
	until, ok := hint.Until()
	if !ok || !until.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("reported %v %t, want the reveal of id 3 at +2h", until, ok)
	}
}

func TestFilterSpoilerItemsContextReportsNothingWithoutSpoilers(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	ctx, hint := cachehint.WithHint(context.Background())
	FilterSpoilerItemsContext(ctx, []map[string]any{{"id": 1, "releaseAt": now.Add(-time.Hour).UnixMilli()}}, now)
	if _, ok := hint.Until(); ok {
		t.Fatal("expected no reported change when nothing is hidden")
	}
	// Outside a cached request the call must not panic.
	FilterSpoilerItemsContext(context.Background(), []map[string]any{{"id": 2, "releaseAt": now.Add(time.Hour).UnixMilli()}}, now)
}
