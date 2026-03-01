package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type fakeCardHandlerCache struct {
	byID          map[string]map[string]map[string]map[string]any
	listItems     []map[string]any
	listTotal     int
	searchMatches []masterdata.SearchMatch
	lastSearch    struct {
		region string
		entity string
		query  string
		fields []string
		limit  int
	}
}

func (cache *fakeCardHandlerCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (cache *fakeCardHandlerCache) GetByID(_ context.Context, region string, entity string, id string) (map[string]any, bool, error) {
	regionData, ok := cache.byID[region]
	if !ok {
		return nil, false, nil
	}
	entityData, ok := regionData[entity]
	if !ok {
		return nil, false, nil
	}
	record, ok := entityData[id]
	if !ok {
		return nil, false, nil
	}
	return record, true, nil
}

func (cache *fakeCardHandlerCache) ListByPage(_ context.Context, _, _ string, _, _ int) ([]map[string]any, int, error) {
	return cache.listItems, cache.listTotal, nil
}

func (cache *fakeCardHandlerCache) Search(_ context.Context, region, entity, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	cache.lastSearch.region = region
	cache.lastSearch.entity = entity
	cache.lastSearch.query = query
	cache.lastSearch.limit = limit
	cache.lastSearch.fields = append([]string{}, fields...)
	return cache.searchMatches, nil
}

func TestBuildCardBaseMapsCardSupply(t *testing.T) {
	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"skills": {
					"20": {
						"id":   20,
						"name": "score up",
					},
				},
				"cardsupplies": {
					"10": {
						"id":   10,
						"name": "limited",
					},
				},
			},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)
	handler := NewCardHandler(syncUsecase)

	card := map[string]any{
		"id":           1001,
		"prefix":       "test-prefix",
		"cardSupplyId": 10,
		"skillId":      20,
	}

	result := handler.buildCardBase(context.Background(), "jp", card)

	if _, exists := result["cardSupplyId"]; exists {
		t.Fatalf("expected cardSupplyId to be removed from response")
	}

	cardSupply, exists := result["cardSupply"]
	if !exists {
		t.Fatalf("expected cardSupply field in response")
	}

	supplyMap, ok := cardSupply.(map[string]any)
	if !ok {
		t.Fatalf("expected cardSupply to be object, got %T", cardSupply)
	}

	if supplyMap["id"] != 10 {
		t.Fatalf("expected cardSupply.id=10, got %v", supplyMap["id"])
	}

	if _, exists := result["skillId"]; exists {
		t.Fatalf("expected skillId to be removed from response")
	}

	skill, exists := result["skill"]
	if !exists {
		t.Fatalf("expected skill field in response")
	}

	skillMap, ok := skill.(map[string]any)
	if !ok {
		t.Fatalf("expected skill to be object, got %T", skill)
	}

	if skillMap["id"] != 20 {
		t.Fatalf("expected skill.id=20, got %v", skillMap["id"])
	}
}

func TestCardByIDEndpointMapsCardSupply(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"skills": {
					"20": {
						"id":   20,
						"name": "score up",
					},
				},
				"cards": {
					"1001": {
						"id":           1001,
						"prefix":       "test-prefix",
						"cardSupplyId": 10,
						"skillId":      20,
					},
				},
				"cardsupplies": {
					"10": {
						"id":   10,
						"name": "limited",
					},
				},
			},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)
	cardHandler := NewCardHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id", cardHandler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if _, exists := body["cardSupplyId"]; exists {
		t.Fatalf("expected cardSupplyId to be removed from endpoint response")
	}

	cardSupplyRaw, exists := body["cardSupply"]
	if !exists {
		t.Fatalf("expected cardSupply field in endpoint response")
	}

	cardSupply, ok := cardSupplyRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected cardSupply object, got %T", cardSupplyRaw)
	}

	if cardSupply["id"] != float64(10) {
		t.Fatalf("expected cardSupply.id=10, got %v", cardSupply["id"])
	}

	if _, exists := body["skillId"]; exists {
		t.Fatalf("expected skillId to be removed from endpoint response")
	}

	skillRaw, exists := body["skill"]
	if !exists {
		t.Fatalf("expected skill field in endpoint response")
	}

	skill, ok := skillRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected skill object, got %T", skillRaw)
	}

	if skill["id"] != float64(20) {
		t.Fatalf("expected skill.id=20, got %v", skill["id"])
	}
}

