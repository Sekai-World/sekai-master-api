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

type fakeVirtualLiveHandlerCache struct {
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

type fakeVirtualLiveHandlerStatusStore struct {
	statuses []masterdata.SyncStatus
}

func newReadyVirtualLiveHandler(cache *fakeVirtualLiveHandlerCache) *VirtualLiveHandler {
	statusStore := &fakeVirtualLiveHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewVirtualLiveHandler(syncUsecase)
}

func (store *fakeVirtualLiveHandlerStatusStore) Save(_ context.Context, _ masterdata.SyncStatus) error {
	return nil
}

func (store *fakeVirtualLiveHandlerStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	return store.statuses, nil
}

func (cache *fakeVirtualLiveHandlerCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (cache *fakeVirtualLiveHandlerCache) GetByID(_ context.Context, region string, entity string, id string) (map[string]any, bool, error) {
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

func (cache *fakeVirtualLiveHandlerCache) ListAll(_ context.Context, _, _ string) ([]map[string]any, error) {
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

func (cache *fakeVirtualLiveHandlerCache) ListByPage(_ context.Context, _, _ string, _, _ int) ([]map[string]any, int, error) {
	return cache.listItems, cache.listTotal, nil
}

func (cache *fakeVirtualLiveHandlerCache) Search(_ context.Context, region, entity, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	cache.lastSearch.region = region
	cache.lastSearch.entity = entity
	cache.lastSearch.query = query
	cache.lastSearch.fields = append([]string{}, fields...)
	cache.lastSearch.limit = limit
	return cache.searchMatches, nil
}

func TestVirtualLiveByIDEndpointReturnsVirtualLive(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":              501,
						"name":            "after live",
						"assetbundleName": "vl_501",
						"startAt":         1000,
						"endAt":           2000,
						"virtualLiveType": "normal",
						"virtualItems": []any{
							map[string]any{"virtualItemId": 1, "quantity": 2},
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id", handler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if body["id"] != float64(501) {
		t.Fatalf("expected id=501, got %v", body["id"])
	}
	if body["name"] != "after live" {
		t.Fatalf("expected name=after live, got %v", body["name"])
	}
}

func TestVirtualLiveAvailableRegionsByIDEndpointReturnsReadyRegionsWithData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {"id": 501},
				},
			},
			"en": {
				"virtuallives": {
					"501": {"id": 501},
				},
			},
			"tw": {
				"virtuallives": {
					"501": {"id": 501},
				},
			},
		},
	}

	statusStore := &fakeVirtualLiveHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "tw", Status: "running"},
			{Region: "en", Status: "success"},
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	handler := NewVirtualLiveHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/virtualLives/regions/:id/availability", handler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/regions/501/availability", nil)
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

func TestVirtualLiveListEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		listItems: []map[string]any{
			{
				"id":              501,
				"name":            "after live",
				"assetbundleName": "vl_501",
				"virtualLiveType": "normal",
				"virtualItems": []any{
					map[string]any{"virtualItemId": 1, "quantity": 2},
				},
			},
		},
		listTotal: 1,
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list?page=1&page_size=20", nil)
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
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 item, got %T len=%d", itemsRaw, len(items))
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected item object, got %T", items[0])
	}

	if first["name"] != "after live" {
		t.Fatalf("expected first.name=after live, got %v", first["name"])
	}
}

func TestVirtualLiveSearchEndpointDefaultsToNameField(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		searchMatches: []masterdata.SearchMatch{
			{
				Item: map[string]any{
					"id":   501,
					"name": "after live",
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/search", handler.Search)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/search?q=after", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	if cache.lastSearch.region != "jp" {
		t.Fatalf("expected search region jp, got %s", cache.lastSearch.region)
	}
	if cache.lastSearch.entity != "virtuallives" {
		t.Fatalf("expected search entity virtuallives, got %s", cache.lastSearch.entity)
	}
	if cache.lastSearch.query != "after" {
		t.Fatalf("expected search query after, got %s", cache.lastSearch.query)
	}

	expectedFields := []string{"name"}
	if !reflect.DeepEqual(cache.lastSearch.fields, expectedFields) {
		t.Fatalf("expected search fields %v, got %v", expectedFields, cache.lastSearch.fields)
	}
}

func TestVirtualLiveSearchEndpointSupportsTypeField(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		searchMatches: []masterdata.SearchMatch{
			{
				Item: map[string]any{
					"id":              501,
					"name":            "after live",
					"virtualLiveType": "normal",
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/search", handler.Search)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/search?q=normal&field=type", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	expectedFields := []string{"virtualLiveType"}
	if !reflect.DeepEqual(cache.lastSearch.fields, expectedFields) {
		t.Fatalf("expected search fields %v, got %v", expectedFields, cache.lastSearch.fields)
	}
}

func TestVirtualLiveSearchEndpointRejectsInvalidField(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := newReadyVirtualLiveHandler(&fakeVirtualLiveHandlerCache{})

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/search", handler.Search)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/search?q=after&field=bad", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}
