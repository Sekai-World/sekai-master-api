package lookups

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
	"sekai-master-api/internal/transport/http/handlers/shared"
)

const (
	costume3DOutfitsListPath = "/api/v1/costume3dOutfits/:region/list"
	costume3DOutfitByIDPath  = "/api/v1/costume3dOutfits/:region/:id"
)

func newCostume3DOutfitTestRouter(handler *LookupHandler, hint **cachehint.Hint) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx, captured := cachehint.WithHint(c.Request.Context())
		if hint != nil {
			*hint = captured
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.GET(costume3DOutfitsListPath, handler.Costume3DOutfitsList)
	router.GET(costume3DOutfitByIDPath, handler.Costume3DOutfitsByID)
	return router
}

func newCostume3DOutfitTestCache(rowsByRegion map[string]map[string][]map[string]any) *missionTrackingCache {
	cache := &missionTrackingCache{fakeLookupCache: &fakeLookupCache{listByEntity: rowsByRegion}}
	indexFakeRecordsByID(cache.fakeLookupCache)
	return cache
}

func costume3DOutfitIDs(items []shared.Costume3DOutfitResponse) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

// jpCostume is a costume row the way JP and EN embed display fields.
func jpCostume(id int, group int, name string, character int, color int, part string) map[string]any {
	return map[string]any{
		"id": id, "costume3dGroupId": group, "name": name, "designer": "Designer of " + name,
		"characterId": character, "colorId": color, "colorName": "Color", "partType": part,
		"costume3dRarity": "rare", "costume3dType": "normal", "assetbundleName": name + "_" + part,
		"archivePublishedAt": 1000 + id,
	}
}

func newJPOutfitCache() *missionTrackingCache {
	rows := []map[string]any{
		jpCostume(30, 3, "Punk", 2, 1, "head"),
		jpCostume(11, 1, "Star", 1, 1, "head"),
		jpCostume(12, 1, "Star", 1, 1, "body"),
		jpCostume(13, 1, "Star", 1, 2, "body"),
		jpCostume(21, 2, "Star", 2, 1, "body"),
		jpCostume(31, 3, "Punk", 2, 1, "body"),
		jpCostume(41, 4, "Bear", 3, 1, "body"),
	}
	rows[6]["costume3dRarity"] = "normal"
	// Placeholders share a group with a real outfit and are never listed.
	placeholder := jpCostume(1, 1, "Not set", 1, 1, "head")
	placeholder["archiveDisplayType"] = "none"
	rows = append(rows, placeholder)
	return newCostume3DOutfitTestCache(map[string]map[string][]map[string]any{"jp": {costume3dsEntity: rows}})
}

func TestCostume3DOutfitsListGroupsCharactersOfOneNameIntoOneOutfit(t *testing.T) {
	router := newCostume3DOutfitTestRouter(newMissionTestHandler(newJPOutfitCache()), nil)

	var list shared.Costume3DOutfitListResponse
	decodeMysekaiResponse(t, router, "/api/v1/costume3dOutfits/jp/list", &list)

	if want := []int64{11, 30, 41}; !reflect.DeepEqual(costume3DOutfitIDs(list.Items), want) {
		t.Fatalf("expected one outfit per name in ID order without placeholders, got %v", costume3DOutfitIDs(list.Items))
	}
	if list.Pagination.Total != 3 {
		t.Fatalf("expected the total to count outfits, got %+v", list.Pagination)
	}
	star := list.Items[0]
	if star.Name == nil || *star.Name != "Star" || star.Designer == nil || *star.Designer != "Designer of Star" {
		t.Fatalf("unexpected outfit fields: %+v", star)
	}
	if star.Rarity == nil || *star.Rarity != "rare" || star.Type == nil || *star.Type != "normal" {
		t.Fatalf("unexpected rarity or type: %+v", star)
	}
	if !reflect.DeepEqual(star.CharacterIDs, []int64{1, 2}) || !reflect.DeepEqual(star.ColorIDs, []int64{1, 2}) || !reflect.DeepEqual(star.PartTypes, []string{"body", "head"}) {
		t.Fatalf("unexpected outfit contents: %+v", star)
	}
	if star.AssetbundleName == nil || *star.AssetbundleName != "Star_body" {
		t.Fatalf("expected the lowest-ID body as the thumbnail, got %v", star.AssetbundleName)
	}
	if star.PublishedAt == nil || *star.PublishedAt != 1011 {
		t.Fatalf("expected the earliest archivePublishedAt, got %v", star.PublishedAt)
	}
}

