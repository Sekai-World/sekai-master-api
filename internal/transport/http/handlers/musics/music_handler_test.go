package musics

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/transport/http/handlers/testutil"
	"sekai-master-api/internal/usecase"
)

type fakeMusicHandlerCache struct {
	byID         map[string]map[string]map[string]map[string]any
	listItems    []map[string]any
	listByEntity map[string][]map[string]any
	listTotal    int
	hasRecords   map[string]map[string]bool
	getByIDCalls []fakeMusicGetByIDCall
}

type fakeMusicGetByIDCall struct {
	region string
	entity string
	id     string
}

type fakeMusicGetByIDsCall struct {
	region string
	entity string
	ids    []string
}

type fakeMusicHandlerStatusStore struct {
	statuses []masterdata.SyncStatus
}

func newReadyMusicHandler(cache usecase.MasterDataCache) *MusicHandler {
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

func (cache *fakeMusicHandlerCache) HasEntityRecords(_ context.Context, region string, entity string) (bool, error) {
	region = strings.ToLower(strings.TrimSpace(region))
	entity = strings.ToLower(strings.TrimSpace(entity))
	if cache.hasRecords == nil {
		if regionData, ok := cache.byID[region]; ok && len(regionData[entity]) > 0 {
			return true, nil
		}
		if len(cache.listByEntity[entity]) > 0 {
			return true, nil
		}
		return len(cache.listItems) > 0, nil
	}
	return cache.hasRecords[region][entity], nil
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

func TestMusicAvailableRegionsByIDEndpointReturnsAvailableRegionsWithData(t *testing.T) {
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

func TestMusicByIDEndpointReturnsPersistedMusic(t *testing.T) {
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

func TestMusicListEndpointReturnsPersistedRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		listItems: []map[string]any{{"id": 1001, "title": "persisted-music"}},
		listTotal: 1,
		hasRecords: map[string]map[string]bool{
			"jp": {"musics": true},
		},
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

func TestMusicEndpointsReturnPersistedRecordsAfterRestart(t *testing.T) {
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

type expectedMusicVideo struct {
	category        string
	assetbundleName string
	musicVocalID    string
}

type musicVideoDetailTestCase struct {
	name                 string
	musicRecord          map[string]any
	categoryRecords      []map[string]any
	assetVariantRecords  []map[string]any
	vocalRecords         []map[string]any
	wantVideos           []expectedMusicVideo
	wantCategories       []string
	wantBatchLookupCalls []fakeMusicGetByIDsCall
}

type musicVideoBatchCache struct {
	*fakeMusicHandlerCache
	getByIDsCalls []fakeMusicGetByIDsCall
	batchErrors   map[string]error
}

type fakeMusicIndexCall struct {
	region  string
	entity  string
	index   string
	lookups [][]any
}

type musicOriginalsIndexCache struct {
	*fakeMusicHandlerCache
	indexCalls  []fakeMusicIndexCall
	indexErrors map[string]error
}

func (cache *musicOriginalsIndexCache) ListByIndex(ctx context.Context, region string, entity string, index string, lookups [][]any) ([][]map[string]any, error) {
	call := fakeMusicIndexCall{
		region: region,
		entity: entity,
		index:  index,
	}
	for _, lookup := range lookups {
		call.lookups = append(call.lookups, append([]any(nil), lookup...))
	}
	cache.indexCalls = append(cache.indexCalls, call)
	if err := cache.indexErrors[entity]; err != nil {
		return nil, err
	}

	records, err := cache.fakeMusicHandlerCache.ListAll(ctx, region, entity)
	if err != nil {
		return nil, err
	}

	results := make([][]map[string]any, len(lookups))
	for lookupIndex, lookup := range lookups {
		results[lookupIndex] = make([]map[string]any, 0)
		lookupKey, ok := masterdata.IndexLookupKey(lookup...)
		if !ok {
			continue
		}
		for _, record := range records {
			for _, indexKey := range masterdata.IndexKeys(record, index) {
				if indexKey == lookupKey {
					results[lookupIndex] = append(results[lookupIndex], record)
					break
				}
			}
		}
	}
	return results, nil
}

func newMusicOriginalsIndexCache(records []map[string]any, indexErrors map[string]error) *musicOriginalsIndexCache {
	return &musicOriginalsIndexCache{
		fakeMusicHandlerCache: &fakeMusicHandlerCache{
			byID: map[string]map[string]map[string]map[string]any{
				"jp": {"musics": {"42": {"id": 42, "title": "Original Test"}}},
			},
			listByEntity: map[string][]map[string]any{
				"musiccategories": {
					{"id": 1, "musicId": 42, "musicCategoryName": "original"},
					{"id": 2, "musicId": 42, "musicCategoryName": "mv_2d"},
				},
				"musicoriginals": records,
			},
			hasRecords: map[string]map[string]bool{"jp": {"musics": true}},
		},
		indexErrors: indexErrors,
	}
}

func TestMusicDetailMusicOriginals(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := newMusicOriginalsIndexCache([]map[string]any{
		{"id": 801, "musicId": 42, "videoLink": "https://youtu.be/abc123"},
		{"id": 802, "musicId": "42", "videoLink": "https://www.nicovideo.jp/watch/sm123456"},
		{"id": 42, "musicId": 99, "videoLink": "https://youtu.be/not-this-music"},
		{"id": 803, "musicId": 42, "videoLink": "  "},
		{"id": map[string]any{"value": 804}, "musicId": 42, "videoLink": "https://youtu.be/malformed-id"},
		{"id": 805, "musicId": true, "videoLink": "https://youtu.be/malformed-music-id"},
		{"id": 806, "musicId": 42, "videoLink": 123},
	}, nil)
	responseBody := decodeMusicOK(t, doMusicGet(newMusicCategoryRouter(newReadyMusicHandler(cache)), "/api/v1/musics/jp/42/detail"))

	originalsRaw, ok := responseBody["musicOriginals"]
	if !ok {
		t.Fatal("expected musicOriginals in detail response")
	}
	originals, ok := originalsRaw.([]any)
	if !ok || len(originals) != 2 {
		t.Fatalf("expected two valid music originals, got %T %v", originalsRaw, originalsRaw)
	}
	wantOriginals := []map[string]any{
		{"id": "801", "musicId": "42", "videoLink": "https://youtu.be/abc123"},
		{"id": "802", "musicId": "42", "videoLink": "https://www.nicovideo.jp/watch/sm123456"},
	}
	for index, want := range wantOriginals {
		got, ok := originals[index].(map[string]any)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("expected musicOriginals[%d]=%v, got %v", index, want, originals[index])
		}
	}

	musicVideos, ok := responseBody["musicVideos"].([]any)
	if !ok || len(musicVideos) != 2 {
		t.Fatalf("expected existing musicVideos to remain in detail, got %v", responseBody["musicVideos"])
	}
	for index, category := range []string{"original", "mv_2d"} {
		video, ok := musicVideos[index].(map[string]any)
		if !ok || video["category"] != category {
			t.Fatalf("expected musicVideos[%d] category %q, got %v", index, category, musicVideos[index])
		}
	}

	wantIndexCalls := []fakeMusicIndexCall{
		{region: "jp", entity: "musiccategories", index: "musicId", lookups: [][]any{{"42"}}},
		{region: "jp", entity: "musicdifficulties", index: "musicId", lookups: [][]any{{"42"}}},
		{region: "jp", entity: "musicvocals", index: "musicId", lookups: [][]any{{"42"}}},
		{region: "jp", entity: "musictags", index: "musicId", lookups: [][]any{{"42"}}},
		{region: "jp", entity: "musicoriginals", index: "musicId", lookups: [][]any{{"42"}}},
	}
	if !reflect.DeepEqual(cache.indexCalls, wantIndexCalls) {
		t.Fatalf("expected music-original relation index lookup %v, got %v", wantIndexCalls, cache.indexCalls)
	}
}

func TestMusicDetailMusicOriginalsMissingDataReturnsEmptyArray(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := newMusicOriginalsIndexCache(nil, nil)
	responseBody := decodeMusicOK(t, doMusicGet(newMusicCategoryRouter(newReadyMusicHandler(cache)), "/api/v1/musics/jp/42/detail"))
	originals, ok := responseBody["musicOriginals"].([]any)
	if !ok || len(originals) != 0 {
		t.Fatalf("expected an empty musicOriginals array, got %T %v", responseBody["musicOriginals"], responseBody["musicOriginals"])
	}
}

func TestMusicDetailMusicOriginalsReadError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := newMusicOriginalsIndexCache(nil, map[string]error{"musicoriginals": errors.New("index read failed")})
	resp := doMusicGet(newMusicCategoryRouter(newReadyMusicHandler(cache)), "/api/v1/musics/jp/42/detail")
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if body.Error.Code != "MUSIC_QUERY_ERROR" {
		t.Fatalf("expected MUSIC_QUERY_ERROR, got %q", body.Error.Code)
	}
}

func (cache *musicVideoBatchCache) GetByIDs(_ context.Context, region string, entity string, ids []string) ([]map[string]any, error) {
	cache.getByIDsCalls = append(cache.getByIDsCalls, fakeMusicGetByIDsCall{
		region: region,
		entity: entity,
		ids:    append([]string(nil), ids...),
	})

	region = strings.ToLower(strings.TrimSpace(region))
	entity = strings.ToLower(strings.TrimSpace(entity))
	if err := cache.batchErrors[entity]; err != nil {
		return nil, err
	}
	records := make([]map[string]any, len(ids))
	for index, id := range ids {
		if record, ok := cache.byID[region][entity][id]; ok {
			records[index] = record
			continue
		}
		for _, candidate := range cache.listByEntity[entity] {
			if shared.NormalizeAnyID(candidate["id"]) == id {
				records[index] = candidate
				break
			}
		}
	}
	return records, nil
}

func (cache *musicVideoBatchCache) GetByCompositeKeys(_ context.Context, _ string, _ string, keys []map[string]any) ([]map[string]any, error) {
	return make([]map[string]any, len(keys)), nil
}

func newMusicVideoBatchCache(t *testing.T, musicRecord map[string]any, categoryRecords []map[string]any, assetVariantRecords []map[string]any, vocalRecords []map[string]any) *musicVideoBatchCache {
	t.Helper()

	if musicRecord == nil {
		musicRecord = map[string]any{"id": 42, "title": "MV Test"}
	}

	return &musicVideoBatchCache{fakeMusicHandlerCache: &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {"musics": {"42": musicRecord}},
		},
		listByEntity: map[string][]map[string]any{
			"musiccategories":    categoryRecords,
			"musicassetvariants": assetVariantRecords,
			"musicvocals":        vocalRecords,
		},
		hasRecords: map[string]map[string]bool{"jp": {"musics": true}},
	}}
}

func requireMusicVideosArray(t *testing.T, responseBody map[string]any) []any {
	t.Helper()

	videosRaw, ok := responseBody["musicVideos"]
	if !ok {
		t.Fatal("expected musicVideos in detail response")
	}
	videos, ok := videosRaw.([]any)
	if !ok {
		t.Fatalf("expected musicVideos array, got %T", videosRaw)
	}
	return videos
}

func assertMusicVideoDescriptor(t *testing.T, rawVideo any, index int, expected expectedMusicVideo) {
	t.Helper()

	video, ok := rawVideo.(map[string]any)
	if !ok {
		t.Fatalf("expected musicVideos[%d] object, got %T", index, rawVideo)
	}
	if video["category"] != expected.category || video["assetbundleName"] != expected.assetbundleName {
		t.Fatalf("unexpected musicVideos[%d]: %v", index, video)
	}
	if expected.musicVocalID == "" {
		if _, exists := video["musicVocalId"]; exists {
			t.Fatalf("expected musicVideos[%d] to omit musicVocalId, got %v", index, video["musicVocalId"])
		}
	} else if video["musicVocalId"] != expected.musicVocalID {
		t.Fatalf("expected musicVideos[%d].musicVocalId=%q, got %v", index, expected.musicVocalID, video["musicVocalId"])
	}
}

func assertMusicVideoBatchLookupCalls(t *testing.T, cache *musicVideoBatchCache, expected []fakeMusicGetByIDsCall) {
	t.Helper()

	if !reflect.DeepEqual(cache.getByIDsCalls, expected) {
		t.Fatalf("expected batched lookups %v, got %v", expected, cache.getByIDsCalls)
	}
	for _, call := range cache.getByIDCalls {
		if call.entity == "musicassetvariants" || call.entity == "musicvocals" {
			t.Fatalf("expected batched lookup for %s, got per-ID read %s", call.entity, call.id)
		}
	}
}

func runMusicVideoDetailTestCase(t *testing.T, testCase musicVideoDetailTestCase) {
	t.Helper()

	cache := newMusicVideoBatchCache(t, testCase.musicRecord, testCase.categoryRecords, testCase.assetVariantRecords, testCase.vocalRecords)
	responseBody := decodeMusicOK(t, doMusicGet(newMusicCategoryRouter(newReadyMusicHandler(cache)), "/api/v1/musics/jp/42/detail"))

	videos := requireMusicVideosArray(t, responseBody)
	if len(videos) != len(testCase.wantVideos) {
		t.Fatalf("expected %d music videos, got %d (%v)", len(testCase.wantVideos), len(videos), responseBody["musicVideos"])
	}
	for index, expected := range testCase.wantVideos {
		assertMusicVideoDescriptor(t, videos[index], index, expected)
	}
	assertMusicHasCategories(t, responseBody, testCase.wantCategories)
	assertMusicVideoBatchLookupCalls(t, cache, testCase.wantBatchLookupCalls)
}

func TestMusicDetailMusicVideos(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []musicVideoDetailTestCase{
		{
			name: "uses padded music id for categories without variants",
			categoryRecords: []map[string]any{
				{"id": 1, "musicId": 42, "musicCategoryName": "original"},
				{"id": 2, "musicId": 42, "musicCategoryName": "mv_2d"},
				{"id": 3, "musicId": 42, "musicCategoryName": "mv"},
			},
			wantVideos: []expectedMusicVideo{
				{category: "original", assetbundleName: "0042"},
				{category: "mv_2d", assetbundleName: "0042"},
			},
			wantCategories: []string{"original", "mv_2d", "mv"},
		},
		{
			name: "uses explicit mv variants and batches variant and vocal reads",
			categoryRecords: []map[string]any{
				{"id": 1, "musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 101},
				{"id": 2, "musicId": 42, "musicCategoryName": "mv_2d", "musicAssetVariantId": 102},
			},
			assetVariantRecords: []map[string]any{
				{"id": 101, "musicVocalId": 201, "musicAssetType": "mv", "assetbundleName": "mv_original_0042"},
				{"id": 102, "musicVocalId": 202, "musicAssetType": "mv", "assetbundleName": "mv_2d_0042"},
			},
			vocalRecords: []map[string]any{
				{"id": 201, "musicId": 42},
				{"id": 202, "musicId": 42},
			},
			wantVideos: []expectedMusicVideo{
				{category: "original", assetbundleName: "mv_original_0042", musicVocalID: "201"},
				{category: "mv_2d", assetbundleName: "mv_2d_0042", musicVocalID: "202"},
			},
			wantCategories: []string{"original", "mv_2d"},
			wantBatchLookupCalls: []fakeMusicGetByIDsCall{
				{region: "jp", entity: "musicassetvariants", ids: []string{"101", "102"}},
				{region: "jp", entity: "musicvocals", ids: []string{"201", "202"}},
			},
		},
		{
			name:        "omits missing and invalid explicit variants without falling back",
			musicRecord: map[string]any{"id": 42, "title": "MV Test", "categories": []any{"original"}},
			categoryRecords: []map[string]any{
				{"id": 1, "musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 301},
				{"id": 2, "musicId": 42, "musicCategoryName": "mv_2d", "musicAssetVariantId": 302},
				{"id": 3, "musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 303},
			},
			assetVariantRecords: []map[string]any{
				{"id": 302, "musicAssetType": "jacket", "assetbundleName": "not_an_mv"},
				{"id": 303, "musicAssetType": "mv", "assetbundleName": "  "},
			},
			wantVideos:     []expectedMusicVideo{},
			wantCategories: []string{"original", "mv_2d", "original"},
			wantBatchLookupCalls: []fakeMusicGetByIDsCall{
				{region: "jp", entity: "musicassetvariants", ids: []string{"301", "302", "303"}},
			},
		},
		{
			name: "returns an empty array when categories are not playable videos",
			categoryRecords: []map[string]any{
				{"id": 1, "musicId": 42, "musicCategoryName": "mv"},
				{"id": 2, "musicId": 42, "musicCategoryName": "image"},
			},
			wantVideos:     []expectedMusicVideo{},
			wantCategories: []string{"mv", "image"},
		},
		{
			name: "omits malformed explicit variant ids instead of using defaults",
			categoryRecords: []map[string]any{
				{"id": 1, "musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": nil},
				{"id": 2, "musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": "  "},
				{"id": 3, "musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": map[string]any{"id": 1}},
				{"id": 4, "musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": true},
				{"id": 5, "musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 1.5},
			},
			wantVideos:     []expectedMusicVideo{},
			wantCategories: []string{"original", "original", "original", "original", "original"},
		},
		{
			name: "preserves embedded object variant ids while keeping categories as strings",
			musicRecord: map[string]any{"id": 42, "title": "MV Test", "categories": []any{
				map[string]any{"musicCategoryName": "original", "musicAssetVariantId": 701},
				map[string]any{"musicCategoryName": "mv_2d", "musicAssetVariantId": 702},
			}},
			assetVariantRecords: []map[string]any{
				{"id": 701, "musicAssetType": "mv", "assetbundleName": "mv_original_0042"},
				{"id": 702, "musicAssetType": "mv", "assetbundleName": "mv_2d_0042"},
			},
			wantVideos: []expectedMusicVideo{
				{category: "original", assetbundleName: "mv_original_0042"},
				{category: "mv_2d", assetbundleName: "mv_2d_0042"},
			},
			wantCategories: []string{"original", "mv_2d"},
			wantBatchLookupCalls: []fakeMusicGetByIDsCall{
				{region: "jp", entity: "musicassetvariants", ids: []string{"701", "702"}},
			},
		},
		{
			name: "rejects unsafe or non-string asset bundle names",
			categoryRecords: []map[string]any{
				{"musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 601},
				{"musicId": 42, "musicCategoryName": "mv_2d", "musicAssetVariantId": 602},
				{"musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 603},
				{"musicId": 42, "musicCategoryName": "mv_2d", "musicAssetVariantId": 604},
				{"musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 605},
				{"musicId": 42, "musicCategoryName": "mv_2d", "musicAssetVariantId": 606},
			},
			assetVariantRecords: []map[string]any{
				{"id": 601, "musicAssetType": "mv", "assetbundleName": 42},
				{"id": 602, "musicAssetType": "mv", "assetbundleName": map[string]any{"name": "bundle"}},
				{"id": 603, "musicAssetType": "mv", "assetbundleName": "../bundle"},
				{"id": 604, "musicAssetType": "mv", "assetbundleName": "bundle/path"},
				{"id": 605, "musicAssetType": "mv", "assetbundleName": `bundle\\path`},
				{"id": 606, "musicAssetType": "mv", "assetbundleName": "bundle\u0000name"},
			},
			wantVideos:     []expectedMusicVideo{},
			wantCategories: []string{"original", "mv_2d", "original", "mv_2d", "original", "mv_2d"},
			wantBatchLookupCalls: []fakeMusicGetByIDsCall{
				{region: "jp", entity: "musicassetvariants", ids: []string{"601", "602", "603", "604", "605", "606"}},
			},
		},
		{
			name: "requires exact category spelling",
			categoryRecords: []map[string]any{
				{"musicId": 42, "musicCategoryName": "Original"},
				{"musicId": 42, "musicCategoryName": "mv_2D"},
			},
			wantVideos:     []expectedMusicVideo{},
			wantCategories: []string{"Original", "mv_2D"},
		},
		{
			name: "rejects a variant whose vocal belongs to another music",
			categoryRecords: []map[string]any{
				{"id": 1, "musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 401},
			},
			assetVariantRecords: []map[string]any{
				{"id": 401, "musicVocalId": 501, "musicAssetType": "mv", "assetbundleName": "mv_0042"},
			},
			vocalRecords: []map[string]any{
				{"id": 501, "musicId": 99},
			},
			wantVideos:     []expectedMusicVideo{},
			wantCategories: []string{"original"},
			wantBatchLookupCalls: []fakeMusicGetByIDsCall{
				{region: "jp", entity: "musicassetvariants", ids: []string{"401"}},
				{region: "jp", entity: "musicvocals", ids: []string{"501"}},
			},
		},
		{
			name:        "falls back to embedded categories when category rows are absent",
			musicRecord: map[string]any{"id": 42, "title": "MV Test", "categories": []any{"original", map[string]any{"musicCategoryName": "mv_2d"}}},
			wantVideos: []expectedMusicVideo{
				{category: "original", assetbundleName: "0042"},
				{category: "mv_2d", assetbundleName: "0042"},
			},
			wantCategories: []string{"original", "mv_2d"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runMusicVideoDetailTestCase(t, testCase)
		})
	}
}

func TestMusicDetailMusicVideoBatchReadErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testCases := []struct {
		name                string
		categoryRecords     []map[string]any
		assetVariantRecords []map[string]any
		failedEntity        string
	}{
		{
			name: "asset variant lookup error",
			categoryRecords: []map[string]any{
				{"musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 801},
			},
			failedEntity: "musicassetvariants",
		},
		{
			name: "vocal lookup error",
			categoryRecords: []map[string]any{
				{"musicId": 42, "musicCategoryName": "original", "musicAssetVariantId": 802},
			},
			assetVariantRecords: []map[string]any{
				{"id": 802, "musicVocalId": 901, "musicAssetType": "mv", "assetbundleName": "mv_0042"},
			},
			failedEntity: "musicvocals",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cache := &musicVideoBatchCache{
				fakeMusicHandlerCache: &fakeMusicHandlerCache{
					byID: map[string]map[string]map[string]map[string]any{
						"jp": {"musics": {"42": {"id": 42, "title": "MV Test"}}},
					},
					listByEntity: map[string][]map[string]any{
						"musiccategories":    testCase.categoryRecords,
						"musicassetvariants": testCase.assetVariantRecords,
					},
					hasRecords: map[string]map[string]bool{"jp": {"musics": true}},
				},
				batchErrors: map[string]error{testCase.failedEntity: errors.New("batch read failed")},
			}
			resp := doMusicGet(newMusicCategoryRouter(newReadyMusicHandler(cache)), "/api/v1/musics/jp/42/detail")
			if resp.Code != http.StatusInternalServerError {
				t.Fatalf("expected status 500, got %d: %s", resp.Code, resp.Body.String())
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal error response: %v", err)
			}
			if body.Error.Code != "MUSIC_QUERY_ERROR" {
				t.Fatalf("expected MUSIC_QUERY_ERROR, got %q", body.Error.Code)
			}
		})
	}
}

func TestMusicEndpointsRequireMusicRecordsEvenIfRelatedRecordsExist(t *testing.T) {
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

func TestMusicAvailabilityEndpointUsesPersistedEntityRecords(t *testing.T) {
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
	}

	musicHandler := newReadyMusicHandler(cache)
	router := gin.New()
	router.GET("/api/v1/musics/regions/:id/availability", musicHandler.AvailableRegionsByID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/regions/1001/availability", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	testutil.AssertRegionAvailabilityResponse(t, resp, []string{"jp"})
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

// batchingMusicHandlerCache adds batch reads to the fake, like the stores in
// production, so tests can check which reads a handler issues.
type batchingMusicHandlerCache struct {
	*fakeMusicHandlerCache
	getByIDsCalls []string
}

func (cache *batchingMusicHandlerCache) GetByIDs(_ context.Context, region string, entity string, ids []string) ([]map[string]any, error) {
	cache.getByIDsCalls = append(cache.getByIDsCalls, entity+":"+strings.Join(ids, ","))
	records := make([]map[string]any, len(ids))
	for position, id := range ids {
		records[position] = cache.byID[region][entity][id]
	}
	return records, nil
}

func (cache *batchingMusicHandlerCache) GetByCompositeKeys(_ context.Context, _ string, _ string, keys []map[string]any) ([]map[string]any, error) {
	return make([]map[string]any, len(keys)), nil
}

func TestMusicListEndpointBatchesRelatedLookups(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &batchingMusicHandlerCache{fakeMusicHandlerCache: &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				"musicartists":      {"77": {"id": 77, "name": "Artist A"}, "78": {"id": 78, "name": "Artist B"}},
				"livestages":        {"66": {"id": 66, "name": "Stage 66"}},
				"releaseconditions": {"5": {"id": 5, "sentence": "clear"}},
			},
		},
		listItems: []map[string]any{
			{"id": 1001, "title": "A", "creatorArtistId": 77, "liveStageId": 66, "releaseConditionId": 5},
			{"id": 1002, "title": "B", "creatorArtistId": 78, "liveStageId": 66, "releaseConditionId": 5},
			{"id": 1003, "title": "C", "creatorArtistId": 77, "liveStageId": 66, "releaseConditionId": 5},
		},
		listTotal: 3,
	}}

	statusStore := &fakeMusicHandlerStatusStore{statuses: []masterdata.SyncStatus{{Region: "jp", Status: "success"}}}
	router := gin.New()
	router.GET("/api/v1/musics/:region/list", NewMusicHandler(usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)).List)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.Code)
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(body.Items))
	}
	assertMusicHasMappedCreatorArtist(t, body.Items[1], 78)
	assertMusicHasMappedLiveStage(t, body.Items[1], 66)
	if condition, ok := body.Items[0]["releaseCondition"].(map[string]any); !ok || condition["sentence"] != "clear" {
		t.Fatalf("expected the release condition to be expanded, got %v", body.Items[0]["releaseCondition"])
	}

	for _, call := range cache.getByIDCalls {
		switch call.entity {
		case "musicartists", "livestages", "releaseconditions":
			t.Fatalf("expected related records to be batched, got GetByID %+v", call)
		}
	}
	want := []string{"livestages:66", "musicartists:77,78", "releaseconditions:5"}
	if !reflect.DeepEqual(cache.getByIDsCalls, want) {
		t.Fatalf("expected one batch per related entity %v, got %v", want, cache.getByIDsCalls)
	}
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

	cache := &fakeMusicHandlerCache{hasRecords: map[string]map[string]bool{"jp": {"musics": true}}}
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

	cache := &fakeMusicHandlerCache{hasRecords: map[string]map[string]bool{"jp": {"musics": true}}}
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

func assertMusicHasCategories(t *testing.T, item map[string]any, expected []string) {
	t.Helper()

	categoriesRaw, ok := item["categories"]
	if !ok {
		t.Fatalf("expected categories in item")
	}

	categories, ok := categoriesRaw.([]any)
	if !ok {
		t.Fatalf("expected categories array, got %T", categoriesRaw)
	}
	if len(categories) != len(expected) {
		t.Fatalf("expected %d categories, got %d (%v)", len(expected), len(categories), categoriesRaw)
	}

	for index, expectedCategory := range expected {
		category, ok := categories[index].(string)
		if !ok {
			t.Fatalf("expected categories[%d] string, got %T", index, categories[index])
		}
		if category != expectedCategory {
			t.Fatalf("expected categories[%d]=%s, got %s", index, expectedCategory, category)
		}
	}
}

func newMusicCategoryRouter(handler *MusicHandler) *gin.Engine {
	router := gin.New()
	router.GET("/api/v1/musics/:region/:id", handler.ByID)
	router.GET("/api/v1/musics/:region/list", handler.List)
	router.GET("/api/v1/musics/:region/:id/detail", handler.DetailByID)
	return router
}

func doMusicGet(router *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func decodeMusicOK(t *testing.T, resp *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if resp.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return body
}

type musicCategoryAggregationCase struct {
	name        string
	cache       *fakeMusicHandlerCache
	path        string
	expect      []string
	assertMusic bool
	listItems   [][]string
	order       []float64
}

func TestMusicCategoryAggregation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []musicCategoryAggregationCase{
		{
			name: "by-id returns aggregated categories",
			cache: &fakeMusicHandlerCache{
				byID: map[string]map[string]map[string]map[string]any{
					"jp": {"musics": {"1001": {"id": 1001, "title": "Test Song", "category": "ignored-embedded"}}},
				},
				listByEntity: map[string][]map[string]any{
					"musiccategories": {
						{"id": 1, "musicId": 1001, "musicCategory": "mv", "seq": 1},
						{"id": 2, "musicId": 1001, "musicCategory": "original", "seq": 2},
					},
				},
			},
			path:   "/api/v1/musics/jp/1001",
			expect: []string{"mv", "original"},
		},
		{
			name: "by-id returns empty categories when absent",
			cache: &fakeMusicHandlerCache{
				byID: map[string]map[string]map[string]map[string]any{
					"jp": {"musics": {"1001": {"id": 1001, "title": "Test Song"}}},
				},
			},
			path:   "/api/v1/musics/jp/1001",
			expect: []string{},
		},
		{
			name: "by-id falls back to embedded categories when entity absent",
			cache: &fakeMusicHandlerCache{
				byID: map[string]map[string]map[string]map[string]any{
					"jp": {"musics": {"1001": {"id": 1001, "title": "Test Song", "categories": []any{"mv", "cover"}}}},
				},
			},
			path:   "/api/v1/musics/jp/1001",
			expect: []string{"mv", "cover"},
		},
		{
			name: "list returns aggregated categories",
			cache: &fakeMusicHandlerCache{
				listItems: []map[string]any{
					{"id": 1, "title": "alpha"},
					{"id": 2, "title": "bravo"},
				},
				listByEntity: map[string][]map[string]any{
					"musiccategories": {
						{"id": 1, "musicId": 1, "musicCategory": "mv", "seq": 1},
						{"id": 2, "musicId": 1, "musicCategory": "original", "seq": 2},
						{"id": 3, "musicId": 2, "musicCategory": "image", "seq": 1},
					},
				},
				listTotal: 2,
			},
			path:      "/api/v1/musics/jp/list?page=1&page_size=20",
			listItems: [][]string{{"mv", "original"}, {"image"}},
		},
		{
			name: "detail returns aggregated categories",
			cache: &fakeMusicHandlerCache{
				byID: map[string]map[string]map[string]map[string]any{
					"jp": {"musics": {"1001": {"id": 1001, "title": "Test Song"}}},
				},
				listByEntity: map[string][]map[string]any{
					"musiccategories": {
						{"id": 1, "musicId": 1001, "musicCategory": "mv", "seq": 1},
						{"id": 2, "musicId": 1001, "musicCategory": "original", "seq": 2},
					},
				},
			},
			path:        "/api/v1/musics/jp/1001/detail",
			expect:      []string{"mv", "original"},
			assertMusic: true,
		},
		{
			name: "list category filter uses aggregated categories",
			cache: &fakeMusicHandlerCache{
				listItems: []map[string]any{
					{"id": 1, "title": "alpha"},
					{"id": 2, "title": "bravo"},
				},
				listByEntity: map[string][]map[string]any{
					"musiccategories": {
						{"id": 1, "musicId": 1, "musicCategory": "mv", "seq": 1},
						{"id": 2, "musicId": 2, "musicCategory": "image", "seq": 1},
					},
				},
				listTotal: 2,
			},
			path:  "/api/v1/musics/jp/list?category=mv",
			order: []float64{1},
		},
		{
			name: "list category filter falls back to embedded categories when entity absent",
			cache: &fakeMusicHandlerCache{
				listItems: []map[string]any{
					{"id": 1, "title": "alpha", "category": "mv"},
					{"id": 2, "title": "bravo", "category": "image"},
				},
				listTotal: 2,
			},
			path:  "/api/v1/musics/jp/list?category=mv",
			order: []float64{1},
		},
		{
			// KR, TW, and CN master data embed each category as an object (the region does not
			// matter to the handler; the fake cache serves jp).
			name: "by-id reads embedded category objects",
			cache: &fakeMusicHandlerCache{
				byID: map[string]map[string]map[string]map[string]any{
					"jp": {"musics": {"1001": {"id": 1001, "title": "Test Song", "categories": []any{
						map[string]any{"musicCategoryName": "mv"},
						map[string]any{"musicCategoryName": "mv_2d"},
						map[string]any{"unrelated": "ignored"},
					}}}},
				},
			},
			path:   "/api/v1/musics/jp/1001",
			expect: []string{"mv", "mv_2d"},
		},
		{
			name: "list reads embedded category objects",
			cache: &fakeMusicHandlerCache{
				listItems: []map[string]any{
					{"id": 1, "title": "alpha", "categories": []any{map[string]any{"musicCategoryName": "mv_2d"}}},
					{"id": 2, "title": "bravo", "categories": []any{"image"}},
				},
				listTotal: 2,
			},
			path:      "/api/v1/musics/jp/list?page=1&page_size=20",
			listItems: [][]string{{"mv_2d"}, {"image"}},
		},
		{
			name: "list category filter matches embedded category objects exactly",
			cache: &fakeMusicHandlerCache{
				listItems: []map[string]any{
					{"id": 1, "title": "alpha", "categories": []any{map[string]any{"musicCategoryName": "mv_2d"}}},
					{"id": 2, "title": "bravo", "categories": []any{
						map[string]any{"musicCategoryName": "mv"},
						map[string]any{"musicCategoryName": "mv_2d"},
					}},
				},
				listTotal: 2,
			},
			path:  "/api/v1/musics/jp/list?category=mv",
			order: []float64{2},
		},
		{
			name: "list category filter matches embedded category names exactly",
			cache: &fakeMusicHandlerCache{
				listItems: []map[string]any{
					{"id": 1, "title": "alpha", "categories": []any{"mv_2d"}},
					{"id": 2, "title": "bravo", "categories": []any{"mv", "mv_2d"}},
				},
				listTotal: 2,
			},
			path:  "/api/v1/musics/jp/list?category=mv",
			order: []float64{2},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runMusicCategoryAggregationCase(t, tc)
		})
	}
}

func runMusicCategoryAggregationCase(t *testing.T, tc musicCategoryAggregationCase) {
	handler := newReadyMusicHandler(tc.cache)
	router := newMusicCategoryRouter(handler)
	resp := doMusicGet(router, tc.path)

	if tc.order != nil {
		assertResponseItemOrder(t, resp.Body.Bytes(), tc.order)
		return
	}

	body := decodeMusicOK(t, resp)

	if tc.listItems != nil {
		items, ok := body["items"].([]any)
		if !ok || len(items) != len(tc.listItems) {
			t.Fatalf("expected %d items, got %v", len(tc.listItems), body["items"])
		}
		for index, expected := range tc.listItems {
			item, ok := items[index].(map[string]any)
			if !ok {
				t.Fatalf("expected item %d object, got %T", index, items[index])
			}
			assertMusicHasCategories(t, item, expected)
		}
		return
	}

	assertMusicHasCategories(t, body, tc.expect)
	if tc.assertMusic {
		music, ok := body["music"].(map[string]any)
		if !ok {
			t.Fatalf("expected music object, got %T", body["music"])
		}
		assertMusicHasCategories(t, music, tc.expect)
	}
}

func TestMusicCategoryMigrationUsesMusicCategoryName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeMusicHandlerCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {"musics": {
				"1001": {"id": 1001, "title": "Canonical Song"},
				"1002": {"id": 1002, "title": "Other Song"},
			}},
		},
		listItems: []map[string]any{
			{"id": 1001, "title": "Canonical Song"},
			{"id": 1002, "title": "Other Song"},
		},
		listByEntity: map[string][]map[string]any{
			"musiccategories": {
				{"id": 1, "musicId": 1001, "musicCategoryName": "mv", "seq": 1},
				{"id": 2, "musicId": 1001, "musicCategoryName": "original", "seq": 2},
				{"id": 3, "musicId": 1002, "musicCategoryName": "image", "seq": 1},
			},
		},
		listTotal: 2,
	}

	handler := newReadyMusicHandler(cache)
	router := newMusicCategoryRouter(handler)

	t.Run("by-id aggregation", func(t *testing.T) {
		resp := doMusicGet(router, "/api/v1/musics/jp/1001")
		assertMusicHasCategories(t, decodeMusicOK(t, resp), []string{"mv", "original"})
	})
	t.Run("list aggregation", func(t *testing.T) {
		resp := doMusicGet(router, "/api/v1/musics/jp/list?page=1&page_size=20")
		body := decodeMusicOK(t, resp)
		items, ok := body["items"].([]any)
		if !ok || len(items) != 2 {
			t.Fatalf("expected 2 items, got %v", body["items"])
		}
		assertMusicHasCategories(t, items[0].(map[string]any), []string{"mv", "original"})
		assertMusicHasCategories(t, items[1].(map[string]any), []string{"image"})
	})
	t.Run("detail aggregation", func(t *testing.T) {
		resp := doMusicGet(router, "/api/v1/musics/jp/1001/detail")
		body := decodeMusicOK(t, resp)
		assertMusicHasCategories(t, body, []string{"mv", "original"})
		music, ok := body["music"].(map[string]any)
		if !ok {
			t.Fatalf("expected music object, got %T", body["music"])
		}
		assertMusicHasCategories(t, music, []string{"mv", "original"})
	})
	t.Run("category filter", func(t *testing.T) {
		resp := doMusicGet(router, "/api/v1/musics/jp/list?category=original")
		assertResponseItemOrder(t, resp.Body.Bytes(), []float64{1001})
	})
}
