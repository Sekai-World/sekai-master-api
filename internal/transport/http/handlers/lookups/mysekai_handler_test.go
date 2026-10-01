package lookups

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
	"sekai-master-api/internal/transport/http/handlers/shared"
)

func newMysekaiTestRouter(handler *LookupHandler) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx, _ := cachehint.WithHint(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.GET("/api/v1/mysekaiFixtures/:region/list", handler.MysekaiFixturesList)
	router.GET("/api/v1/mysekaiFixtures/:region/filters", handler.MysekaiFixtureFilters)
	router.GET("/api/v1/mysekaiFixtures/:region/:id", handler.MysekaiFixturesByID)
	router.GET("/api/v1/mysekaiMaterials/:region/list", handler.MysekaiMaterialsList)
	router.GET("/api/v1/mysekaiMaterials/:region/:id", handler.MysekaiMaterialsByID)
	router.GET("/api/v1/mysekaiMusicRecords/:region/list", handler.MysekaiMusicRecordsList)
	router.GET("/api/v1/mysekaiMusicRecords/:region/filters", handler.MysekaiMusicRecordFilters)
	router.GET("/api/v1/mysekaiShops/:region/list", handler.MysekaiShopsList)
	return router
}

func newMysekaiTestCache() *missionTrackingCache {
	tagGroup := func(id int, tags ...int) map[string]any {
		group := map[string]any{"id": id}
		for index, tag := range tags {
			group["mysekaiFixtureTagId"+formatID(int64(index+1))] = tag
		}
		return group
	}
	cache := &missionTrackingCache{fakeLookupCache: &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				mysekaiFixturesEntity: {
					{"id": 1, "seq": 30, "name": "Table", "pronunciation": "てーぶる", "mysekaiFixtureType": "normal",
						"mysekaiFixtureMainGenreId": 2, "mysekaiFixtureSubGenreId": 3, "mysekaiSettableLayoutType": "floor",
						"gridSize": map[string]any{"width": 2, "depth": 2, "height": 1}, "assetbundleName": "mdl_table",
						"mysekaiFixtureTagGroup": tagGroup(1, 1001, 5), "flavorText": "A table", "colorCode": "#4455dd",
						"mysekaiFixtureAnotherColors": []any{map[string]any{"textureId": 2, "colorCode": "#33aaee"}},
						"mysekaiSettableSiteType":     "any", "isAssembled": true, "isDisassembled": true, "firstPutCost": 35, "secondPutCost": 2,
						"mysekaiFixtureGameCharacterGroupPerformanceBonusId": 7},
					{"id": 2, "seq": 10, "name": "Chair", "pronunciation": "いす", "mysekaiFixtureMainGenreId": 2,
						"mysekaiFixtureSubGenreId": 4, "mysekaiFixtureTagGroup": tagGroup(2, 1002, 5, 61)},
					{"id": 1, "name": "duplicate must be ignored"},
					{"id": 3, "seq": 20, "name": "Wall", "mysekaiFixtureType": "surface_appearance", "mysekaiFixtureMainGenreId": 7,
						"mysekaiFixtureTagGroup": tagGroup(3, 61)},
				},
				mysekaiFixtureMainGenresEntity: {
					{"id": 1, "name": "All", "assetbundleName": "icon_all"},
					{"id": 2, "name": "General", "assetbundleName": "icon_general"},
					{"id": 7, "name": "Wall", "assetbundleName": "icon_wall"},
				},
				mysekaiFixtureSubGenresEntity: {
					{"id": 3, "name": "Tables"},
					{"id": 4, "name": "Chairs"},
					{"id": 9, "name": "Unused"},
				},
				mysekaiFixtureTagsEntity: {
					{"id": 5, "name": "Ichika", "mysekaiFixtureTagType": "game_character", "externalId": 1},
					{"id": 6, "name": "Saki", "mysekaiFixtureTagType": "game_character", "externalId": 2},
					{"id": 61, "name": "Series", "mysekaiFixtureTagType": "series"},
					{"id": 1001, "name": "Table", "mysekaiFixtureTagType": "none"},
					{"id": 1002, "name": "Chair", "mysekaiFixtureTagType": "none"},
				},
				mysekaiBlueprintsEntity: {
					{"id": 11, "mysekaiCraftType": "mysekai_fixture", "craftTargetId": 1, "isEnableSketch": true,
						"isObtainedByConvert": false, "craftCountLimit": 1},
					{"id": 12, "mysekaiCraftType": "mysekai_tool", "craftTargetId": 1},
					{"id": 13, "mysekaiCraftType": "mysekai_fixture", "craftTargetId": 2},
				},
				mysekaiBlueprintMaterialCostsEntity: {
					{"id": 1, "mysekaiBlueprintId": 11, "mysekaiMaterialId": 2, "seq": 2, "quantity": 5},
					{"id": 2, "mysekaiBlueprintId": 11, "mysekaiMaterialId": 1, "seq": 1, "quantity": 10},
					{"id": 3, "mysekaiBlueprintId": 12, "mysekaiMaterialId": 1, "seq": 1, "quantity": 99},
					{"id": 4, "mysekaiBlueprintId": 13, "mysekaiMaterialId": 1, "seq": 1, "quantity": 3},
				},
				mysekaiFixtureDisassembleMaterialsEntity: {
					{"id": 1, "mysekaiFixtureId": 1, "mysekaiMaterialId": 2, "seq": 1, "quantity": 1},
				},
				mysekaiFixtureCharacterBonusesEntity: {
					{"id": 7, "mysekaiFixtureGameCharacterGroupId": 70, "bonusRate": 1.5},
				},
				mysekaiFixtureCharacterGroupsEntity: {
					{"id": 1, "groupId": 70, "gameCharacterId": 3},
					{"id": 2, "groupId": 70, "gameCharacterId": 1},
					{"id": 3, "groupId": 71, "gameCharacterId": 9},
				},
				mysekaiMaterialsEntity: {
					{"id": 1, "seq": 1, "name": "Wood", "mysekaiMaterialType": "wood", "mysekaiMaterialRarityType": "rarity_1",
						"iconAssetbundleName": "item_wood_1", "description": "Logs", "mysekaiSiteIds": []any{5, 7}},
					{"id": 2, "seq": 2, "name": "Stone", "mysekaiMaterialType": "mineral", "iconAssetbundleName": "item_mineral_1"},
					{"id": 3, "seq": 3, "name": "Ichika's thing", "mysekaiMaterialType": "game_character"},
				},
				mysekaiSitesEntity: {
					{"id": 5, "name": "Grassland"},
					{"id": 7, "name": "Beach"},
				},
				mysekaiMaterialCharacterRelationEntity: {
					{"id": 1, "groupId": 1, "mysekaiMaterialId": 3, "gameCharacterId": 1},
				},
				mysekaiShopsEntity: {
					{"id": 1, "mysekaiShopType": "material", "seq": 1, "resourceBoxId": 1,
						"mysekaiShopExchangeLimitType": "limited_per_mysekai_colorful_pass", "mysekaiShopExchangeLimitValue": 3},
					{"id": 2, "mysekaiShopType": "material", "seq": 2, "resourceBoxId": 2, "mysekaiShopExchangeLimitType": "none"},
					{"id": 101, "mysekaiShopType": "tool", "seq": 1, "resourceBoxId": 101,
						"mysekaiShopExchangeLimitType": "limited_per_mysekai_colorful_pass", "mysekaiShopExchangeLimitValue": 99},
				},
				mysekaiShopCostsEntity: {
					{"id": 1, "mysekaiShopId": 1, "seq": 1, "resourceType": "jewel", "quantity": 500},
					{"id": 2, "mysekaiShopId": 2, "seq": 1, "resourceType": "jewel", "quantity": 100},
					{"id": 3, "mysekaiShopId": 101, "seq": 1, "resourceType": "jewel", "quantity": 100},
				},
				// Box IDs repeat across purposes; the shop reads only mysekai_shop boxes.
				"resourceboxes": {
					{"id": 1, "resourceBoxPurpose": "mission_reward", "details": []any{
						map[string]any{"resourceType": "jewel", "resourceQuantity": 50}}},
					{"id": 1, "resourceBoxPurpose": "mysekai_shop", "details": []any{
						map[string]any{"resourceType": "mysekai_material", "resourceId": 2, "resourceQuantity": 1, "seq": 1}}},
					{"id": 2, "resourceBoxPurpose": "mysekai_shop", "details": []any{
						map[string]any{"resourceType": "mysekai_material", "resourceId": 1, "resourceQuantity": 15, "seq": 1}}},
					{"id": 101, "resourceBoxPurpose": "mysekai_shop", "details": []any{
						map[string]any{"resourceType": "mysekai_tool", "resourceId": 10, "resourceQuantity": 1, "seq": 1}}},
				},
				mysekaiToolsEntity: {
					{"id": 10, "name": "Chainsaw", "mysekaiToolType": "axe", "assetbundleName": "ax0005", "description": "Cuts trees"},
				},
			},
		},
	}}
	indexFakeRecordsByID(cache.fakeLookupCache)
	return cache
}