func TestCardListEndpointMapsCardSupply(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"skills": {
					"20": {
						"id":   20,
						"name": "score up",
					},
				},
				"cardsupplies": {
					"10": {
						"id":   10,
						"name": "limited",
					},
				},
			},
		},
		listItems: []map[string]any{
			{
				"id":           1001,
				"prefix":       "test-prefix",
				"cardSupplyId": 10,
				"skillId":      20,
			},
		},
		listTotal: 1,
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)
	cardHandler := NewCardHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	assertFirstItemHasMappedCardSupply(t, body)
}

func TestCardSearchEndpointMapsCardSupply(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"skills": {
					"20": {
						"id":   20,
						"name": "score up",
					},
				},
				"cardsupplies": {
					"10": {
						"id":   10,
						"name": "limited",
					},
				},
			},
		},
		searchMatches: []masterdata.SearchMatch{
			{
				Item: map[string]any{
					"id":           1001,
					"prefix":       "test-prefix",
					"cardSupplyId": 10,
					"skillId":      20,
				},
				MatchScore:   80,
				MatchType:    "prefix",
				MatchedField: "prefix",
			},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)
	cardHandler := NewCardHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/cards/:region/search", cardHandler.SearchByPrefix)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/search?q=test&page=1&limit=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	assertFirstItemHasMappedCardSupply(t, body)
}

func TestCardSearchFieldNameMapsToPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)
	cardHandler := NewCardHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/cards/:region/search", cardHandler.SearchByPrefix)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/search?q=test&field=name&page=1&limit=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	if len(cache.lastSearch.fields) != 1 || cache.lastSearch.fields[0] != "prefix" {
		t.Fatalf("expected search fields [prefix], got %v", cache.lastSearch.fields)
	}
}

func TestCardSearchFieldSkillMapsToCardSkillName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)
	cardHandler := NewCardHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/cards/:region/search", cardHandler.SearchByPrefix)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/search?q=test&field=skill&page=1&limit=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	if len(cache.lastSearch.fields) != 1 || cache.lastSearch.fields[0] != "cardSkillName" {
		t.Fatalf("expected search fields [cardSkillName], got %v", cache.lastSearch.fields)
	}
}

func TestCardSearchFieldInvalidReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, nil, nil, 1)
	cardHandler := NewCardHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/cards/:region/search", cardHandler.SearchByPrefix)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/search?q=test&field=unknown&page=1&limit=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}

func assertFirstItemHasMappedCardSupply(t *testing.T, body map[string]any) {
	t.Helper()

	itemsRaw, ok := body["items"]
	if !ok {
		t.Fatalf("expected items in response")
	}

	items, ok := itemsRaw.([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", itemsRaw)
	}
	if len(items) == 0 {
		t.Fatalf("expected non-empty items")
	}

	firstItem, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}

	if _, exists := firstItem["cardSupplyId"]; exists {
		t.Fatalf("expected cardSupplyId to be removed from item")
	}

	cardSupplyRaw, exists := firstItem["cardSupply"]
	if !exists {
		t.Fatalf("expected cardSupply field in item")
	}

	cardSupply, ok := cardSupplyRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected cardSupply object, got %T", cardSupplyRaw)
	}

	if cardSupply["id"] != float64(10) {
		t.Fatalf("expected cardSupply.id=10, got %v", cardSupply["id"])
	}

	if _, exists := firstItem["skillId"]; exists {
		t.Fatalf("expected skillId to be removed from item")
	}

	skillRaw, exists := firstItem["skill"]
	if !exists {
		t.Fatalf("expected skill field in item")
	}

	skill, ok := skillRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected skill object, got %T", skillRaw)
	}

	if skill["id"] != float64(20) {
		t.Fatalf("expected skill.id=20, got %v", skill["id"])
	}
}
