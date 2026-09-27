package lookups

import (
	"context"
	"net/http"
	"strings"
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

func TestCharacterRanksReadRanksRewardsAndEXPThroughIndexes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &missionTrackingCache{fakeLookupCache: &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{"jp": {
			characterRanksEntity: {
				{"id": 1, "characterId": 1, "characterRank": 1, "rewardResourceBoxIds": []any{1001}},
				{"id": 2, "characterId": 2, "characterRank": 1, "rewardResourceBoxIds": []any{1002}},
			},
			resourceBoxesEntity: {
				{"id": 1001, "resourceBoxPurpose": "character_rank_reward", "details": []any{map[string]any{"resourceType": "jewel", "resourceQuantity": 10}}},
				{"id": 1001, "resourceBoxPurpose": "mission_reward", "details": []any{map[string]any{"resourceType": "coin", "resourceQuantity": 1}}},
			},
			levelsEntity: {
				{"levelType": "character", "level": 1, "totalExp": 0},
				{"levelType": "card", "level": 1, "totalExp": 99},
			},
		}},
	}}
	router := gin.New()
	router.GET("/api/v1/characterRanks/:region/list", newMissionTestHandler(cache).CharacterRanksList)

	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/characterRanks/jp/list?character_id=1")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{`"characterId":1`, `"totalExp":0`, `"resourceBoxPurpose":"character_rank_reward"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %s in %s", want, body)
		}
	}
	for _, unwanted := range []string{`"characterId":2`, `"mission_reward"`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("expected no %s in %s", unwanted, body)
		}
	}
	if len(cache.listCalls) != 0 {
		t.Fatalf("expected character ranks to read only through indexes, got ListAll calls %#v", cache.listCalls)
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
