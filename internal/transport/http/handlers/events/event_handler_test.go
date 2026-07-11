package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/usecase"
)

type fakeEventHandlerCache struct {
	byID           map[string]map[string]map[string]map[string]any
	listByEntity   map[string]map[string][]map[string]any
	storeCallCount int
	searchCalls    []fakeEventSearchCall
	searchErr      error
	hasRecords     map[string]map[string]bool
	hasIndex       bool
	hasIndexSet    bool
}

type fakeEventSearchCall struct {
	region string
	entity string
	query  string
	fields []string
	limit  int
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

func (cache *fakeEventHandlerCache) ListAll(_ context.Context, region string, entity string) ([]map[string]any, error) {
	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	normalizedEntity := strings.ToLower(strings.TrimSpace(entity))
	regionData, ok := cache.listByEntity[normalizedRegion]
	if !ok {
		return []map[string]any{}, nil
	}

	records := regionData[normalizedEntity]
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

func (cache *fakeEventHandlerCache) HasEntityRecords(_ context.Context, region string, entity string) (bool, error) {
	if cache.hasRecords == nil {
		return false, nil
	}
	return cache.hasRecords[strings.ToLower(strings.TrimSpace(region))][strings.ToLower(strings.TrimSpace(entity))], nil
}

func (cache *fakeEventHandlerCache) HasRegionIndex(_ string) bool {
	if !cache.hasIndexSet {
		return true
	}
	return cache.hasIndex
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

func TestEventByIDEndpointMapsEventPointAssetbundleNameToIcon(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":                        101,
						"name":                      "test-event",
						"eventPointAssetbundleName": "point_101",
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

	if _, exists := body["eventPointAssetbundleName"]; exists {
		t.Fatalf("expected eventPointAssetbundleName to be omitted from response")
	}

	if body["eventPointIcon"] != "thumbnail/common_event/point_101/icon_eventpoint" {
		t.Fatalf("expected mapped eventPointIcon, got %v", body["eventPointIcon"])
	}
}

func TestEventAvailableRegionsByIDEndpointReturnsReadyRegionsWithData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {"id": 101},
				},
			},
			"en": {
				"events": {
					"101": {"id": 101},
				},
			},
			"tw": {
				"events": {
					"101": {"id": 101},
				},
			},
		},
	}

	statusStore := &fakeEventHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "tw", Status: "running"},
			{Region: "en", Status: "success"},
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	handler := NewEventHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/events/regions/:id/availability", handler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/regions/101/availability", nil)
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

func TestEventByIDEndpointRequiresRuntimeIndexWhenOnlyEntityRecordsExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {"id": 101, "name": "persisted-event"},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {{"id": 101, "name": "persisted-event"}},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"events": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id", handler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", resp.Code)
	}
}

