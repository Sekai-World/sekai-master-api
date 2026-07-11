package musics

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

type fakeMusicHandlerCache struct {
	byID          map[string]map[string]map[string]map[string]any
	listItems     []map[string]any
	listByEntity  map[string][]map[string]any
	listTotal     int
	hasRecords    map[string]map[string]bool
	hasIndex      bool
	hasIndexSet   bool
	searchMatches []masterdata.SearchMatch
	searchCalls   []fakeMusicSearchCall
	getByIDCalls  []fakeMusicGetByIDCall
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

type fakeMusicGetByIDCall struct {
	region string
	entity string
	id     string
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
	cache.getByIDCalls = append(cache.getByIDCalls, fakeMusicGetByIDCall{
		region: region,
		entity: entity,
		id:     id,
	})
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

func (cache *fakeMusicHandlerCache) ListAll(_ context.Context, _, entity string) ([]map[string]any, error) {
	source := cache.listItems
	if cache.listByEntity != nil {
		if entityItems, ok := cache.listByEntity[entity]; ok {
			source = entityItems
		}
	}

	items := make([]map[string]any, 0, len(source))
	for _, item := range source {
		copied := make(map[string]any, len(item))
		for key, value := range item {
			copied[key] = value
		}
		items = append(items, copied)
	}
	return items, nil
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

func (cache *fakeMusicHandlerCache) HasEntityRecords(_ context.Context, region string, entity string) (bool, error) {
	if cache.hasRecords == nil {
		return false, nil
	}
	return cache.hasRecords[strings.ToLower(strings.TrimSpace(region))][strings.ToLower(strings.TrimSpace(entity))], nil
}

func (cache *fakeMusicHandlerCache) HasRegionIndex(_ string) bool {
	if !cache.hasIndexSet {
		return true
	}
	return cache.hasIndex
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
				"releaseconditions": {
					"88": {
						"id":                   88,
						"releaseConditionType": "music",
						"sentence":             "Unlock Test Song",
					},
				},
				"musics": {
					"1001": {
						"id":                 1001,
						"title":              "Test Song",
						"lyricist":           "Alice",
						"creatorArtistId":    77,
						"liveStageId":        66,
						"releaseConditionId": 88,
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

	releaseConditionRaw, ok := body["releaseCondition"]
	if !ok {
		t.Fatalf("expected releaseCondition in response")
	}
	releaseCondition, ok := releaseConditionRaw.(map[string]any)
	if !ok {
		t.Fatalf("expected releaseCondition object, got %T", releaseConditionRaw)
	}
	if releaseCondition["id"] != float64(88) {
		t.Fatalf("expected releaseCondition.id=88, got %v", releaseCondition["id"])
	}
	if _, exists := body["releaseConditionId"]; exists {
		t.Fatalf("expected releaseConditionId removed from response")
	}
	if _, exists := body["musicDifficulties"]; exists {
		t.Fatalf("expected musicDifficulties to be absent from by-id response")
	}
	if _, exists := body["musicTags"]; exists {
		t.Fatalf("expected musicTags to be absent from by-id response")
	}
	if _, exists := body["difficulties"]; exists {
		t.Fatalf("expected difficulties to be absent from by-id response")
	}
	if _, exists := body["tags"]; exists {
		t.Fatalf("expected tags to be absent from by-id response")
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

func TestMusicByIDEndpointReturnsPersistedMusicWithoutRuntimeIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musics": {
					"1001": {"id": 1001, "title": "persisted-music"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"musics": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
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
}

func TestMusicListEndpointReturnsPersistedRecordsWithoutRuntimeIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{{"id": 1001, "title": "persisted-music"}},
		listTotal: 1,
		hasRecords: map[string]map[string]bool{
			"jp": {"musics": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
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

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1001})
}

func TestMusicEndpointsReturnPersistedRecordsAfterRestartWithoutRuntimeIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)

	vocal := map[string]any{
		"id":                 501,
		"musicId":            1001,
		"musicVocalType":     "virtual_singer",
		"caption":            "Virtual Singer version",
		"assetbundleName":    "music_vocal_001",
		"releaseConditionId": 88,
	}
	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musicartists": {
					"77": {"id": 77, "name": "Persisted Artist"},
				},
				"livestages": {
					"66": {"id": 66, "name": "Persisted Stage"},
				},
				"releaseconditions": {
					"88": {"id": 88, "sentence": "Persisted unlock condition"},
				},
				"musics": {
					"1001": {
						"id":                 1001,
						"title":              "Persisted Music",
						"creatorArtistId":    77,
						"liveStageId":        66,
						"releaseConditionId": 88,
					},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"musicdifficulties": {
				{
					"id":                 1,
					"musicId":            1001,
					"musicDifficulty":    "expert",
					"playLevel":          25,
					"totalNoteCount":     500,
					"releaseConditionId": 88,
				},
			},
			"musicvocals": {vocal},
			"musictags": {
				{"id": 1, "musicId": 1001, "musicTag": "vocaloid", "seq": 1},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"musics": true},
		},
		hasIndexSet:   true,
		hasIndex:      false,
		searchMatches: []masterdata.SearchMatch{{Item: vocal}},
	}

	musicHandler := newReadyMusicHandler(cache)
	router := gin.New()
	router.GET("/api/v1/musics/:region/:id/detail", musicHandler.DetailByID)
	router.GET("/api/v1/musics/:region/:id/difficulties", musicHandler.DifficultiesByID)
	router.GET("/api/v1/musics/:region/:id/vocals", musicHandler.VocalsByID)

	testCases := []struct {
		name           string
		path           string
		assertResponse func(*testing.T, map[string]any)
	}{
		{
			name: "difficulties",
			path: "/api/v1/musics/jp/1001/difficulties",
			assertResponse: func(t *testing.T, body map[string]any) {
				t.Helper()
				items, ok := body["items"].([]any)
				if !ok || len(items) != 1 {
					t.Fatalf("expected one persisted difficulty, got %v", body["items"])
				}
				difficulty, ok := items[0].(map[string]any)
				if !ok || difficulty["musicDifficulty"] != "expert" || difficulty["totalNoteCount"] != float64(500) {
					t.Fatalf("expected complete persisted expert difficulty, got %v", items[0])
				}
			},
		},
		{
			name: "vocals",
			path: "/api/v1/musics/jp/1001/vocals",
			assertResponse: func(t *testing.T, body map[string]any) {
				t.Helper()
				items, ok := body["items"].([]any)
				if !ok || len(items) != 1 {
					t.Fatalf("expected one persisted vocal, got %v", body["items"])
				}
				persistedVocal, ok := items[0].(map[string]any)
				if !ok || persistedVocal["id"] != float64(501) || persistedVocal["caption"] != "Virtual Singer version" {
					t.Fatalf("expected persisted vocal 501, got %v", items[0])
				}
			},
		},
		{
			name: "detail",
			path: "/api/v1/musics/jp/1001/detail",
			assertResponse: func(t *testing.T, body map[string]any) {
				t.Helper()
				music, ok := body["music"].(map[string]any)
				if !ok || music["title"] != "Persisted Music" {
					t.Fatalf("expected persisted music section, got %v", body["music"])
				}
				creatorArtist, ok := music["creatorArtist"].(map[string]any)
				if !ok || creatorArtist["name"] != "Persisted Artist" {
					t.Fatalf("expected persisted creator artist enrichment, got %v", music["creatorArtist"])
				}
				liveStage, ok := music["liveStage"].(map[string]any)
				if !ok || liveStage["name"] != "Persisted Stage" {
					t.Fatalf("expected persisted live stage enrichment, got %v", music["liveStage"])
				}
				difficulties, difficultiesOK := body["difficulties"].([]any)
				vocals, vocalsOK := body["vocals"].([]any)
				tags, tagsOK := body["tags"].([]any)
				if !difficultiesOK || len(difficulties) != 1 {
					t.Fatalf("expected persisted detail difficulties, got %v", body["difficulties"])
				}
				if !vocalsOK || len(vocals) != 1 {
					t.Fatalf("expected persisted detail vocals, got %v", body["vocals"])
				}
				detailVocal, ok := vocals[0].(map[string]any)
				if !ok || detailVocal["id"] != float64(501) {
					t.Fatalf("expected persisted detail vocal 501, got %v", vocals[0])
				}
				if !tagsOK || len(tags) != 1 || tags[0] != "vocaloid" {
					t.Fatalf("expected persisted detail tags, got %v", body["tags"])
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
			testCase.assertResponse(t, body)
		})
	}
}

func TestMusicEndpointsWithoutMusicRecordsStillRequireRuntimeIndexEvenIfRelatedRecordsExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musicartists": {
					"77": {"id": 77, "name": "Artist A"},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"musicdifficulties": {
				{"musicId": 1001, "musicDifficulty": "expert", "playLevel": 25},
			},
			"musictags": {
				{"musicId": 1001, "musicTag": "vocaloid", "seq": 1},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {
				"musicartists":      true,
				"musicdifficulties": true,
				"musictags":         true,
			},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	musicHandler := newReadyMusicHandler(cache)
	router := gin.New()
	router.GET("/api/v1/musics/:region/:id", musicHandler.ByID)
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	for _, testCase := range []struct {
		name string
		path string
	}{
		{name: "by id", path: "/api/v1/musics/jp/1001"},
		{name: "list", path: "/api/v1/musics/jp/list?page=1&page_size=20"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected status 503, got %d", resp.Code)
			}
		})
	}
}

func TestMusicAvailabilityEndpointRequiresRuntimeIndexWhenOnlyEntityRecordsExist(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musics": {
					"1001": {"id": 1001, "title": "persisted-music"},
				},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {"musics": true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}

	musicHandler := newReadyMusicHandler(cache)
	router := gin.New()
	router.GET("/api/v1/musics/regions/:id/availability", musicHandler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/regions/1001/availability", nil)
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
		listByEntity: map[string][]map[string]any{
			"musicdifficulties": {
				{"id": 1, "musicId": 1001, "musicDifficulty": "expert", "playLevel": 25, "totalNoteCount": 500},
				{"id": 2, "musicId": 1001, "musicDifficulty": "master", "playLevel": 30, "totalNoteCount": 800},
				{"id": 3, "musicId": 1002, "musicDifficulty": "append", "playLevel": 34, "totalNoteCount": 1200},
			},
			"musictags": {
				{"id": 1, "musicId": 1001, "musicTag": "vocaloid", "seq": 1},
				{"id": 2, "musicId": 1001, "musicTag": "street", "seq": 3},
				{"id": 3, "musicId": 1002, "musicTag": "idol", "seq": 4},
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
	assertMusicHasDifficulties(t, firstItem, []string{"expert", "master"})
	assertMusicHasTags(t, firstItem, []string{"vocaloid", "street"})
}

func TestMusicDifficultiesByIDEndpointReturnsFullDifficulties(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musics": {
					"1001": {"id": 1001, "title": "Test Song"},
				},
			},
		},
		listByEntity: map[string][]map[string]any{
			"musicdifficulties": {
				{"id": 1, "musicId": 1001, "musicDifficulty": "expert", "playLevel": 25, "totalNoteCount": 500},
				{"id": 2, "musicId": 1001, "musicDifficulty": "master", "playLevel": 30, "totalNoteCount": 800},
				{"id": 3, "musicId": 1002, "musicDifficulty": "append", "playLevel": 34, "totalNoteCount": 1200},
			},
		},
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/:id/difficulties", musicHandler.DifficultiesByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/1001/difficulties", nil)
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
		t.Fatalf("expected 2 difficulties, got %d", len(items))
	}

	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first difficulty object, got %T", items[0])
	}
	if first["id"] != float64(1) || first["musicId"] != float64(1001) || first["totalNoteCount"] != float64(500) {
		t.Fatalf("expected full first difficulty fields, got %v", first)
	}
}

