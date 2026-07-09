package cards

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
	listByEntity  map[string][]map[string]any
	listItems     []map[string]any
	listTotal     int
	hasRecords    map[string]map[string]bool
	hasIndex      bool
	hasIndexSet   bool
	searchMatches []masterdata.SearchMatch
	rarityMatches []masterdata.SearchMatch
	searchCalls   []fakeCardSearchCall
	lastSearch    struct {
		region string
		entity string
		query  string
		fields []string
		limit  int
	}
}

type fakeCardSearchCall struct {
	region string
	entity string
	query  string
	fields []string
	limit  int
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

func (cache *fakeCardHandlerCache) ListAll(_ context.Context, _ string, entity string) ([]map[string]any, error) {
	if cache.listByEntity != nil {
		if entityItems, ok := cache.listByEntity[entity]; ok {
			return copyCardTestItems(entityItems), nil
		}
	}

	return copyCardTestItems(cache.listItems), nil
}

func copyCardTestItems(source []map[string]any) []map[string]any {
	items := make([]map[string]any, 0, len(source))
	for _, item := range source {
		copied := make(map[string]any, len(item))
		for key, value := range item {
			copied[key] = value
		}
		items = append(items, copied)
	}
	return items
}

func (cache *fakeCardHandlerCache) ListByPage(_ context.Context, _, _ string, _, _ int) ([]map[string]any, int, error) {
	return cache.listItems, cache.listTotal, nil
}

func (cache *fakeCardHandlerCache) HasEntityRecords(_ context.Context, region string, entity string) (bool, error) {
	if cache.hasRecords == nil {
		if regionData, ok := cache.byID[region]; ok && len(regionData[entity]) > 0 {
			return true, nil
		}
		return len(cache.listItems) > 0, nil
	}
	return cache.hasRecords[region][entity], nil
}

func (cache *fakeCardHandlerCache) HasRegionIndex(_ string) bool {
	if !cache.hasIndexSet {
		return true
	}
	return cache.hasIndex
}

func (cache *fakeCardHandlerCache) Search(_ context.Context, region, entity, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	cache.lastSearch.region = region
	cache.lastSearch.entity = entity
	cache.lastSearch.query = query
	cache.lastSearch.limit = limit
	cache.lastSearch.fields = append([]string{}, fields...)
	cache.searchCalls = append(cache.searchCalls, fakeCardSearchCall{
		region: region,
		entity: entity,
		query:  query,
		fields: append([]string{}, fields...),
		limit:  limit,
	})
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20&spoiler=true", nil)
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

func TestCardListEndpointUsesPersistedCardDataWhenRuntimeIndexMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		listItems: []map[string]any{
			{"id": 1001, "prefix": "persisted-card"},
		},
		listTotal:   1,
		hasIndexSet: true,
		hasIndex:    false,
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

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1001})
}

func TestCardListEndpointReturnsNotReadyWhenNoRuntimeIndexOrCardData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		hasIndexSet: true,
		hasIndex:    false,
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", resp.Code)
	}
}

func TestCardEpisodesEndpointRequiresRuntimeIndexWhenOnlyCardDataExists(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"1001": {"id": 1001, "prefix": "persisted-card"},
				},
			},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/episodes", cardHandler.EpisodesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/episodes", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", resp.Code)
	}
}

func TestCardDetailEndpointRequiresRuntimeIndexWhenOnlyCardDataExists(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"1001": {"id": 1001, "prefix": "persisted-card"},
				},
			},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/detail", cardHandler.DetailByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/detail", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", resp.Code)
	}
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

func TestCardListEndpointSupportsSpoilerOption(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "prefix": "released", "releaseAt": 946684800000},
			{"id": 2, "prefix": "future", "releaseAt": 4102444800000},
		},
		listTotal: 2,
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?spoiler=false", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1})

	req = httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?spoiler=true", nil)
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1, 2})
}

func TestCardListInvalidSpoilerReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{}
	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?spoiler=maybe", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}