func TestEventSecondaryEndpointsUsePersistedEventRecordsWhenRuntimeIndexMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":               101,
						"name":             "persisted-event",
						"eventBreakTimeId": 501,
						"eventRankingRewardRanges": []any{
							map[string]any{
								"fromRank": 1,
								"toRank":   100,
								"eventRankingRewards": []any{
									map[string]any{"id": 701, "resourceType": "jewel", "resourceQuantity": 500},
								},
							},
						},
					},
				},
				"eventbreaktimes": {
					"501": {"id": 501, "startAt": 1000, "endAt": 2000},
				},
				"cards": {
					"2001": {"id": 2001, "prefix": "Persisted Card", "assetbundleName": "card_2001", "attr": "cool"},
				},
				"musics": {
					"3001": {"id": 3001, "title": "Persisted Music", "assetbundleName": "music_3001"},
				},
				"releaseconditions": {
					"1": {"id": 1, "releaseConditionType": "event_music", "sentence": "Persisted unlock"},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "persisted-event"},
				},
				"eventcards": {
					{"eventId": 101, "cardId": 2001, "bonusRate": 50},
				},
				"eventmusics": {
					{"eventId": 101, "musicId": 3001, "releaseConditionId": 1, "seq": 1},
				},
				"eventcardbonuslimits": {
					{"eventId": 101, "memberCountLimit": 4},
				},
				"eventdeckbonuses": {
					{"eventId": 101, "gameCharacterUnitId": 1, "cardAttr": "cool", "bonusRate": 50},
				},
				"eventhonorbonuses": {
					{"eventId": 101, "honorId": 123, "bonusRate": 25},
				},
				"eventmysekaifixturegamecharacterperformancebonuslimits": {
					{"eventId": 101, "bonusRateLimit": 20},
				},
				"eventraritybonusrates": {
					{"cardRarityType": "rarity_4", "masterRank": 0, "bonusRate": 20},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"events": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/break-times", handler.BreakTimesByID)
	router.GET("/api/v1/events/:region/:id/rewards", handler.RewardsByID)
	router.GET("/api/v1/events/:region/:id/cards", handler.CardsByID)
	router.GET("/api/v1/events/:region/:id/musics", handler.MusicsByID)
	router.GET("/api/v1/events/:region/:id/bonuses", handler.BonusesByID)

	testCases := []struct {
		name   string
		path   string
		assert func(*testing.T, map[string]any)
	}{
		{
			name: "break times",
			path: "/api/v1/events/jp/101/break-times",
			assert: func(t *testing.T, body map[string]any) {
				t.Helper()
				if body["id"] != float64(501) || body["startAt"] != float64(1000) || body["endAt"] != float64(2000) {
					t.Fatalf("expected persisted break time, got %v", body)
				}
			},
		},
		{
			name: "rewards",
			path: "/api/v1/events/jp/101/rewards",
			assert: func(t *testing.T, body map[string]any) {
				t.Helper()
				items := body["items"].([]any)
				rewardRange := items[0].(map[string]any)
				reward := rewardRange["eventRankingRewards"].([]any)[0].(map[string]any)
				if rewardRange["fromRank"] != float64(1) || reward["id"] != float64(701) || reward["resourceQuantity"] != float64(500) {
					t.Fatalf("expected persisted ranking reward, got %v", body)
				}
			},
		},
		{
			name: "cards",
			path: "/api/v1/events/jp/101/cards",
			assert: func(t *testing.T, body map[string]any) {
				t.Helper()
				item := body["items"].([]any)[0].(map[string]any)
				if item["cardId"] != float64(2001) || item["prefix"] != "Persisted Card" || item["bonusRate"] != float64(50) {
					t.Fatalf("expected persisted event card with details, got %v", body)
				}
			},
		},
		{
			name: "musics",
			path: "/api/v1/events/jp/101/musics",
			assert: func(t *testing.T, body map[string]any) {
				t.Helper()
				item := body["items"].([]any)[0].(map[string]any)
				releaseCondition := item["releaseCondition"].(map[string]any)
				if item["musicId"] != float64(3001) || item["title"] != "Persisted Music" || releaseCondition["id"] != float64(1) {
					t.Fatalf("expected persisted event music with details, got %v", body)
				}
			},
		},
		{
			name: "bonuses",
			path: "/api/v1/events/jp/101/bonuses",
			assert: func(t *testing.T, body map[string]any) {
				t.Helper()
				cardLimit := body["eventCardBonusLimits"].([]any)[0].(map[string]any)
				deckBonus := body["eventDeckBonuses"].([]any)[0].(map[string]any)
				honorBonus := body["eventHonorBonuses"].([]any)[0].(map[string]any)
				mysekaiLimit := body["eventMysekaiFixtureGameCharacterPerformanceBonusLimits"].([]any)[0].(map[string]any)
				rarityRate := body["eventRarityBonusRates"].([]any)[0].(map[string]any)
				if cardLimit["memberCountLimit"] != float64(4) || deckBonus["gameCharacterUnitId"] != float64(1) || honorBonus["honorId"] != float64(123) || mysekaiLimit["bonusRateLimit"] != float64(20) || rarityRate["cardRarityType"] != "rarity_4" {
					t.Fatalf("expected complete persisted event bonuses, got %v", body)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			testCase.assert(t, body)
		})
	}
}

func TestEventSecondaryEndpointsReturnNotFoundWhenPersistedEventIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		hasRecords: map[string]map[string]bool{
			"jp": {"events": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}
	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/break-times", handler.BreakTimesByID)
	router.GET("/api/v1/events/:region/:id/rewards", handler.RewardsByID)
	router.GET("/api/v1/events/:region/:id/cards", handler.CardsByID)
	router.GET("/api/v1/events/:region/:id/musics", handler.MusicsByID)
	router.GET("/api/v1/events/:region/:id/bonuses", handler.BonusesByID)

	paths := []string{
		"/api/v1/events/jp/999/break-times",
		"/api/v1/events/jp/999/rewards",
		"/api/v1/events/jp/999/cards",
		"/api/v1/events/jp/999/musics",
		"/api/v1/events/jp/999/bonuses",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusNotFound {
				t.Fatalf("expected status 404, got %d: %s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestEventSecondaryEndpointsRejectPersistedRecordsWhenSyncFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		hasRecords: map[string]map[string]bool{
			"jp": {"events": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}
	statusStore := &fakeEventHandlerStatusStore{
		statuses: []masterdata.SyncStatus{{Region: "jp", Status: "failed"}},
	}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	handler := NewEventHandler(syncUsecase)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/break-times", handler.BreakTimesByID)
	router.GET("/api/v1/events/:region/:id/rewards", handler.RewardsByID)
	router.GET("/api/v1/events/:region/:id/cards", handler.CardsByID)
	router.GET("/api/v1/events/:region/:id/musics", handler.MusicsByID)
	router.GET("/api/v1/events/:region/:id/bonuses", handler.BonusesByID)

	paths := []string{
		"/api/v1/events/jp/101/break-times",
		"/api/v1/events/jp/101/rewards",
		"/api/v1/events/jp/101/cards",
		"/api/v1/events/jp/101/musics",
		"/api/v1/events/jp/101/bonuses",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected status 503, got %d: %s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestEventListAndCurrentUsePersistedEventRecordsWhenRuntimeIndexMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	nowMillis := time.Now().UTC().UnixMilli()
	currentRecord := map[string]any{
		"id":              101,
		"name":            "persisted-current-event",
		"startAt":         nowMillis - int64(time.Hour/time.Millisecond),
		"closedAt":        nowMillis + int64(time.Hour/time.Millisecond),
		"aggregateAt":     nowMillis + int64(2*time.Hour/time.Millisecond),
		"assetbundleName": "event_persisted",
		"eventType":       "marathon",
		"unit":            "idol",
	}
	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": currentRecord,
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {currentRecord},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"events": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	statusStore := &fakeEventHandlerStatusStore{
		statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}},
	}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	handler := NewEventHandler(syncUsecase)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)
	router.GET("/api/v1/events/:region/current", handler.Current)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?page=1&page_size=20", nil)
	listResp := httptest.NewRecorder()
	router.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d: %s", listResp.Code, listResp.Body.String())
	}

	currentReq := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/current", nil)
	currentResp := httptest.NewRecorder()
	router.ServeHTTP(currentResp, currentReq)
	if currentResp.Code != http.StatusOK {
		t.Fatalf("expected current status 200, got %d: %s", currentResp.Code, currentResp.Body.String())
	}

	var currentBody map[string]any
	if err := json.Unmarshal(currentResp.Body.Bytes(), &currentBody); err != nil {
		t.Fatalf("unmarshal current response: %v", err)
	}
	if currentBody["id"] != float64(101) {
		t.Fatalf("expected current event id 101, got %v", currentBody["id"])
	}
}

func TestEventAvailabilityEndpointRequiresRuntimeIndexWhenOnlyEntityRecordsExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {"id": 101, "name": "persisted-event"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"events": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/regions/:id/availability", handler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/regions/101/availability", nil)
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

func TestEventByIDEndpointExpandsUnitAndVirtualLive(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":            101,
						"name":          "test-event",
						"unit":          "light_sound",
						"virtualLiveId": 501,
					},
				},
				"gamecharacters": {
					"6": {
						"id":        6,
						"firstName": "Kiritani",
						"givenName": "Haruka",
					},
				},
				"gamecharacterunits": {
					"18": {
						"id":              18,
						"gameCharacterId": 6,
						"unit":            "idol",
						"colorCode":       "#99ccff",
					},
				},
				"virtuallives": {
					"501": {
						"id":                   501,
						"name":                 "after live",
						"assetbundleName":      "vl_501",
						"startAt":              1000,
						"endAt":                2000,
						"virtualLiveType":      "normal",
						"screenMvMusicVocalId": 99,
					},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"eventstories": {
					{
						"id":                        7001,
						"eventId":                   101,
						"bannerGameCharacterUnitId": 18,
					},
				},
				"eventstoryunits": {
					{
						"id":                     1,
						"eventStoryId":           7001,
						"eventStoryUnitRelation": "sub",
						"unit":                   "light_sound",
					},
					{
						"id":                     2,
						"eventStoryId":           7001,
						"eventStoryUnitRelation": "main",
						"unit":                   "idol",
					},
				},
				"unitprofiles": {
					{
						"unit":            "idol",
						"unitName":        "MORE MORE JUMP！",
						"colorCode":       "#88dd44",
						"unitProfileName": "MORE MORE JUMP！",
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

	unit, ok := body["unit"].(map[string]any)
	if !ok {
		t.Fatalf("expected expanded unit object, got %T", body["unit"])
	}
	if unit["unit"] != "idol" {
		t.Fatalf("expected unit.unit=idol, got %v", unit["unit"])
	}
	if unit["unitName"] != "MORE MORE JUMP！" {
		t.Fatalf("expected unit.unitName to be preserved, got %v", unit["unitName"])
	}
	if unit["colorCode"] != "#88dd44" {
		t.Fatalf("expected unit.colorCode to be preserved, got %v", unit["colorCode"])
	}
	if _, exists := unit["unitProfileName"]; exists {
		t.Fatalf("expected unitProfileName to be omitted from expanded unit")
	}

	if _, exists := body["virtualLiveId"]; exists {
		t.Fatalf("expected virtualLiveId to be removed from by-id response")
	}

	bannerGameCharacter, ok := body["bannerGameCharacter"].(map[string]any)
	if !ok {
		t.Fatalf("expected bannerGameCharacter object, got %T", body["bannerGameCharacter"])
	}
	if bannerGameCharacter["gameCharacterUnitId"] != float64(18) {
		t.Fatalf("expected bannerGameCharacter.gameCharacterUnitId=18, got %v", bannerGameCharacter["gameCharacterUnitId"])
	}
	if bannerGameCharacter["gameCharacterId"] != float64(6) {
		t.Fatalf("expected bannerGameCharacter.gameCharacterId=6, got %v", bannerGameCharacter["gameCharacterId"])
	}
	if bannerGameCharacter["unit"] != "idol" {
		t.Fatalf("expected bannerGameCharacter.unit=idol, got %v", bannerGameCharacter["unit"])
	}
	if bannerGameCharacter["colorCode"] != "#99ccff" {
		t.Fatalf("expected bannerGameCharacter.colorCode=#99ccff, got %v", bannerGameCharacter["colorCode"])
	}
	if bannerGameCharacter["firstName"] != "Kiritani" {
		t.Fatalf("expected bannerGameCharacter.firstName=Kiritani, got %v", bannerGameCharacter["firstName"])
	}
	if bannerGameCharacter["givenName"] != "Haruka" {
		t.Fatalf("expected bannerGameCharacter.givenName=Haruka, got %v", bannerGameCharacter["givenName"])
	}

	virtualLive, ok := body["virtualLive"].(map[string]any)
	if !ok {
		t.Fatalf("expected expanded virtualLive object, got %T", body["virtualLive"])
	}
	if virtualLive["id"] != float64(501) {
		t.Fatalf("expected virtualLive.id=501, got %v", virtualLive["id"])
	}
	if virtualLive["assetbundleName"] != "vl_501" {
		t.Fatalf("expected virtualLive.assetbundleName to be preserved, got %v", virtualLive["assetbundleName"])
	}
	if virtualLive["virtualLiveType"] != "normal" {
		t.Fatalf("expected virtualLive.virtualLiveType to be preserved, got %v", virtualLive["virtualLiveType"])
	}
	if _, exists := virtualLive["screenMvMusicVocalId"]; exists {
		t.Fatalf("expected screenMvMusicVocalId to be omitted from expanded virtualLive")
	}
}

func TestEventDetailByIDEndpointReturnsCompleteAggregateFromPersistedRecordsWithoutRuntimeIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const eventStartAt = int64(946684800000)
	const eventClosedAt = int64(4102444800000)
	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":                        101,
						"name":                      "test-event",
						"startAt":                   eventStartAt,
						"closedAt":                  eventClosedAt,
						"unit":                      "light_sound",
						"virtualLiveId":             501,
						"eventPointAssetbundleName": "point_101",
						"eventRankingRewardRanges": []any{
							map[string]any{"fromRank": 1, "toRank": 10, "isToRankBorder": false, "eventRankingRewards": []any{map[string]any{"id": 1, "resourceBoxId": 9001}}},
							map[string]any{"fromRank": 5000, "toRank": 10000, "isToRankBorder": true, "eventRankingRewards": []any{map[string]any{"id": 2, "resourceBoxId": 9002}}},
							map[string]any{"fromRank": 10001, "toRank": 20000, "isToRankBorder": false, "eventRankingRewards": []any{map[string]any{"id": 3, "resourceBoxId": 9003}}},
						},
					},
				},
				"cards": {
					"2001": {"id": 2001, "prefix": "A New Song", "assetbundleName": "card_2001", "attr": "cool", "cardRarityType": "rarity_4", "initialSpecialTrainingStatus": "done"},
				},
				"musics": {
					"3001": {"id": 3001, "title": "Tell Your World", "assetbundleName": "music_3001", "seq": 99},
				},
				"virtuallives": {
					"501": {"id": 501, "name": "after live", "assetbundleName": "vl_501", "startAt": 1000, "endAt": 2000, "virtualLiveType": "normal"},
				},
				"resourceboxes": {
					"9001": {"id": 9001, "resourceBoxPurpose": "event_ranking_reward", "details": []any{map[string]any{"resourceType": "jewel", "resourceQuantity": 100, "seq": 1}}},
					"9002": {"id": 9002, "resourceBoxPurpose": "event_ranking_reward", "details": []any{map[string]any{"resourceType": "honor", "resourceId": 301, "resourceQuantity": 1, "seq": 1}}},
				},
				"gamecharacters": {
					"6": {"id": 6, "firstName": "Kiritani", "givenName": "Haruka"},
				},
				"gamecharacterunits": {
					"1": {"id": 1, "gameCharacterId": 6, "unit": "idol", "colorCode": "#99ccff"},
				},
				"honors": {
					"301": {"id": 301, "groupId": 401, "assetbundleName": "degree_event_301"},
				},
				"honorgroups": {
					"401": {"id": 401, "backgroundAssetbundleName": "degree_event_bg", "frameName": "event_frame"},
				},
			},
			"en": {
				"events": {
					"101": {"id": 101, "name": "test-event-en"},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "test-event", "startAt": eventStartAt, "closedAt": eventClosedAt},
				},
				"currentevents": {
					{"id": 101, "name": "test-event", "startAt": eventStartAt, "closedAt": eventClosedAt},
				},
				"eventcards": {
					{"eventId": 101, "cardId": 2001, "bonusRate": 50, "leaderBonusRate": 10},
				},
				"eventmusics": {
					{"eventId": 101, "musicId": 3001, "seq": 1},
				},
				"eventcardbonuslimits": {
					{"eventId": 101, "memberCountLimit": 4},
				},
				"eventdeckbonuses": {
					{"eventId": 101, "gameCharacterUnitId": 1, "cardAttr": "cool", "bonusRate": 50},
					{"eventId": 101, "gameCharacterUnitId": 99, "cardAttr": "pure", "bonusRate": 25},
				},
				"eventhonorbonuses": {
					{"eventId": 101, "honorId": 123, "bonusRate": 25},
				},
				"eventmysekaifixturegamecharacterperformancebonuslimits": {
					{"eventId": 101, "bonusRateLimit": 20},
				},
				"eventraritybonusrates": {
					{"cardRarityType": "rarity_4", "masterRank": 0, "bonusRate": 20},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"events": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	statusStore := &fakeEventHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
			{Region: "en", Status: "success"},
		},
	}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	handler := NewEventHandler(syncUsecase)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/detail", handler.DetailByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101/detail", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	event := body["event"].(map[string]any)
	if event["id"] != float64(101) {
		t.Fatalf("expected event.id=101, got %v", event["id"])
	}
	if _, exists := event["eventRankingRewardRanges"]; exists {
		t.Fatalf("expected aggregate event object to omit full eventRankingRewardRanges")
	}
	if event["eventPointIcon"] != "thumbnail/common_event/point_101/icon_eventpoint" {
		t.Fatalf("expected mapped eventPointIcon, got %v", event["eventPointIcon"])
	}

	availableRegions := body["availableRegions"].([]any)
	if len(availableRegions) != 0 {
		t.Fatalf("expected no available regions without runtime indexes, got %v", availableRegions)
	}
	if body["isCurrentEvent"] != true {
		t.Fatalf("expected isCurrentEvent=true, got %v", body["isCurrentEvent"])
	}
	if cache.storeCallCount != 0 {
		t.Fatalf("expected seeded current event cache to avoid StoreRegion, got %d calls", cache.storeCallCount)
	}

	cards := body["cards"].(map[string]any)
	if cards["count"] != float64(1) {
		t.Fatalf("expected cards.count=1, got %v", cards["count"])
	}
	card := cards["items"].([]any)[0].(map[string]any)
	if card["prefix"] != "A New Song" || card["assetbundleName"] != "card_2001" {
		t.Fatalf("expected enriched card fields, got %v", card)
	}

	musics := body["musics"].(map[string]any)
	if musics["count"] != float64(1) {
		t.Fatalf("expected musics.count=1, got %v", musics["count"])
	}
	music := musics["items"].([]any)[0].(map[string]any)
	if music["title"] != "Tell Your World" || music["seq"] != float64(1) {
		t.Fatalf("expected enriched music fields with relation seq, got %v", music)
	}

	bonuses := body["bonuses"].(map[string]any)
	cardBonusLimits := bonuses["eventCardBonusLimits"].([]any)
	if len(cardBonusLimits) != 1 || cardBonusLimits[0].(map[string]any)["memberCountLimit"] != float64(4) {
		t.Fatalf("expected aggregate card bonus limits, got %v", cardBonusLimits)
	}
	deckBonuses := bonuses["eventDeckBonuses"].([]any)
	if len(deckBonuses) != 2 {
		t.Fatalf("expected aggregate bonuses to include deck bonuses, got %v", bonuses)
	}
	resolvedDeckBonus := deckBonuses[0].(map[string]any)
	if resolvedDeckBonus["gameCharacterUnitId"] != float64(1) || resolvedDeckBonus["cardAttr"] != "cool" || resolvedDeckBonus["bonusRate"] != float64(50) {
		t.Fatalf("expected aggregate deck bonus to preserve raw fields, got %v", resolvedDeckBonus)
	}
	if resolvedDeckBonus["gameCharacterId"] != float64(6) || resolvedDeckBonus["unit"] != "idol" || resolvedDeckBonus["firstName"] != "Kiritani" || resolvedDeckBonus["givenName"] != "Haruka" || resolvedDeckBonus["colorCode"] != "#99ccff" {
		t.Fatalf("expected aggregate deck bonus character fields, got %v", resolvedDeckBonus)
	}
	missingDeckBonus := deckBonuses[1].(map[string]any)
	if missingDeckBonus["gameCharacterUnitId"] != float64(99) || missingDeckBonus["cardAttr"] != "pure" || missingDeckBonus["bonusRate"] != float64(25) {
		t.Fatalf("expected aggregate deck bonus with missing lookup to preserve raw fields, got %v", missingDeckBonus)
	}
	if _, exists := missingDeckBonus["gameCharacterId"]; exists {
		t.Fatalf("expected missing lookup deck bonus to omit gameCharacterId, got %v", missingDeckBonus)
	}
	honorBonuses := bonuses["eventHonorBonuses"].([]any)
	if len(honorBonuses) != 1 || honorBonuses[0].(map[string]any)["honorId"] != float64(123) {
		t.Fatalf("expected aggregate honor bonuses, got %v", honorBonuses)
	}
	mysekaiLimits := bonuses["eventMysekaiFixtureGameCharacterPerformanceBonusLimits"].([]any)
	if len(mysekaiLimits) != 1 || mysekaiLimits[0].(map[string]any)["bonusRateLimit"] != float64(20) {
		t.Fatalf("expected aggregate Mysekai bonus limits, got %v", mysekaiLimits)
	}
	rarityRates := bonuses["eventRarityBonusRates"].([]any)
	if len(rarityRates) != 1 || rarityRates[0].(map[string]any)["cardRarityType"] != "rarity_4" {
		t.Fatalf("expected aggregate rarity bonus rates, got %v", rarityRates)
	}

	rewards := body["rewards"].(map[string]any)
	summary := rewards["summary"].(map[string]any)
	if summary["totalRangeCount"] != float64(3) {
		t.Fatalf("expected totalRangeCount=3, got %v", summary["totalRangeCount"])
	}
	if summary["totalRewardCount"] != float64(3) {
		t.Fatalf("expected totalRewardCount=3, got %v", summary["totalRewardCount"])
	}
	if summary["previewRangeCount"] != float64(2) {
		t.Fatalf("expected previewRangeCount=2, got %v", summary["previewRangeCount"])
	}
	if summary["hasMore"] != true {
		t.Fatalf("expected hasMore=true, got %v", summary["hasMore"])
	}
	if summary["fullEndpoint"] != "/api/v1/events/jp/101/rewards" {
		t.Fatalf("expected fullEndpoint path, got %v", summary["fullEndpoint"])
	}
	previewRanges := rewards["previewRanges"].([]any)
	if len(previewRanges) != 2 {
		t.Fatalf("expected 2 preview ranges, got %d", len(previewRanges))
	}
	previewReward := previewRanges[1].(map[string]any)["eventRankingRewards"].([]any)[0].(map[string]any)
	resourceBox := previewReward["resourceBox"].(map[string]any)
	detail := resourceBox["details"].([]any)[0].(map[string]any)
	if detail["honor"].(map[string]any)["assetbundleName"] != "degree_event_301" {
		t.Fatalf("expected preview rewards to be enriched, got %v", detail)
	}
}

