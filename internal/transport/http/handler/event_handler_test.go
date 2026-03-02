package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type fakeEventHandlerCache struct {
	byID           map[string]map[string]map[string]map[string]any
	listByEntity   map[string]map[string][]map[string]any
	storeCallCount int
}

type fakeEventHandlerStatusStore struct {
	statuses []masterdata.SyncStatus
}

func newReadyEventHandler(cache *fakeEventHandlerCache) *EventHandler {
	statusStore := &fakeEventHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewEventHandler(syncUsecase)
}

func (store *fakeEventHandlerStatusStore) Save(_ context.Context, _ masterdata.SyncStatus) error {
	return nil
}

func (store *fakeEventHandlerStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	return store.statuses, nil
}

func (cache *fakeEventHandlerCache) StoreRegion(ctx context.Context, region string, payload map[string]any) error {
	_ = ctx

	if cache.listByEntity == nil {
		cache.listByEntity = make(map[string]map[string][]map[string]any)
	}
	if cache.byID == nil {
		cache.byID = make(map[string]map[string]map[string]map[string]any)
	}

	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	if _, ok := cache.listByEntity[normalizedRegion]; !ok {
		cache.listByEntity[normalizedRegion] = make(map[string][]map[string]any)
	}
	if _, ok := cache.byID[normalizedRegion]; !ok {
		cache.byID[normalizedRegion] = make(map[string]map[string]map[string]any)
	}

	for filePath, value := range payload {
		entity := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))))
		if entity == "" {
			continue
		}

		recordsRaw, ok := value.([]any)
		if !ok {
			continue
		}

		records := make([]map[string]any, 0, len(recordsRaw))
		byID := make(map[string]map[string]any)
		for _, item := range recordsRaw {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			records = append(records, record)
			if id, ok := record["id"]; ok {
				byID[strings.TrimSpace(fmt.Sprintf("%v", id))] = record
			}
		}

		cache.listByEntity[normalizedRegion][entity] = records
		cache.byID[normalizedRegion][entity] = byID
	}

	cache.storeCallCount++
	return nil
}

func (cache *fakeEventHandlerCache) GetByID(_ context.Context, region string, entity string, id string) (map[string]any, bool, error) {
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

func (cache *fakeEventHandlerCache) ListByPage(_ context.Context, region string, entity string, page int, pageSize int) ([]map[string]any, int, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	normalizedEntity := strings.ToLower(strings.TrimSpace(entity))
	regionData, ok := cache.listByEntity[normalizedRegion]
	if !ok {
		return []map[string]any{}, 0, nil
	}
	records := regionData[normalizedEntity]
	total := len(records)
	if total == 0 {
		return []map[string]any{}, 0, nil
	}

	start := (page - 1) * pageSize
	if start >= total {
		return []map[string]any{}, total, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return records[start:end], total, nil
}

func (cache *fakeEventHandlerCache) Search(_ context.Context, _, _, _ string, _ []string, _ int) ([]masterdata.SearchMatch, error) {
	return nil, nil
}

func TestEventByIDEndpointOmitsRankingRewardsField(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":   101,
						"name": "test-event",
						"eventRankingRewardRanges": []any{
							map[string]any{"fromRank": 1, "toRank": 100},
						},
					},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id", handler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if _, exists := body["eventRankingRewardRanges"]; exists {
		t.Fatalf("expected eventRankingRewardRanges to be omitted from by-id response")
	}

	if body["id"] != float64(101) {
		t.Fatalf("expected id=101, got %v", body["id"])
	}
}

func TestEventRewardsByIDEndpointReturnsRankingRewards(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":   101,
						"name": "test-event",
						"eventRankingRewardRanges": []any{
							map[string]any{"fromRank": 1, "toRank": 100},
							map[string]any{"fromRank": 101, "toRank": 500},
						},
					},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/rewards", handler.RewardsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101/rewards", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", body["items"])
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 rewards, got %d", len(items))
	}
}

func TestCurrentEventEndpointWritesCacheOnMiss(t *testing.T) {
	gin.SetMode(gin.TestMode)

	nowMillis := time.Now().UTC().UnixMilli()
	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"201": {
						"id":       201,
						"name":     "live-event",
						"startAt":  nowMillis - 60_000,
						"closedAt": nowMillis + 60_000,
						"eventRankingRewardRanges": []any{
							map[string]any{"fromRank": 1, "toRank": 100},
						},
					},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{
						"id":       201,
						"name":     "live-event",
						"startAt":  nowMillis - 60_000,
						"closedAt": nowMillis + 60_000,
						"eventRankingRewardRanges": []any{
							map[string]any{"fromRank": 1, "toRank": 100},
						},
					},
				},
				"currentevents": {},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/current", handler.Current)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/current", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["id"] != float64(201) {
		t.Fatalf("expected id=201, got %v", body["id"])
	}
	if _, exists := body["eventRankingRewardRanges"]; exists {
		t.Fatalf("expected eventRankingRewardRanges to be omitted from current response")
	}
	if cache.storeCallCount == 0 {
		t.Fatalf("expected current event lookup to write cache on miss")
	}
}

func TestCurrentEventEndpointRefreshesExpiredCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	nowMillis := time.Now().UTC().UnixMilli()
	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"301": {
						"id":       301,
						"name":     "new-live-event",
						"startAt":  nowMillis - 60_000,
						"closedAt": nowMillis + 60_000,
					},
				},
				"currentevents": {
					"300": {
						"id":       300,
						"name":     "expired-event",
						"startAt":  nowMillis - 3600_000,
						"closedAt": nowMillis - 1800_000,
					},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{
						"id":       301,
						"name":     "new-live-event",
						"startAt":  nowMillis - 60_000,
						"closedAt": nowMillis + 60_000,
					},
				},
				"currentevents": {
					{
						"id":       300,
						"name":     "expired-event",
						"startAt":  nowMillis - 3600_000,
						"closedAt": nowMillis - 1800_000,
					},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/current", handler.Current)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/current", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["id"] != float64(301) {
		t.Fatalf("expected id=301 after refresh, got %v", body["id"])
	}
	if cache.storeCallCount == 0 {
		t.Fatalf("expected expired cache to trigger cache rewrite")
	}

	currentCached := cache.listByEntity["jp"]["currentevents"]
	if len(currentCached) != 1 {
		t.Fatalf("expected refreshed currentevents len=1, got %d", len(currentCached))
	}
	if currentCached[0]["id"] != 301 {
		t.Fatalf("expected refreshed currentevents id=301, got %v", currentCached[0]["id"])
	}
}