func TestCardListSortingBuildsOnlyCurrentPage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		listItems: []map[string]any{
			{"id": 2, "prefix": "bravo", "cardRarityType": "rarity_4"},
			{"id": 1, "prefix": "alpha", "cardRarityType": "rarity_4"},
		},
		listTotal: 2,
		rarityMatches: []masterdata.SearchMatch{
			{Item: map[string]any{"id": 4, "cardRarityType": "rarity_4"}},
		},
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=1&sort_by=prefix&sort_order=asc", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	rarityLookups := 0
	for _, call := range cache.searchCalls {
		if call.entity == "cardrarities" {
			rarityLookups++
		}
	}
	if rarityLookups != 1 {
		t.Fatalf("expected 1 cardrarities lookup for current page, got %d", rarityLookups)
	}
}

func TestCardListEndpointSupportsFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		listByEntity: map[string][]map[string]any{
			"cards": {
				{
					"id":             1001,
					"characterId":    1,
					"skillId":        10,
					"cardSupplyId":   5,
					"attr":           "cool",
					"cardRarityType": "rarity_4",
					"supportUnit":    "none",
				},
				{
					"id":             1002,
					"characterId":    2,
					"skillId":        11,
					"cardSupplyId":   6,
					"attr":           "cute",
					"cardRarityType": "rarity_3",
					"supportUnit":    "idol",
				},
				{
					"id":             1003,
					"characterId":    3,
					"skillId":        12,
					"cardSupplyId":   7,
					"attr":           "cool",
					"cardRarityType": "rarity_4",
					"supportUnit":    "street",
				},
			},
			"gamecharacters": {
				{"id": 1, "unit": "light_sound"},
				{"id": 2, "unit": "idol"},
				{"id": 3, "unit": "street"},
			},
			"skills": {
				{
					"id":                    10,
					"descriptionSpriteName": "score_up",
					"skillEffects": []any{
						map[string]any{"activateNotesJudgmentType": "perfect", "skillEffectType": "score_up"},
					},
				},
				{
					"id":                    11,
					"descriptionSpriteName": "score_up",
					"skillEffects": []any{
						map[string]any{"activateNotesJudgmentType": "good", "skillEffectType": "score_up_condition_life"},
					},
				},
				{
					"id":                    12,
					"descriptionSpriteName": "life_recovery",
					"skillEffects": []any{
						map[string]any{"activateNotesJudgmentType": "good", "skillEffectType": "life_recovery"},
					},
				},
			},
			"cardsupplies": {
				{"id": 5, "cardSupplyType": "limited"},
				{"id": 6, "cardSupplyType": "normal"},
				{"id": 7, "cardSupplyType": "birthday"},
			},
			"another3dmvcutins": {
				{"id": 6001, "cardId": 1001, "musicId": 60, "cutInNo": 1},
			},
		},
		listTotal: 3,
	}
	cache.listItems = cache.listByEntity["cards"]

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20&unit=light_sound&character=1&skill=score_up&type=limited&attr=cool&rarity=4&supportUnit=none&has3dmvCutIn=true", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1001})
	assertResponsePaginationTotal(t, resp.Body.Bytes(), 1)
}

func TestCardListEndpointSupportsCommaSeparatedFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		listItems: []map[string]any{
			{"id": 1001, "characterId": 1, "attr": "cool", "cardRarityType": "rarity_4", "supportUnit": "none"},
			{"id": 1002, "characterId": 2, "attr": "cute", "cardRarityType": "rarity_3", "supportUnit": "idol"},
			{"id": 1003, "characterId": 3, "attr": "mysterious", "cardRarityType": "rarity_2", "supportUnit": "street"},
		},
		listTotal: 3,
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20&character=1,2&attr=cool,cute", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1001, 1002})
	assertResponsePaginationTotal(t, resp.Body.Bytes(), 2)
}