func TestEventBreakTimesByIDEndpointReturnsBreakTime(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":               101,
						"eventBreakTimeId": 1,
					},
				},
				"eventbreaktimes": {
					"1": {
						"id":                      1,
						"initialPoint":            0,
						"maxPoint":                6600000,
						"notificationBorderPoint": 5940000,
						"requiredIntervalMinutes": 2,
					},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"eventbreaktimes": {
					{
						"id":                      1,
						"initialPoint":            0,
						"maxPoint":                6600000,
						"notificationBorderPoint": 5940000,
						"gaugeDisplayBorderPoint": 3300000,
						"pointsPerMusicSecond":    115,
						"musicOffsetSeconds":      10,
						"decreaseMinutes":         30,
						"decreasePoint":           550000,
						"requiredIntervalMinutes": 2,
					},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/break-times", handler.BreakTimesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101/break-times", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["id"] != float64(1) {
		t.Fatalf("expected id=1, got %v", body["id"])
	}
	if body["requiredIntervalMinutes"] != float64(2) {
		t.Fatalf("expected requiredIntervalMinutes=2, got %v", body["requiredIntervalMinutes"])
	}
}

func TestEventListEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"gamecharacterunits": {
					"18": {
						"id":              18,
						"gameCharacterId": 6,
						"unit":            "idol",
						"colorCode":       "#99ccff",
					},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{
						"id":                         101,
						"name":                       "test-event",
						"eventType":                  "marathon",
						"assetbundleName":            "event_101",
						"unit":                       "street",
						"startAt":                    1000,
						"aggregateAt":                2000,
						"closedAt":                   3000,
						"isCountLeaderCharacterPlay": true,
						"virtualLiveId":              501,
						"eventBreakTimeId":           9,
						"eventRankingRewardRanges": []any{
							map[string]any{"fromRank": 1, "toRank": 100},
						},
					},
				},
				"eventstories": {
					{
						"id":                        7001,
						"eventId":                   101,
						"bannerGameCharacterUnitId": 18,
					},
				},
				"eventstoryunits": {
					{
						"id":                     1,
						"eventStoryId":           7001,
						"eventStoryUnitRelation": "main",
						"unit":                   "idol",
					},
				},
				"unitprofiles": {
					{
						"unit":      "idol",
						"unitName":  "MORE MORE JUMP！",
						"colorCode": "#88dd44",
					},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?page=1&page_size=20", nil)
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
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 item, got %T len=%d", body["items"], len(items))
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}
	expected := map[string]any{
		"id":                         float64(101),
		"name":                       "test-event",
		"eventType":                  "marathon",
		"assetbundleName":            "event_101",
		"unit":                       "idol",
		"bannerGameCharacterId":      float64(6),
		"startAt":                    float64(1000),
		"aggregateAt":                float64(2000),
		"closedAt":                   float64(3000),
		"isCountLeaderCharacterPlay": true,
	}
	if !reflect.DeepEqual(first, expected) {
		t.Fatalf("expected simplified list item %#v, got %#v", expected, first)
	}
}

