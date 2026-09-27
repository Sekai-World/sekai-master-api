package testutil

import "sekai-master-api/internal/transport/http/handlers/shared"

// MatchCompositeKeys answers a composite-key read from a fake cache's records:
// for each key it returns the first record whose key fields equal the key's,
// comparing values the way handlers normalize IDs, or nil when none matches.
func MatchCompositeKeys(records []map[string]any, keys []map[string]any) []map[string]any {
	matches := make([]map[string]any, len(keys))
	for index, key := range keys {
		for _, record := range records {
			if recordMatchesKey(record, key) {
				matches[index] = record
				break
			}
		}
	}
	return matches
}

func recordMatchesKey(record map[string]any, key map[string]any) bool {
	if record == nil || len(key) == 0 {
		return false
	}
	for field, want := range key {
		got, ok := record[field]
		if !ok || shared.NormalizeAnyID(got) != shared.NormalizeAnyID(want) {
			return false
		}
	}
	return true
}