func TestCostume3DOutfitsListNormalizesEveryRegionSchema(t *testing.T) {
	groups := []map[string]any{
		{"groupId": 1, "name": "Star", "characterId": 1, "rarity": "rare", "designer": "D", "publishedAt": 500, "archivePublishedAt": 600, "archiveDisplayType": "normal"},
		{"groupId": 2, "name": "Star", "characterId": 2, "rarity": "rare", "designer": "D", "publishedAt": 500, "archivePublishedAt": 600, "archiveDisplayType": "normal"},
		{"groupId": 3, "name": "Hidden", "characterId": 3, "archiveDisplayType": "none"},
	}
	grouped := []map[string]any{
		{"id": 11, "costume3dGroupId": 1, "colorId": 1, "partType": "body", "assetbundleName": "star_body"},
		{"id": 21, "costume3dGroupId": 2, "colorId": 1, "partType": "body", "assetbundleName": "star_body"},
		{"id": 31, "costume3dGroupId": 3, "colorId": 1, "partType": "body", "assetbundleName": "hidden_body"},
	}
	// CN keeps the older key names on the costume.
	cnStyle := []map[string]any{
		{"id": 11, "groupId": 1, "costume3dColorId": 1, "costume3dPartType": "body", "assetbundleName": "star_body"},
		{"id": 21, "groupId": 2, "costume3dColorId": 1, "costume3dPartType": "body", "assetbundleName": "star_body"},
		{"id": 31, "groupId": 3, "costume3dColorId": 1, "costume3dPartType": "body", "assetbundleName": "hidden_body"},
	}
	embedded := []map[string]any{jpCostume(11, 1, "Star", 1, 1, "body"), jpCostume(21, 2, "Star", 2, 1, "body")}
	for _, row := range embedded {
		row["archivePublishedAt"], row["assetbundleName"] = 600, "star_body"
	}
	cache := newCostume3DOutfitTestCache(map[string]map[string][]map[string]any{
		"jp": {costume3dsEntity: embedded},
		"en": {costume3dsEntity: embedded},
		"tw": {costume3dsEntity: grouped, costume3dGroupsEntity: groups},
		"kr": {costume3dsEntity: grouped, costume3dGroupsEntity: groups},
		"cn": {costume3dsEntity: cnStyle, costume3dGroupsEntity: groups},
	})
	router := newCostume3DOutfitTestRouter(newCostume3DLookupHandler(cache.fakeLookupCache, "jp", "en", "tw", "kr", "cn"), nil)

	for _, region := range []string{"jp", "en", "tw", "kr", "cn"} {
		var list shared.Costume3DOutfitListResponse
		decodeMysekaiResponse(t, router, "/api/v1/costume3dOutfits/"+region+"/list", &list)
		if len(list.Items) != 1 {
			t.Fatalf("%s: expected one outfit, got %+v", region, list.Items)
		}
		outfit := list.Items[0]
		if outfit.ID != 11 || outfit.Name == nil || *outfit.Name != "Star" {
			t.Fatalf("%s: unexpected outfit %+v", region, outfit)
		}
		if !reflect.DeepEqual(outfit.CharacterIDs, []int64{1, 2}) || !reflect.DeepEqual(outfit.ColorIDs, []int64{1}) || !reflect.DeepEqual(outfit.PartTypes, []string{"body"}) {
			t.Fatalf("%s: unexpected contents %+v", region, outfit)
		}
		if outfit.AssetbundleName == nil || *outfit.AssetbundleName != "star_body" || outfit.PublishedAt == nil || *outfit.PublishedAt != 600 {
			t.Fatalf("%s: unexpected thumbnail or publish time %+v", region, outfit)
		}
	}
}