// indexFakeRecordsByID fills byID from listByEntity, keeping the first record
// of each ID like the store does, so GetByID finds listed records.
func indexFakeRecordsByID(cache *fakeLookupCache) {
	cache.byID = map[string]map[string]map[string]map[string]any{}
	for region, entities := range cache.listByEntity {
		cache.byID[region] = map[string]map[string]map[string]any{}
		for entity, records := range entities {
			byID := map[string]map[string]any{}
			for _, record := range records {
				id := shared.NormalizeAnyID(record["id"])
				if _, exists := byID[id]; !exists {
					byID[id] = record
				}
			}
			cache.byID[region][entity] = byID
		}
	}
}

func decodeMysekaiResponse(t *testing.T, router *gin.Engine, path string, target any) {
	t.Helper()
	resp := serveLookupRequest(t, router, http.MethodGet, path)
	if resp.Code != http.StatusOK {
		t.Fatalf("%s: expected 200, got %d: %s", path, resp.Code, resp.Body.String())
	}
	if err := json.Unmarshal(resp.Body.Bytes(), target); err != nil {
		t.Fatalf("%s: unmarshal: %v", path, err)
	}
}

func mysekaiFixtureIDs(items []shared.MysekaiFixtureListItemResponse) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestMysekaiFixtureListFiltersAndSortsTheProjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newMysekaiTestCache()
	router := newMysekaiTestRouter(newMissionTestHandler(cache))

	tests := []struct {
		path string
		ids  []int64
	}{
		{path: "/api/v1/mysekaiFixtures/jp/list", ids: []int64{1, 2, 3}},
		{path: "/api/v1/mysekaiFixtures/jp/list?sort_by=seq&sort_order=asc", ids: []int64{2, 3, 1}},
		{path: "/api/v1/mysekaiFixtures/jp/list?sort_by=name&sort_order=desc", ids: []int64{3, 1, 2}},
		{path: "/api/v1/mysekaiFixtures/jp/list?sort_by=id&sort_order=desc", ids: []int64{3, 2, 1}},
		{path: "/api/v1/mysekaiFixtures/jp/list?main_genre_id=2", ids: []int64{1, 2}},
		{path: "/api/v1/mysekaiFixtures/jp/list?sub_genre_id=4,9", ids: []int64{2}},
		{path: "/api/v1/mysekaiFixtures/jp/list?tag_id=5", ids: []int64{1, 2}},
		{path: "/api/v1/mysekaiFixtures/jp/list?tag_id=5,61", ids: []int64{2}},
		{path: "/api/v1/mysekaiFixtures/jp/list?name=%E3%81%84%E3%81%99", ids: []int64{2}},
		{path: "/api/v1/mysekaiFixtures/jp/list?name=TAB", ids: []int64{1}},
		{path: "/api/v1/mysekaiFixtures/jp/list?page=2&page_size=2", ids: []int64{3}},
	}
	for _, test := range tests {
		var body shared.MysekaiFixtureListResponse
		decodeMysekaiResponse(t, router, test.path, &body)
		if got := mysekaiFixtureIDs(body.Items); !reflect.DeepEqual(got, test.ids) {
			t.Fatalf("%s: expected %v, got %v", test.path, test.ids, got)
		}
	}

	var body shared.MysekaiFixtureListResponse
	decodeMysekaiResponse(t, router, "/api/v1/mysekaiFixtures/jp/list?page_size=1", &body)
	first := body.Items[0]
	if body.Pagination.Total != 3 || !body.Pagination.HasNext {
		t.Fatalf("unexpected pagination %+v", body.Pagination)
	}
	if first.Name != "Table" || *first.MysekaiFixtureMainGenreID != 2 || *first.MysekaiSettableLayoutType != "floor" ||
		!reflect.DeepEqual(first.TagIDs, []int64{1001, 5}) || *first.GridSize != (shared.MysekaiFixtureGridSizeResponse{Width: 2, Depth: 2, Height: 1}) {
		t.Fatalf("unexpected projected fixture %+v", first)
	}

	for _, path := range []string{
		"/api/v1/mysekaiFixtures/jp/list?sort_by=flavorText",
		"/api/v1/mysekaiFixtures/jp/list?tag_id=x",
		"/api/v1/mysekaiFixtures/jp/list?page_size=101",
	} {
		if resp := serveLookupRequest(t, router, http.MethodGet, path); resp.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", path, resp.Code)
		}
	}
	if len(cache.listCalls) != 0 {
		t.Fatalf("expected the list to read projections, not whole entities: %+v", cache.listCalls)
	}
}

