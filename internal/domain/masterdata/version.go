package masterdata

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// IsCompleteVersionPayload reports whether a versions.json payload carries at
// least one usable version field. It encodes the same contract enforced by the
// public /versions response so readiness checks can require usable version
// metadata instead of merely a present-but-empty cache key.
func IsCompleteVersionPayload(payload map[string]any) bool {
	if _, ok := VersionStringValue(payload, "appVersion"); ok {
		return true
	}
	if _, ok := VersionStringValue(payload, "assetVersion"); ok {
		return true
	}
	if _, ok := VersionStringValue(payload, "dataVersion"); ok {
		return true
	}
	if _, ok := VersionSmallIntValue(payload, "cdnVersion"); ok {
		return true
	}
	return false
}

// VersionStringValue extracts a non-empty trimmed string version field,
// accepting both plain strings and fmt.Stringer values.
func VersionStringValue(payload map[string]any, key string) (string, bool) {
	value, ok := payload[key]
	if !ok {
		return "", false
	}

	switch typedValue := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typedValue)
		if trimmed == "" {
			return "", false
		}
		return trimmed, true
	case fmt.Stringer:
		trimmed := strings.TrimSpace(typedValue.String())
		if trimmed == "" {
			return "", false
		}
		return trimmed, true
	default:
		return "", false
	}
}

// VersionSmallIntValue extracts a bounded small-int version field (0-999), used
// for cdnVersion.
func VersionSmallIntValue(payload map[string]any, key string) (int, bool) {
	value, ok := payload[key]
	if !ok {
		return 0, false
	}

	parsed, ok := ParseSmallInt(value)
	if !ok {
		return 0, false
	}

	if parsed < 0 || parsed > 999 {
		return 0, false
	}

	return parsed, true
}

// ParseSmallInt normalizes many numeric representations (ints, floats without a
// fractional part, and numeric strings) into a small int. It rejects values
// that overflow int or carry a fractional component.
func ParseSmallInt(value any) (int, bool) {
	switch typedValue := value.(type) {
	case int:
		return typedValue, true
	case int8:
		return int(typedValue), true
	case int16:
		return int(typedValue), true
	case int32:
		return int(typedValue), true
	case int64:
		converted := int(typedValue)
		if int64(converted) != typedValue {
			return 0, false
		}
		return converted, true
	case uint:
		converted := int(typedValue)
		if uint(converted) != typedValue {
			return 0, false
		}
		return converted, true
	case uint8:
		return int(typedValue), true
	case uint16:
		return int(typedValue), true
	case uint32:
		converted := int(typedValue)
		if uint32(converted) != typedValue {
			return 0, false
		}
		return converted, true
	case uint64:
		converted := int(typedValue)
		if uint64(converted) != typedValue {
			return 0, false
		}
		return converted, true
	case float32:
		asFloat := float64(typedValue)
		if asFloat != math.Trunc(asFloat) {
			return 0, false
		}
		return int(asFloat), true
	case float64:
		if typedValue != math.Trunc(typedValue) {
			return 0, false
		}
		return int(typedValue), true
	case string:
		trimmed := strings.TrimSpace(typedValue)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
