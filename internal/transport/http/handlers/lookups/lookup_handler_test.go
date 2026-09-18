package lookups

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/usecase"
)

type fakeLookupCache struct {
	byID         map[string]map[string]map[string]map[string]any
	listByEntity map[string]map[string][]map[string]any
	hasRecords   map[string]map[string]bool
	hasIndex     bool
	hasIndexSet  bool
}

type fakeLookupStatusStore struct {
	statuses []masterdata.SyncStatus
}

func newReadyLookupHandler(cache *fakeLookupCache) *LookupHandler {
	statusStore := &fakeLookupStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
			{Region: "en", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewLookupHandler(syncUsecase)
}

func serveLookupRequest(t *testing.T, router *gin.Engine, method string, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, target, nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func (store *fakeLookupStatusStore) Save(_ context.Context, _ masterdata.SyncStatus) error {
	return nil
}

func (store *fakeLookupStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	return store.statuses, nil
}

func (cache *fakeLookupCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (cache *fakeLookupCache) GetByID(_ context.Context, region string, entity string, id string) (map[string]any, bool, error) {
	regionData, ok := cache.byID[strings.ToLower(strings.TrimSpace(region))]
	if !ok {
		return nil, false, nil
	}
	entityData, ok := regionData[strings.ToLower(strings.TrimSpace(entity))]
	if !ok {
		return nil, false, nil
	}
	record, ok := entityData[id]
	if !ok {
		return nil, false, nil
	}
	return record, true, nil
}

func (cache *fakeLookupCache) ListAll(_ context.Context, region string, entity string) ([]map[string]any, error) {
	regionData, ok := cache.listByEntity[strings.ToLower(strings.TrimSpace(region))]
	if !ok {
		return []map[string]any{}, nil
	}
	records := regionData[strings.ToLower(strings.TrimSpace(entity))]
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		copied := make(map[string]any, len(record))
		for key, value := range record {
			copied[key] = value
		}
		items = append(items, copied)
	}
	return items, nil
}

func (cache *fakeLookupCache) ListByPage(_ context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error) {
	items, err := cache.ListAll(context.Background(), region, entity)
	if err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []map[string]any{}, len(items), nil
	}
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], len(items), nil
}

func (cache *fakeLookupCache) Search(_ context.Context, _, _, _ string, _ []string, _ int) ([]masterdata.SearchMatch, error) {
	return []masterdata.SearchMatch{}, nil
}

func (cache *fakeLookupCache) HasEntityRecords(_ context.Context, region string, entity string) (bool, error) {
	if cache.hasRecords == nil {
		return false, nil
	}
	return cache.hasRecords[strings.ToLower(strings.TrimSpace(region))][strings.ToLower(strings.TrimSpace(entity))], nil
}

func (cache *fakeLookupCache) HasRegionIndex(_ string) bool {
	if !cache.hasIndexSet {
		return true
	}
	return cache.hasIndex
}

func TestUnitProfilesByUnitEndpointReturnsRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"unitprofiles": {
					{
						"id":        10,
						"unit":      "idol",
						"unitName":  "MORE MORE JUMP！",
						"colorCode": "#88dd44",
					},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/unitProfiles/:region/:unit", handler.UnitProfilesByUnit)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/unitProfiles/jp/idol", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if body["unit"] != "idol" {
		t.Fatalf("expected unit=idol, got %v", body["unit"])
	}
	if body["unitName"] != "MORE MORE JUMP！" {
		t.Fatalf("expected unitName preserved, got %v", body["unitName"])
	}
}

