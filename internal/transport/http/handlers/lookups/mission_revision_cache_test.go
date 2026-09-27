package lookups

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

type revisionTrackingMissionCache struct {
	*missionTrackingCache
	revisions map[string]map[string]string
}

func (cache *revisionTrackingMissionCache) EntityRevision(_ context.Context, region string, entity string) (string, error) {
	return cache.revisions[region][entity], nil
}

func TestMissionRewardLookupsReuseResourceBoxesWhileRevisionIsUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &revisionTrackingMissionCache{
		missionTrackingCache: &missionTrackingCache{
			fakeLookupCache: &fakeLookupCache{
				listByEntity: map[string]map[string][]map[string]any{
					"jp": {
						storyMissionsEntity: {
							{"id": 5, "seq": 1, "requirement": 50, "resourceBoxId": 50},
						},
						characterRanksEntity: {
							{"id": 1, "characterId": 1, "characterRank": 1, "rewardResourceBoxIds": []any{1001}},
						},
						resourceBoxesEntity: {
							{"id": 50, "resourceBoxPurpose": "story_mission"},
							{"id": 1001, "resourceBoxPurpose": "character_rank_reward", "details": []any{map[string]any{"resourceType": "material", "resourceQuantity": 1}}},
						},
						resourceBoxDetailsEntity: {
							{"resourceBoxId": 50, "resourceBoxPurpose": "story_mission", "seq": 1, "resourceType": "jewel"},
						},
					},
				},
			},
		},
		revisions: map[string]map[string]string{
			"jp": {resourceBoxesEntity: "boxes-1", resourceBoxDetailsEntity: "details-1"},
		},
	}
	router := newMissionTestRouter(newMissionTestHandler(cache))

	for range 2 {
		assertStoryMissionRewardResolved(t, router)
	}
	characterRanks := serveLookupRequest(t, router, http.MethodGet, "/api/v1/characterRanks/jp/list?character_id=1")
	if characterRanks.Code != http.StatusOK {
		t.Fatalf("expected character ranks 200, got %d: %s", characterRanks.Code, characterRanks.Body.String())
	}
	assertMissionEntityListCalls(t, cache.missionTrackingCache, map[string]int{
		resourceBoxesEntity:      1,
		resourceBoxDetailsEntity: 1,
	})

	cache.revisions["jp"][resourceBoxesEntity] = "boxes-2"
	assertStoryMissionRewardResolved(t, router)
	assertMissionEntityListCalls(t, cache.missionTrackingCache, map[string]int{
		resourceBoxesEntity:      2,
		resourceBoxDetailsEntity: 1,
	})
}

func assertStoryMissionRewardResolved(t *testing.T, router *gin.Engine) {
	t.Helper()

	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/missions/jp/list?family=storyMissions")
	if response.Code != http.StatusOK {
		t.Fatalf("expected missions 200, got %d: %s", response.Code, response.Body.String())
	}
	items, _ := decodeMissionList(t, response.Body.Bytes())
	if len(items) != 1 {
		t.Fatalf("expected one story mission, got %#v", items)
	}
	rewards, ok := items[0]["rewards"].([]any)
	if !ok || len(rewards) != 1 {
		t.Fatalf("expected one story mission reward, got %#v", items[0]["rewards"])
	}
	if reward := rewards[0].(map[string]any); reward["status"] != missionRewardResolvedStatus {
		t.Fatalf("expected resolved story mission reward, got %#v", reward)
	}
}

func assertMissionEntityListCalls(t *testing.T, cache *missionTrackingCache, want map[string]int) {
	t.Helper()

	got := make(map[string]int)
	for _, call := range cache.listCalls {
		if _, tracked := want[call.entity]; tracked {
			got[call.entity]++
		}
	}
	for entity, count := range want {
		if got[entity] != count {
			t.Fatalf("expected %d %s ListAll calls, got %d: %#v", count, entity, got[entity], cache.listCalls)
		}
	}
}