func TestEventListEndpointSupportsSorting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 2, "startAt": 2000},
					{"id": 1, "startAt": 1000},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?page=1&page_size=20&sort_by=startAt&sort_order=asc", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1, 2})
}

func TestEventListEndpointFiltersByNameUnitAndEventTypeUsingEventStoryUnits(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "Cheerful Carnival", "unit": "street", "eventType": "cheerful_carnival"},
					{"id": 102, "name": "Another Event", "unit": "idol", "eventType": "marathon"},
				},
				"eventstories": {
					{"id": 7001, "eventId": 101},
					{"id": 7002, "eventId": 102},
				},
				"eventstoryunits": {
					{"id": 8001, "eventStoryId": 7001, "unit": "idol"},
					{"id": 8002, "eventStoryId": 7002, "unit": "street"},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?name=carnival&unit=idol&event_type=cheerful_carnival&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{101})

	if len(cache.searchCalls) != 0 {
		t.Fatalf("expected event list filters not to call Search, got %v", cache.searchCalls)
	}
}

func TestEventListEndpointUnitFilterRequiresExactUnitCode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "Exact Idol", "unit": "idol", "eventType": "marathon"},
					{"id": 102, "name": "Contains Idol", "unit": "virtual_singer_idol", "eventType": "marathon"},
				},
				"eventstories": {
					{"id": 7001, "eventId": 101},
					{"id": 7002, "eventId": 102},
				},
				"eventstoryunits": {
					{"id": 8001, "eventStoryId": 7001, "unit": "idol"},
					{"id": 8002, "eventStoryId": 7002, "unit": "virtual_singer_idol"},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?unit=idol&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{101})
}