func TestMusicListEndpointSupportsSorting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 2, "title": "bravo"},
			{"id": 1, "title": "alpha"},
		},
		listTotal: 2,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?page=1&page_size=20&sort_by=title&sort_order=asc", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1, 2})
}

func TestMusicListEndpointSupportsSpoilerOption(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "released", "publishedAt": 946684800000},
			{"id": 2, "title": "future", "publishedAt": 4102444800000},
		},
		listTotal: 2,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?spoiler=false", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1})

	req = httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?spoiler=true", nil)
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1, 2})
}

func TestMusicListInvalidSpoilerReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{}
	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?spoiler=maybe", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}

func TestMusicListEndpointSupportsSearchFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{
				"id":         1,
				"title":      "Tell Your World",
				"categories": []any{"mv", "original"},
				"composer":   "kz",
				"arranger":   "kz",
				"lyricist":   "kz",
			},
			{
				"id":         2,
				"title":      "Melt",
				"categories": []any{"mv", "cover"},
				"composer":   "ryo",
				"arranger":   "ryo",
				"lyricist":   "ryo",
			},
			{
				"id":         3,
				"title":      "World Is Mine",
				"categories": []any{"image"},
				"composer":   "ryo",
				"arranger":   "ryo",
				"lyricist":   "ryo",
			},
		},
		listTotal: 3,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?name=world&category=mv&composer=kz", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1})
}

