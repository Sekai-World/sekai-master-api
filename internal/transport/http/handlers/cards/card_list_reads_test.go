package cards

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

func TestCardProjectionCoversListSortAndBaseFields(t *testing.T) {
	fields := make(map[string]struct{})
	for _, field := range masterdata.ProjectionFields("cards") {
		fields[field] = struct{}{}
	}
	baseFields := []string{"id", "seq", "attr", "supportUnit", "cardSkillName", "prefix", "assetbundleName", "gachaPhrase", "flavorText", "releaseAt", "archivePublishedAt", "initialSpecialTrainingStatus"}
	lookupAndFilterFields := []string{"characterId", "cardRarityType", "skillId", "cardSupplyId", "releastAt", "publishedAt", "startAt"}
	for _, field := range slices.Concat(sortableCardFields, baseFields, lookupAndFilterFields) {
		if _, ok := fields[field]; !ok {
			t.Errorf("the cards projection is missing %q", field)
		}
	}
}

// projectionCardCache answers projection reads from the fake's lists without
// counting them as full-entity reads.
type projectionCardCache struct {
	*fakeCardHandlerCache
	projectionCalls []string
}

func (cache *projectionCardCache) LoadProjection(_ context.Context, _ string, entity string) (*masterdata.Projection, error) {
	cache.projectionCalls = append(cache.projectionCalls, entity)
	records := cache.listByEntity[entity]
	keys := make([]string, len(records))
	for position, record := range records {
		keys[position], _ = masterdata.CanonicalKeyPart(record["id"])
	}
	return masterdata.BuildProjection(entity, keys, records), nil
}

func TestCardListReadsTheCardProjection(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &projectionCardCache{fakeCardHandlerCache: &fakeCardHandlerCache{
		listByEntity: map[string][]map[string]any{
			"cards":        {{"id": 1001, "prefix": "Card", "cardParameters": []any{map[string]any{"power": 1}}}},
			"cardrarities": {},
		},
	}}
	statusStore := &fakeCardHandlerStatusStore{statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}}}
	router := gin.New()
	router.GET("/api/v1/cards/:region/list", NewCardHandler(usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)).List)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"prefix":"Card"`) || strings.Contains(resp.Body.String(), "cardParameters") {
		t.Fatalf("expected the card's list fields only, got %s", resp.Body.String())
	}
	if cache.listAllCalls["cards"] != 0 || !slices.Equal(cache.projectionCalls, []string{"cards"}) {
		t.Fatalf("expected one projection read and no full cards read, got projection=%v list=%v", cache.projectionCalls, cache.listAllCalls)
	}
}

// indexedCardCache answers index reads from the fake's lists without counting
// them as full-entity reads.
type indexedCardCache struct {
	*fakeCardHandlerCache
	indexCalls []string
}

func (cache *indexedCardCache) ListByIndex(_ context.Context, _ string, entity string, index string, lookups [][]any) ([][]map[string]any, error) {
	cache.indexCalls = append(cache.indexCalls, entity+"/"+index)
	results := make([][]map[string]any, len(lookups))
	for position, lookup := range lookups {
		key, _ := masterdata.IndexLookupKey(lookup...)
		for _, record := range cache.listByEntity[entity] {
			if slices.Contains(masterdata.IndexKeys(record, index), key) {
				results[position] = append(results[position], record)
			}
		}
	}
	return results, nil
}

func TestCardGachasReadPickupGachasThroughIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &indexedCardCache{fakeCardHandlerCache: &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{"jp": {"cards": {"1001": {"id": 1001}}}},
		listByEntity: map[string][]map[string]any{"gachas": {
			{"id": 10, "name": "Gacha", "gachaDetails": []any{}, "gachaPickups": []any{map[string]any{"cardId": 1001}}},
			{"id": 11, "name": "Other", "gachaPickups": []any{map[string]any{"cardId": 9}}},
		}},
	}}
	statusStore := &fakeCardHandlerStatusStore{statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}}}
	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/gachas", NewCardHandler(usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)).GachaByID)

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/gachas", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"gachas":[{"id":10,"name":"Gacha"}]`) {
		t.Fatalf("expected only the pickup gacha's summary, got %s", resp.Body.String())
	}
	if cache.listAllCalls["gachas"] != 0 || !slices.Equal(cache.indexCalls, []string{"gachas/gachaPickups.cardId"}) {
		t.Fatalf("expected one index read and no full gachas read, got index=%v list=%v", cache.indexCalls, cache.listAllCalls)
	}
}