func TestEventListEndpointUnitFilterMatchesDisplayedPrimaryUnit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "Mixed Story Units", "unit": "idol", "eventType": "marathon"},
					{"id": 102, "name": "Idol Main", "unit": "idol", "eventType": "marathon"},
				},
				"eventstories": {
					{"id": 7001, "eventId": 101},
					{"id": 7002, "eventId": 102},
				},
				"eventstoryunits": {
					{"id": 8001, "eventStoryId": 7001, "unit": "idol", "eventStoryUnitRelation": "sub"},
					{"id": 8002, "eventStoryId": 7001, "unit": "street", "eventStoryUnitRelation": "main"},
					{"id": 8003, "eventStoryId": 7002, "unit": "idol", "eventStoryUnitRelation": "main"},
				},
				"unitprofiles": {
					{"id": 9001, "unit": "idol", "unitName": "MORE MORE JUMP！"},
					{"id": 9002, "unit": "street", "unitName": "Vivid BAD SQUAD"},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?unit=idol&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{102})
}

func TestEventListEndpointUnitFilterMatchesRawUnitWhenNoEventStory(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "No Story None", "unit": "none", "eventType": "cheerful_carnival"},
					{"id": 102, "name": "Has Story Idol", "unit": "idol", "eventType": "marathon"},
				},
				"eventstories": {
					{"id": 7002, "eventId": 102},
				},
				"eventstoryunits": {
					{"id": 8002, "eventStoryId": 7002, "unit": "idol"},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?unit=none&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{101})
}

