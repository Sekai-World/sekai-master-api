package shared

import (
	"testing"
	"time"
)

func TestFilterSpoilerItemsRemovesFutureReleaseAndStartTimes(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	items := []map[string]any{
		{"id": 1, "releaseAt": now.Add(-time.Hour).UnixMilli()},
		{"id": 2, "releaseAt": now.Add(time.Hour).UnixMilli()},
		{"id": 3, "startAt": now.Add(time.Hour).UnixMilli()},
		{"id": 4, "releastAt": now.Add(time.Hour).UnixMilli()},
		{"id": 5},
	}

	filtered := FilterSpoilerItems(items, now)

	if len(filtered) != 2 {
		t.Fatalf("expected two non-spoiler items, got %d", len(filtered))
	}
	if filtered[0]["id"] != 1 || filtered[1]["id"] != 5 {
		t.Fatalf("expected non-spoiler ids [1 5], got %v", filtered)
	}
}

func TestParseTimestampMillisSupportsCommonTimestampFormats(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  int64
	}{
		{name: "seconds", value: int64(1_700_000_000), want: 1_700_000_000_000},
		{name: "millis", value: int64(1_700_000_000_000), want: 1_700_000_000_000},
		{name: "micros", value: int64(1_700_000_000_000_000), want: 1_700_000_000_000},
		{name: "rfc3339", value: "2023-11-14T22:13:20Z", want: 1_700_000_000_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseTimestampMillis(tt.value)
			if !ok {
				t.Fatalf("expected timestamp to parse")
			}
			if got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}
