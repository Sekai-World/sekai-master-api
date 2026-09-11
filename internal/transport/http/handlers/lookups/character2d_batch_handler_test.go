package lookups

import (
	"testing"
)

func TestCharacter2DsBatchReturnsOrderedMappingsAndMissingIDs(t *testing.T) {
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{"jp": {"character2ds": {
			"1180": {"id": 1180, "characterType": "game_character", "isNextGrade": false, "characterId": 13, "unit": "theme_park", "isEnabledFlipDisplay": true, "assetName": "13tsukasa"},
			"1197": {"id": 1197, "characterType": "game_character", "isNextGrade": false, "characterId": 26, "unit": "piapro", "isEnabledFlipDisplay": true, "assetName": "26kaito"},
		}}},
		hasRecords: map[string]map[string]bool{"jp": {"character2ds": true}},
	}
	body := serveCharacterBatchRequest(t, "/character2ds/:region/batch", newReadyLookupHandler(cache).Character2DsBatch, "/character2ds/jp/batch?ids=1197,1180,1197,9999")
	assertCharacterBatchItems(t, body, []int64{1197, 1180}, []int64{26, 13}, []int64{9999})
	if body.Items[0]["characterType"] != "game_character" || body.Items[0]["unit"] != "piapro" || body.Items[0]["assetName"] != "26kaito" || body.Items[0]["isNextGrade"] != false || body.Items[0]["isEnabledFlipDisplay"] != true {
		t.Fatalf("unexpected first item: %#v", body.Items[0])
	}
	if body.Items[1]["characterType"] != "game_character" || body.Items[1]["unit"] != "theme_park" || body.Items[1]["assetName"] != "13tsukasa" {
		t.Fatalf("unexpected second item: %#v", body.Items[1])
	}
}

func TestCharacter2DsBatchOmitsFieldsAbsentFromTCRecords(t *testing.T) {
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{"en": {"character2ds": {
			"32": {"id": 32, "characterType": "mob", "characterId": 1, "unit": "none"},
		}}},
		hasRecords: map[string]map[string]bool{"en": {"character2ds": true}},
	}
	body := serveCharacterBatchRequest(t, "/character2ds/:region/batch", newReadyLookupHandler(cache).Character2DsBatch, "/character2ds/en/batch?ids=32")
	if len(body.Items) != 1 {
		t.Fatalf("expected one item, got %#v", body.Items)
	}
	item := body.Items[0]
	if item["id"] != float64(32) || item["gameCharacterId"] != float64(1) || item["characterType"] != "mob" || item["unit"] != "none" {
		t.Fatalf("unexpected item: %#v", item)
	}
	for _, field := range []string{"assetName", "isNextGrade", "isEnabledFlipDisplay"} {
		if _, ok := item[field]; ok {
			t.Fatalf("expected %s to be omitted: %#v", field, item)
		}
	}
}

func TestCharacter2DsBatchResolvesTypeSpecificDisplayNames(t *testing.T) {
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{"jp": {
			"character2ds": {
				"10": {"id": 10, "characterType": "game_character", "characterId": 7},
				"11": {"id": 11, "characterType": "mob", "characterId": 7},
				"12": {"id": 12, "characterType": "sub_game_character", "characterId": 8},
				"13": {"id": 13, "characterType": "game_character", "characterId": 9},
				"14": {"id": 14, "characterType": "", "characterId": 10},
			},
			"gamecharacters": {
				"7":  {"id": 7, "firstName": "Game", "givenName": "Character"},
				"10": {"id": 10, "firstName": "Legacy", "givenName": ""},
			},
			"mobcharacters": {
				"7": {"id": 7, "name": "Mob Character"},
				"8": {"id": 8, "name": "Wrong Mob Character"},
			},
			"subgamecharacters": {
				"8": {"id": 8, "name": "Sub Character"},
			},
		}},
		hasRecords: map[string]map[string]bool{"jp": {"character2ds": true}},
	}
	body := serveCharacterBatchRequest(t, "/character2ds/:region/batch", newReadyLookupHandler(cache).Character2DsBatch, "/character2ds/jp/batch?ids=10,11,12,13,14")
	if len(body.Items) != 5 {
		t.Fatalf("expected five items, got %#v", body.Items)
	}

	expectedIDs := []float64{10, 11, 12, 13, 14}
	expectedNames := []string{"Game Character", "Mob Character", "Sub Character", "", "Legacy"}
	for index, item := range body.Items {
		if item["id"] != expectedIDs[index] {
			t.Fatalf("expected item %d to have id %v, got %#v", index, expectedIDs[index], item)
		}
		if expectedNames[index] == "" {
			if _, ok := item["displayName"]; ok {
				t.Fatalf("expected displayName to be omitted for item %#v", item)
			}
			continue
		}
		if item["displayName"] != expectedNames[index] {
			t.Fatalf("expected displayName=%q for item %#v", expectedNames[index], item)
		}
	}
}

func TestCharacter2DsBatchRejectsInvalidIDs(t *testing.T) {
	assertCharacterBatchRejectsInvalidIDs(t, "/character2ds/:region/batch", newReadyLookupHandler(&fakeLookupCache{}).Character2DsBatch, "/character2ds/jp/batch")
}
