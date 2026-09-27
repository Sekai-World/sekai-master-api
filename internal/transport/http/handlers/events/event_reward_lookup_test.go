package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

// listCountingEventCache counts full-entity reads made by handlers. Its
// composite-key reads go to the embedded fake and are not counted.
type listCountingEventCache struct {
	*fakeEventHandlerCache
	listCalls map[string]int
}

func (cache *listCountingEventCache) ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error) {
	cache.listCalls[strings.ToLower(entity)]++
	return cache.fakeEventHandlerCache.ListAll(ctx, region, entity)
}

func TestEventRewardsReadRankingBoxesByCompositeKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rankingBox := func(id int) map[string]any {
		return map[string]any{
			"id":                 id,
			"resourceBoxPurpose": "event_ranking_reward",
			"resourceBoxType":    "expand",
			"details": []any{
				map[string]any{"resourceBoxId": id, "resourceType": "jewel", "resourceQuantity": 100},
			},
		}
	}
	cache := &listCountingEventCache{
		fakeEventHandlerCache: &fakeEventHandlerCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {"events": {"101": {
					"id": 101,
					"eventRankingRewardRanges": []any{
						map[string]any{"fromRank": 1, "toRank": 1, "eventRankingRewards": []any{
							map[string]any{"id": 1, "resourceBoxId": 11},
						}},
						map[string]any{"fromRank": 2, "toRank": 2, "eventRankingRewards": []any{
							map[string]any{"id": 2, "resourceBoxId": 12},
						}},
					},
				}}},
			},
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {"resourceboxes": {
					{"id": 11, "resourceBoxPurpose": "mission_reward", "details": []any{map[string]any{}}},
					rankingBox(11),
					rankingBox(12),
				}},
			},
		},
		listCalls: map[string]int{},
	}
	statusStore := &fakeEventHandlerStatusStore{statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}}}
	handler := NewEventHandler(usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1))
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/rewards", handler.RewardsByID)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101/rewards", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for index, wantID := range []float64{11, 12} {
		rewards := body.Items[index]["eventRankingRewards"].([]any)
		box, _ := rewards[0].(map[string]any)["resourceBox"].(map[string]any)
		if box["id"] != wantID || box["resourceBoxPurpose"] != "event_ranking_reward" {
			t.Fatalf("range %d: expected ranking box %v, got %v", index, wantID, box)
		}
	}
	if calls := cache.listCalls["resourceboxes"]; calls != 0 {
		t.Fatalf("expected no full resourceboxes read, got %d", calls)
	}
}
