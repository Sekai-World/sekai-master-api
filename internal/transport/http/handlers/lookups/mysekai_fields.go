package lookups

import (
	"slices"
	"strconv"
	"strings"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/transport/http/handlers/shared"
)

// fieldSource reads one record's fields: a decoded record, or a row of the
// entity's list projection.
type fieldSource interface {
	value(key string) (any, bool)
}

type recordSource map[string]any

func (record recordSource) value(key string) (any, bool) {
	value, ok := record[key]
	return value, ok
}

type projectionRowSource struct {
	projection *masterdata.Projection
	row        int
}

func (source projectionRowSource) value(key string) (any, bool) {
	return source.projection.Value(key, source.row)
}

func sourceInt64(source fieldSource, key string) (int64, bool) {
	value, ok := source.value(key)
	if !ok || value == nil {
		return 0, false
	}
	return lookupInt64(value)
}

func sourceOptionalInt64(source fieldSource, key string) *int64 {
	if number, ok := sourceInt64(source, key); ok {
		return &number
	}
	return nil
}

func sourceOptionalFloat64(source fieldSource, key string) *float64 {
	value, _ := source.value(key)
	return lookupOptionalFloat64(value)
}

func sourceString(source fieldSource, key string) string {
	value, _ := source.value(key)
	text, _ := value.(string)
	return text
}

// sourceOptionalString returns a non-blank string field, or nil.
func sourceOptionalString(source fieldSource, key string) *string {
	text := sourceString(source, key)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return &text
}

func sourceOptionalBool(source fieldSource, key string) *bool {
	value, _ := source.value(key)
	flag, ok := value.(bool)
	if !ok {
		return nil
	}
	return &flag
}

// sourceID returns the record's positive ID.
func sourceID(source fieldSource) (int64, bool) {
	id, ok := sourceInt64(source, "id")
	return id, ok && id > 0
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func formatIDs(ids []int64) []string {
	formatted := make([]string, 0, len(ids))
	for _, id := range ids {
		formatted = append(formatted, formatID(id))
	}
	return formatted
}

// appendUniqueID appends id unless ids already holds it.
func appendUniqueID(ids []int64, id int64) []int64 {
	if slices.Contains(ids, id) {
		return ids
	}
	return append(ids, id)
}

// sortRecordsBySeq orders records by their seq field, then ID, keeping records
// without a seq last.
func sortRecordsBySeq(records []map[string]any) {
	slices.SortStableFunc(records, func(left map[string]any, right map[string]any) int {
		leftSeq, leftOK := lookupInt64(left["seq"])
		rightSeq, rightOK := lookupInt64(right["seq"])
		switch {
		case leftOK && rightOK && leftSeq != rightSeq:
			if leftSeq < rightSeq {
				return -1
			}
			return 1
		case leftOK != rightOK:
			if leftOK {
				return -1
			}
			return 1
		}
		return shared.CompareIDValues(left["id"], right["id"])
	})
}

// parseCommaSeparatedValues splits a comma-separated query value into its
// trimmed, non-empty parts.
func parseCommaSeparatedValues(raw string) []string {
	values := []string{}
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return values
}

// matchesName reports whether any of the fields contains the normalized name
// filter.
func matchesName(source fieldSource, nameFilter string, keys ...string) bool {
	if nameFilter == "" {
		return true
	}
	for _, key := range keys {
		value, _ := source.value(key)
		if strings.Contains(shared.NormalizeComparableText(value), nameFilter) {
			return true
		}
	}
	return false
}
