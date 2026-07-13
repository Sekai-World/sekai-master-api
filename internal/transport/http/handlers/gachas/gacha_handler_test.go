package gachas

import (
	"context"
	"encoding/json"
	"errors"
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
	listAllCalls []string
	searchCalls  int
	listAllErr   error
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
	cache.listAllCalls = append(cache.listAllCalls, strings.ToLower(strings.TrimSpace(entity)))
	if cache.listAllErr != nil {
		return nil, cache.listAllErr
	}
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
	cache.searchCalls++
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
	router.GET("/api/v1/gachas/:region/:id/rate-choice-wishes", handler.RateChoiceWishesByID)
	router.GET("/api/v1/gachas/:region/list", handler.List)
	router.GET("/api/v1/gachas/:region/:id", handler.ByID)

	testCases := []struct {
		name string
		path string
	}{
		{name: "by id", path: "/api/v1/gachas/jp/10"},
		{name: "list", path: "/api/v1/gachas/jp/list?page=1&page_size=20"},
		{name: "rate choice wishes", path: "/api/v1/gachas/jp/10/rate-choice-wishes"},
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
	router.GET("/api/v1/gachas/:region/:id/rate-choice-wishes", handler.RateChoiceWishesByID)
	router.GET("/api/v1/gachas/:region/list", handler.List)
	router.GET("/api/v1/gachas/:region/:id", handler.ByID)

	testCases := []struct {
		name string
		path string
	}{
		{name: "by id", path: "/api/v1/gachas/jp/10"},
		{name: "list", path: "/api/v1/gachas/jp/list?page=1&page_size=20"},
		{name: "rate choice wishes", path: "/api/v1/gachas/jp/10/rate-choice-wishes"},
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

func TestRateChoiceWishesEndpointFiltersProjectsAndSortsPersistedRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeGachaHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gachas": {
					"100": {
						"id":                         json.Number("100"),
						"rateChoiceGachaWishGroupId": json.Number("42"),
					},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"gachas": {
					{"id": 10, "name": "First Gacha", "gachaType": "normal"},
				},
				"ratechoicegachawishes": {
					{"id": float64(10), "groupId": float64(42), "lotteryType": "ten", "selectCount": float64(2), "seq": float64(2)},
					{"id": json.Number("2"), "groupId": int64(42), "lotteryType": "two", "selectCount": json.Number("1"), "seq": json.Number("1")},
					{"id": int(3), "groupId": uint64(42), "lotteryType": "three", "selectCount": int(4), "seq": int(1)},
					{"id": int(1), "groupId": int(7), "lotteryType": "excluded", "selectCount": int(9), "seq": int(0)},
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
	router.GET("/api/v1/gachas/:region/:id/rate-choice-wishes", handler.RateChoiceWishesByID)
	router.GET("/api/v1/gachas/:region/:id", handler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gachas/jp/100/rate-choice-wishes", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		GachaID                    any              `json:"gachaId"`
		RateChoiceGachaWishGroupID any              `json:"rateChoiceGachaWishGroupId"`
		Items                      []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.GachaID != float64(100) {
		t.Fatalf("expected gachaId 100, got %v", body.GachaID)
	}
	if body.RateChoiceGachaWishGroupID != float64(42) {
		t.Fatalf("expected rateChoiceGachaWishGroupId 42, got %v", body.RateChoiceGachaWishGroupID)
	}
	if len(body.Items) != 3 {
		t.Fatalf("expected 3 matching wishes, got %d", len(body.Items))
	}

	for index, expectedID := range []float64{2, 3, 10} {
		if body.Items[index]["id"] != expectedID {
			t.Fatalf("expected item %d id %v, got %v", index, expectedID, body.Items[index]["id"])
		}
		if len(body.Items[index]) != 5 {
			t.Fatalf("expected projected item %d to contain 5 fields, got %v", index, body.Items[index])
		}
	}
	if body.Items[0]["lotteryType"] != "two" || body.Items[1]["lotteryType"] != "three" || body.Items[2]["lotteryType"] != "ten" {
		t.Fatalf("unexpected item order: %v", body.Items)
	}
	if cache.searchCalls != 0 {
		t.Fatalf("expected no search calls, got %d", cache.searchCalls)
	}
}

func TestRateChoiceWishesEndpointReturnsEmptyItemsWhenGachaHasNoConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeGachaHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gachas": {
					"100": {"id": 100},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"ratechoicegachawishes": {
					{"id": 1, "groupId": 42},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"gachas": true},
		},
	}

	handler := newReadyGachaHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gachas/:region/:id/rate-choice-wishes", handler.RateChoiceWishesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gachas/jp/100/rate-choice-wishes", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		RateChoiceGachaWishGroupID any              `json:"rateChoiceGachaWishGroupId"`
		Items                      []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.RateChoiceGachaWishGroupID != nil {
		t.Fatalf("expected null rateChoiceGachaWishGroupId, got %v", body.RateChoiceGachaWishGroupID)
	}
	if body.Items == nil || len(body.Items) != 0 {
		t.Fatalf("expected empty items array, got %v", body.Items)
	}
	if len(cache.listAllCalls) != 0 {
		t.Fatalf("expected no rate choice wish query without configuration, got %v", cache.listAllCalls)
	}
}

func TestRateChoiceWishesEndpointReturnsNotFoundWhenGachaIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeGachaHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {"gachas": {}},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"gachas": true},
		},
	}

	handler := newReadyGachaHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gachas/:region/:id/rate-choice-wishes", handler.RateChoiceWishesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gachas/jp/100/rate-choice-wishes", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Error.Code != "GACHA_NOT_FOUND" {
		t.Fatalf("expected GACHA_NOT_FOUND, got %q", body.Error.Code)
	}
}