func TestMysekaiFixtureFiltersListOnlyUsedGenresAndFilterTags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newMysekaiTestCache()
	router := newMysekaiTestRouter(newMissionTestHandler(cache))

	var body shared.MysekaiFixtureFiltersResponse
	decodeMysekaiResponse(t, router, "/api/v1/mysekaiFixtures/jp/filters", &body)

	if len(body.MainGenres) != 2 || body.MainGenres[0].ID != 2 || body.MainGenres[1].ID != 7 {
		t.Fatalf("expected the used main genres 2 and 7, got %+v", body.MainGenres)
	}
	if subGenres := body.MainGenres[0].SubGenres; len(subGenres) != 2 || subGenres[0].Name != "Tables" || subGenres[1].Name != "Chairs" {
		t.Fatalf("expected genre 2's used sub-genres in ID order, got %+v", subGenres)
	}
	if len(body.MainGenres[1].SubGenres) != 0 {
		t.Fatalf("expected no sub-genres for genre 7, got %+v", body.MainGenres[1].SubGenres)
	}
	tagIDs := make([]int64, 0, len(body.Tags))
	for _, tag := range body.Tags {
		tagIDs = append(tagIDs, tag.ID)
	}
	if !reflect.DeepEqual(tagIDs, []int64{61, 5}) {
		t.Fatalf("expected the used series then character tags, got %+v", body.Tags)
	}
	if len(cache.listCalls) != 0 {
		t.Fatalf("expected filters to read projections and indexes only: %+v", cache.listCalls)
	}
}

