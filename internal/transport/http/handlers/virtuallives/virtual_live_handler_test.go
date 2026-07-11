package virtuallives

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type fakeVirtualLiveHandlerCache struct {
	byID                  map[string]map[string]map[string]map[string]any
	listItems             []map[string]any
	listTotal             int
	hasRecords            map[string]map[string]bool
	hasIndex              bool
	hasIndexSet           bool
	searchMatches         []masterdata.SearchMatch
	searchMatchesByEntity map[string][]masterdata.SearchMatch
	searchCalls           []struct {
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
	cache.searchCalls = append(cache.searchCalls, struct {
		region string
		entity string
		query  string
		fields []string
		limit  int
	}{
		region: region,
		entity: entity,
		query:  query,
		fields: append([]string{}, fields...),
		limit:  limit,
	})
	if cache.searchMatchesByEntity != nil {
		if matches, ok := cache.searchMatchesByEntity[entity]; ok {
			return matches, nil
		}
	}
	return cache.searchMatches, nil
}

func (cache *fakeVirtualLiveHandlerCache) HasEntityRecords(_ context.Context, region string, entity string) (bool, error) {
	if cache.hasRecords == nil {
		return false, nil
	}
	return cache.hasRecords[strings.ToLower(strings.TrimSpace(region))][strings.ToLower(strings.TrimSpace(entity))], nil
}

func (cache *fakeVirtualLiveHandlerCache) HasRegionIndex(_ string) bool {
	if !cache.hasIndexSet {
		return true
	}
	return cache.hasIndex
}

func TestVirtualLiveByIDEndpointReturnsVirtualLive(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musicvocals": {
					"29": {
						"id":             29,
						"musicId":        27,
						"caption":        "VIRTUAL SINGER ver.",
						"musicVocalType": "original_song",
					},
				},
				"virtuallivegroups": {
					"77": {
						"id":   77,
						"name": "Group A",
					},
				},
				"virtuallives": {
					"501": {
						"id":                   501,
						"name":                 "after live",
						"assetbundleName":      "vl_501",
						"startAt":              1000,
						"endAt":                2000,
						"screenMvMusicVocalId": 29,
						"virtualLiveType":      "normal",
						"virtualLiveGroupId":   77,
						"virtualItems": []any{
							map[string]any{"virtualItemId": 1, "quantity": 2},
						},
						"virtualLiveSchedules": []any{
							map[string]any{"id": 10, "startAt": 1000},
						},
						"virtualLiveSetlists": []any{
							map[string]any{"musicId": 1, "seq": 1},
						},
					},
				},
			},
		},
		searchMatchesByEntity: map[string][]masterdata.SearchMatch{
			"virtuallivepamphlets": {
				{
					Item: map[string]any{
						"id":              9001,
						"name":            "Pamphlet A",
						"assetbundleName": "pamphlet_501",
						"flavorText":      "hello",
						"virtualLiveId":   501,
					},
				},
			},
			"virtuallivetickets": {
				{
					Item: map[string]any{
						"id":                    9101,
						"name":                  "Ticket A",
						"assetbundleName":       "ticket_501",
						"flavorText":            "admit one",
						"virtualLiveTicketType": "normal",
						"virtualLiveId":         501,
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
	if _, exists := body["virtualItems"]; exists {
		t.Fatalf("expected virtualItems to be omitted from by-id response")
	}
	if _, exists := body["virtualLiveSchedules"]; exists {
		t.Fatalf("expected virtualLiveSchedules to be omitted from by-id response")
	}
	if _, exists := body["virtualLiveSetlists"]; exists {
		t.Fatalf("expected virtualLiveSetlists to be omitted from by-id response")
	}
	virtualLiveGroupRaw, ok := body["virtualLiveGroup"]
	if !ok {
		t.Fatalf("expected virtualLiveGroup in response")
	}
	virtualLiveGroup, ok := virtualLiveGroupRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected virtualLiveGroup object, got %T", virtualLiveGroupRaw)
	}
	if virtualLiveGroup["id"] != float64(77) {
		t.Fatalf("expected virtualLiveGroup.id=77, got %v", virtualLiveGroup["id"])
	}
	if _, exists := body["virtualLiveGroupId"]; exists {
		t.Fatalf("expected virtualLiveGroupId to be omitted from response")
	}
	screenMvMusicVocalRaw, ok := body["screenMvMusicVocal"]
	if !ok {
		t.Fatalf("expected screenMvMusicVocal in response")
	}
	screenMvMusicVocal, ok := screenMvMusicVocalRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected screenMvMusicVocal object, got %T", screenMvMusicVocalRaw)
	}
	if screenMvMusicVocal["id"] != float64(29) {
		t.Fatalf("expected screenMvMusicVocal.id=29, got %v", screenMvMusicVocal["id"])
	}
	if _, exists := body["screenMvMusicVocalId"]; exists {
		t.Fatalf("expected screenMvMusicVocalId to be omitted from response")
	}
	pamphletRaw, ok := body["pamphlet"]
	if !ok {
		t.Fatalf("expected pamphlet in response")
	}
	pamphlet, ok := pamphletRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected pamphlet object, got %T", pamphletRaw)
	}
	if pamphlet["id"] != float64(9001) {
		t.Fatalf("expected pamphlet.id=9001, got %v", pamphlet["id"])
	}
	if _, exists := pamphlet["virtualLiveId"]; exists {
		t.Fatalf("expected pamphlet.virtualLiveId to be omitted")
	}
	ticketRaw, ok := body["ticket"]
	if !ok {
		t.Fatalf("expected ticket in response")
	}
	ticket, ok := ticketRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected ticket object, got %T", ticketRaw)
	}
	if ticket["id"] != float64(9101) {
		t.Fatalf("expected ticket.id=9101, got %v", ticket["id"])
	}
	if _, exists := ticket["virtualLiveId"]; exists {
		t.Fatalf("expected ticket.virtualLiveId to be omitted")
	}
}

func TestVirtualLiveItemsByIDEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":   501,
						"name": "after live",
						"virtualItems": []any{
							map[string]any{"virtualItemId": 1, "quantity": 2},
							map[string]any{"virtualItemId": 2, "quantity": 1},
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id/items", handler.ItemsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501/items", nil)
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
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestVirtualLiveSchedulesByIDEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":   501,
						"name": "after live",
						"virtualLiveSchedules": []any{
							map[string]any{"id": 10, "startAt": 1000},
							map[string]any{"id": 11, "startAt": 2000},
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id/schedules", handler.SchedulesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501/schedules", nil)
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
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestVirtualLiveSetlistsByIDEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":   501,
						"name": "after live",
						"virtualLiveSetlists": []any{
							map[string]any{"musicId": 1, "seq": 1},
							map[string]any{"musicId": 2, "seq": 2},
						},
					},
				},
			},
		},
	}

	handler := newReadyVirtualLiveHandler(cache)

	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id/setlists", handler.SetlistsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/501/setlists", nil)
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
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
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

func TestVirtualLiveAvailabilityEndpointRequiresRuntimeIndexWhenOnlyEntityRecordsExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {"id": 501, "name": "persisted-virtual-live"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"virtuallives": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/regions/:id/availability", handler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/regions/501/availability", nil)
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
				"virtualLiveSchedules": []any{
					map[string]any{"id": 10, "startAt": 1000},
				},
				"virtualLiveSetlists": []any{
					map[string]any{"musicId": 1, "seq": 1},
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
	if _, exists := first["virtualItems"]; exists {
		t.Fatalf("expected virtualItems to be omitted from list response")
	}
	if _, exists := first["virtualLiveSchedules"]; exists {
		t.Fatalf("expected virtualLiveSchedules to be omitted from list response")
	}
	if _, exists := first["virtualLiveSetlists"]; exists {
		t.Fatalf("expected virtualLiveSetlists to be omitted from list response")
	}
	if virtualLiveGroup, exists := first["virtualLiveGroup"]; exists && virtualLiveGroup != nil {
		t.Fatalf("expected virtualLiveGroup to be absent or null when not found, got %v", first["virtualLiveGroup"])
	}
	if screenMvMusicVocal, exists := first["screenMvMusicVocal"]; exists && screenMvMusicVocal != nil {
		t.Fatalf("expected screenMvMusicVocal to be absent or null when not found, got %v", first["screenMvMusicVocal"])
	}
	if pamphlet, exists := first["pamphlet"]; !exists || pamphlet != nil {
		t.Fatalf("expected pamphlet to be null when not found, got %v", first["pamphlet"])
	}
	if ticket, exists := first["ticket"]; !exists || ticket != nil {
		t.Fatalf("expected ticket to be null when not found, got %v", first["ticket"])
	}
}

func TestVirtualLivePersistedRecordsReturnDataWhenIndexMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVirtualLiveHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"virtuallives": {
					"501": {
						"id":                   501,
						"name":                 "persisted-after-restart",
						"assetbundleName":      "vl_501",
						"startAt":              1000,
						"endAt":                2000,
						"virtualLiveType":      "normal",
						"virtualLiveGroupId":   77,
						"screenMvMusicVocalId": 29,
						"virtualItems": []any{
							map[string]any{"virtualItemId": 1, "quantity": 2},
						},
						"virtualLiveSchedules": []any{
							map[string]any{"id": 10, "startAt": 1000},
						},
						"virtualLiveSetlists": []any{
							map[string]any{"musicId": 1, "seq": 1},
						},
					},
				},
			},
		},
		listItems: []map[string]any{
			{
				"id":              501,
				"name":            "persisted-after-restart",
				"assetbundleName": "vl_501",
				"virtualLiveType": "normal",
				"virtualItems": []any{
					map[string]any{"virtualItemId": 1, "quantity": 2},
				},
				"virtualLiveSchedules": []any{
					map[string]any{"id": 10, "startAt": 1000},
				},
				"virtualLiveSetlists": []any{
					map[string]any{"musicId": 1, "seq": 1},
				},
			},
		},
		listTotal: 1,
		hasRecords: map[string]map[string]bool{
			"jp": {"virtuallives": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	handler := newReadyVirtualLiveHandler(cache)
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/:id", handler.ByID)
	router.GET("/api/v1/virtualLives/:region/:id/items", handler.ItemsByID)
	router.GET("/api/v1/virtualLives/:region/:id/schedules", handler.SchedulesByID)
	router.GET("/api/v1/virtualLives/:region/:id/setlists", handler.SetlistsByID)
	router.GET("/api/v1/virtualLives/:region/list", handler.List)

	testCases := []struct {
		name string
		path string
	}{
		{name: "by id", path: "/api/v1/virtualLives/jp/501"},
		{name: "items", path: "/api/v1/virtualLives/jp/501/items"},
		{name: "schedules", path: "/api/v1/virtualLives/jp/501/schedules"},
		{name: "setlists", path: "/api/v1/virtualLives/jp/501/setlists"},
		{name: "list", path: "/api/v1/virtualLives/jp/list?page=1&page_size=20"},
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