func TestMusicListEndpointSupportsRepeatedAndCommaSeparatedFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "alpha", "category": "image", "arranger": "Alice Arrangement", "lyricist": "Carol"},
			{"id": 2, "title": "bravo", "category": "mv", "arranger": "Bob", "lyricist": "Dana Lyrics"},
			{"id": 3, "title": "charlie", "category": "mv", "arranger": "Eve", "lyricist": "Frank"},
		},
		listTotal: 3,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?category=image,mv&arranger=arrange&lyricist=carol", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1})
}

func TestMusicListEndpointSupportsTagFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "alpha"},
			{"id": 2, "title": "bravo"},
			{"id": 3, "title": "charlie"},
		},
		listByEntity: map[string][]map[string]any{
			"musictags": {
				{"musicId": 1, "musicTag": "vocaloid", "seq": 1},
				{"musicId": 2, "musicTag": "street", "seq": 3},
				{"musicId": 3, "musicTag": "idol", "seq": 4},
			},
		},
		listTotal: 3,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?tag=street,idol", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{2, 3})
}

func TestMusicListEndpointSupportsHasAppendFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "alpha"},
			{"id": 2, "title": "bravo"},
			{"id": 3, "title": "charlie"},
		},
		listByEntity: map[string][]map[string]any{
			"musicdifficulties": {
				{"musicId": 1, "musicDifficulty": "hard", "playLevel": 25},
				{"musicId": 2, "musicDifficulty": "append", "playLevel": 34},
				{"musicId": 3, "musicDifficulty": "master", "playLevel": 30},
			},
		},
		listTotal: 3,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?hasAppend=true", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{2})

	req = httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?hasAppend=false", nil)
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1, 3})
}

func TestMusicListInvalidHasAppendReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{}
	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?hasAppend=maybe", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}

func TestMusicListEndpointSupportsPlayLevelExactFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "alpha"},
			{"id": 2, "title": "bravo"},
			{"id": 3, "title": "charlie"},
		},
		listByEntity: map[string][]map[string]any{
			"musicdifficulties": {
				{"musicId": 1, "playLevel": 25},
				{"musicId": 2, "playLevel": 26},
				{"musicId": 2, "playLevel": 30},
				{"musicId": 3, "playLevel": 29},
			},
		},
		listTotal: 3,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?playLevel=30", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{2})
}

func TestMusicListEndpointSupportsPlayLevelAliasFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "alpha"},
			{"id": 2, "title": "bravo"},
			{"id": 3, "title": "charlie"},
		},
		listByEntity: map[string][]map[string]any{
			"musicdifficulties": {
				{"musicId": 1, "playLevel": 25},
				{"musicId": 2, "playLevel": 30},
				{"musicId": 3, "playLevel": 32},
			},
		},
		listTotal: 3,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	testCases := []struct {
		name     string
		path     string
		expected []float64
	}{
		{
			name:     "snake case",
			path:     "/api/v1/musics/jp/list?play_level=30",
			expected: []float64{2},
		},
		{
			name:     "short level",
			path:     "/api/v1/musics/jp/list?level=%3E30",
			expected: []float64{3},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, testCase.path, nil)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", resp.Code)
			}

			assertResponseItemOrder(t, resp.Body.Bytes(), testCase.expected)
		})
	}
}

func TestMusicListEndpointSupportsPlayLevelRangeFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "alpha"},
			{"id": 2, "title": "bravo"},
			{"id": 3, "title": "charlie"},
		},
		listByEntity: map[string][]map[string]any{
			"musicdifficulties": {
				{"musicId": 1, "playLevel": 25},
				{"musicId": 2, "playLevel": 27},
				{"musicId": 3, "playLevel": 30},
			},
		},
		listTotal: 3,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?playLevel=26-29", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{2})
}