func TestMysekaiFixtureDetailJoinsGenresTagsBlueprintAndBonus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newMysekaiTestCache()
	router := newMysekaiTestRouter(newMissionTestHandler(cache))

	var detail shared.MysekaiFixtureDetailResponse
	decodeMysekaiResponse(t, router, "/api/v1/mysekaiFixtures/jp/1", &detail)

	if detail.MainGenre == nil || detail.MainGenre.Name != "General" || detail.SubGenre == nil || detail.SubGenre.Name != "Tables" {
		t.Fatalf("unexpected genres %+v %+v", detail.MainGenre, detail.SubGenre)
	}
	if len(detail.Tags) != 2 || detail.Tags[0].ID != 1001 || detail.Tags[1].MysekaiFixtureTagType != "game_character" {
		t.Fatalf("expected tags in slot order, got %+v", detail.Tags)
	}
	if detail.Blueprint == nil || detail.Blueprint.ID != 11 || !*detail.Blueprint.IsEnableSketch || *detail.Blueprint.CraftCountLimit != 1 {
		t.Fatalf("expected the fixture blueprint, not the tool one, got %+v", detail.Blueprint)
	}
	costs := detail.Blueprint.MaterialCosts
	if len(costs) != 2 || costs[0].Material.Name != "Wood" || costs[0].Quantity != 10 || costs[1].Material.ID != 2 || costs[1].Quantity != 5 {
		t.Fatalf("expected material costs in cost order, got %+v", costs)
	}
	if len(detail.DisassembleMaterials) != 1 || detail.DisassembleMaterials[0].Material.Name != "Stone" {
		t.Fatalf("unexpected disassemble materials %+v", detail.DisassembleMaterials)
	}
	if detail.CharacterBonus == nil || *detail.CharacterBonus.BonusRate != 1.5 || !reflect.DeepEqual(detail.CharacterBonus.GameCharacterIDs, []int64{3, 1}) {
		t.Fatalf("unexpected character bonus %+v", detail.CharacterBonus)
	}
	if *detail.FlavorText != "A table" || len(detail.AnotherColors) != 1 || *detail.AnotherColors[0].ColorCode != "#33aaee" || !*detail.IsAssembled || *detail.FirstPutCost != 35 {
		t.Fatalf("unexpected fixture fields %+v", detail)
	}

	var bare shared.MysekaiFixtureDetailResponse
	decodeMysekaiResponse(t, router, "/api/v1/mysekaiFixtures/jp/3", &bare)
	if bare.Blueprint != nil || bare.CharacterBonus != nil || bare.SubGenre != nil || bare.DisassembleMaterials == nil || len(bare.Tags) != 1 {
		t.Fatalf("expected a fixture without blueprint or bonus to omit them, got %+v", bare)
	}

	if resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/mysekaiFixtures/jp/404"); resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Code)
	}
	if resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/mysekaiFixtures/jp/abc"); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}
	if len(cache.listCalls) != 0 {
		t.Fatalf("expected the detail to read by ID and index only: %+v", cache.listCalls)
	}
}

func TestMysekaiMaterialsListAndDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newMysekaiTestCache()
	router := newMysekaiTestRouter(newMissionTestHandler(cache))

	var list shared.MysekaiMaterialListResponse
	decodeMysekaiResponse(t, router, "/api/v1/mysekaiMaterials/jp/list", &list)
	if len(list.Items) != 3 || list.Pagination.Total != 3 {
		t.Fatalf("expected three materials, got %+v", list)
	}
	wood := list.Items[0]
	if len(wood.Sites) != 2 || wood.Sites[0].Name != "Grassland" || wood.Sites[1].ID != 7 || *wood.Description != "Logs" {
		t.Fatalf("unexpected wood material %+v", wood)
	}
	if !reflect.DeepEqual(list.Items[2].GameCharacterIDs, []int64{1}) || len(list.Items[1].GameCharacterIDs) != 0 {
		t.Fatalf("unexpected material characters %+v", list.Items)
	}

	decodeMysekaiResponse(t, router, "/api/v1/mysekaiMaterials/jp/list?material_type=mineral,game_character&page_size=1&page=2", &list)
	if len(list.Items) != 1 || list.Items[0].ID != 3 || list.Pagination.Total != 2 {
		t.Fatalf("expected the second filtered material, got %+v", list)
	}

	var detail shared.MysekaiMaterialDetailResponse
	decodeMysekaiResponse(t, router, "/api/v1/mysekaiMaterials/jp/1", &detail)
	if len(detail.UsedBy) != 2 || detail.UsedBy[0].Fixture.ID != 1 || detail.UsedBy[0].Quantity != 10 ||
		detail.UsedBy[1].Fixture.Name != "Chair" || detail.UsedBy[1].Quantity != 3 {
		t.Fatalf("expected the fixtures whose blueprints cost wood, without the tool blueprint, got %+v", detail.UsedBy)
	}
	if *detail.UsedBy[0].Fixture.AssetbundleName != "mdl_table" || len(detail.Sites) != 2 {
		t.Fatalf("unexpected material detail %+v", detail)
	}
	if resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/mysekaiMaterials/jp/99"); resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Code)
	}
	if len(cache.listCalls) != 0 {
		t.Fatalf("expected materials to read projections, IDs, and indexes only: %+v", cache.listCalls)
	}
}