func TestCostume3DOutfitsListFiltersSortsAndPages(t *testing.T) {
	router := newCostume3DOutfitTestRouter(newMissionTestHandler(newJPOutfitCache()), nil)

	ids := func(query string) ([]int64, shared.PaginationResponse) {
		t.Helper()
		var list shared.Costume3DOutfitListResponse
		decodeMysekaiResponse(t, router, "/api/v1/costume3dOutfits/jp/list?"+query, &list)
		return costume3DOutfitIDs(list.Items), list.Pagination
	}

	cases := []struct {
		query string
		want  []int64
	}{
		{"name=sta", []int64{11}},
		{"character_id=2", []int64{11, 30}},
		{"character_id=1,3", []int64{11, 41}},
		{"rarity=normal", []int64{41}},
		{"rarity=normal,rare&character_id=2&name=punk", []int64{30}},
		{"name=nothing", []int64{}},
		{"sort_by=name&sort_order=asc", []int64{41, 30, 11}},
		{"sort_by=id&sort_order=desc", []int64{41, 30, 11}},
		{"sort_by=publishedAt&sort_order=desc", []int64{41, 30, 11}},
		{"page=2&page_size=2", []int64{41}},
		{"page=3&page_size=2", []int64{}},
	}
	for _, testCase := range cases {
		if got, _ := ids(testCase.query); !reflect.DeepEqual(got, testCase.want) {
			t.Fatalf("%s: expected %v, got %v", testCase.query, testCase.want, got)
		}
	}
	if _, pagination := ids("page=1&page_size=2"); pagination.Total != 3 || pagination.TotalPages != 2 || !pagination.HasNext {
		t.Fatalf("unexpected pagination %+v", pagination)
	}

	for _, query := range []string{"page=0", "page_size=101", "sort_by=rarity", "character_id=x", "spoiler=maybe"} {
		if resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume3dOutfits/jp/list?"+query); resp.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", query, resp.Code)
		}
	}
}

func TestCostume3DOutfitsHideUnpublishedCostumesAndTellTheCache(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	soon, later := now.Add(2*time.Hour), now.Add(48*time.Hour)
	withPublishTime := func(row map[string]any, at time.Time) map[string]any {
		row["archivePublishedAt"] = at.UnixMilli()
		return row
	}
	cache := newCostume3DOutfitTestCache(map[string]map[string][]map[string]any{"jp": {costume3dsEntity: {
		withPublishTime(jpCostume(11, 1, "Star", 1, 1, "body"), now.Add(-time.Hour)),
		// A character added to an outfit later stays hidden until then.
		withPublishTime(jpCostume(21, 2, "Star", 2, 1, "body"), later),
		withPublishTime(jpCostume(31, 3, "Next", 3, 1, "body"), soon),
	}}})
	var hint *cachehint.Hint
	router := newCostume3DOutfitTestRouter(newMissionTestHandler(cache), &hint)

	var list shared.Costume3DOutfitListResponse
	decodeMysekaiResponse(t, router, "/api/v1/costume3dOutfits/jp/list", &list)
	if want := []int64{11}; !reflect.DeepEqual(costume3DOutfitIDs(list.Items), want) {
		t.Fatalf("expected the unpublished outfit hidden, got %v", costume3DOutfitIDs(list.Items))
	}
	if !reflect.DeepEqual(list.Items[0].CharacterIDs, []int64{1}) {
		t.Fatalf("expected the unpublished character hidden, got %v", list.Items[0].CharacterIDs)
	}
	if until, ok := hint.Until(); !ok || !until.Equal(soon) {
		t.Fatalf("expected the cache to learn the first publish time %v, got %v (%v)", soon, until, ok)
	}

	if resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume3dOutfits/jp/31"); resp.Code != http.StatusNotFound {
		t.Fatalf("expected an unpublished outfit to be not found, got %d", resp.Code)
	}
	if until, ok := hint.Until(); !ok || !until.Equal(soon) {
		t.Fatalf("expected the by-ID answer to expire at the first publish time, got %v (%v)", until, ok)
	}

	decodeMysekaiResponse(t, router, "/api/v1/costume3dOutfits/jp/list?spoiler=true", &list)
	if want := []int64{11, 31}; !reflect.DeepEqual(costume3DOutfitIDs(list.Items), want) {
		t.Fatalf("expected spoiler=true to list every outfit, got %v", costume3DOutfitIDs(list.Items))
	}
	if _, ok := hint.Until(); ok {
		t.Fatal("expected no cache hint when nothing is hidden")
	}
}

