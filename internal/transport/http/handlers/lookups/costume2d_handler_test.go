package lookups

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/handlers/shared"
)

func newCostume2DsRouter() *gin.Engine {
	router, _ := newCostume2DsRouterWithCache()
	return router
}

func newCostume2DsRouterWithCache() (*gin.Engine, *fakeLookupCache) {
	gin.SetMode(gin.TestMode)
	both := map[string]any{
		"id": 1, "character2dId": 1, "costume2dGroupId": 1, "fromMmddhh": "010100", "toMmddhh": "123123",
		"live2dAssetbundleName": "01ichika_normal", "spineAssetbundleName": "sd_01ichika_normal",
		"unknownField": "must not be exposed",
	}
	live2dOnly := map[string]any{
		"id": 2, "character2dId": 2, "costume2dGroupId": 1, "fromMmddhh": "010100", "toMmddhh": "123123",
		"live2dAssetbundleName": "02saki_normal",
	}
	sharedSpine := map[string]any{
		"id": 3, "character2dId": 3, "costume2dGroupId": 2, "fromMmddhh": "010100", "toMmddhh": "123123",
		"spineAssetbundleName": "sd_01ichika_normal",
	}
	blankSpine := map[string]any{
		"id": 4, "character2dId": 4, "costume2dGroupId": 3, "fromMmddhh": "010100", "toMmddhh": "123123",
		"spineAssetbundleName": "  ",
	}
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {costume2dsEntity: {sharedSpine, blankSpine, both, live2dOnly}},
		},
	}
	router := gin.New()
	handler := newConfiguredLookupHandler(cache, "jp")
	router.GET("/api/v1/costume2ds/:region/list", handler.Costume2DsList)
	return router, cache
}

func listCostume2Ds(t *testing.T, target string) shared.Costume2DListResponse {
	t.Helper()
	resp := serveLookupRequest(t, newCostume2DsRouter(), http.MethodGet, target)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var list shared.Costume2DListResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	return list
}

func costume2DIDs(items []shared.Costume2DResponse) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestCostume2DsListReturnsEveryCostumeInIDOrder(t *testing.T) {
	list := listCostume2Ds(t, "/api/v1/costume2ds/jp/list")
	if ids := costume2DIDs(list.Items); !slices.Equal(ids, []int64{1, 2, 3, 4}) {
		t.Fatalf("expected costumes in ID order, got %v", ids)
	}
	if list.Pagination.Total != 4 {
		t.Fatalf("expected a total of 4, got %+v", list.Pagination)
	}
}

func TestCostume2DsListReadsTheProjection(t *testing.T) {
	router, cache := newCostume2DsRouterWithCache()
	if resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume2ds/jp/list"); resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !slices.Equal(cache.projections, []string{"jp/" + costume2dsEntity}) {
		t.Fatalf("expected one costume2ds projection read, got %v", cache.projections)
	}
}

func TestCostume2DsListWithHasSpineKeepsCostumesThatShareAModel(t *testing.T) {
	list := listCostume2Ds(t, "/api/v1/costume2ds/jp/list?has_spine=true&page_size=1&page=2")
	if ids := costume2DIDs(list.Items); !slices.Equal(ids, []int64{3}) {
		t.Fatalf("expected the second costume with a Spine model on page 2, got %v", ids)
	}
	if list.Pagination.Total != 2 {
		t.Fatalf("expected the total to count only costumes with a Spine model, got %+v", list.Pagination)
	}
}

func TestCostume2DsListWithHasSpineFalseKeepsEveryCostume(t *testing.T) {
	if list := listCostume2Ds(t, "/api/v1/costume2ds/jp/list?has_spine=false"); list.Pagination.Total != 4 {
		t.Fatalf("expected a total of 4, got %+v", list.Pagination)
	}
}

