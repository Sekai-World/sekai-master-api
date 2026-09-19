package lookups

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

func newCostume3DLookupHandler(cache *fakeLookupCache, regions ...string) *LookupHandler {
	sources := make([]masterdata.Source, 0, len(regions))
	statuses := make([]masterdata.SyncStatus, 0, len(regions))
	for _, region := range regions {
		sources = append(sources, masterdata.Source{Region: region})
		statuses = append(statuses, masterdata.SyncStatus{Region: region, Status: "success"})
	}

	statusStore := &fakeLookupStatusStore{statuses: statuses}
	syncUsecase := usecase.NewMasterDataSyncUsecase(sources, nil, cache, statusStore, nil, 1)
	return NewLookupHandler(syncUsecase)
}

func newCostume3DTestRouter(handler *LookupHandler) *gin.Engine {
	router := gin.New()
	router.GET("/api/v1/costume3ds/:region/list", handler.Costume3DsList)
	router.GET("/api/v1/costume3ds/:region/:id", handler.Costume3DsByID)
	return router
}

func TestCostume3DListProjectsEmbeddedJPAndENRecordsWithEpochMillis(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jpPublishedAt := time.Date(2020, time.January, 2, 3, 4, 5, 6_000_000, time.UTC)
	enPublishedAt := int64(1_700_000_000_123)
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {
				costume3dsEntity: {{
					"id": 101, "groupId": 11, "colorId": 12, "partType": "hair", "seq": 1,
					"name": "JP Costume", "designer": "JP Designer", "characterId": 13,
					"rarity": "rarity_3", "type": "casual", "assetbundleName": "costume/jp",
					"publishedAt": jpPublishedAt.Format(time.RFC3339Nano), "unknownField": "hidden",
				}},
			},
			"en": {
				costume3dsEntity: {{
					"id": 201, "groupId": 21, "colorId": 22, "partType": "outfit", "seq": 2,
					"name": "EN Costume", "designer": "EN Designer", "characterId": 23,
					"rarity": "rarity_4", "type": "school", "assetbundleName": "costume/en",
					"publishedAt": enPublishedAt,
				}},
			},
		},
	}
	router := newCostume3DTestRouter(newCostume3DLookupHandler(cache, "jp", "en"))

	tests := []struct {
		region        string
		id            float64
		name          string
		publishedAtMS int64
	}{
		{region: "jp", id: 101, name: "JP Costume", publishedAtMS: jpPublishedAt.UnixMilli()},
		{region: "en", id: 201, name: "EN Costume", publishedAtMS: enPublishedAt},
	}
	for _, test := range tests {
		t.Run(test.region, func(t *testing.T) {
			resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume3ds/"+test.region+"/list")
			if resp.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
			}

			var body struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal list response: %v", err)
			}
			if len(body.Items) != 1 {
				t.Fatalf("expected one costume, got %#v", body.Items)
			}
			item := body.Items[0]
			if item["id"] != test.id || item["name"] != test.name || item["publishedAt"] != float64(test.publishedAtMS) {
				t.Fatalf("unexpected projected costume: %#v", item)
			}
			if len(item) != 12 {
				t.Fatalf("expected only the 12 stable costume fields, got %#v", item)
			}
			if _, exists := item["unknownField"]; exists {
				t.Fatalf("unexpected source field leaked: %#v", item)
			}
		})
	}
}