func TestUnitProfilesAvailableRegionsByUnitEndpointReturnsAvailableRegionsWithData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"unitprofiles": {
					{"id": 1, "unit": "idol", "unitName": "MORE MORE JUMP！", "colorCode": "#88dd44"},
				},
			},
			"en": {
				"unitprofiles": {
					{"id": 2, "unit": "idol", "unitName": "MORE MORE JUMP!", "colorCode": "#88dd44"},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/unitProfiles/regions/:unit/availability", handler.UnitProfilesAvailableRegionsByUnit)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/unitProfiles/regions/idol/availability", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body struct {
		Regions []string `json:"regions"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	sort.Strings(body.Regions)
	expected := []string{"en", "jp"}
	if !reflect.DeepEqual(body.Regions, expected) {
		t.Fatalf("expected regions %v, got %v", expected, body.Regions)
	}
}

func TestUnitProfileMembersEndpointReturnsNormalizedUnitMembersInGameCharacterIDOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"unitprofiles": {{"id": 1, "unit": "idol"}},
				"gamecharacterunits": {
					{"id": 20, "gameCharacterId": 12, "unit": " IDOL ", "colorCode": "#2"},
					{"id": 10, "gameCharacterId": 6, "unit": "idol", "colorCode": "#1"},
					{"id": 30, "gameCharacterId": 99, "unit": "idol", "colorCode": "#orphan"},
				},
				"gamecharacters": {
					{"id": 6, "resourceId": 106, "firstName": "Kiritani", "givenName": "Haruka", "firstNameEnglish": "HARUKA", "givenNameEnglish": "KIRITANI"},
					{"id": 12, "resourceId": 112, "firstName": "Aoyagi", "givenName": "Toya"},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/unitProfiles/:region/:unit/members", handler.UnitProfileMembers)

	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/unitProfiles/jp/%20idol%20/members")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("expected orphan membership to be excluded, got %#v", body.Items)
	}
	if body.Items[0]["gameCharacterId"] != float64(6) || body.Items[1]["gameCharacterId"] != float64(12) {
		t.Fatalf("expected ascending gameCharacterId order, got %#v", body.Items)
	}
	if body.Items[0]["id"] != float64(10) || body.Items[0]["resourceId"] != float64(106) || body.Items[0]["firstNameEnglish"] != "HARUKA" {
		t.Fatalf("expected joined frontend fields, got %#v", body.Items[0])
	}
}

func TestUnitProfileMembersEndpointReturnsNotFoundForUnknownUnit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {"unitprofiles": {{"id": 1, "unit": "idol"}}},
		},
	}
	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/unitProfiles/:region/:unit/members", handler.UnitProfileMembers)

	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/unitProfiles/jp/street/members")
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}
}

func TestGameCharacterUnitsListEndpointSupportsSorting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"gamecharacterunits": {
					{"id": 2, "gameCharacterId": 11, "unit": "street", "colorCode": "#ff7722"},
					{"id": 1, "gameCharacterId": 6, "unit": "idol", "colorCode": "#99ccff"},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gameCharacterUnits/:region/list", handler.GameCharacterUnitsList)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gameCharacterUnits/jp/list?sort_by=gameCharacterId&sort_order=asc&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	items, ok := body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected 2 items, got %T len=%d", body["items"], len(items))
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}
	if first["id"] != float64(1) {
		t.Fatalf("expected sorted first id=1, got %v", first["id"])
	}
}

func TestWorldBloomsListEndpointReturnsPaginatedRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"worldblooms": {
					{"id": 1, "eventId": 101, "startAt": 100},
					{"id": 2, "eventId": 102, "startAt": 200},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/worldBlooms/:region/list", handler.WorldBloomsList)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/worldBlooms/jp/list?page=2&page_size=1", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Items      []map[string]any `json:"items"`
		Pagination struct {
			Page       int  `json:"page"`
			PageSize   int  `json:"page_size"`
			Total      int  `json:"total"`
			TotalPages int  `json:"total_pages"`
			HasNext    bool `json:"has_next"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0]["id"] != float64(2) {
		t.Fatalf("expected page 2 to contain world bloom id=2, got %#v", body.Items)
	}
	if body.Pagination.Page != 2 || body.Pagination.PageSize != 1 || body.Pagination.Total != 2 || body.Pagination.TotalPages != 2 || body.Pagination.HasNext {
		t.Fatalf("unexpected pagination: %+v", body.Pagination)
	}
}

type lookupListResponse struct {
	Items      []map[string]any `json:"items"`
	Pagination struct {
		Total int `json:"total"`
	} `json:"pagination"`
}

func fetchLookupListResponse(t *testing.T, router *gin.Engine, path string) lookupListResponse {
	t.Helper()
	resp := serveLookupRequest(t, router, http.MethodGet, path)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 for %s, got %d: %s", path, resp.Code, resp.Body.String())
	}
	var body lookupListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return body
}

