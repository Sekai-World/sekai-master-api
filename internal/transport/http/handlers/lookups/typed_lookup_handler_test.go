package lookups

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

func newConfiguredLookupHandler(cache *fakeLookupCache, regions ...string) *LookupHandler {
	sources := make([]masterdata.Source, 0, len(regions))
	for _, region := range regions {
		sources = append(sources, masterdata.Source{Region: region})
	}
	statusStore := &fakeLookupStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
			{Region: "en", Status: "success"},
		},
	}
	return NewLookupHandler(usecase.NewMasterDataSyncUsecase(sources, nil, cache, statusStore, nil, 1))
}

func newTypedLookupRouter(handler *LookupHandler) *gin.Engine {
	router := gin.New()
	router.GET("/api/v1/mysekaiPhotoDecorations/:region/list", handler.MysekaiPhotoDecorationsList)
	router.GET("/api/v1/mysekaiPhotoDecorations/:region/:id", handler.MysekaiPhotoDecorationsByID)
	router.GET("/api/v1/honors/:region/list", handler.HonorsList)
	router.GET("/api/v1/honors/:region/:id", handler.HonorsByID)
	return router
}

func TestMysekaiPhotoDecorationEndpointsProjectOnlyStableFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	photoDecoration := map[string]any{
		"id":              1001,
		"seq":             3,
		"name":            "Test Decoration",
		"description":     "A short description",
		"assetbundleName": "mysekai/decoration_1001",
		"unknownField":    "must not be exposed",
	}
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {mysekaiPhotoDecorationsEntity: {"1001": photoDecoration}},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {mysekaiPhotoDecorationsEntity: {photoDecoration}},
		},
	}
	router := newTypedLookupRouter(newReadyLookupHandler(cache))

	for _, path := range []string{
		"/api/v1/mysekaiPhotoDecorations/jp/list",
		"/api/v1/mysekaiPhotoDecorations/jp/1001",
	} {
		resp := serveLookupRequest(t, router, http.MethodGet, path)
		if resp.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d: %s", path, resp.Code, resp.Body.String())
		}

		var body map[string]any
		if path == "/api/v1/mysekaiPhotoDecorations/jp/list" {
			var list struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
				t.Fatalf("unmarshal list response: %v", err)
			}
			if len(list.Items) != 1 {
				t.Fatalf("expected one decoration, got %#v", list.Items)
			}
			body = list.Items[0]
		} else if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal by-id response: %v", err)
		}

		if body["id"] != float64(1001) || body["seq"] != float64(3) || body["name"] != "Test Decoration" || body["description"] != "A short description" || body["assetbundleName"] != "mysekai/decoration_1001" {
			t.Fatalf("unexpected stable decoration fields: %#v", body)
		}
		if len(body) != 5 {
			t.Fatalf("expected only five stable fields, got %#v", body)
		}
	}
}