func TestMysekaiMusicRecordsJoinTracksAndHideUnpublishedSongs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	publishedAt := time.Now().UTC().Add(6 * time.Hour).Truncate(time.Millisecond)
	cache := &missionTrackingCache{fakeLookupCache: &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				mysekaiMusicRecordsEntity: {
					{"id": 1, "mysekaiMusicTrackType": "music", "externalId": 100},
					{"id": 2, "mysekaiMusicTrackType": "music", "externalId": 101},
					{"id": 3, "mysekaiMusicTrackType": "music", "externalId": 404},
					{"id": 10001, "mysekaiMusicTrackType": "music_sound_track", "externalId": 1},
					{"id": 10002, "mysekaiMusicTrackType": "music_sound_track", "externalId": 2},
				},
				musicsEntity: {
					{"id": 100, "title": "Released", "assetbundleName": "jacket_s_100", "publishedAt": int64(1_600_000_000_000)},
					{"id": 101, "title": "Upcoming", "assetbundleName": "jacket_s_101", "publishedAt": publishedAt.UnixMilli()},
				},
				musicSoundTracksEntity: {
					{"id": 1, "title": "With Gratitude", "musicSoundTrackCategoryId": 1, "assetbundleName": "sound/scenario/bgm/bgm_area00001", "assetbundleFileName": "bgm_area00001"},
					{"id": 2, "title": "Other", "musicSoundTrackCategoryId": 3},
				},
				musicSoundTrackCategoriesEntity: {
					{"id": 1, "name": "Units", "assetbundleName": "jacket_s_soundtrack_1"},
					{"id": 2, "name": "Unused"},
					{"id": 3, "name": "Others"},
				},
			},
		},
	}}
	handler := newMissionTestHandler(cache)
	router := newMysekaiTestRouter(handler)

	recordIDs := func(path string) []int64 {
		t.Helper()
		var body shared.MysekaiMusicRecordListResponse
		decodeMysekaiResponse(t, router, path, &body)
		ids := make([]int64, 0, len(body.Items))
		for _, item := range body.Items {
			ids = append(ids, item.ID)
		}
		return ids
	}
	tests := []struct {
		path string
		ids  []int64
	}{
		{path: "/api/v1/mysekaiMusicRecords/jp/list", ids: []int64{1, 10001, 10002}},
		{path: "/api/v1/mysekaiMusicRecords/jp/list?spoiler=true", ids: []int64{1, 2, 10001, 10002}},
		{path: "/api/v1/mysekaiMusicRecords/jp/list?track_type=music", ids: []int64{1}},
		{path: "/api/v1/mysekaiMusicRecords/jp/list?sound_track_category_id=3", ids: []int64{10002}},
		{path: "/api/v1/mysekaiMusicRecords/jp/list?name=gratitude", ids: []int64{10001}},
	}
	for _, test := range tests {
		if got := recordIDs(test.path); !reflect.DeepEqual(got, test.ids) {
			t.Fatalf("%s: expected %v, got %v", test.path, test.ids, got)
		}
	}

	var body shared.MysekaiMusicRecordListResponse
	decodeMysekaiResponse(t, router, "/api/v1/mysekaiMusicRecords/jp/list", &body)
	if music := body.Items[0].Music; music == nil || music.Title != "Released" || *music.AssetbundleName != "jacket_s_100" || body.Items[0].SoundTrack != nil {
		t.Fatalf("unexpected music record %+v", body.Items[0])
	}
	if track := body.Items[1].SoundTrack; track == nil || *track.AssetbundleFileName != "bgm_area00001" || *track.MusicSoundTrackCategoryID != 1 {
		t.Fatalf("unexpected sound-track record %+v", body.Items[1])
	}
	if resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/mysekaiMusicRecords/jp/list?track_type=bgm"); resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.Code)
	}

	var hint *cachehint.Hint
	hinted := gin.New()
	hinted.GET("/api/v1/mysekaiMusicRecords/:region/list", func(c *gin.Context) {
		ctx, captured := cachehint.WithHint(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		hint = captured
		c.Next()
	}, handler.MysekaiMusicRecordsList)
	hinted.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/mysekaiMusicRecords/jp/list", nil))
	if until, ok := hint.Until(); !ok || !until.Equal(publishedAt) {
		t.Fatalf("reported %v %t, want the hidden song's publish time %v", until, ok, publishedAt)
	}

	var filters shared.MysekaiMusicRecordFiltersResponse
	decodeMysekaiResponse(t, router, "/api/v1/mysekaiMusicRecords/jp/filters", &filters)
	if len(filters.SoundTrackCategories) != 2 || filters.SoundTrackCategories[0].Name != "Units" || filters.SoundTrackCategories[1].ID != 3 {
		t.Fatalf("expected the used sound-track categories, got %+v", filters.SoundTrackCategories)
	}
	if len(cache.listCalls) != 0 {
		t.Fatalf("expected music records to read projections only: %+v", cache.listCalls)
	}
}