func TestRateChoiceWishesEndpointReturnsQueryErrorWhenListAllFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeGachaHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gachas": {
					"100": {"id": 100, "rateChoiceGachaWishGroupId": 42},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"gachas": true},
		},
		listAllErr: errors.New("redis unavailable"),
	}

	handler := newReadyGachaHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gachas/:region/:id/rate-choice-wishes", handler.RateChoiceWishesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gachas/jp/100/rate-choice-wishes", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Error.Code != "GACHA_QUERY_ERROR" {
		t.Fatalf("expected GACHA_QUERY_ERROR, got %q", body.Error.Code)
	}
}

func TestRateChoiceWishesEndpointSkipsMalformedProjectedValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeGachaHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gachas": {
					"100": {"id": 100, "rateChoiceGachaWishGroupId": "42"},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"ratechoicegachawishes": {
					{"id": "1", "groupId": 42.0, "lotteryType": "valid", "selectCount": "2", "seq": json.Number("1")},
					{"id": "2", "groupId": "42", "lotteryType": "missing-count", "seq": 2},
					{"id": "3", "groupId": "42", "lotteryType": "fractional-count", "selectCount": "2.5", "seq": 3},
					{"id": "4", "groupId": "42", "lotteryType": 99, "selectCount": 4, "seq": 4},
					{"id": "5", "groupId": "42", "lotteryType": "overflow-seq", "selectCount": 5, "seq": "999999999999999999999999999"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"gachas": true},
		},
	}

	handler := newReadyGachaHandler(cache)
	router := gin.New()
	router.GET("/api/v1/gachas/:region/:id/rate-choice-wishes", handler.RateChoiceWishesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gachas/jp/100/rate-choice-wishes", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Items []struct {
			ID          any    `json:"id"`
			LotteryType string `json:"lotteryType"`
			SelectCount int    `json:"selectCount"`
			Seq         int    `json:"seq"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("expected one valid item, got %d: %s", len(body.Items), resp.Body.String())
	}
	if body.Items[0].ID != "1" || body.Items[0].LotteryType != "valid" || body.Items[0].SelectCount != 2 || body.Items[0].Seq != 1 {
		t.Fatalf("unexpected valid item: %+v", body.Items[0])
	}
}