type lookupNumericFilterCase struct {
	name         string
	resource     string
	entity       string
	endpoint     func(*LookupHandler) gin.HandlerFunc
	field        string
	records      []map[string]any
	singleQuery  string
	singleValues []float64
	multiQuery   string
	multiValues  []float64
	invalidQuery string
}

func assertLookupFilterItems(t *testing.T, router *gin.Engine, path string, field string, expected []float64) {
	t.Helper()
	body := fetchLookupListResponse(t, router, path)
	if len(body.Items) != len(expected) {
		t.Fatalf("expected %d items for %s, got %#v", len(expected), path, body.Items)
	}
	for index, value := range expected {
		if body.Items[index][field] != value {
			t.Fatalf("expected item %d %s=%v, got %#v", index, field, value, body.Items)
		}
	}
}

func TestLookupListEndpointsFilterByNumericID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []lookupNumericFilterCase{
		{
			name:     "event stories by event id",
			resource: "eventStories",
			entity:   "eventstories",
			endpoint: func(handler *LookupHandler) gin.HandlerFunc { return handler.EventStoriesList },
			field:    "eventId",
			records: []map[string]any{
				{"id": 1, "eventId": 34, "eventStoryEpisodes": []any{}},
				{"id": 2, "eventId": 35, "eventStoryEpisodes": []any{}},
				{"id": 3, "eventId": 36, "eventStoryEpisodes": []any{}},
			},
			singleQuery:  "?spoiler=true&event_id=34",
			singleValues: []float64{34},
			multiQuery:   "?spoiler=true&event_id=34,36",
			multiValues:  []float64{34, 36},
			invalidQuery: "?spoiler=true&event_id=abc",
		},
		{
			name:     "card episodes by card id",
			resource: "cardEpisodes",
			entity:   "cardepisodes",
			endpoint: func(handler *LookupHandler) gin.HandlerFunc { return handler.CardEpisodesList },
			field:    "cardId",
			records: []map[string]any{
				{"id": 2101, "cardId": 3001, "seq": 1, "title": "EP1"},
				{"id": 2102, "cardId": 3001, "seq": 2, "title": "EP2"},
				{"id": 2111, "cardId": 3002, "seq": 1, "title": "Other EP"},
			},
			singleQuery:  "?spoiler=true&card_id=3002",
			singleValues: []float64{3002},
			multiQuery:   "?spoiler=true&card_id=3001,3002",
			multiValues:  []float64{3001, 3001, 3002},
			invalidQuery: "?spoiler=true&card_id=abc",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cache := &fakeLookupCache{
				listByEntity: map[string]map[string][]map[string]any{
					"jp": {testCase.entity: testCase.records},
				},
			}
			handler := newReadyLookupHandler(cache)
			router := gin.New()
			basePath := "/api/v1/" + testCase.resource
			router.GET(basePath+"/:region/list", testCase.endpoint(handler))
			requestPath := basePath + "/jp/list"

			assertLookupFilterItems(t, router, requestPath+testCase.singleQuery, testCase.field, testCase.singleValues)
			assertLookupFilterItems(t, router, requestPath+testCase.multiQuery, testCase.field, testCase.multiValues)

			badResponse := serveLookupRequest(t, router, http.MethodGet, requestPath+testCase.invalidQuery)
			if badResponse.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for invalid filter, got %d: %s", badResponse.Code, badResponse.Body.String())
			}
		})
	}
}

