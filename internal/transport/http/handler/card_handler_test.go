package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	rarityMatches []masterdata.SearchMatch
	lastSearch    struct {
		region string
		entity string
		query  string
		fields []string
		limit  int
	}
}

type fakeCardHandlerStatusStore struct {
	statuses []masterdata.SyncStatus
}

func newReadyCardHandler(cache *fakeCardHandlerCache) *CardHandler {
	statusStore := &fakeCardHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewCardHandler(syncUsecase)
}

func (store *fakeCardHandlerStatusStore) Save(_ context.Context, _ masterdata.SyncStatus) error {
	return nil
}

func (store *fakeCardHandlerStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	return store.statuses, nil
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

func (cache *fakeCardHandlerCache) ListAll(_ context.Context, _, _ string) ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(cache.listItems))
	for _, item := range cache.listItems {
		copied := make(map[string]any, len(item))
		for key, value := range item {
			copied[key] = value
		}
		items = append(items, copied)
	}
	return items, nil
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
	if entity == "cardrarities" {
		return cache.rarityMatches, nil
	}
	return cache.searchMatches, nil
}

func TestBuildCardBaseMapsCardSupply(t *testing.T) {
	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gamecharacters": {
					"30": {
						"id":                     30,
						"firstName":              "Miku",
						"live2dHeightAdjustment": 1,
						"figure":                 "x",
						"breastSize":             "y",
						"modelName":              "z",
					},
				},
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
		"id":             1001,
		"characterId":    30,
		"cardRarityType": "rarity_4",
		"prefix":         "test-prefix",
		"cardSupplyId":   10,
		"skillId":        20,
	}
	cache.rarityMatches = []masterdata.SearchMatch{
		{Item: map[string]any{"id": 4, "cardRarityType": "rarity_4", "label": "4★"}},
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

	if _, exists := result["characterId"]; exists {
		t.Fatalf("expected characterId to be removed from response")
	}

	characterRaw, exists := result["character"]
	if !exists {
		t.Fatalf("expected character field in response")
	}

	characterMap, ok := characterRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected character to be object, got %T", characterRaw)
	}

	if characterMap["id"] != 30 {
		t.Fatalf("expected character.id=30, got %v", characterMap["id"])
	}

	for _, removed := range []string{"live2dHeightAdjustment", "figure", "breastSize", "modelName"} {
		if _, exists := characterMap[removed]; exists {
			t.Fatalf("expected character.%s to be removed", removed)
		}
	}

	if _, exists := result["cardRarityType"]; exists {
		t.Fatalf("expected cardRarityType to be removed from response")
	}

	cardRarityRaw, exists := result["cardRarity"]
	if !exists {
		t.Fatalf("expected cardRarity field in response")
	}

	cardRarity, ok := cardRarityRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected cardRarity to be object, got %T", cardRarityRaw)
	}

	if cardRarity["cardRarityType"] != "rarity_4" {
		t.Fatalf("expected cardRarity.cardRarityType=rarity_4, got %v", cardRarity["cardRarityType"])
	}
}

func TestCardByIDEndpointMapsCardSupply(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gamecharacters": {
					"30": {
						"id":                     30,
						"firstName":              "Miku",
						"live2dHeightAdjustment": 1,
						"figure":                 "x",
						"breastSize":             "y",
						"modelName":              "z",
					},
				},
				"skills": {
					"20": {
						"id":   20,
						"name": "score up",
					},
				},
				"cards": {
					"1001": {
						"id":             1001,
						"cardRarityType": "rarity_4",
						"characterId":    30,
						"prefix":         "test-prefix",
						"cardSupplyId":   10,
						"skillId":        20,
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
	cache.rarityMatches = []masterdata.SearchMatch{
		{Item: map[string]any{"id": 4, "cardRarityType": "rarity_4", "label": "4★"}},
	}

	cardHandler := newReadyCardHandler(cache)

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

	if _, exists := body["characterId"]; exists {
		t.Fatalf("expected characterId to be removed from endpoint response")
	}

	characterRaw, exists := body["character"]
	if !exists {
		t.Fatalf("expected character field in endpoint response")
	}

	character, ok := characterRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected character object, got %T", characterRaw)
	}

	if character["id"] != float64(30) {
		t.Fatalf("expected character.id=30, got %v", character["id"])
	}

	for _, removed := range []string{"live2dHeightAdjustment", "figure", "breastSize", "modelName"} {
		if _, exists := character[removed]; exists {
			t.Fatalf("expected character.%s to be removed", removed)
		}
	}

	if _, exists := body["cardRarityType"]; exists {
		t.Fatalf("expected cardRarityType to be removed from endpoint response")
	}

	cardRarityRaw, exists := body["cardRarity"]
	if !exists {
		t.Fatalf("expected cardRarity field in endpoint response")
	}

	cardRarity, ok := cardRarityRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected cardRarity object, got %T", cardRarityRaw)
	}

	if cardRarity["cardRarityType"] != "rarity_4" {
		t.Fatalf("expected cardRarity.cardRarityType=rarity_4, got %v", cardRarity["cardRarityType"])
	}
}