func TestHonorsListPreservesSourceOrderAndNormalizesGroupAndLevels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				honorGroupsEntity: {
					"11": {
						"id":                        float64(11),
						"name":                      "JP Group",
						"honorType":                 "degree",
						"backgroundAssetbundleName": "degree_bg_jp",
						"frameName":                 "jp_frame",
						"unknownField":              "hidden",
					},
					"12": {"id": 12, "name": "Second JP Group"},
				},
			},
			"en": {
				honorGroupsEntity: {
					"11": {"id": 11, "name": "Wrong-region group"},
				},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				honorsEntity: {
					{
						"id":               100,
						"seq":              5,
						"groupId":          11,
						"name":             "First Honor",
						"honorRarity":      "rarity_normal",
						"honorMissionType": "character_rank",
						"honorType":        "degree",
						"assetbundleName":  "honor_100",
						"levels": []any{
							map[string]any{
								"honorId":         100,
								"level":           float64(1),
								"bonus":           float64(0.5),
								"description":     "Level one",
								"honorRarity":     "rarity_normal",
								"assetbundleName": "honor_100_1",
								"unknownField":    "hidden",
							},
							"not an object",
						},
						"unknownField": "hidden",
					},
					{
						"id":              200,
						"seq":             2,
						"groupId":         12,
						"name":            "Second Honor",
						"honorRarity":     "rarity_rare",
						"assetbundleName": "honor_200",
						"levels": []map[string]any{
							{"level": 2, "bonus": 0, "unknownField": "hidden"},
						},
					},
					{
						"id":              300,
						"seq":             1,
						"groupId":         99,
						"name":            "Honor without a group",
						"honorRarity":     "rarity_normal",
						"assetbundleName": "honor_300",
					},
					{"id": 400, "seq": 4, "groupId": 30, "name": "Outside current page"},
				},
			},
		},
	}
	router := newTypedLookupRouter(newReadyLookupHandler(cache))
	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honors/jp/list?page=1&page_size=3")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Items) != 3 {
		t.Fatalf("expected only the current page, got %#v", body.Items)
	}
	if body.Items[0]["id"] != float64(100) || body.Items[1]["id"] != float64(200) || body.Items[2]["id"] != float64(300) {
		t.Fatalf("honors must preserve source order, got %#v", body.Items)
	}
	if len(cache.byIDCalls) != 3 {
		t.Fatalf("expected one same-region lookup per unique current-page group id, got %#v", cache.byIDCalls)
	}
	expectedGroupIDs := []string{"11", "12", "99"}
	for index, call := range cache.byIDCalls {
		if call.region != "jp" || call.entity != honorGroupsEntity || call.id != expectedGroupIDs[index] {
			t.Fatalf("unexpected group lookup at index %d: %+v", index, call)
		}
	}

	first := body.Items[0]
	if len(first) != 10 {
		t.Fatalf("expected only stable honor fields, got %#v", first)
	}
	if first["honorMissionType"] != "character_rank" || first["honorType"] != "degree" {
		t.Fatalf("expected optional honor fields to be projected, got %#v", first)
	}
	firstGroup, ok := first["group"].(map[string]any)
	if !ok || firstGroup["name"] != "JP Group" || firstGroup["backgroundAssetbundleName"] != "degree_bg_jp" || firstGroup["frameName"] != "jp_frame" {
		t.Fatalf("expected same-region typed group enrichment, got %#v", first["group"])
	}
	if len(firstGroup) != 5 {
		t.Fatalf("expected only stable honor-group fields, got %#v", firstGroup)
	}
	levels, ok := first["levels"].([]any)
	if !ok || len(levels) != 1 {
		t.Fatalf("expected invalid level entries to be skipped, got %#v", first["levels"])
	}
	firstLevel, ok := levels[0].(map[string]any)
	if !ok || len(firstLevel) != 6 || firstLevel["honorId"] != float64(100) || firstLevel["level"] != float64(1) || firstLevel["bonus"] != float64(0.5) {
		t.Fatalf("expected normalized stable level fields, got %#v", levels[0])
	}
	if _, leaked := firstLevel["unknownField"]; leaked {
		t.Fatalf("unexpected unknown level field: %#v", firstLevel)
	}

	second := body.Items[1]
	if _, exists := second["honorMissionType"]; exists {
		t.Fatalf("honorMissionType must be omitted when absent, got %#v", second)
	}
	if _, exists := second["honorType"]; exists {
		t.Fatalf("honorType must be omitted when absent, got %#v", second)
	}
	secondLevels, ok := second["levels"].([]any)
	if !ok || len(secondLevels) != 1 {
		t.Fatalf("expected normalized []map[string]any levels, got %#v", second["levels"])
	}
	secondLevel := secondLevels[0].(map[string]any)
	if secondLevel["level"] != float64(2) || secondLevel["bonus"] != float64(0) || len(secondLevel) != 2 {
		t.Fatalf("expected optional zero level fields to be retained, got %#v", secondLevel)
	}
	if _, exists := body.Items[2]["group"]; exists {
		t.Fatalf("missing honor group must be omitted, got %#v", body.Items[2])
	}
}

func TestHonorByIDProjectsFieldsAndReturnsNotFoundForMissingRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				honorsEntity: {
					"7": {
						"id": 7, "seq": 1, "groupId": 8, "name": "By ID Honor", "honorRarity": "rarity_normal",
						"honorType": "degree", "assetbundleName": "honor_7", "extra": "hidden",
					},
				},
				honorGroupsEntity: {"8": {"id": 8, "name": "Honor Group", "other": "hidden"}},
			},
		},
	}
	router := newTypedLookupRouter(newReadyLookupHandler(cache))

	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honors/jp/7")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body) != 9 || body["id"] != float64(7) || body["name"] != "By ID Honor" {
		t.Fatalf("expected typed honor projection, got %#v", body)
	}
	if _, exists := body["honorMissionType"]; exists {
		t.Fatalf("honorMissionType must be omitted when absent, got %#v", body)
	}
	if body["group"].(map[string]any)["name"] != "Honor Group" {
		t.Fatalf("expected group enrichment, got %#v", body["group"])
	}

	missing := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honors/jp/999")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing honor, got %d: %s", missing.Code, missing.Body.String())
	}
	missingPhoto := serveLookupRequest(t, router, http.MethodGet, "/api/v1/mysekaiPhotoDecorations/jp/999")
	if missingPhoto.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing decoration, got %d: %s", missingPhoto.Code, missingPhoto.Body.String())
	}
}