func TestMusicListEndpointSupportsPlayLevelComparisonFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "alpha"},
			{"id": 2, "title": "bravo"},
			{"id": 3, "title": "charlie"},
		},
		listByEntity: map[string][]map[string]any{
			"musicdifficulties": {
				{"musicId": 1, "playLevel": 26},
				{"musicId": 2, "playLevel": 28},
				{"musicId": 3, "playLevel": 30},
			},
		},
		listTotal: 3,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?playLevel=%3E=28,%3C30", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	assertResponseItemOrder(t, resp.Body.Bytes(), []float64{2})
}

func TestMusicListEndpointInvalidPlayLevelReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "alpha"},
		},
		listTotal: 1,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?playLevel=hard", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
}

func TestMusicListSortingBuildsOnlyCurrentPage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musicartists": {
					"77": {"id": 77, "name": "Artist A"},
				},
			},
		},
		listItems: []map[string]any{
			{"id": 2, "title": "bravo", "creatorArtistId": 88},
			{"id": 1, "title": "alpha", "creatorArtistId": 77},
		},
		listTotal: 2,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?page=1&page_size=1&sort_by=title&sort_order=asc", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}

	if len(cache.getByIDCalls) != 1 {
		t.Fatalf("expected 1 related GetByID call for current page, got %d", len(cache.getByIDCalls))
	}
	if cache.getByIDCalls[0].entity != "musicartists" || cache.getByIDCalls[0].id != "77" {
		t.Fatalf("expected creatorArtist lookup for id=77, got %+v", cache.getByIDCalls[0])
	}
}

func TestMusicListInvalidSortByReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "title": "alpha"},
		},
		listTotal: 1,
	}

	musicHandler := newReadyMusicHandler(cache)

	router := gin.New()
	router.GET("/api/v1/musics/:region/list", musicHandler.List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?page=1&page_size=20&sort_by=creatorArtist&sort_order=asc", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.Code)
	}
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

	testCases := []string{
		"/api/v1/musics/jp/1001",
		"/api/v1/musics/jp/list?page=1&page_size=20",
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

func assertMusicHasDifficulties(t *testing.T, item map[string]any, expected []string) {
	t.Helper()

	difficultiesRaw, ok := item["difficulties"]
	if !ok {
		t.Fatalf("expected difficulties in item")
	}

	difficulties, ok := difficultiesRaw.([]any)
	if !ok {
		t.Fatalf("expected difficulties array, got %T", difficultiesRaw)
	}
	if len(difficulties) != len(expected) {
		t.Fatalf("expected %d difficulties, got %d", len(expected), len(difficulties))
	}

	for index, expectedDifficulty := range expected {
		difficulty, ok := difficulties[index].(map[string]any)
		if !ok {
			t.Fatalf("expected difficulties[%d] object, got %T", index, difficulties[index])
		}
		if difficulty["musicDifficulty"] != expectedDifficulty {
			t.Fatalf("expected difficulties[%d].musicDifficulty=%s, got %v", index, expectedDifficulty, difficulty["musicDifficulty"])
		}
		for _, hiddenField := range []string{"id", "musicId", "totalNoteCount"} {
			if _, exists := difficulty[hiddenField]; exists {
				t.Fatalf("expected difficulties[%d].%s to be omitted", index, hiddenField)
			}
		}
	}
}

func assertMusicHasTags(t *testing.T, item map[string]any, expected []string) {
	t.Helper()

	tagsRaw, ok := item["tags"]
	if !ok {
		t.Fatalf("expected tags in item")
	}

	tags, ok := tagsRaw.([]any)
	if !ok {
		t.Fatalf("expected tags array, got %T", tagsRaw)
	}
	if len(tags) != len(expected) {
		t.Fatalf("expected %d tags, got %d", len(expected), len(tags))
	}

	for index, expectedTag := range expected {
		tag, ok := tags[index].(string)
		if !ok {
			t.Fatalf("expected tags[%d] string, got %T", index, tags[index])
		}
		if tag != expectedTag {
			t.Fatalf("expected tags[%d]=%s, got %s", index, expectedTag, tag)
		}
	}
}