func TestEventListEndpointEventTypeFilterRequiresExactEventType(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "Exact Type", "unit": "idol", "eventType": "marathon"},
					{"id": 102, "name": "Contains Type", "unit": "idol", "eventType": "marathon_extra"},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?event_type=marathon&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{101})
}

func TestEventListEndpointNameFilterKeepsFuzzyMatching(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "Cheerful Carnival", "unit": "idol", "eventType": "marathon"},
					{"id": 102, "name": "Quiet Evening", "unit": "idol", "eventType": "marathon"},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?name=carnival&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{101})
}

func TestEventListEndpointFiltersByMultipleUnitsAndEventTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "Cheerful Carnival", "unit": "street", "eventType": "cheerful_carnival"},
					{"id": 102, "name": "Marathon Show", "unit": "idol", "eventType": "marathon"},
					{"id": 103, "name": "World Link", "unit": "light_sound", "eventType": "world_bloom"},
				},
				"eventstories": {
					{"id": 7001, "eventId": 101},
					{"id": 7002, "eventId": 102},
					{"id": 7003, "eventId": 103},
				},
				"eventstoryunits": {
					{"id": 8001, "eventStoryId": 7001, "unit": "street"},
					{"id": 8002, "eventStoryId": 7002, "unit": "idol"},
					{"id": 8003, "eventStoryId": 7003, "unit": "light_sound"},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?unit=idol&unit=light_sound&event_type=marathon&event_type=world_bloom&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{102, 103})
}

func TestEventListEndpointFiltersByCommaSeparatedUnitsAndEventTypes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "Cheerful Carnival", "unit": "street", "eventType": "cheerful_carnival"},
					{"id": 102, "name": "Marathon Show", "unit": "idol", "eventType": "marathon"},
					{"id": 103, "name": "World Link", "unit": "light_sound", "eventType": "world_bloom"},
				},
				"eventstories": {
					{"id": 7001, "eventId": 101},
					{"id": 7002, "eventId": 102},
					{"id": 7003, "eventId": 103},
				},
				"eventstoryunits": {
					{"id": 8001, "eventStoryId": 7001, "unit": "street"},
					{"id": 8002, "eventStoryId": 7002, "unit": "idol"},
					{"id": 8003, "eventStoryId": 7003, "unit": "light_sound"},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?unit=idol,light_sound&event_type=marathon,world_bloom&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{102, 103})
}

func TestEventListEndpointFiltersByID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"events": {
					{"id": 101, "name": "first"},
					{"id": 2101, "name": "second"},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/list", handler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?id=101&page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{101})
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

func TestEventRewardsByIDEndpointEnrichesHonorRewardDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":   101,
						"name": "test-event",
						"eventRankingRewardRanges": []any{
							map[string]any{
								"fromRank": 1,
								"toRank":   100,
								"eventRankingRewards": []any{
									map[string]any{"id": 10, "resourceBoxId": 9001},
								},
							},
						},
					},
				},
				"resourceboxes": {
					"9001": {
						"id":                 9001,
						"resourceBoxPurpose": "event_ranking_reward",
						"details": []any{
							map[string]any{"resourceType": "honor", "resourceId": 301, "resourceLevel": 2, "resourceQuantity": 1, "seq": 1},
						},
					},
				},
				"honors": {
					"301": {
						"id":              301,
						"groupId":         77,
						"honorRarity":     "high",
						"assetbundleName": "degree_event_301",
						"name":            "Event Honor",
						"levels": []any{
							map[string]any{"honorId": 301, "level": 1, "assetbundleName": "degree_event_301"},
							map[string]any{"honorId": 301, "level": 2, "assetbundleName": "degree_event_301_lv2"},
						},
					},
				},
				"honorgroups": {
					"77": {
						"id":                        77,
						"name":                      "Event Group",
						"honorType":                 "event",
						"backgroundAssetbundleName": "degree_event_bg",
						"frameName":                 "event_frame",
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

	items := body["items"].([]any)
	firstRange := items[0].(map[string]any)
	rewards := firstRange["eventRankingRewards"].([]any)
	firstReward := rewards[0].(map[string]any)
	resourceBox := firstReward["resourceBox"].(map[string]any)
	details := resourceBox["details"].([]any)
	detail := details[0].(map[string]any)
	honor := detail["honor"].(map[string]any)
	group := honor["group"].(map[string]any)

	if honor["assetbundleName"] != "degree_event_301" {
		t.Fatalf("expected honor assetbundleName to be enriched, got %v", honor["assetbundleName"])
	}
	if group["backgroundAssetbundleName"] != "degree_event_bg" {
		t.Fatalf("expected honor group backgroundAssetbundleName to be enriched, got %v", group["backgroundAssetbundleName"])
	}
	if group["frameName"] != "event_frame" {
		t.Fatalf("expected honor group frameName to be enriched, got %v", group["frameName"])
	}
}

func TestEventMusicsByIDEndpointReturnsEventMusics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":   101,
						"name": "test-event",
					},
				},
				"musics": {
					"2001": {
						"id":              2001,
						"title":           "Tell Your World",
						"assetbundleName": "music_2001",
						"seq":             99,
					},
					"2003": {
						"id":              2003,
						"title":           "Newly Edgy Idols",
						"assetbundleName": "music_2003",
					},
				},
				"releaseconditions": {
					"1": {
						"id":                   1,
						"releaseConditionType": "event_music",
						"sentence":             "Default unlock",
					},
					"5": {
						"id":                   5,
						"releaseConditionType": "event_music",
						"sentence":             "Second unlock",
					},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"eventmusics": {
					{"eventId": 101, "musicId": 2001, "releaseConditionId": 1, "seq": 1},
					{"eventId": 102, "musicId": 2002, "releaseConditionId": 1, "seq": 1},
					{"eventId": "101", "musicId": 2003, "releaseConditionId": 5, "seq": 2},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/musics", handler.MusicsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101/musics", nil)
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
		t.Fatalf("expected 2 event musics, got %d", len(items))
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}
	if first["musicId"] != float64(2001) {
		t.Fatalf("expected first musicId=2001, got %v", first["musicId"])
	}
	if first["title"] != "Tell Your World" {
		t.Fatalf("expected first title from music detail, got %v", first["title"])
	}
	if first["assetbundleName"] != "music_2001" {
		t.Fatalf("expected first assetbundleName from music detail, got %v", first["assetbundleName"])
	}
	if first["seq"] != float64(1) {
		t.Fatalf("expected event music seq=1 to be preserved, got %v", first["seq"])
	}
	releaseConditionRaw, ok := first["releaseCondition"]
	if !ok {
		t.Fatalf("expected releaseCondition in first event music")
	}
	releaseCondition, ok := releaseConditionRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected releaseCondition object, got %T", releaseConditionRaw)
	}
	if releaseCondition["id"] != float64(1) {
		t.Fatalf("expected first releaseCondition.id=1, got %v", releaseCondition["id"])
	}
	if _, exists := first["releaseConditionId"]; exists {
		t.Fatalf("expected releaseConditionId to be removed from event music")
	}
	if _, exists := first["musicVocalId"]; exists {
		t.Fatalf("expected musicVocalId to be absent from real event musics payload")
	}
}

