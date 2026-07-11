package gachas

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

type fakeGachaHandlerCache struct {
	byID         map[string]map[string]map[string]map[string]any
	listByEntity map[string]map[string][]map[string]any
	hasRecords   map[string]map[string]bool
	hasIndex     bool
	hasIndexSet  bool
}

type fakeGachaHandlerStatusStore struct {
	statuses []masterdata.SyncStatus
}

func newReadyGachaHandler(cache *fakeGachaHandlerCache) *GachaHandler {
	statusStore := &fakeGachaHandlerStatusStore{
		statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}},
	}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewGachaHandler(syncUsecase)
}

func (store *fakeGachaHandlerStatusStore) Save(_ context.Context, _ masterdata.SyncStatus) error {
	return nil
}

func (store *fakeGachaHandlerStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	return store.statuses, nil
}

func (cache *fakeGachaHandlerCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (cache *fakeGachaHandlerCache) GetByID(_ context.Context, region string, entity string, id string) (map[string]any, bool, error) {
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

func (cache *fakeGachaHandlerCache) ListAll(_ context.Context, region string, entity string) ([]map[string]any, error) {
	regionData, ok := cache.listByEntity[strings.ToLower(strings.TrimSpace(region))]
	if !ok {
		return []map[string]any{}, nil
	}
	return copyGachaTestItems(regionData[strings.ToLower(strings.TrimSpace(entity))]), nil
}

func (cache *fakeGachaHandlerCache) ListByPage(ctx context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error) {
	records, err := cache.ListAll(ctx, region, entity)
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
	if start >= len(records) {
		return []map[string]any{}, len(records), nil
	}
	end := start + pageSize
	if end > len(records) {
		end = len(records)
	}
	return records[start:end], len(records), nil
}

func (cache *fakeGachaHandlerCache) Search(_ context.Context, _, _, _ string, _ []string, _ int) ([]masterdata.SearchMatch, error) {
	return []masterdata.SearchMatch{}, nil
}

func (cache *fakeGachaHandlerCache) HasEntityRecords(_ context.Context, region string, entity string) (bool, error) {
	if cache.hasRecords == nil {
		return false, nil
	}
	return cache.hasRecords[strings.ToLower(strings.TrimSpace(region))][strings.ToLower(strings.TrimSpace(entity))], nil
}

func (cache *fakeGachaHandlerCache) HasRegionIndex(_ string) bool {
	if !cache.hasIndexSet {
		return true
	}
	return cache.hasIndex
}

func copyGachaTestItems(source []map[string]any) []map[string]any {
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

func TestGachaRecordEndpointsUsePersistedEntityRecordsWhenRuntimeIndexMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeGachaHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gachas": {
					"10": {"id": 10, "name": "First Gacha", "gachaType": "normal"},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"gachas": {
					{"id": 10, "name": "First Gacha", "gachaType": "normal"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"gachas": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyGachaHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gachas/:region/:id", handler.ByID)
	router.GET("/api/v1/gachas/:region/list", handler.List)

	testCases := []struct {
		name string
		path string
	}{
		{name: "by id", path: "/api/v1/gachas/jp/10"},
		{name: "list", path: "/api/v1/gachas/jp/list?page=1&page_size=20"},
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

func TestGachaRecordEndpointsPreserveNotReadyResponseContract(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeGachaHandlerCache{
		hasRecords: map[string]map[string]bool{
			"jp": {"gachas": false},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}
	statusStore := &fakeGachaHandlerStatusStore{
		statuses: []masterdata.SyncStatus{},
	}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	handler := NewGachaHandler(syncUsecase)
	router := gin.New()
	router.GET("/api/v1/gachas/:region/:id", handler.ByID)
	router.GET("/api/v1/gachas/:region/list", handler.List)

	testCases := []struct {
		name string
		path string
	}{
		{name: "by id", path: "/api/v1/gachas/jp/10"},
		{name: "list", path: "/api/v1/gachas/jp/list?page=1&page_size=20"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected status 503, got %d: %s", resp.Code, resp.Body.String())
			}

			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if body.Error.Code != "REGION_NOT_READY" {
				t.Fatalf("expected REGION_NOT_READY, got %q", body.Error.Code)
			}
			if body.Error.Message != "master data for this region is not available" {
				t.Fatalf("unexpected error message: %q", body.Error.Message)
			}
		})
	}
}

func TestGachaAvailabilityEndpointRequiresRuntimeIndexWhenOnlyEntityRecordsExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeGachaHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gachas": {
					"10": {"id": 10, "name": "First Gacha"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"gachas": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyGachaHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gachas/regions/:id/availability", handler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gachas/regions/10/availability", nil)
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
}
