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

func TestUnitProfilesAvailableRegionsByUnitEndpointReturnsReadyRegionsWithData(t *testing.T) {
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

func TestGameCharactersAvailableRegionsByIDEndpointReturnsReadyRegionsWithData(t *testing.T) {
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

func TestLookupAvailabilityEndpointsRequireRuntimeIndexWhenOnlyEntityRecordsExist(t *testing.T) {
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
			if len(body.Regions) != 0 {
				t.Fatalf("expected no available regions without runtime index, got %v", body.Regions)
			}
		})
	}
}
