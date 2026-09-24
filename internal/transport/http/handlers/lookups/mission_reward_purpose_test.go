package lookups

import (
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
