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

// newStampSourceTestCache rewards the test stamps through resource boxes the two
// ways regions store them: JP keeps the details inside the box; TW keeps them in
// separate rows.
func newStampSourceTestCache() *missionTrackingCache {
	cache := newStampTestCache()
	detail := func(resourceType string, resourceID int) map[string]any {
		return map[string]any{"resourceType": resourceType, "resourceId": resourceID, "resourceQuantity": 1}
	}
	box := func(id int, purpose string, details ...map[string]any) map[string]any {
		items := make([]any, 0, len(details))
		for _, item := range details {
			items = append(items, item)
		}
		return map[string]any{"id": id, "resourceBoxPurpose": purpose, "details": items}
	}
	cache.listByEntity["jp"][resourceBoxesEntity] = []map[string]any{
		box(1, "shop_item", detail("stamp", 1), detail("material", 7)),
		box(2, "bonds_reward", detail("stamp", 2)),
		box(3, "material_exchange", detail("stamp", 3), detail("stamp", 2)),
		box(4, "character_rank_reward", detail("stamp", 3)),
		box(5, "gift_detail", detail("stamp", 4)),
		box(6, "login_bonus", detail("stamp", 6)),
		// A material with a stamp's ID rewards nothing of the stamp's.
		box(7, "virtual_live_reward", detail("material", 1)),
		// A box of an unnamed purpose does not count as a named source.
		box(8, "mission_reward", detail("stamp", 5)),
	}
	cache.listByEntity["tw"] = map[string][]map[string]any{
		stampsEntity: cache.listByEntity["jp"][stampsEntity],
		resourceBoxDetailsEntity: {
			{"resourceBoxId": 1, "resourceBoxPurpose": "virtual_live_reward", "resourceType": "stamp", "resourceId": 1},
			{"resourceBoxId": 1, "resourceBoxPurpose": "virtual_live_reward", "resourceType": "jewel", "resourceId": 2},
			{"resourceBoxId": 2, "resourceBoxPurpose": "billing_shop_item", "resourceType": "stamp", "resourceId": 3},
		},
	}
	return cache
}

func TestStampsListFiltersBySourceThroughTheReverseIndexes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newStampSourceTestCache()
	router := newStampTestRouter(newMissionTestHandler(cache), nil)

	for path, want := range map[string][]int64{
		"/api/v1/stamps/jp/list?source=shop":                    {1},
		"/api/v1/stamps/jp/list?source=bond":                    {2},
		"/api/v1/stamps/jp/list?source=exchange":                {2, 3},
		"/api/v1/stamps/jp/list?source=rank":                    {3},
		"/api/v1/stamps/jp/list?source=live":                    {},
		"/api/v1/stamps/jp/list?source=crystal":                 {},
		"/api/v1/stamps/jp/list?source=other":                   {4, 5, 6},
		"/api/v1/stamps/jp/list?source=shop,rank":               {1, 3},
		"/api/v1/stamps/jp/list?source=shop,other":              {1, 4, 5, 6},
		"/api/v1/stamps/jp/list?source=other&category=text":     {4, 6},
		"/api/v1/stamps/jp/list?source=exchange&character_id=2": {2, 3},
		"/api/v1/stamps/tw/list?source=live":                    {1},
		"/api/v1/stamps/tw/list?source=crystal":                 {3},
		"/api/v1/stamps/tw/list?source=other":                   {2, 4, 5, 6},
	} {
		var list shared.StampListResponse
		decodeMysekaiResponse(t, router, path, &list)
		if !reflect.DeepEqual(stampIDs(list.Items), want) {
			t.Fatalf("%s: expected %v, got %v", path, want, stampIDs(list.Items))
		}
	}

	// The filter reads reverse indexes only, and not at all without a source.
	if len(cache.listCalls) != 0 {
		t.Fatalf("expected no full entity reads: %+v", cache.listCalls)
	}
	var usedBoxIndex, usedDetailIndex bool
	for _, call := range cache.indexCalls {
		usedBoxIndex = usedBoxIndex || (call.entity == resourceBoxesEntity && call.index == resourceBoxItemIndex)
		usedDetailIndex = usedDetailIndex || (call.entity == resourceBoxDetailsEntity && call.index == resourceBoxDetailItemIndex)
	}
	if !usedBoxIndex || !usedDetailIndex {
		t.Fatalf("expected both reverse indexes to be read, got %+v", cache.indexCalls)
	}
	cache.indexCalls = nil
	var all shared.StampListResponse
	decodeMysekaiResponse(t, router, "/api/v1/stamps/jp/list", &all)
	if len(cache.indexCalls) != 0 {
		t.Fatalf("expected no index reads without a source filter, got %+v", cache.indexCalls)
	}

	if resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/stamps/jp/list?source=gifts"); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown source, got %d", resp.Code)
	}
}

func TestStampComesFrom(t *testing.T) {
	for name, test := range map[string]struct {
		purposes []string
		sources  []string
		want     bool
	}{
		"a named purpose":                   {[]string{"shop_item"}, []string{"shop"}, true},
		"another source":                    {[]string{"shop_item"}, []string{"live"}, false},
		"one of several purposes":           {[]string{"material_exchange", "shop_item"}, []string{"shop"}, true},
		"no box is other":                   {nil, []string{"other"}, true},
		"an unnamed purpose is other":       {[]string{"gift_detail"}, []string{"other"}, true},
		"a named purpose is not other":      {[]string{"bonds_reward"}, []string{"other"}, false},
		"a named and an unnamed purpose":    {[]string{"gift_detail", "bonds_reward"}, []string{"other"}, false},
		"no box is not any named source":    {nil, []string{"shop", "bond"}, false},
		"other or the named source matches": {[]string{"bonds_reward"}, []string{"other", "bond"}, true},
	} {
		if got := stampComesFrom(test.purposes, test.sources); got != test.want {
			t.Fatalf("%s: expected %v, got %v", name, test.want, got)
		}
	}
}
