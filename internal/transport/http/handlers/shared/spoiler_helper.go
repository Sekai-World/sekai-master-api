package shared

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
	"sekai-master-api/internal/transport/http/response"
)

func ParseSpoilerOption(c *gin.Context) (bool, bool) {
	rawSpoiler := strings.TrimSpace(c.Query("spoiler"))
	if rawSpoiler == "" {
		return false, true
	}

	includeSpoilers, err := strconv.ParseBool(rawSpoiler)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "spoiler must be a boolean")
		return false, false
	}

	return includeSpoilers, true
}

func FilterSpoilerItems(items []map[string]any, now time.Time) []map[string]any {
	if len(items) == 0 {
		return items
	}

	nowMillis := now.UTC().UnixMilli()
	filtered := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if IsSpoilerItem(item, nowMillis) {
			continue
		}
		filtered = append(filtered, item)
	}

	return filtered
}

// FilterSpoilerItemsContext filters like FilterSpoilerItems and reports to
// the response cache when the first dropped item stops being a spoiler, since
// the filtered list changes then.
func FilterSpoilerItemsContext(ctx context.Context, items []map[string]any, now time.Time) []map[string]any {
	filtered := FilterSpoilerItems(items, now)
	if len(filtered) == len(items) {
		return filtered
	}
	nowMillis := now.UTC().UnixMilli()
	earliest := int64(0)
	for _, item := range items {
		revealAt, ok := spoilerRevealAt(item, nowMillis)
		if ok && (earliest == 0 || revealAt < earliest) {
			earliest = revealAt
		}
	}
	if earliest > 0 {
		cachehint.ValidUntil(ctx, time.UnixMilli(earliest))
	}
	return filtered
}

// spoilerRevealAt returns when a spoiler item stops being one: the latest of
// its future timestamps, since IsSpoilerItem holds while any is ahead.
func spoilerRevealAt(item map[string]any, nowMillis int64) (int64, bool) {
	if !IsSpoilerItem(item, nowMillis) {
		return 0, false
	}
	latest := int64(0)
	for _, key := range spoilerTimestampKeys {
		value, exists := item[key]
		if !exists {
			continue
		}
		if timestamp, ok := ParseTimestampMillis(value); ok && timestamp > nowMillis && timestamp > latest {
			latest = timestamp
		}
	}
	return latest, latest > 0
}

// spoilerTimestampKeys are the fields IsSpoilerItem compares with now.
var spoilerTimestampKeys = []string{"releaseAt", "releastAt", "publishedAt", "startAt"}

func IsSpoilerItem(item map[string]any, nowMillis int64) bool {
	if item == nil {
		return false
	}

	for _, key := range spoilerTimestampKeys {
		value, exists := item[key]
		if !exists {
			continue
		}

		timestamp, ok := ParseTimestampMillis(value)
		if ok && timestamp > nowMillis {
			return true
		}
	}

	return false
}

func ParseTimestampMillis(value any) (int64, bool) {
	toMillis := func(timestamp int64) int64 {
		return normalizeEpochTimestamp(timestamp)
	}

	switch typed := value.(type) {
	case int64:
		return toMillis(typed), true
	case int:
		return toMillis(int64(typed)), true
	case int32:
		return toMillis(int64(typed)), true
	case float64:
		return toMillis(int64(typed)), true
	case float32:
		return toMillis(int64(typed)), true
	case uint64:
		if typed > uint64(9223372036854775807) {
			return 0, false
		}
		return toMillis(int64(typed)), true
	case uint:
		return toMillis(int64(typed)), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return toMillis(parsed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}

		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err == nil {
			return toMillis(parsed), true
		}

		parsedFloat, parseFloatErr := strconv.ParseFloat(trimmed, 64)
		if parseFloatErr == nil {
			return toMillis(int64(parsedFloat)), true
		}

		parsedTime, parseTimeErr := time.Parse(time.RFC3339Nano, trimmed)
		if parseTimeErr != nil {
			parsedTime, parseTimeErr = time.Parse(time.RFC3339, trimmed)
			if parseTimeErr != nil {
				for _, layout := range []string{
					"2006-01-02 15:04:05",
					"2006-01-02 15:04:05Z07:00",
					"2006-01-02 15:04:05 -0700",
				} {
					parsedTime, parseTimeErr = time.Parse(layout, trimmed)
					if parseTimeErr == nil {
						return parsedTime.UnixMilli(), true
					}
				}

				return 0, false
			}
		}

		return parsedTime.UnixMilli(), true
	default:
		return 0, false
	}
}

func normalizeEpochTimestamp(timestamp int64) int64 {
	abs := timestamp
	if abs < 0 {
		abs = -abs
	}

	switch {
	case abs == 0:
		return 0
	case abs < 100_000_000_000:
		return timestamp * 1000
	case abs >= 100_000_000_000_000_000:
		return timestamp / 1_000_000
	case abs >= 100_000_000_000_000:
		return timestamp / 1000
	default:
		return timestamp
	}
}