func TestEventCardsByIDEndpointReturnsEventCards(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":   101,
						"name": "test-event",
					},
				},
				"cards": {
					"2001": {
						"id":                           2001,
						"prefix":                       "A New Song",
						"assetbundleName":              "card_2001",
						"attr":                         "cool",
						"cardRarityType":               "rarity_4",
						"initialSpecialTrainingStatus": "done",
					},
					"2003": {
						"id":              2003,
						"prefix":          "Stage Ready",
						"assetbundleName": "card_2003",
					},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"eventcards": {
					{"eventId": 101, "cardId": 2001, "bonusRate": 50, "leaderBonusRate": 10},
					{"eventId": 102, "cardId": 2002, "bonusRate": 60},
					{"eventId": "101", "cardId": 2003, "bonusRate": 70},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/cards", handler.CardsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101/cards", nil)
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
		t.Fatalf("expected 2 event cards, got %d", len(items))
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}
	if first["cardId"] != float64(2001) {
		t.Fatalf("expected first cardId=2001, got %v", first["cardId"])
	}
	if first["prefix"] != "A New Song" {
		t.Fatalf("expected first prefix from card detail, got %v", first["prefix"])
	}
	if first["assetbundleName"] != "card_2001" {
		t.Fatalf("expected first assetbundleName from card detail, got %v", first["assetbundleName"])
	}
	if first["attr"] != "cool" {
		t.Fatalf("expected first attr from card detail, got %v", first["attr"])
	}
	if first["cardRarityType"] != "rarity_4" {
		t.Fatalf("expected first cardRarityType from card detail, got %v", first["cardRarityType"])
	}
	if first["initialSpecialTrainingStatus"] != "done" {
		t.Fatalf("expected first initialSpecialTrainingStatus from card detail, got %v", first["initialSpecialTrainingStatus"])
	}
	if first["bonusRate"] != float64(50) {
		t.Fatalf("expected first bonusRate=50, got %v", first["bonusRate"])
	}
	if first["leaderBonusRate"] != float64(10) {
		t.Fatalf("expected first leaderBonusRate=10, got %v", first["leaderBonusRate"])
	}
}

