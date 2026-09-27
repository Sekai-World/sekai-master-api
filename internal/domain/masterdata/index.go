package masterdata

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

// indexLayoutVersion tags stored index definitions. Bump it when the way index
// keys are derived changes, so the next sync rebuilds every entity's indexes.
const indexLayoutVersion = "1"

// entityIndexes lists the relation indexes built for each entity at sync time.
// An index maps the canonical value of its fields to the storage keys of the
// records carrying that value, in stored order. Several fields are joined with
// ","; a field path steps into objects with "." and indexes every element of
// an array it reaches.
var entityIndexes = map[string][]string{
	"gachas":             {"gachaPickups.cardId"},
	"levels":             {"levelType"},
	"resourceboxdetails": {"resourceBoxId,resourceBoxPurpose"},
	"resourceboxes":      {"id"},
}

// EntityIndexes returns the relation indexes defined for entity.
func EntityIndexes(entity string) []string {
	return entityIndexes[normalizeEntity(entity)]
}

// HasIndex reports whether index is defined for entity.
func HasIndex(entity string, index string) bool {
	for _, defined := range EntityIndexes(entity) {
		if defined == index {
			return true
		}
	}
	return false
}

// IndexVersion identifies entity's index definitions. Storage records it next
// to the built indexes; a mismatch means the indexes must be rebuilt. Entities
// without indexes have an empty version.
func IndexVersion(entity string) string {
	indexes := EntityIndexes(entity)
	if len(indexes) == 0 {
		return ""
	}
	return indexLayoutVersion + ":" + strings.Join(indexes, ";")
}

// IndexNamesFromVersion returns the index names recorded in a stored
// IndexVersion value, so storage can remove indexes that are no longer defined.
func IndexNamesFromVersion(version string) []string {
	_, names, found := strings.Cut(version, ":")
	if !found || names == "" {
		return nil
	}
	return strings.Split(names, ";")
}

// IndexKeys returns the distinct index keys record contributes to index, in
// the order they appear.
func IndexKeys(record map[string]any, index string) []string {
	fields := strings.Split(index, ",")
	keys := []string{""}
	for _, field := range fields {
		parts := make([]string, 0, 1)
		for _, value := range pathValues(record, strings.Split(field, ".")) {
			if part, ok := CanonicalKeyPart(value); ok {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			return nil
		}

		next := make([]string, 0, len(keys)*len(parts))
		for _, prefix := range keys {
			for _, part := range parts {
				next = append(next, appendIndexKeyPart(prefix, part, len(fields)))
			}
		}
		keys = next
	}

	return distinct(keys)
}

// IndexLookupKey returns the index key for values given in the index's field
// order. It reports false when a value cannot be part of a key.
func IndexLookupKey(values ...any) (string, bool) {
	key := ""
	for _, value := range values {
		part, ok := CanonicalKeyPart(value)
		if !ok {
			return "", false
		}
		key = appendIndexKeyPart(key, part, len(values))
	}
	return key, len(values) > 0
}

// appendIndexKeyPart keeps a single-field key readable and length-prefixes the
// parts of a multi-field key so values containing separators cannot collide.
func appendIndexKeyPart(key string, part string, count int) string {
	if count == 1 {
		return part
	}
	return key + strconv.Itoa(len(part)) + ":" + part
}

// CanonicalKeyPart renders a key value the way storage keys spell it: trimmed
// strings, decimal numbers, and booleans. Other values cannot be key parts.
func CanonicalKeyPart(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		part := strings.TrimSpace(typed)
		return part, part != ""
	case json.Number:
		return canonicalNumber(typed.String())
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return "", false
		}
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case float32:
		value := float64(typed)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", false
		}
		return strconv.FormatFloat(value, 'f', -1, 32), true
	case int:
		return strconv.Itoa(typed), true
	case int8:
		return strconv.FormatInt(int64(typed), 10), true
	case int16:
		return strconv.FormatInt(int64(typed), 10), true
	case int32:
		return strconv.FormatInt(int64(typed), 10), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case uint:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint8:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint16:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint32:
		return strconv.FormatUint(uint64(typed), 10), true
	case uint64:
		return strconv.FormatUint(typed, 10), true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
}

func canonicalNumber(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if integer, err := strconv.ParseInt(value, 10, 64); err == nil {
		return strconv.FormatInt(integer, 10), true
	}
	if integer, err := strconv.ParseUint(value, 10, 64); err == nil {
		return strconv.FormatUint(integer, 10), true
	}

	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return "", false
	}
	return strconv.FormatFloat(number, 'f', -1, 64), true
}

// pathValues follows path through objects, fanning out over arrays, and
// returns the scalar values it reaches.
func pathValues(record map[string]any, path []string) []any {
	current := []any{record}
	for _, segment := range path {
		next := make([]any, 0, len(current))
		for _, value := range current {
			for _, object := range objectsOf(value) {
				if child, ok := object[segment]; ok && child != nil {
					next = append(next, child)
				}
			}
		}
		current = next
	}

	values := make([]any, 0, len(current))
	for _, value := range current {
		if items, ok := value.([]any); ok {
			values = append(values, items...)
			continue
		}
		values = append(values, value)
	}
	return values
}

func objectsOf(value any) []map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		objects := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				objects = append(objects, object)
			}
		}
		return objects
	default:
		return nil
	}
}

func distinct(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeEntity(entity string) string {
	return strings.ToLower(strings.TrimSpace(entity))
}

// IndexedEntities returns the entities that define relation indexes.
func IndexedEntities() []string {
	entities := make([]string, 0, len(entityIndexes))
	for entity := range entityIndexes {
		entities = append(entities, entity)
	}
	sort.Strings(entities)
	return entities
}
