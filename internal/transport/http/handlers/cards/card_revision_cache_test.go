package cards

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type revisionTrackingCardCache struct {
	*fakeCardHandlerCache
	revisions map[string]string
}

func (cache *revisionTrackingCardCache) EntityRevision(_ context.Context, _ string, entity string) (string, error) {
	return cache.revisions[entity], nil
}

func TestCardListFieldsCoverSortAndBaseFields(t *testing.T) {
	fields := make(map[string]struct{}, len(cardListFields))
	for _, field := range cardListFields {
		fields[field] = struct{}{}
	}
	for _, field := range append(append([]string{}, sortableCardFields...), "characterId", "cardRarityType", "skillId", "cardSupplyId") {
		if _, ok := fields[field]; !ok {
			t.Errorf("cardListFields is missing %q", field)
		}
	}
}

func TestCardHandlerDecodesCardsAndGachasOncePerRevision(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &revisionTrackingCardCache{
		fakeCardHandlerCache: &fakeCardHandlerCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {"cards": {"1001": {"id": 1001}}},
			},
			listByEntity: map[string][]map[string]any{
				"cards": {{"id": 1001, "prefix": "Card", "cardParameters": []any{map[string]any{"power": 1}}}},
				"gachas": {{
					"id":           10,
					"name":         "Gacha",
					"gachaPickups": []any{map[string]any{"cardId": 1001}},
				}},
				"cardrarities": {},
			},
		},
		revisions: map[string]string{"cards": "c1", "gachas": "g1"},
	}
	statusStore := &fakeCardHandlerStatusStore{statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}}}
	handler := NewCardHandler(usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1))
	router := gin.New()
	router.GET("/api/v1/cards/:region/list", handler.List)
	router.GET("/api/v1/cards/:region/:id/gachas", handler.GachaByID)

	get := func(path string) string {
		t.Helper()
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, path, nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", path, resp.Code, resp.Body.String())
		}
		return resp.Body.String()
	}

	for range 2 {
		get("/api/v1/cards/jp/list")
		get("/api/v1/cards/jp/1001/gachas")
	}
	if cache.listAllCalls["cards"] != 1 || cache.listAllCalls["gachas"] != 1 {
		t.Fatalf("expected cards and gachas decoded once for unchanged revisions, got %v", cache.listAllCalls)
	}

	cache.revisions["gachas"] = "g2"
	get("/api/v1/cards/jp/1001/gachas")
	if cache.listAllCalls["gachas"] != 2 {
		t.Fatalf("expected a new gachas revision to rebuild the index, got %v", cache.listAllCalls)
	}
}