func TestEventBonusesByIDEndpointReturnsBonusDatasets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeEventHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"events": {
					"101": {
						"id":   101,
						"name": "test-event",
					},
				},
				"gamecharacters": {
					"6": {"id": 6, "firstName": "Kiritani", "givenName": "Haruka"},
				},
				"gamecharacterunits": {
					"1": {"id": 1, "gameCharacterId": 6, "unit": "idol", "colorCode": "#99ccff"},
					"3": {"id": 3, "gameCharacterId": 404, "unit": "piapro"},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"eventcardbonuslimits": {
					{"eventId": 101, "memberCountLimit": 4},
					{"eventId": 102, "memberCountLimit": 3},
				},
				"eventdeckbonuses": {
					{"eventId": 101, "gameCharacterUnitId": 1, "cardAttr": "cool", "bonusRate": 50},
					{"eventId": 101, "gameCharacterUnitId": 3, "cardAttr": "happy", "bonusRate": 40},
					{"eventId": 101, "gameCharacterUnitId": 99, "cardAttr": "pure", "bonusRate": 25},
					{"eventId": 102, "gameCharacterUnitId": 2, "cardAttr": "cute", "bonusRate": 60},
				},
				"eventhonorbonuses": {
					{"eventId": 101, "honorId": 123, "bonusRate": 25},
					{"eventId": 102, "honorId": 456, "bonusRate": 35},
				},
				"eventmysekaifixturegamecharacterperformancebonuslimits": {
					{"eventId": 101, "bonusRateLimit": 20},
					{"eventId": 102, "bonusRateLimit": 10},
				},
				"eventraritybonusrates": {
					{"cardRarityType": "rarity_4", "masterRank": 0, "bonusRate": 20},
					{"cardRarityType": "rarity_3", "masterRank": 0, "bonusRate": 10},
				},
			},
		},
	}

	handler := newReadyEventHandler(cache)
	router := gin.New()
	router.GET("/api/v1/events/:region/:id/bonuses", handler.BonusesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101/bonuses", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	cardBonusLimits, ok := body["eventCardBonusLimits"].([]any)
	if !ok {
		t.Fatalf("expected eventCardBonusLimits array, got %T", body["eventCardBonusLimits"])
	}
	if len(cardBonusLimits) != 1 {
		t.Fatalf("expected 1 eventCardBonusLimits item, got %d", len(cardBonusLimits))
	}

	deckBonuses, ok := body["eventDeckBonuses"].([]any)
	if !ok {
		t.Fatalf("expected eventDeckBonuses array, got %T", body["eventDeckBonuses"])
	}
	if len(deckBonuses) != 3 {
		t.Fatalf("expected 3 eventDeckBonuses items, got %d", len(deckBonuses))
	}
	resolvedDeckBonus := deckBonuses[0].(map[string]any)
	if resolvedDeckBonus["gameCharacterUnitId"] != float64(1) || resolvedDeckBonus["cardAttr"] != "cool" || resolvedDeckBonus["bonusRate"] != float64(50) {
		t.Fatalf("expected deck bonus to preserve raw fields, got %v", resolvedDeckBonus)
	}
	if resolvedDeckBonus["gameCharacterId"] != float64(6) || resolvedDeckBonus["unit"] != "idol" || resolvedDeckBonus["firstName"] != "Kiritani" || resolvedDeckBonus["givenName"] != "Haruka" || resolvedDeckBonus["colorCode"] != "#99ccff" {
		t.Fatalf("expected enriched deck bonus character fields, got %v", resolvedDeckBonus)
	}
	partialDeckBonus := deckBonuses[1].(map[string]any)
	if partialDeckBonus["gameCharacterUnitId"] != float64(3) || partialDeckBonus["gameCharacterId"] != float64(404) || partialDeckBonus["unit"] != "piapro" {
		t.Fatalf("expected partial unit fields when character lookup is missing, got %v", partialDeckBonus)
	}
	if _, exists := partialDeckBonus["firstName"]; exists {
		t.Fatalf("expected partial deck bonus to omit firstName, got %v", partialDeckBonus)
	}
	missingDeckBonus := deckBonuses[2].(map[string]any)
	if missingDeckBonus["gameCharacterUnitId"] != float64(99) || missingDeckBonus["cardAttr"] != "pure" || missingDeckBonus["bonusRate"] != float64(25) {
		t.Fatalf("expected deck bonus with missing unit lookup to preserve raw fields, got %v", missingDeckBonus)
	}
	if _, exists := missingDeckBonus["gameCharacterId"]; exists {
		t.Fatalf("expected missing unit lookup to omit gameCharacterId, got %v", missingDeckBonus)
	}

	honorBonuses, ok := body["eventHonorBonuses"].([]any)
	if !ok {
		t.Fatalf("expected eventHonorBonuses array, got %T", body["eventHonorBonuses"])
	}
	if len(honorBonuses) != 1 {
		t.Fatalf("expected 1 eventHonorBonuses item, got %d", len(honorBonuses))
	}

	mysekaiLimits, ok := body["eventMysekaiFixtureGameCharacterPerformanceBonusLimits"].([]any)
	if !ok {
		t.Fatalf("expected eventMysekaiFixtureGameCharacterPerformanceBonusLimits array, got %T", body["eventMysekaiFixtureGameCharacterPerformanceBonusLimits"])
	}
	if len(mysekaiLimits) != 1 {
		t.Fatalf("expected 1 eventMysekaiFixtureGameCharacterPerformanceBonusLimits item, got %d", len(mysekaiLimits))
	}

	rarityBonusRates, ok := body["eventRarityBonusRates"].([]any)
	if !ok {
		t.Fatalf("expected eventRarityBonusRates array, got %T", body["eventRarityBonusRates"])
	}
	if len(rarityBonusRates) != 2 {
		t.Fatalf("expected 2 eventRarityBonusRates items, got %d", len(rarityBonusRates))
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
						"id":              201,
						"name":            "live-event",
						"startAt":         nowMillis - 60_000,
						"aggregateAt":     nowMillis + 120_000,
						"assetbundleName": "event_201",
						"closedAt":        nowMillis + 60_000,
						"eventType":       "marathon",
						"unit":            "light_sound",
						"virtualLiveId":   901,
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
						"id":              201,
						"name":            "live-event",
						"startAt":         nowMillis - 60_000,
						"aggregateAt":     nowMillis + 120_000,
						"assetbundleName": "event_201",
						"closedAt":        nowMillis + 60_000,
						"eventType":       "marathon",
						"unit":            "light_sound",
						"virtualLiveId":   901,
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
	expectedKeys := map[string]bool{
		"id":              true,
		"name":            true,
		"startAt":         true,
		"aggregateAt":     true,
		"assetbundleName": true,
		"closedAt":        true,
		"eventType":       true,
		"unit":            true,
	}
	if len(body) != len(expectedKeys) {
		t.Fatalf("expected only current event base fields, got keys %v", body)
	}
	for key := range expectedKeys {
		if _, exists := body[key]; !exists {
			t.Fatalf("expected current response to include %s", key)
		}
	}
	if _, exists := body["virtualLiveId"]; exists {
		t.Fatalf("expected virtualLiveId to be omitted from current response")
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

func (cache *fakeEventHandlerCache) Search(_ context.Context, region string, entity string, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	if cache.searchErr != nil {
		return nil, cache.searchErr
	}
	if limit <= 0 {
		limit = 20
	}

	normalizedRegion := strings.ToLower(strings.TrimSpace(region))
	normalizedEntity := strings.ToLower(strings.TrimSpace(entity))
	normalizedQuery := shared.NormalizeComparableText(query)
	if normalizedRegion == "" || normalizedEntity == "" || normalizedQuery == "" {
		return []masterdata.SearchMatch{}, nil
	}

	regionData, ok := cache.listByEntity[normalizedRegion]
	if !ok {
		return []masterdata.SearchMatch{}, nil
	}

	records := regionData[normalizedEntity]
	if len(records) == 0 {
		return []masterdata.SearchMatch{}, nil
	}

	if len(fields) == 0 {
		fields = []string{"name"}
	}

	cache.searchCalls = append(cache.searchCalls, fakeEventSearchCall{
		region: normalizedRegion,
		entity: normalizedEntity,
		query:  query,
		fields: append([]string{}, fields...),
		limit:  limit,
	})

	results := make([]masterdata.SearchMatch, 0, len(records))
	for _, record := range records {
		for _, field := range fields {
			if shared.NormalizeComparableText(record[field]) != normalizedQuery {
				continue
			}

			results = append(results, masterdata.SearchMatch{
				Item:         record,
				MatchScore:   100,
				MatchType:    "exact",
				MatchedField: field,
			})
			break
		}

		if len(results) >= limit {
			break
		}
	}

	return results, nil
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