func TestTypedLookupEndpointsRejectInvalidRegionIDsAndPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newConfiguredLookupHandler(&fakeLookupCache{}, "jp")
	router := newTypedLookupRouter(handler)

	tests := []struct {
		name string
		path string
	}{
		{name: "unknown decoration region", path: "/api/v1/mysekaiPhotoDecorations/xx/list"},
		{name: "unknown honor region", path: "/api/v1/honors/xx/1"},
		{name: "invalid decoration id", path: "/api/v1/mysekaiPhotoDecorations/jp/not-a-number"},
		{name: "zero honor id", path: "/api/v1/honors/jp/0"},
		{name: "invalid page", path: "/api/v1/honors/jp/list?page=0"},
		{name: "invalid page size", path: "/api/v1/mysekaiPhotoDecorations/jp/list?page_size=invalid"},
		{name: "page size too large", path: "/api/v1/honors/jp/list?page_size=101"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := serveLookupRequest(t, router, http.MethodGet, test.path)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestHonorsListReturnsQueryErrorWhenGroupStorageFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		byIDErr: errors.New("group storage unavailable"),
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {honorsEntity: {{"id": 1, "groupId": 2}}},
		},
	}
	router := newTypedLookupRouter(newReadyLookupHandler(cache))
	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honors/jp/list")
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if body.Error.Code != "HONOR_QUERY_ERROR" {
		t.Fatalf("expected HONOR_QUERY_ERROR, got %q", body.Error.Code)
	}
}

func TestTypedLookupEndpointsUsePersistedRecordsWithoutRuntimeSearchIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	photoDecoration := map[string]any{"id": 1, "seq": 1, "name": "Decoration", "description": "", "assetbundleName": "decoration_1"}
	honor := map[string]any{"id": 2, "seq": 1, "groupId": 0, "name": "Honor", "honorRarity": "rarity_normal", "assetbundleName": "honor_2"}
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {
				mysekaiPhotoDecorationsEntity: {"1": photoDecoration},
				honorsEntity:                  {"2": honor},
			},
		},
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				mysekaiPhotoDecorationsEntity: {photoDecoration},
				honorsEntity:                  {honor},
			},
		},
		hasRecords: map[string]map[string]bool{
			"jp": {mysekaiPhotoDecorationsEntity: true, honorsEntity: true},
		},
		hasIndexSet: true,
		hasIndex:    false,
	}
	router := newTypedLookupRouter(newReadyLookupHandler(cache))
	paths := []string{
		"/api/v1/mysekaiPhotoDecorations/jp/list",
		"/api/v1/mysekaiPhotoDecorations/jp/1",
		"/api/v1/honors/jp/list",
		"/api/v1/honors/jp/2",
	}
	for _, path := range paths {
		resp := serveLookupRequest(t, router, http.MethodGet, path)
		if resp.Code != http.StatusOK {
			t.Fatalf("expected persisted records to be readable for %s, got %d: %s", path, resp.Code, resp.Body.String())
		}
	}
	if cache.searchCalls != 0 {
		t.Fatalf("persisted-record reads must not depend on decoded search-index state, got %d search calls", cache.searchCalls)
	}
}

func TestTypedLookupEndpointsReturnServiceUnavailableWhenEntityIsNotReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{hasIndexSet: true, hasIndex: false}
	router := newTypedLookupRouter(newReadyLookupHandler(cache))
	for _, path := range []string{
		"/api/v1/mysekaiPhotoDecorations/jp/list",
		"/api/v1/honors/jp/1",
	} {
		resp := serveLookupRequest(t, router, http.MethodGet, path)
		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 for unavailable entity on %s, got %d: %s", path, resp.Code, resp.Body.String())
		}
	}
}

func TestGenericLookupListRejectsPageSizeAboveMaximum(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/gameCharacters/:region/list", newReadyLookupHandler(&fakeLookupCache{}).GameCharactersList)
	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/gameCharacters/jp/list?page_size=101")
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected generic lookup list page_size=101 to return 400, got %d: %s", resp.Code, resp.Body.String())
	}
}
