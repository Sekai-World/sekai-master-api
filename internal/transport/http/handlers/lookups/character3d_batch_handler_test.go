package lookups

import (
	"testing"
)

func TestCharacter3DsBatchReturnsOrderedMappingsAndMissingIDs(t *testing.T) {
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{"jp": {"character3ds": {
			"1180": {"id": 1180, "characterId": 13, "unit": "theme_park", "name": "Tsukasa"},
			"1197": {"id": 1197, "characterId": 26, "unit": "piapro", "name": "KAITO"},
		}}},
		hasRecords: map[string]map[string]bool{"jp": {"character3ds": true}},
	}
	body := serveCharacterBatchRequest(t, "/character3ds/:region/batch", newReadyLookupHandler(cache).Character3DsBatch, "/character3ds/jp/batch?ids=1197,1180,1197,9999")
	assertCharacterBatchItems(t, body, []int64{1197, 1180}, []int64{26, 13}, []int64{9999})
}

func TestCharacter3DsBatchRejectsInvalidIDs(t *testing.T) {
	assertCharacterBatchRejectsInvalidIDs(t, "/character3ds/:region/batch", newReadyLookupHandler(&fakeLookupCache{}).Character3DsBatch, "/character3ds/jp/batch")
}