func TestCardListEndpointFiltersBy3dmvCutIn(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		listByEntity: map[string][]map[string]any{
			"cards": {
				{"id": 1001, "prefix": "alpha"},
				{"id": 1002, "prefix": "bravo"},
			},
			"another3dmvcutins": {
				{"id": 6001, "cardId": 1002, "musicId": 60, "cutInNo": 1},
			},
		},
		listTotal: 2,
	}
	cache.listItems = cache.listByEntity["cards"]

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20&has3dmvCutIn=true", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1002})
	assertResponsePaginationTotal(t, resp.Body.Bytes(), 1)
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

func TestCardParamsByIDEndpointReturnsSeparateCardParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"1001": {
						"id":                              1001,
						"specialTrainingPower1BonusFixed": 300,
						"specialTrainingPower2BonusFixed": 300,
						"specialTrainingPower3BonusFixed": 300,
					},
				},
			},
		},
		searchMatches: []masterdata.SearchMatch{
			{Item: map[string]any{"id": 1, "cardId": 1001, "cardLevel": 1, "cardParameterType": "param1", "power": 100}},
			{Item: map[string]any{"id": 2, "cardId": 1001, "cardLevel": 1, "cardParameterType": "param2", "power": 200}},
			{Item: map[string]any{"id": 3, "cardId": 10010, "cardLevel": 1, "cardParameterType": "param1", "power": 999}},
		},
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/params", cardHandler.ParamsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/params", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	if cache.lastSearch.entity != "cardparameters" {
		t.Fatalf("expected search entity cardparameters, got %s", cache.lastSearch.entity)
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

	itemsRaw, ok := body["cardParameters"]
	if !ok {
		t.Fatalf("expected cardParameters in response")
	}

	items, ok := itemsRaw.([]any)
	if !ok {
		t.Fatalf("expected cardParameters array, got %T", itemsRaw)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 exact-match card parameters, got %d", len(items))
	}

	if body["specialTrainingPower1BonusFixed"] != float64(300) {
		t.Fatalf("expected special training bonus from card record, got %v", body["specialTrainingPower1BonusFixed"])
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
				"releaseconditions": {
					"7": {
						"id":                   7,
						"releaseConditionType": "card_episode",
						"sentence":             "Unlock Side Story",
					},
				},
			},
		},
		searchMatches: []masterdata.SearchMatch{
			{Item: map[string]any{"id": 1, "cardId": 1001, "episodeNo": 1, "releaseConditionId": 7}},
			{Item: map[string]any{"id": 2, "cardId": 1001, "episodeNo": 2, "releaseConditionId": 7}},
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

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}
	releaseConditionRaw, ok := first["releaseCondition"]
	if !ok {
		t.Fatalf("expected releaseCondition in first episode")
	}
	releaseCondition, ok := releaseConditionRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected releaseCondition object, got %T", releaseConditionRaw)
	}
	if releaseCondition["id"] != float64(7) {
		t.Fatalf("expected releaseCondition.id=7, got %v", releaseCondition["id"])
	}
	if _, exists := first["releaseConditionId"]; exists {
		t.Fatalf("expected releaseConditionId removed from response")
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

func TestCardEventsByIDEndpointReturnsEventCards(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"1001": {
						"id":             1001,
						"characterId":    3,
						"attr":           "cool",
						"supportUnit":    "none",
						"cardRarityType": "rarity_3",
					},
				},
				"events": {
					"143": {
						"id":              143,
						"name":            "that day, the sky was far away",
						"eventType":       "marathon",
						"assetbundleName": "event_201",
						"startAt":         1700000000000,
						"aggregateAt":     1700100000000,
						"closedAt":        1700200000000,
					},
					"202": {
						"id":              202,
						"name":            "Walk on and on",
						"eventType":       "cheerful_carnival",
						"assetbundleName": "event_202",
						"startAt":         1700300000000,
						"aggregateAt":     1700400000000,
						"closedAt":        1700500000000,
					},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"eventdeckbonuses": {
				{"eventId": 143, "gameCharacterUnitId": 3, "bonusRate": 25},
				{"eventId": 143, "gameCharacterUnitId": 3, "cardAttr": "mysterious", "bonusRate": 50},
				{"eventId": 202, "cardAttr": "cool", "bonusRate": 20},
			},
			"gamecharacterunits": {
				{"id": 3, "gameCharacterId": 3, "unit": "light_sound"},
			},
		},
		searchMatches: []masterdata.SearchMatch{
			{Item: map[string]any{"eventId": 143, "cardId": 1001, "bonusRate": 0, "isDisplayCardStory": true}},
			{Item: map[string]any{"eventId": 202, "cardId": 1001, "bonusRate": 30}},
		},
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/events", cardHandler.EventsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/events", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	if cache.lastSearch.entity != "eventcards" {
		t.Fatalf("expected search entity eventcards, got %s", cache.lastSearch.entity)
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

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}
	if first["eventId"] != float64(143) {
		t.Fatalf("expected first eventId=143, got %v", first["eventId"])
	}
	if first["cardId"] != float64(1001) {
		t.Fatalf("expected first cardId=1001, got %v", first["cardId"])
	}
	if first["bonusRate"] != float64(0) {
		t.Fatalf("expected first bonusRate=0, got %v", first["bonusRate"])
	}
	if first["isDisplayCardStory"] != true {
		t.Fatalf("expected first isDisplayCardStory=true, got %v", first["isDisplayCardStory"])
	}
	if first["finalBonusRateMin"] != float64(25) {
		t.Fatalf("expected first finalBonusRateMin=25, got %v", first["finalBonusRateMin"])
	}
	if first["finalBonusRateMax"] != float64(30) {
		t.Fatalf("expected first finalBonusRateMax=30, got %v", first["finalBonusRateMax"])
	}

	eventRaw, ok := first["event"]
	if !ok {
		t.Fatalf("expected event in first item")
	}
	event, ok := eventRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected event object, got %T", eventRaw)
	}
	if event["id"] != float64(143) {
		t.Fatalf("expected event.id=143, got %v", event["id"])
	}
	if event["name"] != "that day, the sky was far away" {
		t.Fatalf("expected event.name, got %v", event["name"])
	}
	if event["eventType"] != "marathon" {
		t.Fatalf("expected event.eventType=marathon, got %v", event["eventType"])
	}
	if event["assetbundleName"] != "event_201" {
		t.Fatalf("expected event.assetbundleName=event_201, got %v", event["assetbundleName"])
	}
	if event["startAt"] != float64(1700000000000) {
		t.Fatalf("expected event.startAt, got %v", event["startAt"])
	}
	if event["aggregateAt"] != float64(1700100000000) {
		t.Fatalf("expected event.aggregateAt, got %v", event["aggregateAt"])
	}
	if event["closedAt"] != float64(1700200000000) {
		t.Fatalf("expected event.closedAt, got %v", event["closedAt"])
	}

	second, ok := items[1].(map[string]any)
	if !ok {
		t.Fatalf("expected second item object, got %T", items[1])
	}
	secondEventRaw, ok := second["event"]
	if !ok {
		t.Fatalf("expected event in second item")
	}
	secondEvent, ok := secondEventRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected second event object, got %T", secondEventRaw)
	}
	if secondEvent["id"] != float64(202) {
		t.Fatalf("expected second event.id=202, got %v", secondEvent["id"])
	}
	if second["finalBonusRateMin"] != float64(50) {
		t.Fatalf("expected second finalBonusRateMin=50, got %v", second["finalBonusRateMin"])
	}
	if second["finalBonusRateMax"] != float64(55) {
		t.Fatalf("expected second finalBonusRateMax=55, got %v", second["finalBonusRateMax"])
	}
}

