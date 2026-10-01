package lookups

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
	"sekai-master-api/internal/transport/http/handlers/shared"
)

const stampsListPath = "/api/v1/stamps/:region/list"

func newStampTestCache(extra ...map[string]any) *missionTrackingCache {
	published := int64(1233284400000)
	stamps := []map[string]any{
		{"id": 1, "seq": 30, "stampType": "illustration", "name": "[スタンプ]一歌：おつかれさま！", "assetbundleName": "stamp0001",
			"characterId1": 1, "characterId2": 0, "gameCharacterUnitId": 1, "archivePublishedAt": published, "description": "一歌のキャラクターランク38で獲得"},
		{"id": 2, "seq": 10, "stampType": "illustration", "name": "[スタンプ]一歌と咲希：がんばろう！", "assetbundleName": "stamp0002",
			"characterId1": 1, "characterId2": 2, "archivePublishedAt": published},
		{"id": 3, "seq": 20, "stampType": "illustration", "name": "[スタンプ]咲希：やったー！", "assetbundleName": "stamp0003",
			"characterId1": 2, "archivePublishedAt": published},
		// A character-name text stamp names a unit only; its character comes from the unit.
		{"id": 4, "stampType": "text", "name": "[テキストスタンプ]一歌！", "assetbundleName": "stamp0333", "gameCharacterUnitId": 1,
			"archivePublishedAt": published},
		{"id": 5, "seq": 40, "stampType": "non_character_illustration", "name": "[スタンプ]Happy Halloween", "assetbundleName": "stamp0715",
			"archivePublishedAt": published},
		{"id": 6, "seq": 50, "stampType": "cheerful_carnival_message", "name": "[テキストスタンプ]ありがとう！", "assetbundleName": "stamp0438",
			"archivePublishedAt": published},
		// The same ID twice: the first record wins, like the store's by-ID reads.
		{"id": 1, "stampType": "illustration", "name": "duplicate must be ignored"},
	}
	stamps = append(stamps, extra...)
	cache := &missionTrackingCache{fakeLookupCache: &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{"jp": {
			stampsEntity: stamps,
			stampGameCharacterUnits: {
				{"id": 1, "gameCharacterId": 1, "unit": "light_sound"},
				{"id": 2, "gameCharacterId": 2, "unit": "light_sound"},
				{"id": 27, "gameCharacterId": 21, "unit": "idol"},
			},
		}},
	}}
	indexFakeRecordsByID(cache.fakeLookupCache)
	return cache
}

func newStampTestRouter(handler *LookupHandler, hint **cachehint.Hint) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx, captured := cachehint.WithHint(c.Request.Context())
		if hint != nil {
			*hint = captured
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.GET(stampsListPath, handler.StampsList)
	return router
}

func stampIDs(items []shared.StampListItemResponse) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestStampsListProjectsCategoriesAndCharacters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newStampTestCache()
	router := newStampTestRouter(newMissionTestHandler(cache), nil)

	var list shared.StampListResponse
	decodeMysekaiResponse(t, router, "/api/v1/stamps/jp/list", &list)
	if want := []int64{1, 2, 3, 4, 5, 6}; !reflect.DeepEqual(stampIDs(list.Items), want) || list.Pagination.Total != 6 {
		t.Fatalf("expected stamps %v in stored order, got %v (%+v)", want, stampIDs(list.Items), list.Pagination)
	}
	wantCategories := []string{"character", "bond", "character", "text", "other", "text"}
	for position, item := range list.Items {
		if item.Category != wantCategories[position] {
			t.Fatalf("stamp %d: expected category %s, got %s", item.ID, wantCategories[position], item.Category)
		}
	}
	first := list.Items[0]
	if first.Name != "[スタンプ]一歌：おつかれさま！" || *first.AssetbundleName != "stamp0001" || *first.Description != "一歌のキャラクターランク38で獲得" ||
		*first.Seq != 30 || *first.GameCharacterUnitID != 1 || !reflect.DeepEqual(first.CharacterIDs, []int64{1}) {
		t.Fatalf("unexpected stamp %+v", first)
	}
	if !reflect.DeepEqual(list.Items[1].CharacterIDs, []int64{1, 2}) || len(list.Items[4].CharacterIDs) != 0 {
		t.Fatalf("expected the bond stamp's characters in slot order and none for the other stamp: %+v", list.Items)
	}
	// The character-name text stamp gets its character from its unit, and stays a text stamp.
	if list.Items[3].Category != "text" || !reflect.DeepEqual(list.Items[3].CharacterIDs, []int64{1}) {
		t.Fatalf("expected the text stamp to be text of character 1, got %+v", list.Items[3])
	}
	if len(cache.listCalls) != 0 {
		t.Fatalf("expected the list to read its projection only: %+v", cache.listCalls)
	}
}