func TestGameCharactersAvailableRegionsByIDEndpointReturnsAvailableRegionsWithData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gamecharacters": {
					"6": {"id": 6, "firstName": "Kiritani", "givenName": "Haruka"},
				},
			},
			"en": {
				"gamecharacters": {
					"6": {"id": 6, "firstName": "Kiritani", "givenName": "Haruka"},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gameCharacters/regions/:id/availability", handler.GameCharactersAvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gameCharacters/regions/6/availability", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body struct {
		Regions []string `json:"regions"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	sort.Strings(body.Regions)
	expected := []string{"en", "jp"}
	if !reflect.DeepEqual(body.Regions, expected) {
		t.Fatalf("expected regions %v, got %v", expected, body.Regions)
	}
}

func TestGameCharactersByIDEndpointPreservesProfileFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gamecharacters": {
					"1": {
						"id":               1,
						"seq":              1,
						"resourceId":       1,
						"firstName":        "星乃",
						"givenName":        "一歌",
						"firstNameRuby":    "ほしの",
						"givenNameRuby":    "いちか",
						"firstNameEnglish": "HOSHINO",
						"givenNameEnglish": "ICHIKA",
						"gender":           "female",
						"unit":             "light_sound",
						"height":           161,
						"supportUnitType":  "none",
					},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gameCharacters/:region/:id", handler.GameCharactersByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gameCharacters/jp/1", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	expected := map[string]any{
		"id":               float64(1),
		"seq":              float64(1),
		"resourceId":       float64(1),
		"firstName":        "星乃",
		"givenName":        "一歌",
		"firstNameRuby":    "ほしの",
		"givenNameRuby":    "いちか",
		"firstNameEnglish": "HOSHINO",
		"givenNameEnglish": "ICHIKA",
		"gender":           "female",
		"unit":             "light_sound",
		"height":           float64(161),
		"supportUnitType":  "none",
	}
	for field, value := range expected {
		if body[field] != value {
			t.Errorf("expected %s=%v, got %v", field, value, body[field])
		}
	}
}

func TestGameCharacterProfilesByIDEndpointReturnsRequestedCharacterProfileOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"characterprofiles": {
					{
						"characterId":    6,
						"birthday":       "December 5",
						"characterVoice": "野口瑠璃子",
						"favoriteFood":   "うどん",
						"hatedFood":      "なし",
						"height":         "161cm",
						"hobby":          "散歩",
						"introduction":   "プロフィール紹介",
						"school":         "宮益坂女子学園",
						"schoolYear":     "1-A",
						"specialSkill":   "ピアノ",
						"weak":           "高いところ",
						"scenarioId":     "hidden-scenario-id",
					},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gameCharacters/:region/:id/profile", handler.GameCharacterProfilesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gameCharacters/jp/6/profile", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["birthday"] != "December 5" {
		t.Fatalf("expected birthday to be returned, got %v", body["birthday"])
	}
	if _, found := body["scenarioId"]; found {
		t.Fatal("response must not expose scenarioId")
	}
	if _, found := body["characterId"]; found {
		t.Fatal("response must not expose characterId")
	}
	if len(body) != 11 {
		t.Fatalf("expected exactly 11 profile fields, got %d: %v", len(body), body)
	}
}

func TestGameCharacterProfilesByIDEndpointReturnsNotFoundWhenCharacterProfileIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"characterprofiles": {
					{"characterId": 6, "birthday": "December 5"},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gameCharacters/:region/:id/profile", handler.GameCharacterProfilesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gameCharacters/jp/25/profile", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Error.Code != "GAME_CHARACTER_PROFILE_NOT_FOUND" {
		t.Fatalf("expected GAME_CHARACTER_PROFILE_NOT_FOUND, got %q", body.Error.Code)
	}
}

func TestGameCharacterProfilesByIDEndpointRejectsInvalidIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := newReadyLookupHandler(&fakeLookupCache{})
	router := gin.New()
	router.GET("/api/v1/gameCharacters/:region/:id/profile", handler.GameCharacterProfilesByID)

	for _, id := range []string{"not-a-number", "0", "-1"} {
		t.Run(id, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/gameCharacters/jp/"+id+"/profile", nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
			}

			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if body.Error.Code != "INVALID_REQUEST" {
				t.Fatalf("expected INVALID_REQUEST, got %q", body.Error.Code)
			}
		})
	}
}

func TestGameCharactersListInvalidSortByReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"gamecharacters": {
					{"id": 2, "firstName": "Tenma", "givenName": "Saki"},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gameCharacters/:region/list", handler.GameCharactersList)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gameCharacters/jp/list?sort_by=unknown", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	errorBody, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error body, got %T", body["error"])
	}
	if shared.NormalizeComparableText(errorBody["code"]) != "invalid_request" {
		t.Fatalf("expected INVALID_REQUEST, got %v", errorBody["code"])
	}
}

func TestLookupRecordEndpointsUsePersistedEntityRecordsWhenRuntimeIndexMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gamecharacters": {
					"6": {"id": 6, "firstName": "Kiritani", "givenName": "Haruka"},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"unitprofiles": {
					{"id": 10, "unit": "idol", "unitName": "MORE MORE JUMP！"},
				},
				"gamecharacters": {
					{"id": 6, "firstName": "Kiritani", "givenName": "Haruka"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {
				"unitprofiles":   true,
				"gamecharacters": true,
			},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/unitProfiles/:region/:unit", handler.UnitProfilesByUnit)
	router.GET("/api/v1/gameCharacters/:region/:id", handler.GameCharactersByID)
	router.GET("/api/v1/gameCharacters/:region/list", handler.GameCharactersList)

	testCases := []struct {
		name string
		path string
	}{
		{name: "unit profile by unit", path: "/api/v1/unitProfiles/jp/idol"},
		{name: "generic by id", path: "/api/v1/gameCharacters/jp/6"},
		{name: "generic list", path: "/api/v1/gameCharacters/jp/list?page=1&page_size=20"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestLookupAvailabilityEndpointsUsePersistedRecordsWithoutRuntimeIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gamecharacters": {
					"6": {"id": 6, "firstName": "Kiritani", "givenName": "Haruka"},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"unitprofiles": {
					{"id": 10, "unit": "idol", "unitName": "MORE MORE JUMP！"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {
				"unitprofiles":   true,
				"gamecharacters": true,
			},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/unitProfiles/regions/:unit/availability", handler.UnitProfilesAvailableRegionsByUnit)
	router.GET("/api/v1/gameCharacters/regions/:id/availability", handler.GameCharactersAvailableRegionsByID)

	testCases := []struct {
		name string
		path string
	}{
		{name: "unit profile availability", path: "/api/v1/unitProfiles/regions/idol/availability"},
		{name: "generic by-id availability", path: "/api/v1/gameCharacters/regions/6/availability"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
			}

			var body struct {
				Regions []string `json:"regions"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if testCase.name == "generic by-id availability" {
				if !reflect.DeepEqual(body.Regions, []string{"jp"}) {
					t.Fatalf("expected persisted record region jp without runtime index, got %v", body.Regions)
				}
			} else if len(body.Regions) != 0 {
				t.Fatalf("expected no available regions without a matching persisted record, got %v", body.Regions)
			}
		})
	}
}