func TestMysekaiShopsListJoinsCostsAndShopBoxResources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := newMysekaiTestCache()
	router := newMysekaiTestRouter(newMissionTestHandler(cache))

	var list shared.MysekaiShopListResponse
	decodeMysekaiResponse(t, router, "/api/v1/mysekaiShops/jp/list", &list)
	if len(list.Items) != 3 || list.Pagination.Total != 3 {
		t.Fatalf("expected three shop items, got %+v", list)
	}
	first := list.Items[0]
	if *first.MysekaiShopExchangeLimitType != "limited_per_mysekai_colorful_pass" || *first.MysekaiShopExchangeLimitValue != 3 {
		t.Fatalf("expected the first item's purchase limit, got %+v", first)
	}
	if !reflect.DeepEqual(first.Costs, []shared.MysekaiShopCostResponse{{ResourceType: "jewel", Quantity: 500}}) {
		t.Fatalf("expected the first item's crystal cost, got %+v", first.Costs)
	}
	if len(first.Resources) != 1 || *first.Resources[0].Name != "Stone" || *first.Resources[0].AssetbundleName != "item_mineral_1" ||
		*first.Resources[0].MysekaiMaterialType != "mineral" || first.Resources[0].ResourceQuantity != 1 {
		t.Fatalf("expected the mysekai_shop box's material, not the mission box, got %+v", first.Resources)
	}
	if list.Items[1].MysekaiShopExchangeLimitValue != nil || list.Items[1].Resources[0].ResourceQuantity != 15 {
		t.Fatalf("expected the unlimited wood bundle, got %+v", list.Items[1])
	}

	decodeMysekaiResponse(t, router, "/api/v1/mysekaiShops/jp/list?shop_type=tool", &list)
	if len(list.Items) != 1 || list.Pagination.Total != 1 {
		t.Fatalf("expected one tool item, got %+v", list)
	}
	tool := list.Items[0].Resources[0]
	if tool.ResourceType != "mysekai_tool" || *tool.Name != "Chainsaw" || *tool.AssetbundleName != "ax0005" || *tool.MysekaiToolType != "axe" {
		t.Fatalf("expected the chainsaw, got %+v", tool)
	}
	if len(cache.listCalls) != 0 {
		t.Fatalf("expected the shop to read projections, IDs, and indexes only: %+v", cache.listCalls)
	}
}
