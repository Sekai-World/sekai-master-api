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

type revisionTrackingEventCache struct {
	*fakeEventHandlerCache
	revisions map[string]string
	listCalls map[string]int
}

func (cache *revisionTrackingEventCache) ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error) {
	cache.listCalls[strings.ToLower(region)+"/"+strings.ToLower(entity)]++
	return cache.fakeEventHandlerCache.ListAll(ctx, region, entity)
}

func (cache *revisionTrackingEventCache) EntityRevision(_ context.Context, region string, entity string) (string, error) {
	return cache.revisions[strings.ToLower(region)+"/"+strings.ToLower(entity)], nil
}

func TestEventRewardsDecodeResourceBoxesOncePerRevision(t *testing.T) {
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
	cache := &revisionTrackingEventCache{
		fakeEventHandlerCache: &fakeEventHandlerCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {
					"events": {
						"101": {
							"id": 101,
							"eventRankingRewardRanges": []any{
								map[string]any{"fromRank": 1, "toRank": 1, "eventRankingRewards": []any{
									map[string]any{"id": 1, "resourceBoxId": 11},
								}},
								map[string]any{"fromRank": 2, "toRank": 2, "eventRankingRewards": []any{
									map[string]any{"id": 2, "resourceBoxId": 12},
								}},
							},
						},
					},
				},
			},
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {"resourceboxes": {
					{"id": 11, "resourceBoxPurpose": "mission_reward", "details": []any{map[string]any{}}},
					rankingBox(11),
					rankingBox(12),
				}},
			},
		},
		revisions: map[string]string{"jp/resourceboxes": "r1"},
		listCalls: map[string]int{},
	}
	statusStore := &fakeEventHandlerStatusStore{statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}}}
	handler := NewEventHandler(usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1))
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/rewards", handler.RewardsByID)

	get := func() []any {
		t.Helper()
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101/rewards", nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
		}
		var body struct {
			Items []any `json:"items"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return body.Items
	}

	items := get()
	for index, wantID := range []float64{11, 12} {
		rewards := items[index].(map[string]any)["eventRankingRewards"].([]any)
		box, _ := rewards[0].(map[string]any)["resourceBox"].(map[string]any)
		if box["id"] != wantID || box["resourceBoxPurpose"] != "event_ranking_reward" {
			t.Fatalf("range %d: expected ranking box %v, got %v", index, wantID, box)
		}
	}
	get()
	if calls := cache.listCalls["jp/resourceboxes"]; calls != 1 {
		t.Fatalf("expected resource boxes decoded once for an unchanged revision, got %d", calls)
	}

	cache.revisions["jp/resourceboxes"] = "r2"
	get()
	if calls := cache.listCalls["jp/resourceboxes"]; calls != 2 {
		t.Fatalf("expected a new revision to rebuild the index, got %d decodes", calls)
	}
}