func verifyStoryListPage(t *testing.T, router *gin.Engine, path string, expectedTotal int, expectedItem map[string]any) {
	t.Helper()

	body := fetchLookupListResponse(t, router, path)
	if body.Pagination.Total != expectedTotal {
		t.Fatalf("expected total %d, got %d", expectedTotal, body.Pagination.Total)
	}
	if len(body.Items) != expectedTotal {
		t.Fatalf("expected %d items, got %d", expectedTotal, len(body.Items))
	}
	for key, value := range expectedItem {
		if body.Items[0][key] != value {
			t.Fatalf("expected item %s=%v, got %v", key, value, body.Items[0][key])
		}
	}
}

func TestStoryLookupListEndpointsReturnPaginatedRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"unitstories": {
					{"unit": "idol", "seq": 1, "chapters": []any{}},
					{"unit": "mmj", "seq": 2, "chapters": []any{}},
				},
				"eventstories": {
					{"id": 1, "eventId": 34, "eventStoryEpisodes": []any{}},
				},
				"characterprofiles": {
					{"characterId": 1, "scenarioId": "char_1"},
				},
				"cardepisodes": {
					{"id": 10, "cardId": 3, "seq": 1, "title": "EP1"},
				},
				"actionsets": {
					{"id": 100, "areaId": 3, "scenarioId": "area3_set1"},
				},
				"specialstories": {
					{"id": 1, "seq": 1, "title": "Bout for Blessing", "startAt": 1000, "endAt": 2000, "episodes": []any{}},
				},
				"character2ds": {
					{"id": 5, "characterId": 1, "characterType": "game_character"},
				},
				"mobcharacters": {
					{"id": 20, "seq": 1, "name": "Mob"},
				},
				"subgamecharacters": {
					{"id": 30, "seq": 1, "name": "Sub"},
				},
				"unitstoryepisodegroups": {
					{"id": 1, "unit": "piapro", "unitEpisodeCategory": "light_sound", "outline": "arc outline"},
				},
				"areas": {
					{"id": 1, "assetbundleName": "area1", "groupId": 10, "areaType": "reality_world", "name": "スクランブル交差点"},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/unitStories/:region/list", handler.UnitStoriesList)
	router.GET("/api/v1/eventStories/:region/list", handler.EventStoriesList)
	router.GET("/api/v1/characterProfiles/:region/list", handler.CharacterProfilesList)
	router.GET("/api/v1/cardEpisodes/:region/list", handler.CardEpisodesList)
	router.GET("/api/v1/actionSets/:region/list", handler.ActionSetsList)
	router.GET("/api/v1/specialStories/:region/list", handler.SpecialStoriesList)
	router.GET("/api/v1/character2ds/:region/list", handler.Character2DsList)
	router.GET("/api/v1/mobCharacters/:region/list", handler.MobCharactersList)
	router.GET("/api/v1/subGameCharacters/:region/list", handler.SubGameCharactersList)
	router.GET("/api/v1/unitStoryEpisodeGroups/:region/list", handler.UnitStoryEpisodeGroupsList)
	router.GET("/api/v1/areas/:region/list", handler.AreasList)

	testCases := []struct {
		name          string
		path          string
		expectedTotal int
		expectedItem  map[string]any
	}{
		{name: "unit stories", path: "/api/v1/unitStories/jp/list", expectedTotal: 2, expectedItem: map[string]any{"unit": "idol"}},
		{name: "event stories", path: "/api/v1/eventStories/jp/list", expectedTotal: 1, expectedItem: map[string]any{"eventId": float64(34)}},
		{name: "character profiles", path: "/api/v1/characterProfiles/jp/list", expectedTotal: 1, expectedItem: map[string]any{"characterId": float64(1)}},
		{name: "card episodes", path: "/api/v1/cardEpisodes/jp/list", expectedTotal: 1, expectedItem: map[string]any{"cardId": float64(3)}},
		{name: "action sets", path: "/api/v1/actionSets/jp/list", expectedTotal: 1, expectedItem: map[string]any{"areaId": float64(3)}},
		{name: "special stories", path: "/api/v1/specialStories/jp/list", expectedTotal: 1, expectedItem: map[string]any{"title": "Bout for Blessing"}},
		{name: "character 2Ds", path: "/api/v1/character2ds/jp/list", expectedTotal: 1, expectedItem: map[string]any{"characterId": float64(1)}},
		{name: "mob characters", path: "/api/v1/mobCharacters/jp/list", expectedTotal: 1, expectedItem: map[string]any{"name": "Mob"}},
		{name: "sub game characters", path: "/api/v1/subGameCharacters/jp/list", expectedTotal: 1, expectedItem: map[string]any{"name": "Sub"}},
		{name: "unit story episode groups", path: "/api/v1/unitStoryEpisodeGroups/jp/list", expectedTotal: 1, expectedItem: map[string]any{"unitEpisodeCategory": "light_sound"}},
		{name: "areas", path: "/api/v1/areas/jp/list", expectedTotal: 1, expectedItem: map[string]any{"areaType": "reality_world"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			verifyStoryListPage(t, router, testCase.path, testCase.expectedTotal, testCase.expectedItem)
		})
	}
}