func TestCardEventsByIDEndpointComputesVirtualSingerBonusRange(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"2101": {
						"id":             2101,
						"characterId":    21,
						"attr":           "cool",
						"supportUnit":    "none",
						"cardRarityType": "rarity_4",
					},
				},
				"events": {
					"143": {"id": 143, "name": "vs event"},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"eventdeckbonuses": {
				{"eventId": 143, "gameCharacterUnitId": 27, "bonusRate": 25},
			},
			"gamecharacterunits": {
				{"id": 27, "gameCharacterId": 21, "unit": "light_sound"},
			},
		},
		searchMatches: []masterdata.SearchMatch{
			{Item: map[string]any{"eventId": 143, "cardId": 2101, "bonusRate": 0}},
		},
	}

	cardHandler := newReadyCardHandler(cache)
	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/events", cardHandler.EventsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/2101/events", nil)
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
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected item object, got %T", items[0])
	}
	if item["finalBonusRateMin"] != float64(35) {
		t.Fatalf("expected finalBonusRateMin=35, got %v", item["finalBonusRateMin"])
	}
	if item["finalBonusRateMax"] != float64(50) {
		t.Fatalf("expected finalBonusRateMax=50, got %v", item["finalBonusRateMax"])
	}
}

func TestCardEventsByIDEndpointUsesEventPeriodMasterRankBonus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"4001": {
						"id":             4001,
						"characterId":    17,
						"attr":           "happy",
						"supportUnit":    "none",
						"cardRarityType": "rarity_4",
					},
				},
				"events": {
					"35": {"id": 35, "name": "old event"},
					"36": {"id": 36, "name": "next event"},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"eventdeckbonuses": {
				{"eventId": 35, "gameCharacterUnitId": 17, "cardAttr": "happy", "bonusRate": 50},
				{"eventId": 36, "gameCharacterUnitId": 17, "cardAttr": "happy", "bonusRate": 50},
			},
			"gamecharacterunits": {
				{"id": 17, "gameCharacterId": 17, "unit": "school_refusal"},
			},
		},
		searchMatches: []masterdata.SearchMatch{
			{Item: map[string]any{"eventId": 35, "cardId": 4001, "bonusRate": 0}},
			{Item: map[string]any{"eventId": 36, "cardId": 4001, "bonusRate": 0}},
		},
	}

	cardHandler := newReadyCardHandler(cache)
	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/events", cardHandler.EventsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/4001/events", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", body["items"])
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	oldEvent, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}
	if oldEvent["finalBonusRateMin"] != float64(50) || oldEvent["finalBonusRateMax"] != float64(50) {
		t.Fatalf("expected event 35 range 50-50, got %v-%v", oldEvent["finalBonusRateMin"], oldEvent["finalBonusRateMax"])
	}

	nextEvent, ok := items[1].(map[string]any)
	if !ok {
		t.Fatalf("expected second item object, got %T", items[1])
	}
	if nextEvent["finalBonusRateMin"] != float64(50) || nextEvent["finalBonusRateMax"] != float64(60) {
		t.Fatalf("expected event 36 range 50-60, got %v-%v", nextEvent["finalBonusRateMin"], nextEvent["finalBonusRateMax"])
	}
}

