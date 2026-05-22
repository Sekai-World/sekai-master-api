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