func TestCardAvailableRegionsByIDEndpointReturnsReadyRegionsWithData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"1001": {"id": 1001},
				},
			},
			"en": {
				"cards": {
					"1001": {"id": 1001},
				},
			},
			"tw": {
				"cards": {
					"1001": {"id": 1001},
				},
			},
		},
	}

	statusStore := &fakeCardHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "tw", Status: "running"},
			{Region: "en", Status: "success"},
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	cardHandler := NewCardHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/cards/regions/:id/availability", cardHandler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/regions/1001/availability", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body struct {
		Regions []string `json:"regions"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	expected := []string{"en", "jp"}
	if !reflect.DeepEqual(body.Regions, expected) {
		t.Fatalf("expected regions %v, got %v", expected, body.Regions)
	}
}

func TestCardListEndpointMapsCardSupply(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gamecharacters": {
					"30": {
						"id":                     30,
						"firstName":              "Miku",
						"live2dHeightAdjustment": 1,
						"figure":                 "x",
						"breastSize":             "y",
						"modelName":              "z",
					},
				},
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
				"id":             1001,
				"cardRarityType": "rarity_4",
				"characterId":    30,
				"prefix":         "test-prefix",
				"cardSupplyId":   10,
				"skillId":        20,
			},
		},
		listTotal: 1,
	}
	cache.rarityMatches = []masterdata.SearchMatch{
		{Item: map[string]any{"id": 4, "cardRarityType": "rarity_4", "label": "4★"}},
	}

	cardHandler := newReadyCardHandler(cache)

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

func TestCardListEndpointSupportsSorting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		listItems: []map[string]any{
			{"id": 2, "prefix": "bravo", "seq": 2},
			{"id": 1, "prefix": "alpha", "seq": 1},
		},
		listTotal: 2,
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20&sort_by=prefix&sort_order=asc", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1, 2})
}

func TestCardSearchEndpointMapsCardSupply(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gamecharacters": {
					"30": {
						"id":                     30,
						"firstName":              "Miku",
						"live2dHeightAdjustment": 1,
						"figure":                 "x",
						"breastSize":             "y",
						"modelName":              "z",
					},
				},
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
					"id":             1001,
					"cardRarityType": "rarity_4",
					"characterId":    30,
					"prefix":         "test-prefix",
					"cardSupplyId":   10,
					"skillId":        20,
				},
				MatchScore:   80,
				MatchType:    "prefix",
				MatchedField: "prefix",
			},
		},
	}
	cache.rarityMatches = []masterdata.SearchMatch{
		{Item: map[string]any{"id": 4, "cardRarityType": "rarity_4", "label": "4★"}},
	}

	cardHandler := newReadyCardHandler(cache)

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

func TestCardSearchEndpointSupportsSorting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		searchMatches: []masterdata.SearchMatch{
			{Item: map[string]any{"id": 1, "prefix": "alpha", "seq": 1}, MatchScore: 100},
			{Item: map[string]any{"id": 2, "prefix": "bravo", "seq": 2}, MatchScore: 90},
		},
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/search", cardHandler.SearchByPrefix)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/search?q=test&page=1&limit=20&sort_by=prefix&sort_order=desc", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{2, 1})
}

func TestCardSearchFieldNameMapsToPrefix(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{}
	cardHandler := newReadyCardHandler(cache)

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
	cardHandler := newReadyCardHandler(cache)

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
	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/search", cardHandler.SearchByPrefix)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/search?q=test&field=unknown&page=1&limit=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}

func TestCardListSortOrderWithoutSortByReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{}
	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20&sort_order=desc", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}

func TestCardListInvalidSortByReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "prefix": "alpha"},
		},
		listTotal: 1,
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20&sort_by=cardSupply&sort_order=asc", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}

func TestCardEpisodesByIDEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"1001": {
						"id": 1001,
					},
				},
			},
		},
		searchMatches: []masterdata.SearchMatch{
			{Item: map[string]any{"id": 1, "cardId": 1001, "episodeNo": 1}},
			{Item: map[string]any{"id": 2, "cardId": 1001, "episodeNo": 2}},
		},
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/episodes", cardHandler.EpisodesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/episodes", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	if cache.lastSearch.entity != "cardepisodes" {
		t.Fatalf("expected search entity cardepisodes, got %s", cache.lastSearch.entity)
	}
	if cache.lastSearch.query != "1001" {
		t.Fatalf("expected search query 1001, got %s", cache.lastSearch.query)
	}
	if len(cache.lastSearch.fields) != 1 || cache.lastSearch.fields[0] != "cardId" {
		t.Fatalf("expected search fields [cardId], got %v", cache.lastSearch.fields)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	itemsRaw, ok := body["items"]
	if !ok {
		t.Fatalf("expected items in response")
	}
	items, ok := itemsRaw.([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", itemsRaw)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestCardEpisodesByIDEndpointFiltersNonExactCardIDMatches(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"1001": {
						"id": 1001,
					},
				},
			},
		},
		searchMatches: []masterdata.SearchMatch{
			{Item: map[string]any{"id": 1, "cardId": 1001, "episodeNo": 1}},
			{Item: map[string]any{"id": 2, "cardId": 10010, "episodeNo": 1}},
			{Item: map[string]any{"id": 3, "cardId": "1001-extra", "episodeNo": 2}},
		},
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/episodes", cardHandler.EpisodesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/episodes", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	itemsRaw, ok := body["items"]
	if !ok {
		t.Fatalf("expected items in response")
	}

	items, ok := itemsRaw.([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", itemsRaw)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 exact-match item, got %d", len(items))
	}

	itemMap, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected item object, got %T", items[0])
	}

	if itemMap["cardId"] != float64(1001) {
		t.Fatalf("expected exact cardId 1001, got %v", itemMap["cardId"])
	}
}

func TestCardEndpointsBlockedWhenRegionSyncInProgress(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{}
	statusStore := &fakeCardHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "running"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	cardHandler := NewCardHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id", cardHandler.ByID)
	router.GET("/api/v1/cards/:region/:id/params", cardHandler.ParamsByID)
	router.GET("/api/v1/cards/:region/:id/episodes", cardHandler.EpisodesByID)
	router.GET("/api/v1/cards/:region/list", cardHandler.List)
	router.GET("/api/v1/cards/:region/search", cardHandler.SearchByPrefix)

	testCases := []string{
		"/api/v1/cards/jp/1001",
		"/api/v1/cards/jp/1001/params",
		"/api/v1/cards/jp/1001/episodes",
		"/api/v1/cards/jp/list?page=1&page_size=20",
		"/api/v1/cards/jp/search?q=test&page=1&limit=20",
	}

	for _, path := range testCases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("path %s: expected status 503, got %d", path, resp.Code)
		}

		var body map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("path %s: unmarshal response: %v", path, err)
		}

		errorRaw, ok := body["error"]
		if !ok {
			t.Fatalf("path %s: expected error object in response", path)
		}

		errorObj, ok := errorRaw.(map[string]any)
		if !ok {
			t.Fatalf("path %s: expected error object type, got %T", path, errorRaw)
		}

		if errorObj["code"] != "REGION_DATA_NOT_READY" {
			t.Fatalf("path %s: expected error code REGION_DATA_NOT_READY, got %v", path, errorObj["code"])
		}

		if errorObj["message"] != "region data is updating or unavailable, please try again later" {
			t.Fatalf("path %s: unexpected error message %v", path, errorObj["message"])
		}
	}
}

func TestCardEndpointsBlockedWhenRegionStatusMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{}
	statusStore := &fakeCardHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "other", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	cardHandler := NewCardHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id", cardHandler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	errorRaw, ok := body["error"]
	if !ok {
		t.Fatalf("expected error object in response")
	}
	errorObj, ok := errorRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected error object type, got %T", errorRaw)
	}

	if errorObj["code"] != "REGION_DATA_NOT_READY" {
		t.Fatalf("expected error code REGION_DATA_NOT_READY, got %v", errorObj["code"])
	}
	if errorObj["message"] != "region data is updating or unavailable, please try again later" {
		t.Fatalf("unexpected error message %v", errorObj["message"])
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

	if _, exists := firstItem["characterId"]; exists {
		t.Fatalf("expected characterId to be removed from item")
	}

	characterRaw, exists := firstItem["character"]
	if !exists {
		t.Fatalf("expected character field in item")
	}

	character, ok := characterRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected character object, got %T", characterRaw)
	}

	if character["id"] != float64(30) {
		t.Fatalf("expected character.id=30, got %v", character["id"])
	}

	for _, removed := range []string{"live2dHeightAdjustment", "figure", "breastSize", "modelName"} {
		if _, exists := character[removed]; exists {
			t.Fatalf("expected character.%s to be removed", removed)
		}
	}

	if _, exists := firstItem["cardRarityType"]; exists {
		t.Fatalf("expected cardRarityType to be removed from item")
	}

	cardRarityRaw, exists := firstItem["cardRarity"]
	if !exists {
		t.Fatalf("expected cardRarity field in item")
	}

	cardRarity, ok := cardRarityRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected cardRarity object, got %T", cardRarityRaw)
	}

	if cardRarity["cardRarityType"] != "rarity_4" {
		t.Fatalf("expected cardRarity.cardRarityType=rarity_4, got %v", cardRarity["cardRarityType"])
	}
}

func assertResponseItemOrder(t *testing.T, bodyBytes []byte, expected []float64) {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	itemsRaw, ok := body["items"]
	if !ok {
		t.Fatalf("expected items in response")
	}
	items, ok := itemsRaw.([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", itemsRaw)
	}
	if len(items) != len(expected) {
		t.Fatalf("expected %d items, got %d", len(expected), len(items))
	}

	for index, want := range expected {
		item, ok := items[index].(map[string]any)
		if !ok {
			t.Fatalf("expected item object, got %T", items[index])
		}
		if item["id"] != want {
			t.Fatalf("expected item %d id=%v, got %v", index, want, item["id"])
		}
	}
}