func TestCostume3DOutfitByIDReturnsItsColorsAndCostumes(t *testing.T) {
	cache := newJPOutfitCache()
	costumes := cache.listByEntity["jp"][costume3dsEntity]
	costumes[2]["howToObtain"] = "Shop"
	costumes[1]["colorName"] = "Original"
	costumes[3]["colorName"] = "Another"
	indexFakeRecordsByID(cache.fakeLookupCache)
	router := newCostume3DOutfitTestRouter(newMissionTestHandler(cache), nil)

	var detail shared.Costume3DOutfitDetailResponse
	decodeMysekaiResponse(t, router, "/api/v1/costume3dOutfits/jp/11", &detail)

	if detail.ID != 11 || detail.Name == nil || *detail.Name != "Star" || !reflect.DeepEqual(detail.CharacterIDs, []int64{1, 2}) {
		t.Fatalf("unexpected summary: %+v", detail.Costume3DOutfitResponse)
	}
	if len(detail.Colors) != 2 || *detail.Colors[0].Name != "Original" || *detail.Colors[1].Name != "Another" {
		t.Fatalf("unexpected colors: %+v", detail.Colors)
	}
	ids := make([]int64, 0, len(detail.Costumes))
	for _, costume := range detail.Costumes {
		ids = append(ids, costume.ID)
	}
	if want := []int64{11, 12, 13, 21}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("expected the outfit's costumes in ID order, got %v", ids)
	}
	second := detail.Costumes[1]
	if second.HowToObtain == nil || *second.HowToObtain != "Shop" || second.GroupID == nil || *second.GroupID != 1 || second.CharacterID == nil || *second.CharacterID != 1 {
		t.Fatalf("unexpected costume: %+v", second)
	}
	if second.PartType == nil || *second.PartType != "body" || second.ColorID == nil || *second.ColorID != 1 || second.AssetbundleName == nil || *second.AssetbundleName != "Star_body" {
		t.Fatalf("unexpected costume parts: %+v", second)
	}
}

func TestCostume3DOutfitByIDNamesColorsFromTheColorEntity(t *testing.T) {
	cache := newCostume3DOutfitTestCache(map[string]map[string][]map[string]any{"tw": {
		costume3dsEntity: {
			{"id": 11, "costume3dGroupId": 1, "colorId": 1, "partType": "body", "assetbundleName": "star_body"},
			{"id": 12, "costume3dGroupId": 1, "colorId": 2, "partType": "body", "assetbundleName": "star_body_01", "colorName": "On the row"},
			{"id": 13, "costume3dGroupId": 1, "colorId": 3, "partType": "body", "assetbundleName": "star_body_02"},
		},
		costume3dGroupsEntity: {{"groupId": 1, "name": "Star", "characterId": 1}},
		costume3DColorsEntity: {{"id": 1, "name": "Original"}, {"id": 2, "name": "Other"}},
	}})
	router := newCostume3DOutfitTestRouter(newMissionTestHandler(cache), nil)

	var detail shared.Costume3DOutfitDetailResponse
	decodeMysekaiResponse(t, router, "/api/v1/costume3dOutfits/tw/11", &detail)

	if len(detail.Colors) != 3 || *detail.Colors[0].Name != "Original" || *detail.Colors[1].Name != "On the row" || detail.Colors[2].Name != nil {
		t.Fatalf("unexpected colors: %+v", detail.Colors)
	}
}

func TestCostume3DOutfitByIDRejectsUnknownAndInvalidIDs(t *testing.T) {
	router := newCostume3DOutfitTestRouter(newMissionTestHandler(newJPOutfitCache()), nil)

	cases := map[string]int{
		// A costume inside an outfit is not an outfit, and neither is a placeholder.
		"12":  http.StatusNotFound,
		"1":   http.StatusNotFound,
		"999": http.StatusNotFound,
		"abc": http.StatusBadRequest,
		"0":   http.StatusBadRequest,
	}
	for id, want := range cases {
		resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume3dOutfits/jp/"+id)
		if resp.Code != want {
			t.Fatalf("%s: expected %d, got %d", id, want, resp.Code)
		}
		if want == http.StatusNotFound && !strings.Contains(resp.Body.String(), "COSTUME_3D_OUTFIT_NOT_FOUND") {
			t.Fatalf("%s: unexpected body %s", id, resp.Body.String())
		}
	}
}

func TestCostume3DOutfitKeyFallsBackToTheGroup(t *testing.T) {
	rows := []costume3DRow{
		{id: 1, source: costume3DRecordSource{"id": 1, "costume3dGroupId": 7}},
		{id: 2, source: costume3DRecordSource{"id": 2, "costume3dGroupId": 7}},
		{id: 3, source: costume3DRecordSource{"id": 3, "costume3dGroupId": 8}},
		{id: 4, source: costume3DRecordSource{"id": 4}},
		{id: 5, source: costume3DRecordSource{"id": 5, "name": " Star "}},
		{id: 6, source: costume3DRecordSource{"id": 6, "name": "Star"}},
	}

	outfits, reveal := groupCostume3DOutfits(rows, false, time.Now())

	if !reveal.IsZero() {
		t.Fatalf("expected nothing hidden, got %v", reveal)
	}
	got := map[int64]int{}
	for _, outfit := range outfits {
		got[outfit.id] = len(outfit.rows)
	}
	if want := map[int64]int{1: 2, 3: 1, 5: 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected nameless costumes grouped by their group and others by trimmed name, got %v", got)
	}
}
