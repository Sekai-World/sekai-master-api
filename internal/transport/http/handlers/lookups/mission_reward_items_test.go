package lookups

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMissionAndRankRewardDetailsNameTheirItems(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					storyMissionsEntity: {
						{"id": 1, "requirement": 10, "resourceBoxId": 1},
					},
					resourceBoxesEntity: {
						{
							"id": 1, "resourceBoxPurpose": "story_mission", "resourceBoxType": "expand",
							"details": []any{map[string]any{
								"resourceBoxPurpose": "story_mission", "resourceBoxId": 1, "seq": 1,
								"resourceType": "gacha_ticket", "resourceId": 17, "resourceQuantity": 1,
							}},
						},
					},
					"gachatickets": {
						{"id": 17, "name": "ミッションガチャチケット", "assetbundleName": "mission_gacha_ticket"},
					},
				},
				// Regional boxes keep their contents in resourceboxdetails.
				"tw": {
					normalMissionsEntity: {
						{"id": 1, "seq": 1, "requirement": 1, "sentence": "Clear a live", "rewards": []any{[]any{5}}},
					},
					characterRanksEntity: {
						{"id": 2, "characterId": 1, "characterRank": 2, "rewardResourceBoxIds": []any{1002}},
					},
					resourceBoxesEntity: {
						{"id": 5, "resourceBoxPurpose": "mission_reward", "resourceBoxType": "expand"},
						{"id": 1002, "resourceBoxPurpose": "character_rank_reward", "resourceBoxType": "expand"},
					},
					resourceBoxDetailsEntity: {
						{"resourceBoxId": 5, "resourceBoxPurpose": "mission_reward", "seq": 1, "resourceType": "material", "resourceId": 13, "resourceQuantity": 10},
						{"resourceBoxId": 5, "resourceBoxPurpose": "mission_reward", "seq": 2, "resourceType": "jewel", "resourceQuantity": 50},
						{"resourceBoxId": 1002, "resourceBoxPurpose": "character_rank_reward", "seq": 1, "resourceType": "skill_practice_ticket", "resourceId": 2, "resourceQuantity": 3},
					},
					"materials": {
						{"id": 13, "name": "音樂卡"},
					},
					"skillpracticetickets": {
						{"id": 2, "name": "技能升級譜（中級）"},
					},
				},
			},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	story := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=storyMissions")
	storyItems, _ := decodeMissionList(t, story.Body.Bytes())
	ticket := storyItems[0]["rewards"].([]any)[0].(map[string]any)["resourceBox"].(map[string]any)["details"].([]any)[0].(map[string]any)
	if ticket["resourceName"] != "ミッションガチャチケット" || ticket["resourceAssetbundleName"] != "mission_gacha_ticket" {
		t.Fatalf("expected the gacha ticket name and asset bundle, got %#v", ticket)
	}

	normal := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/tw/list?family=normalMissions")
	normalItems, _ := decodeMissionList(t, normal.Body.Bytes())
	details := normalItems[0]["rewards"].([]any)[0].(map[string]any)["resourceBox"].(map[string]any)["details"].([]any)
	material := details[0].(map[string]any)
	if material["resourceName"] != "音樂卡" {
		t.Fatalf("expected the material name joined from materials, got %#v", material)
	}
	if _, ok := material["resourceAssetbundleName"]; ok {
		t.Fatalf("expected no asset bundle for a material, got %#v", material)
	}
	jewel := details[1].(map[string]any)
	if _, ok := jewel["resourceName"]; ok {
		t.Fatalf("expected no item lookup for a currency, got %#v", jewel)
	}

	ranks := serveLookupRequest(t, router, http.MethodGet, "/api/v1/characterRanks/tw/list?character_id=1")
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(ranks.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode character ranks: %v", err)
	}
	rankDetail := body.Items[0]["rewardResourceBoxes"].([]any)[0].(map[string]any)["details"].([]any)[0].(map[string]any)
	if rankDetail["resourceName"] != "技能升級譜（中級）" {
		t.Fatalf("expected the skill practice ticket name on character rank rewards, got %#v", rankDetail)
	}
}