func TestStampsListFiltersSortsAndPages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newStampTestRouter(newMissionTestHandler(newStampTestCache()), nil)

	for path, want := range map[string][]int64{
		"/api/v1/stamps/jp/list?category=bond":                     {2},
		"/api/v1/stamps/jp/list?category=character,other":          {1, 3, 5},
		"/api/v1/stamps/jp/list?category=text":                     {4, 6},
		"/api/v1/stamps/jp/list?character_id=1":                    {1, 2, 4},
		"/api/v1/stamps/jp/list?character_id=1,2":                  {2},
		"/api/v1/stamps/jp/list?character_id=2&category=character": {3},
		"/api/v1/stamps/jp/list?name=ハロウィン":                        {},
		"/api/v1/stamps/jp/list?name=%E5%92%B2%E5%B8%8C":           {2, 3},
		"/api/v1/stamps/jp/list?sort_by=seq&sort_order=desc":       {6, 5, 1, 3, 2, 4},
		"/api/v1/stamps/jp/list?sort_by=id&sort_order=desc":        {6, 5, 4, 3, 2, 1},
		"/api/v1/stamps/jp/list?page=2&page_size=4":                {5, 6},
	} {
		var list shared.StampListResponse
		decodeMysekaiResponse(t, router, path, &list)
		if !reflect.DeepEqual(stampIDs(list.Items), want) {
			t.Fatalf("%s: expected %v, got %v", path, want, stampIDs(list.Items))
		}
	}

	var list shared.StampListResponse
	decodeMysekaiResponse(t, router, "/api/v1/stamps/jp/list?page=2&page_size=4", &list)
	if list.Pagination.Total != 6 || list.Pagination.TotalPages != 2 || list.Pagination.HasNext {
		t.Fatalf("unexpected pagination %+v", list.Pagination)
	}
	for _, path := range []string{
		"/api/v1/stamps/jp/list?category=bonds",
		"/api/v1/stamps/jp/list?character_id=one",
		"/api/v1/stamps/jp/list?sort_by=name",
		"/api/v1/stamps/jp/list?spoiler=maybe",
		"/api/v1/stamps/jp/list?page_size=101",
	} {
		if resp := serveLookupRequest(t, router, http.MethodGet, path); resp.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", path, resp.Code)
		}
	}
}

func TestStampsListHidesUnpublishedStampsAndTellsTheCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC().Truncate(time.Millisecond)
	soon, later := now.Add(2*time.Hour), now.Add(48*time.Hour)
	cache := newStampTestCache(
		map[string]any{"id": 7, "stampType": "illustration", "name": "later", "characterId1": 3, "archivePublishedAt": later.UnixMilli()},
		map[string]any{"id": 8, "stampType": "illustration", "name": "soon", "characterId1": 3, "archivePublishedAt": soon.UnixMilli()},
	)
	var hint *cachehint.Hint
	router := newStampTestRouter(newMissionTestHandler(cache), &hint)

	var list shared.StampListResponse
	decodeMysekaiResponse(t, router, "/api/v1/stamps/jp/list", &list)
	if want := []int64{1, 2, 3, 4, 5, 6}; !reflect.DeepEqual(stampIDs(list.Items), want) {
		t.Fatalf("expected the unpublished stamps hidden, got %v", stampIDs(list.Items))
	}
	if until, ok := hint.Until(); !ok || !until.Equal(soon) {
		t.Fatalf("expected the cache to learn the first publish time %v, got %v (%v)", soon, until, ok)
	}

	decodeMysekaiResponse(t, router, "/api/v1/stamps/jp/list?spoiler=true", &list)
	if want := []int64{1, 2, 3, 4, 5, 6, 7, 8}; !reflect.DeepEqual(stampIDs(list.Items), want) {
		t.Fatalf("expected spoiler=true to list every stamp, got %v", stampIDs(list.Items))
	}
	if _, ok := hint.Until(); ok {
		t.Fatal("expected no cache hint when nothing is hidden")
	}
}