func TestCostume2DsListExposesOnlyStableFieldsAndOmitsMissingModels(t *testing.T) {
	resp := serveLookupRequest(t, newCostume2DsRouter(), http.MethodGet, "/api/v1/costume2ds/jp/list")
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.Items) != 4 {
		t.Fatalf("expected 4 items, got %#v", body.Items)
	}
	first := body.Items[0]
	if first["id"] != float64(1) || first["character2dId"] != float64(1) || first["costume2dGroupId"] != float64(1) ||
		first["live2dAssetbundleName"] != "01ichika_normal" || first["spineAssetbundleName"] != "sd_01ichika_normal" ||
		first["fromMmddhh"] != "010100" || first["toMmddhh"] != "123123" {
		t.Fatalf("unexpected costume fields: %#v", first)
	}
	if len(first) != 7 {
		t.Fatalf("expected only seven stable fields, got %#v", first)
	}
	if _, exists := body.Items[1]["spineAssetbundleName"]; exists {
		t.Fatalf("expected no spineAssetbundleName on a Live2D-only costume, got %#v", body.Items[1])
	}
	if _, exists := body.Items[3]["spineAssetbundleName"]; exists {
		t.Fatalf("expected a blank spineAssetbundleName to be omitted, got %#v", body.Items[3])
	}
}

func TestCostume2DsListRejectsInvalidRequests(t *testing.T) {
	router := newCostume2DsRouter()
	cases := []struct {
		name   string
		target string
		status int
	}{
		{"non-boolean has_spine", "/api/v1/costume2ds/jp/list?has_spine=maybe", http.StatusBadRequest},
		{"page size above 100", "/api/v1/costume2ds/jp/list?page_size=101", http.StatusBadRequest},
		{"page zero", "/api/v1/costume2ds/jp/list?page=0", http.StatusBadRequest},
		{"unconfigured region", "/api/v1/costume2ds/xx/list", http.StatusBadRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if resp := serveLookupRequest(t, router, http.MethodGet, test.target); resp.Code != test.status {
				t.Fatalf("expected %d, got %d: %s", test.status, resp.Code, resp.Body.String())
			}
		})
	}
}

func TestCostume2DsListReturnsAnEmptyArrayPastTheEnd(t *testing.T) {
	resp := serveLookupRequest(t, newCostume2DsRouter(), http.MethodGet, "/api/v1/costume2ds/jp/list?page=9")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 past the end, got %d: %s", resp.Code, resp.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if string(raw["items"]) != "[]" {
		t.Fatalf("expected an empty items array past the end, got %s", raw["items"])
	}
}

func serveCostume2DsList(t *testing.T, handler *LookupHandler, region string) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/costume2ds/:region/list", handler.Costume2DsList)
	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/costume2ds/"+region+"/list")
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	return resp.Code, body.Error.Code
}

func TestCostume2DsListIsUnavailableWithoutMasterData(t *testing.T) {
	status, code := serveCostume2DsList(t, NewLookupHandler(nil), "jp")
	if status != http.StatusServiceUnavailable || code != "MASTER_DATA_DISABLED" {
		t.Fatalf("expected 503 MASTER_DATA_DISABLED, got %d %q", status, code)
	}
}

func TestCostume2DsListIsUnavailableForARegionThatHasNotSynced(t *testing.T) {
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"kr": {costume2dsEntity: {{"id": 1}}},
		},
	}
	// The status store reports only jp and en as synced.
	status, _ := serveCostume2DsList(t, newConfiguredLookupHandler(cache, "jp", "kr"), "kr")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for a region without a successful sync, got %d", status)
	}
	if len(cache.projections) != 0 {
		t.Fatalf("expected no projection read for a region that is not ready, got %v", cache.projections)
	}
}

func TestCostume2DsListReportsAProjectionReadFailure(t *testing.T) {
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {costume2dsEntity: {{"id": 1}}},
		},
		projectionErr: errors.New("store unavailable"),
	}
	status, code := serveCostume2DsList(t, newConfiguredLookupHandler(cache, "jp"), "jp")
	if status != http.StatusInternalServerError || code != "COSTUME_2D_QUERY_ERROR" {
		t.Fatalf("expected 500 COSTUME_2D_QUERY_ERROR, got %d %q", status, code)
	}
}

func TestProjectCostume2DRejectsARecordWithoutAPositiveID(t *testing.T) {
	for _, record := range []map[string]any{
		{"spineAssetbundleName": "sd_01ichika_normal"},
		{"id": 0, "spineAssetbundleName": "sd_01ichika_normal"},
	} {
		if costume, ok := projectCostume2D(recordSource(record)); ok {
			t.Fatalf("expected %#v to be rejected, got %#v", record, costume)
		}
	}
}