func TestCostume3DGroupedListUsesSameRegionFallbackAndFiltersBeforeStablePagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"tw": {
				costume3dsEntity: {
					{"id": 31, "costume3dGroupId": 701, "colorId": 8, "partType": "hair", "seq": 0, "name": "Primary Name", "characterId": 0},
					{"id": 31, "costume3dGroupId": 701, "name": "Duplicate must be ignored", "seq": 99},
					{"id": 32, "costume3dGroupId": 701, "colorId": 8, "partType": "hair", "seq": 1},
					{"id": 33, "costume3dGroupId": 702, "seq": 2},
				},
				costume3dGroupsEntity: {
					{"id": 9001, "groupId": 701, "name": "Group Name", "designer": "Group Designer", "characterId": 9,
						"rarity": "rarity_group", "type": "group_type", "assetbundleName": "group/701", "publishedAt": int64(1_700_000_000_000), "seq": 10},
					{"id": 9002, "groupId": 702, "name": "Second Group Name", "designer": "Second Designer"},
				},
			},
			"en": {
				costume3dGroupsEntity: {{"id": 8001, "groupId": 701, "name": "Wrong Region Name"}},
			},
		},
	}
	router := newCostume3DTestRouter(newCostume3DLookupHandler(cache, "tw", "en"))
	path := "/api/v1/costume3ds/tw/list?group_id=701&sort_by=seq&sort_order=asc&page_size=1"

	firstPage := serveLookupRequest(t, router, http.MethodGet, path+"&page=1")
	if firstPage.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", firstPage.Code, firstPage.Body.String())
	}
	var firstBody struct {
		Items      []map[string]any `json:"items"`
		Pagination struct {
			Page     int  `json:"page"`
			PageSize int  `json:"page_size"`
			Total    int  `json:"total"`
			HasNext  bool `json:"has_next"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(firstPage.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("unmarshal first page: %v", err)
	}
	if len(firstBody.Items) != 1 || firstBody.Items[0]["id"] != float64(31) {
		t.Fatalf("expected the first stable page to contain costume 31, got %#v", firstBody.Items)
	}
	first := firstBody.Items[0]
	if first["name"] != "Primary Name" || first["designer"] != "Group Designer" || first["seq"] != float64(0) || first["characterId"] != float64(0) {
		t.Fatalf("expected primary fields to win and missing fields to fall back, got %#v", first)
	}
	if first["groupId"] != float64(701) || firstBody.Pagination.Total != 2 || !firstBody.Pagination.HasNext {
		t.Fatalf("expected deduplicated group filter and paginated metadata, got item=%#v pagination=%+v", first, firstBody.Pagination)
	}

	secondPage := serveLookupRequest(t, router, http.MethodGet, path+"&page=2")
	if secondPage.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", secondPage.Code, secondPage.Body.String())
	}
	var secondBody struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(secondPage.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("unmarshal second page: %v", err)
	}
	if len(secondBody.Items) != 1 || secondBody.Items[0]["id"] != float64(32) || secondBody.Items[0]["name"] != "Group Name" {
		t.Fatalf("expected second page to use the same-region group fallback, got %#v", secondBody.Items)
	}

	nameFilter := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume3ds/tw/list?name=group%20name")
	if nameFilter.Code != http.StatusOK {
		t.Fatalf("expected 200 for normalized display-field filter, got %d: %s", nameFilter.Code, nameFilter.Body.String())
	}
	var filteredBody struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(nameFilter.Body.Bytes(), &filteredBody); err != nil {
		t.Fatalf("unmarshal filtered list: %v", err)
	}
	if len(filteredBody.Items) != 2 || filteredBody.Items[0]["id"] != float64(32) || filteredBody.Items[1]["id"] != float64(33) {
		t.Fatalf("expected filtering to use normalized fallback names but not replace the primary name, got %#v", filteredBody.Items)
	}
	if cache.searchCalls != 0 {
		t.Fatalf("costume display-field filtering must not use Redis Search, got %d search calls", cache.searchCalls)
	}
}

func TestCostume3DByIDPreservesPrimaryRecordWhenGroupIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"kr": {
				costume3dsEntity: {
					"81": {"id": 81, "costume3dGroupId": 404, "name": "Retained Costume", "seq": 5, "extra": "hidden"},
				},
			},
		},
	}
	router := newCostume3DTestRouter(newCostume3DLookupHandler(cache, "kr"))
	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume3ds/kr/81")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 when the optional group is missing, got %d: %s", resp.Code, resp.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal by-id response: %v", err)
	}
	if body["id"] != float64(81) || body["groupId"] != float64(404) || body["name"] != "Retained Costume" || body["seq"] != float64(5) {
		t.Fatalf("expected the primary costume record to survive a missing group, got %#v", body)
	}
	if len(body) != 4 {
		t.Fatalf("expected only available stable fields, got %#v", body)
	}
}

func TestCostume3DByIDNotFoundAndInvalidIDResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		byID: map[string]map[string]map[string]map[string]any{
			"jp": {costume3dsEntity: {"7": {"id": 7, "name": "Costume"}}},
		},
	}
	router := newCostume3DTestRouter(newCostume3DLookupHandler(cache, "jp"))

	found := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume3ds/jp/7")
	if found.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", found.Code, found.Body.String())
	}
	missing := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume3ds/jp/999")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing costume, got %d: %s", missing.Code, missing.Body.String())
	}

	for _, path := range []string{
		"/api/v1/costume3ds/jp/not-a-number",
		"/api/v1/costume3ds/jp/0",
		"/api/v1/costume3ds/jp/list?page_size=101",
	} {
		resp := serveLookupRequest(t, router, http.MethodGet, path)
		if resp.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for %s, got %d: %s", path, resp.Code, resp.Body.String())
		}
	}
}
