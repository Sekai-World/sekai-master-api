package masterdata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Record keys identify one record of an entity in storage:
//
//   - the record's `id`, rendered by FormatRecordID (decimal for numbers);
//   - for the entities listed in CompositeKeyEntities, a composite key built
//     from several fields by EncodeCompositeKey;
//   - otherwise, or when those fields are missing, an AutoKeyPrefix key
//     derived from the record's JSON body.

// CompositeKeyPrefix starts every composite record key.
const CompositeKeyPrefix = "composite:v1:"

// AutoKeyPrefix starts the key of a record that has no usable id.
const AutoKeyPrefix = "auto:"

var compositeKeyEntities = [...]string{
	"resourceboxes",
	"resourceboxdetails",
	"charactermissionv2parametergroups",
}

// CompositeKeyEntities returns the entities whose records are keyed by
// several fields rather than by `id`.
func CompositeKeyEntities() []string {
	return append([]string(nil), compositeKeyEntities[:]...)
}

// CompositeKeyFields returns the fields that key a composite-key entity's
// records, in key order, or nil for an entity keyed by `id`.
func CompositeKeyFields(entity string) []string {
	switch normalizeEntity(entity) {
	case "resourceboxes":
		return []string{"id", "resourceBoxPurpose"}
	case "resourceboxdetails":
		return []string{"resourceBoxId", "resourceBoxPurpose", "seq"}
	case "charactermissionv2parametergroups":
		return []string{"id", "seq"}
	default:
		return nil
	}
}

// UsesCompositeKey reports whether entity's records are keyed by several
// fields.
func UsesCompositeKey(entity string) bool {
	return len(CompositeKeyFields(entity)) > 0
}

// CompositeRecordKey returns the composite key of a decoded record of entity.
// It reports false when entity is not a composite-key entity or the record
// lacks a usable key field.
func CompositeRecordKey(entity string, record map[string]any) (string, bool) {
	fields := CompositeKeyFields(entity)
	if len(fields) == 0 || record == nil {
		return "", false
	}

	values := make([]string, 0, len(fields))
	for _, field := range fields {
		value, exists := record[field]
		if !exists {
			return "", false
		}
		part, ok := CanonicalKeyPart(value)
		if !ok {
			return "", false
		}
		values = append(values, part)
	}

	return EncodeCompositeKey(entity, fields, values), true
}

// CompositeRecordKeyFromRaw is CompositeRecordKey for a record's JSON body.
func CompositeRecordKeyFromRaw(entity string, body []byte) (string, bool) {
	fields := CompositeKeyFields(entity)
	if len(fields) == 0 || len(body) == 0 {
		return "", false
	}

	var record map[string]json.RawMessage
	if err := json.Unmarshal(body, &record); err != nil {
		return "", false
	}

	values := make([]string, 0, len(fields))
	for _, field := range fields {
		rawValue, exists := record[field]
		if !exists || len(rawValue) == 0 {
			return "", false
		}

		decoder := json.NewDecoder(bytes.NewReader(rawValue))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return "", false
		}
		part, ok := CanonicalKeyPart(value)
		if !ok {
			return "", false
		}
		values = append(values, part)
	}

	return EncodeCompositeKey(entity, fields, values), true
}

// EncodeCompositeKey builds a composite key from canonical field values given
// in the order of fields. Every part is length-prefixed, so values containing
// separators cannot collide.
func EncodeCompositeKey(entity string, fields []string, values []string) string {
	var builder strings.Builder
	builder.WriteString(CompositeKeyPrefix)
	appendCompositeKeyPart(&builder, normalizeEntity(entity))
	for index, field := range fields {
		appendCompositeKeyPart(&builder, field)
		appendCompositeKeyPart(&builder, values[index])
	}
	return builder.String()
}

func appendCompositeKeyPart(builder *strings.Builder, value string) {
	builder.WriteString(strconv.Itoa(len(value)))
	builder.WriteByte(':')
	builder.WriteString(value)
}

// RecordID returns a decoded record's `id` as lookups spell it, or "" when it
// has none.
func RecordID(record map[string]any) string {
	idValue, ok := record["id"]
	if !ok || idValue == nil {
		return ""
	}

	return FormatRecordID(idValue)
}

// FormatRecordID renders an id the way lookups spell it. Numbers are decimal:
// "%v" on a decoded float64 gives "1.010201e+06" for ids of a million or more.
func FormatRecordID(value any) string {
	if id, ok := CanonicalKeyPart(value); ok {
		return id
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

// RecordKey returns the key of a decoded record keyed by `id`: its id, or an
// auto key derived from body when it has none. It returns "" for a record
// with neither.
func RecordKey(record map[string]any, body []byte) string {
	if id := RecordID(record); id != "" {
		return id
	}
	return AutoRecordKey(body)
}

// RecordKeyFromRaw is RecordKey for a record's JSON body.
func RecordKeyFromRaw(body []byte) string {
	var fields struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &fields); err == nil && len(fields.ID) > 0 && string(fields.ID) != "null" {
		decoder := json.NewDecoder(bytes.NewReader(fields.ID))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) == nil {
			if id := FormatRecordID(value); id != "" {
				return id
			}
		}
	}
	return AutoRecordKey(body)
}

// AutoRecordKey derives the key of a record without a usable id from its JSON
// body, or returns "" for an empty body.
func AutoRecordKey(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	sum := sha256.Sum256(body)
	return AutoKeyPrefix + hex.EncodeToString(sum[:])
}

// maxDecimalSortKeyDigits is the width decimal record keys are zero-padded to
// in block sort keys; it holds every uint64.
const maxDecimalSortKeyDigits = 20

// BlockSortKey maps a record key to the key its record block is sorted and
// located by in the PostgreSQL store:
//
//   - a canonical decimal key (digits only, no leading zero unless it is "0")
//     of up to 20 digits becomes "0" followed by the key zero-padded to 20
//     digits, so numeric keys sort numerically;
//   - every other key becomes "1" followed by the key.
//
// The mapping is injective, and the store compares sort keys byte for byte
// (COLLATE "C"), exactly as Go compares strings. Numeric keys therefore sort
// before all other keys.
func BlockSortKey(recordKey string) string {
	if isCanonicalDecimal(recordKey) && len(recordKey) <= maxDecimalSortKeyDigits {
		return "0" + strings.Repeat("0", maxDecimalSortKeyDigits-len(recordKey)) + recordKey
	}
	return "1" + recordKey
}

func isCanonicalDecimal(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}