func TestCardEventsByIDEndpointFiltersNonExactCardIDMatches(t *testing.T) {
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
			{Item: map[string]any{"eventId": 201, "cardId": 1001, "bonusRate": 50}},
			{Item: map[string]any{"eventId": 202, "cardId": 10010, "bonusRate": 60}},
			{Item: map[string]any{"eventId": 203, "cardId": "1001-extra", "bonusRate": 70}},
		},
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/events", cardHandler.EventsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/events", nil)
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

func TestCardEventsByIDEndpointReturnsEmptyItemsWhenNoEvents(t *testing.T) {
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
		searchMatches: nil,
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/events", cardHandler.EventsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/events", nil)
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
	if len(items) != 0 {
		t.Fatalf("expected empty items array, got %d items", len(items))
	}
}

func TestCardEventsByIDEndpointCardNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"9999": {
						"id": 9999,
					},
				},
			},
		},
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/events", cardHandler.EventsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/events", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 when card not found, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	errorRaw, ok := body["error"]
	if !ok {
		t.Fatalf("expected error in response")
	}
	errorObj, ok := errorRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %T", errorRaw)
	}
	if errorObj["code"] != "CARD_NOT_FOUND" {
		t.Fatalf("expected error code CARD_NOT_FOUND, got %v", errorObj["code"])
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
	router.GET("/api/v1/cards/:region/:id/events", cardHandler.EventsByID)
	router.GET("/api/v1/cards/:region/list", cardHandler.List)

	testCases := []string{
		"/api/v1/cards/jp/1001",
		"/api/v1/cards/jp/1001/params",
		"/api/v1/cards/jp/1001/episodes",
		"/api/v1/cards/jp/1001/events",
		"/api/v1/cards/jp/list?page=1&page_size=20",
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

func TestCardHandler_GachaByID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeCardHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"cards": {
					"1001": {"id": 1001, "prefix": "Test Card"},
					"3000": {"id": 3000, "prefix": "No Pickup Card"},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"gachas": {
				{
					"id": 10, "name": "First Gacha", "assetbundleName": "ab_gacha_10",
					"gachaPickups": []any{
						map[string]any{"gachaId": 10, "cardId": 1001},
						map[string]any{"gachaId": 10, "cardId": 2000},
					},
				},
				{
					"id": 20, "name": "Second Gacha", "assetbundleName": "ab_gacha_20",
					"gachaPickups": []any{
						map[string]any{"gachaId": 20, "cardId": 1001},
					},
				},
				{
					"id": 30, "name": "Unrelated Gacha", "assetbundleName": "ab_gacha_30",
					"gachaPickups": []any{
						map[string]any{"gachaId": 30, "cardId": 9999},
					},
				},
				{
					"id": 40, "name": "No Pickups Gacha", "assetbundleName": "ab_gacha_40",
				},
			},
		},
	}

	cardHandler := newReadyCardHandler(cache)

	router := gin.New()
	router.GET("/api/v1/cards/:region/:id/gachas", cardHandler.GachaByID)

	t.Run("success - card has pickup gachas", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/gachas", nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.Code)
		}

		var body map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		gachasRaw, ok := body["gachas"]
		if !ok {
			t.Fatalf("expected gachas in response")
		}
		gachas, ok := gachasRaw.([]any)
		if !ok {
			t.Fatalf("expected gachas array, got %T", gachasRaw)
		}
		if len(gachas) != 2 {
			t.Fatalf("expected 2 gachas, got %d", len(gachas))
		}

		first, ok := gachas[0].(map[string]any)
		if !ok {
			t.Fatalf("expected first gacha object, got %T", gachas[0])
		}
		if first["id"] != float64(10) {
			t.Fatalf("expected first gacha id=10, got %v", first["id"])
		}
		if first["name"] != "First Gacha" {
			t.Fatalf("expected first gacha name, got %v", first["name"])
		}
		if first["assetbundleName"] != "ab_gacha_10" {
			t.Fatalf("expected first gacha assetbundleName, got %v", first["assetbundleName"])
		}

		second, ok := gachas[1].(map[string]any)
		if !ok {
			t.Fatalf("expected second gacha object, got %T", gachas[1])
		}
		if second["id"] != float64(20) {
			t.Fatalf("expected second gacha id=20, got %v", second["id"])
		}
	})

	t.Run("success - card has no pickup gachas", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/3000/gachas", nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.Code)
		}

		var body map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		gachasRaw, ok := body["gachas"]
		if !ok {
			t.Fatalf("expected gachas in response")
		}
		gachas, ok := gachasRaw.([]any)
		if !ok {
			t.Fatalf("expected gachas array, got %T", gachasRaw)
		}
		if len(gachas) != 0 {
			t.Fatalf("expected 0 gachas, got %d", len(gachas))
		}
	})

	t.Run("card not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/9999/gachas", nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		if resp.Code != http.StatusNotFound {
			t.Fatalf("expected status 404, got %d", resp.Code)
		}
	})
}

func assertResponsePaginationTotal(t *testing.T, bodyBytes []byte, expected float64) {
	t.Helper()

	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	paginationRaw, ok := body["pagination"]
	if !ok {
		t.Fatalf("expected pagination in response")
	}
	pagination, ok := paginationRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected pagination object, got %T", paginationRaw)
	}
	if pagination["total"] != expected {
		t.Fatalf("expected pagination.total=%v, got %v", expected, pagination["total"])
	}
}
