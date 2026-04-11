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

type fakeMusicHandlerCache struct {
	byID          map[string]map[string]map[string]map[string]any
	listItems     []map[string]any
	listTotal     int
	searchMatches []masterdata.SearchMatch
	searchCalls   []fakeMusicSearchCall
	lastSearch    struct {
		region string
		entity string
		query  string
		fields []string
		limit  int
	}
}

type fakeMusicSearchCall struct {
	region string
	entity string
	query  string
	fields []string
	limit  int
}

type fakeMusicHandlerStatusStore struct {
	statuses []masterdata.SyncStatus
}

func newReadyMusicHandler(cache *fakeMusicHandlerCache) *MusicHandler {
	statusStore := &fakeMusicHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewMusicHandler(syncUsecase)
}

func (store *fakeMusicHandlerStatusStore) Save(_ context.Context, _ masterdata.SyncStatus) error {
	return nil
}

func (store *fakeMusicHandlerStatusStore) List(_ context.Context) ([]masterdata.SyncStatus, error) {
	return store.statuses, nil
}

func (cache *fakeMusicHandlerCache) StoreRegion(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (cache *fakeMusicHandlerCache) GetByID(_ context.Context, region string, entity string, id string) (map[string]any, bool, error) {
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

func (cache *fakeMusicHandlerCache) ListByPage(_ context.Context, _, _ string, _, _ int) ([]map[string]any, int, error) {
	return cache.listItems, cache.listTotal, nil
}

func (cache *fakeMusicHandlerCache) Search(_ context.Context, region, entity, query string, fields []string, limit int) ([]masterdata.SearchMatch, error) {
	cache.lastSearch.region = region
	cache.lastSearch.entity = entity
	cache.lastSearch.query = query
	cache.lastSearch.limit = limit
	cache.lastSearch.fields = append([]string{}, fields...)
	cache.searchCalls = append(cache.searchCalls, fakeMusicSearchCall{
		region: region,
		entity: entity,
		query:  query,
		fields: append([]string{}, fields...),
		limit:  limit,
	})
	return cache.searchMatches, nil
}

func TestMusicByIDEndpointReturnsMusic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musicartists": {
					"77": {
						"id":   77,
						"name": "Artist A",
					},
				},
				"livestages": {
					"66": {
						"id":   66,
						"name": "Stage 66",
					},
				},
				"musics": {
					"1001": {
						"id":              1001,
						"title":           "Test Song",
						"lyricist":        "Alice",
						"creatorArtistId": 77,
						"liveStageId":     66,
					},
				},
			},
		},
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/:id", musicHandler.ByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/1001", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if body["title"] != "Test Song" {
		t.Fatalf("expected title Test Song, got %v", body["title"])
	}

	creatorArtistRaw, ok := body["creatorArtist"]
	if !ok {
		t.Fatalf("expected creatorArtist in response")
	}
	creatorArtist, ok := creatorArtistRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected creatorArtist object, got %T", creatorArtistRaw)
	}
	if creatorArtist["id"] != float64(77) {
		t.Fatalf("expected creatorArtist.id=77, got %v", creatorArtist["id"])
	}
	if _, exists := body["creatorArtistId"]; exists {
		t.Fatalf("expected creatorArtistId removed from response")
	}

	liveStageRaw, ok := body["liveStage"]
	if !ok {
		t.Fatalf("expected liveStage in response")
	}
	liveStage, ok := liveStageRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected liveStage object, got %T", liveStageRaw)
	}
	if liveStage["id"] != float64(66) {
		t.Fatalf("expected liveStage.id=66, got %v", liveStage["id"])
	}
	if _, exists := body["liveStageId"]; exists {
		t.Fatalf("expected liveStageId removed from response")
	}
}

func TestMusicAvailableRegionsByIDEndpointReturnsReadyRegionsWithData(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musics": {
					"1001": {"id": 1001},
				},
			},
			"en": {
				"musics": {
					"1001": {"id": 1001},
				},
			},
			"kr": {
				"musics": {
					"1001": {"id": 1001},
				},
			},
		},
	}

	statusStore := &fakeMusicHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "kr", Status: "running"},
			{Region: "en", Status: "success"},
			{Region: "jp", Status: "success"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	musicHandler := NewMusicHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/musics/regions/:id/availability", musicHandler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/regions/1001/availability", nil)
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

func TestMusicListEndpointReturnsItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musicartists": {
					"77": {
						"id":   77,
						"name": "Artist A",
					},
				},
				"livestages": {
					"66": {
						"id":   66,
						"name": "Stage 66",
					},
				},
			},
		},
		listItems: []map[string]any{
			{
				"id":              1001,
				"title":           "Test Song",
				"lyricist":        "Alice",
				"creatorArtistId": 77,
				"liveStageId":     66,
			},
		},
		listTotal: 1,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?page=1&page_size=20", nil)
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
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	firstItem, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}
	assertMusicHasMappedCreatorArtist(t, firstItem, 77)
	assertMusicHasMappedLiveStage(t, firstItem, 66)
}

func TestMusicSearchByTitleParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{}
	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/search", musicHandler.Search)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/search?title=test&page=1&limit=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	if len(cache.searchCalls) != 1 {
		t.Fatalf("expected 1 search call, got %d", len(cache.searchCalls))
	}
	if len(cache.searchCalls[0].fields) != 1 || cache.searchCalls[0].fields[0] != "title" || cache.searchCalls[0].query != "test" {
		t.Fatalf("expected search title=test, got fields=%v query=%s", cache.searchCalls[0].fields, cache.searchCalls[0].query)
	}
}

func TestMusicSearchFieldKeywordMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		param         string
		expectedField string
	}{
		{name: "lyricist", param: "lyricist", expectedField: "lyricist"},
		{name: "composer", param: "composer", expectedField: "composer"},
		{name: "arranger", param: "arranger", expectedField: "arranger"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &fakeMusicHandlerCache{}
			musicHandler := newReadyMusicHandler(cache)

			router := gin.New()
			router.GET("/api/v1/musics/:region/search", musicHandler.Search)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/search?"+tt.param+"=test&page=1&limit=20", nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", resp.Code)
			}

			if len(cache.searchCalls) != 1 {
				t.Fatalf("expected 1 search call, got %d", len(cache.searchCalls))
			}
			if len(cache.searchCalls[0].fields) != 1 || cache.searchCalls[0].fields[0] != tt.expectedField || cache.searchCalls[0].query != "test" {
				t.Fatalf("expected search %s=test, got fields=%v query=%s", tt.expectedField, cache.searchCalls[0].fields, cache.searchCalls[0].query)
			}
		})
	}
}

func TestMusicSearchFieldMultiParams(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{}
	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/search", musicHandler.Search)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/search?title=song&lyricist=alice&page=1&limit=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	if len(cache.searchCalls) != 2 {
		t.Fatalf("expected 2 search calls, got %d", len(cache.searchCalls))
	}
	if len(cache.searchCalls[0].fields) != 1 || cache.searchCalls[0].fields[0] != "title" || cache.searchCalls[0].query != "song" {
		t.Fatalf("expected first search title=song, got fields=%v query=%s", cache.searchCalls[0].fields, cache.searchCalls[0].query)
	}
	if len(cache.searchCalls[1].fields) != 1 || cache.searchCalls[1].fields[0] != "lyricist" || cache.searchCalls[1].query != "alice" {
		t.Fatalf("expected second search lyricist=alice, got fields=%v query=%s", cache.searchCalls[1].fields, cache.searchCalls[1].query)
	}
}

func TestMusicSearchWithoutFieldKeywordsReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{}
	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/search", musicHandler.Search)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/search?page=1&limit=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}

func TestMusicSearchEndpointMapsCreatorArtist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musicartists": {
					"77": {
						"id":   77,
						"name": "Artist A",
					},
				},
				"livestages": {
					"66": {
						"id":   66,
						"name": "Stage 66",
					},
				},
			},
		},
		searchMatches: []masterdata.SearchMatch{
			{
				Item: map[string]any{
					"id":              1001,
					"title":           "Test Song",
					"creatorArtistId": 77,
					"liveStageId":     66,
				},
			},
		},
	}
	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/search", musicHandler.Search)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/search?title=test&page=1&limit=20", nil)
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
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	firstItem, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first item object, got %T", items[0])
	}
	assertMusicHasMappedCreatorArtist(t, firstItem, 77)
	assertMusicHasMappedLiveStage(t, firstItem, 66)
}

func TestMusicEndpointsBlockedWhenRegionSyncInProgress(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{}
	statusStore := &fakeMusicHandlerStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "running"},
		},
	}

	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	musicHandler := NewMusicHandler(syncUsecase)

	router := gin.New()
	router.GET("/api/v1/musics/:region/:id", musicHandler.ByID)
	router.GET("/api/v1/musics/:region/list", musicHandler.List)
	router.GET("/api/v1/musics/:region/search", musicHandler.Search)

	testCases := []string{
		"/api/v1/musics/jp/1001",
		"/api/v1/musics/jp/list?page=1&page_size=20",
		"/api/v1/musics/jp/search?title=test&page=1&limit=20",
	}

	for _, path := range testCases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("path %s: expected status 503, got %d", path, resp.Code)
		}

		var body map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("path %s: unmarshal response: %v", path, err)
		}

		errorRaw, ok := body["error"]
		if !ok {
			t.Fatalf("path %s: expected error object in response", path)
		}

		errorObj, ok := errorRaw.(map[string]any)
		if !ok {
			t.Fatalf("path %s: expected error object type, got %T", path, errorRaw)
		}

		if errorObj["code"] != "REGION_DATA_NOT_READY" {
			t.Fatalf("path %s: expected error code REGION_DATA_NOT_READY, got %v", path, errorObj["code"])
		}
	}
}

func assertMusicHasMappedCreatorArtist(t *testing.T, item map[string]any, expectedArtistID int) {
	t.Helper()

	creatorArtistRaw, ok := item["creatorArtist"]
	if !ok {
		t.Fatalf("expected creatorArtist in item")
	}

	creatorArtist, ok := creatorArtistRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected creatorArtist object, got %T", creatorArtistRaw)
	}

	if creatorArtist["id"] != float64(expectedArtistID) {
		t.Fatalf("expected creatorArtist.id=%d, got %v", expectedArtistID, creatorArtist["id"])
	}
	if _, exists := item["creatorArtistId"]; exists {
		t.Fatalf("expected creatorArtistId removed from item")
	}
}

func assertMusicHasMappedLiveStage(t *testing.T, item map[string]any, expectedLiveStageID int) {
	t.Helper()

	liveStageRaw, ok := item["liveStage"]
	if !ok {
		t.Fatalf("expected liveStage in item")
	}

	liveStage, ok := liveStageRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected liveStage object, got %T", liveStageRaw)
	}

	if liveStage["id"] != float64(expectedLiveStageID) {
		t.Fatalf("expected liveStage.id=%d, got %v", expectedLiveStageID, liveStage["id"])
	}
	if _, exists := item["liveStageId"]; exists {
		t.Fatalf("expected liveStageId removed from item")
	}
}
