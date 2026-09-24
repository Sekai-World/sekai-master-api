package lookups

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

func sharedIDRewardBoxes() []map[string]any {
	box := func(id int, purpose string, resourceType string, quantity int) map[string]any {
		return map[string]any{
			"id":                 id,
			"resourceBoxPurpose": purpose,
			"resourceBoxType":    "expand",
			"details": []any{map[string]any{
				"resourceBoxPurpose": purpose,
				"resourceBoxId":      id,
				"seq":                1,
				"resourceType":       resourceType,
				"resourceQuantity":   quantity,
			}},
		}
	}
	return []map[string]any{
		box(1, "ad_reward", "coin", 1000),
		box(1, "story_mission", "gacha_ticket", 1),
		box(5, "ad_reward", "coin", 500),
		box(5, "mission_reward", "jewel", 50),
		box(7, "ad_reward", "coin", 100),
		box(7, "bonds_reward", "coin", 200),
	}
}

func TestMissionRewardsUseFamilyPurposeWhenBoxIDsAreShared(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					storyMissionsEntity: {
						{"id": 1, "requirement": 10, "resourceBoxId": 1},
					},
					normalMissionsEntity: {
						{
							"id":          1,
							"seq":         1,
							"requirement": 3,
							"sentence":    "Clear 3 lives",
							"rewards": []any{
								map[string]any{"id": 11, "missionType": "normal_mission", "missionId": 1, "seq": 1, "resourceBoxId": 5},
								map[string]any{"id": 12, "missionType": "normal_mission", "missionId": 1, "seq": 2, "resourceBoxId": 5, "resourceBoxPurpose": "ad_reward"},
								map[string]any{"id": 13, "missionType": "normal_mission", "missionId": 1, "seq": 3, "resourceBoxId": 7},
							},
						},
					},
					resourceBoxesEntity: sharedIDRewardBoxes(),
				},
			},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	story := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=storyMissions")
	storyItems, _ := decodeMissionList(t, story.Body.Bytes())
	storyReward := storyItems[0]["rewards"].([]any)[0].(map[string]any)
	if storyReward["status"] != missionRewardResolvedStatus || storyReward["resourceBoxPurpose"] != "story_mission" {
		t.Fatalf("expected the story_mission box for a story mission, got %#v", storyReward)
	}

	normal := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=normalMissions")
	normalItems, _ := decodeMissionList(t, normal.Body.Bytes())
	rewards := normalItems[0]["rewards"].([]any)
	byID := map[float64]map[string]any{}
	for _, reward := range rewards {
		entry := reward.(map[string]any)
		byID[entry["id"].(float64)] = entry
	}
	if byID[11]["status"] != missionRewardResolvedStatus || byID[11]["resourceBoxPurpose"] != "mission_reward" {
		t.Fatalf("expected the mission_reward box for a normal mission reward, got %#v", byID[11])
	}
	if byID[12]["status"] != missionRewardResolvedStatus || byID[12]["resourceBoxPurpose"] != "ad_reward" {
		t.Fatalf("expected an explicit purpose to win over the family default, got %#v", byID[12])
	}
	if byID[13]["status"] != missionRewardUnresolvedStatus {
		t.Fatalf("expected a shared ID without a mission_reward box to stay unresolved, got %#v", byID[13])
	}
}

// TW/KR/CN keep resource-box contents in resourceboxdetails and store normal
// mission rewards as bare box-ID groups.
func TestRegionalMissionAndRankRewardsJoinSeparateDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	detail := func(id int, purpose string, resourceType string, quantity int) map[string]any {
		return map[string]any{
			"resourceBoxId":      id,
			"resourceBoxPurpose": purpose,
			"seq":                1,
			"resourceType":       resourceType,
			"resourceQuantity":   quantity,
		}
	}
	cache := &missionTrackingCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"tw": {
					normalMissionsEntity: {
						{"id": 1, "seq": 1, "requirement": 1, "sentence": "Make a friend", "rewards": []any{[]any{5}}},
					},
					characterRanksEntity: {
						{"id": 2, "characterId": 1, "characterRank": 2, "rewardResourceBoxIds": []any{1002}},
					},
					resourceBoxesEntity: {
						{"id": 5, "resourceBoxPurpose": "ad_reward", "resourceBoxType": "expand"},
						{"id": 5, "resourceBoxPurpose": "mission_reward", "resourceBoxType": "expand"},
						{"id": 1002, "resourceBoxPurpose": "character_rank_reward", "resourceBoxType": "expand"},
					},
					resourceBoxDetailsEntity: {
						detail(5, "ad_reward", "coin", 500),
						detail(5, "mission_reward", "jewel", 50),
						detail(1002, "character_rank_reward", "material", 2),
					},
				},
			},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	normal := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/tw/list?family=normalMissions")
	normalItems, _ := decodeMissionList(t, normal.Body.Bytes())
	reward := normalItems[0]["rewards"].([]any)[0].(map[string]any)
	if reward["status"] != missionRewardResolvedStatus || reward["resourceBoxPurpose"] != "mission_reward" {
		t.Fatalf("expected a bare-ID normal mission reward to resolve its mission_reward box, got %#v", reward)
	}
	details := reward["resourceBox"].(map[string]any)["details"].([]any)
	if len(details) != 1 || details[0].(map[string]any)["resourceType"] != "jewel" {
		t.Fatalf("expected mission_reward details joined from resourceboxdetails, got %#v", details)
	}

	ranks := serveLookupRequest(t, router, http.MethodGet, "/api/v1/characterRanks/tw/list?character_id=1")
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(ranks.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode character ranks: %v", err)
	}
	boxes := body.Items[0]["rewardResourceBoxes"].([]any)
	rankDetails := boxes[0].(map[string]any)["details"].([]any)
	if len(rankDetails) != 1 || rankDetails[0].(map[string]any)["resourceType"] != "material" {
		t.Fatalf("expected character rank details joined from resourceboxdetails, got %#v", boxes)
	}
}
