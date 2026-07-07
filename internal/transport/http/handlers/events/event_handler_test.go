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

	if len(cache.searchCalls) == 0 {
		t.Fatalf("expected event story search calls to be recorded")
	}

	foundEventStoryUnitsSearch := false
	for _, call := range cache.searchCalls {
		if call.entity == "eventstoryunits" {
			foundEventStoryUnitsSearch = true
			break
		}
	}
	if !foundEventStoryUnitsSearch {
		t.Fatalf("expected unit filter to search eventstoryunits")
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
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				"eventcards": {
					{"eventId": 101, "cardId": 2001, "bonusRate": 50},
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
	if len(deckBonuses) != 1 {
		t.Fatalf("expected 1 eventDeckBonuses item, got %d", len(deckBonuses))
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