func verifyReleaseConditionExpansion(t *testing.T, router *gin.Engine, path string) {
	t.Helper()

	body := fetchLookupListResponse(t, router, path)
	if len(body.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(body.Items))
	}
	item := body.Items[0]
	if _, exposed := item["releaseConditionId"]; exposed {
		t.Fatalf("expected releaseConditionId to be expanded, got %v", item)
	}
	expanded, ok := item["releaseCondition"].(map[string]any)
	if !ok {
		t.Fatalf("expected expanded releaseCondition object, got %v", item["releaseCondition"])
	}
	if expanded["releaseConditionType"] != "story_event" {
		t.Fatalf("expected releaseCondition content from releaseconditions entity, got %v", expanded)
	}
}

func TestStoryLookupListEndpointsExpandTopLevelReleaseCondition(t *testing.T) {
	gin.SetMode(gin.TestMode)

	releaseCondition := map[string]any{"id": 5, "releaseConditionType": "story_event"}
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"releaseconditions": {
					"5": releaseCondition,
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"cardepisodes": {
					{"id": 10, "cardId": 3, "releaseConditionId": 5},
				},
				"actionsets": {
					{"id": 100, "areaId": 3, "releaseConditionId": 5},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/cardEpisodes/:region/list", handler.CardEpisodesList)
	router.GET("/api/v1/actionSets/:region/list", handler.ActionSetsList)

	testCases := []struct {
		name string
		path string
	}{
		{name: "card episodes", path: "/api/v1/cardEpisodes/jp/list"},
		{name: "action sets", path: "/api/v1/actionSets/jp/list"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			verifyReleaseConditionExpansion(t, router, testCase.path)
		})
	}
}

func TestSpecialStoriesListEndpointFiltersUnreleasedByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"specialstories": {
					{"id": 1, "seq": 1, "title": "Released", "startAt": 1000, "endAt": 2000},
					{"id": 2, "seq": 2, "title": "Unreleased", "startAt": 9999999999999, "endAt": 99999999999999},
				},
			},
		},
	}

	handler := newReadyLookupHandler(cache)
	router := gin.New()
	router.GET("/api/v1/specialStories/:region/list", handler.SpecialStoriesList)

	testCases := []struct {
		name          string
		query         string
		expectedTotal int
	}{
		{name: "default filters unreleased", query: "", expectedTotal: 1},
		{name: "spoiler includes unreleased", query: "?spoiler=true", expectedTotal: 2},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			body := fetchLookupListResponse(t, router, "/api/v1/specialStories/jp/list"+testCase.query)
			if len(body.Items) != testCase.expectedTotal {
				t.Fatalf("expected %d items, got %d: %v", testCase.expectedTotal, len(body.Items), body.Items)
			}
			if testCase.expectedTotal == 1 && body.Items[0]["title"] != "Released" {
				t.Fatalf("expected only the released story, got %v", body.Items[0])
			}
		})
	}
}
