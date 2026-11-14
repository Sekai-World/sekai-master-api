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

func newBoostsRouterWithCache() (*gin.Engine, *fakeLookupCache) {
	gin.SetMode(gin.TestMode)
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			// JP rows carry every field; they are stored out of costBoost order here.
			"jp": {boostsEntity: {
				{
					"id": 7, "costBoost": 6, "isEventOnly": false, "expRate": 26, "rewardRate": 26,
					"livePointRate": 26, "eventPointRate": 27, "bondsExpRate": 26,
					"unknownField": "must not be exposed",
				},
				{
					"id": 1, "costBoost": 0, "isEventOnly": false, "expRate": 1, "rewardRate": 1,
					"livePointRate": 1, "eventPointRate": 1, "bondsExpRate": 1,
				},
				{
					"id": 2, "costBoost": 1, "isEventOnly": false, "expRate": 5, "rewardRate": 5,
					"livePointRate": 5, "eventPointRate": 5, "bondsExpRate": 5,
				},
				{"id": 90, "eventPointRate": 5},
				{"id": 91, "costBoost": 3},
				{"id": 92, "costBoost": -1, "eventPointRate": 5},
			}},
			// Rows as TW, KR and CN store them: no id, isEventOnly or livePointRate.
			// The test status store reports only jp and en as synced.
			"en": {boostsEntity: {
				{"costBoost": 1, "expRate": 5, "rewardRate": 5, "eventPointRate": 5, "bondsExpRate": 5},
				{"costBoost": 0, "expRate": 1, "rewardRate": 1, "eventPointRate": 1, "bondsExpRate": 1},
			}},
		},
	}
	router := gin.New()
	handler := newConfiguredLookupHandler(cache, "jp", "en")
	router.GET("/api/v1/boosts/:region/list", handler.BoostsList)
	return router, cache
}

func listBoostItems(t *testing.T, target string) ([]map[string]any, shared.PaginationResponse) {
	t.Helper()
	router, _ := newBoostsRouterWithCache()
	resp := serveLookupRequest(t, router, http.MethodGet, target)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Items      []map[string]any          `json:"items"`
		Pagination shared.PaginationResponse `json:"pagination"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	return body.Items, body.Pagination
}

func boostCosts(items []map[string]any) []float64 {
	costs := make([]float64, 0, len(items))
	for _, item := range items {
		cost, _ := item["costBoost"].(float64)
		costs = append(costs, cost)
	}
	return costs
}

func TestBoostsListReturnsBoostsInCostOrderAndDropsIncompleteRows(t *testing.T) {
	items, pagination := listBoostItems(t, "/api/v1/boosts/jp/list")
	if costs := boostCosts(items); !slices.Equal(costs, []float64{0, 1, 6}) {
		t.Fatalf("expected the complete boosts in costBoost order, got %v", costs)
	}
	if pagination.Total != 3 {
		t.Fatalf("expected a total of 3, got %+v", pagination)
	}
}

func TestBoostsListExposesOnlyTheBoostFields(t *testing.T) {
	items, _ := listBoostItems(t, "/api/v1/boosts/jp/list")
	last := items[len(items)-1]
	expected := map[string]any{
		"id": float64(7), "costBoost": float64(6), "isEventOnly": false, "expRate": float64(26),
		"rewardRate": float64(26), "livePointRate": float64(26), "eventPointRate": float64(27),
		"bondsExpRate": float64(26),
	}
	if len(last) != len(expected) {
		t.Fatalf("expected only the eight boost fields, got %#v", last)
	}
	for field, value := range expected {
		if last[field] != value {
			t.Fatalf("expected %s to be %v, got %#v", field, value, last)
		}
	}
}

func TestBoostsListOmitsFieldsARegionDoesNotHave(t *testing.T) {
	items, pagination := listBoostItems(t, "/api/v1/boosts/en/list")
	if costs := boostCosts(items); !slices.Equal(costs, []float64{0, 1}) {
		t.Fatalf("expected rows without an id in costBoost order, got %v", costs)
	}
	if pagination.Total != 2 {
		t.Fatalf("expected a total of 2, got %+v", pagination)
	}
	second := items[1]
	if second["eventPointRate"] != float64(5) || second["expRate"] != float64(5) ||
		second["rewardRate"] != float64(5) || second["bondsExpRate"] != float64(5) {
		t.Fatalf("unexpected boost fields: %#v", second)
	}
	for _, field := range []string{"id", "isEventOnly", "livePointRate"} {
		if _, exists := second[field]; exists {
			t.Fatalf("expected no %s on a row that has none, got %#v", field, second)
		}
	}
}

func TestBoostsListPaginates(t *testing.T) {
	items, pagination := listBoostItems(t, "/api/v1/boosts/jp/list?page_size=2&page=2")
	if costs := boostCosts(items); !slices.Equal(costs, []float64{6}) {
		t.Fatalf("expected the third boost on page 2, got %v", costs)
	}
	if pagination.Total != 3 || pagination.TotalPages != 2 || pagination.HasNext {
		t.Fatalf("unexpected pagination: %+v", pagination)
	}
}

func TestBoostsListReadsTheProjection(t *testing.T) {
	router, cache := newBoostsRouterWithCache()
	if resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/boosts/jp/list"); resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if !slices.Equal(cache.projections, []string{"jp/" + boostsEntity}) {
		t.Fatalf("expected one boosts projection read, got %v", cache.projections)
	}
}

func TestBoostsListRejectsInvalidRequests(t *testing.T) {
	router, _ := newBoostsRouterWithCache()
	cases := []struct {
		name   string
		target string
	}{
		{"page size above 100", "/api/v1/boosts/jp/list?page_size=101"},
		{"page zero", "/api/v1/boosts/jp/list?page=0"},
		{"unconfigured region", "/api/v1/boosts/xx/list"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if resp := serveLookupRequest(t, router, http.MethodGet, test.target); resp.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestBoostsListReturnsAnEmptyArrayPastTheEnd(t *testing.T) {
	router, _ := newBoostsRouterWithCache()
	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/boosts/jp/list?page=9")
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

func serveBoostsList(t *testing.T, handler *LookupHandler, region string) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/v1/boosts/:region/list", handler.BoostsList)
	resp := serveLookupRequest(t, router, http.MethodGet, "/api/v1/boosts/"+region+"/list")
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

func TestBoostsListIsUnavailableWithoutMasterData(t *testing.T) {
	status, code := serveBoostsList(t, NewLookupHandler(nil), "jp")
	if status != http.StatusServiceUnavailable || code != "MASTER_DATA_DISABLED" {
		t.Fatalf("expected 503 MASTER_DATA_DISABLED, got %d %q", status, code)
	}
}

func TestBoostsListIsUnavailableForARegionThatHasNotSynced(t *testing.T) {
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"kr": {boostsEntity: {{"costBoost": 0, "eventPointRate": 1}}},
		},
	}
	// The status store reports only jp and en as synced.
	status, _ := serveBoostsList(t, newConfiguredLookupHandler(cache, "jp", "kr"), "kr")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for a region without a successful sync, got %d", status)
	}
	if len(cache.projections) != 0 {
		t.Fatalf("expected no projection read for a region that is not ready, got %v", cache.projections)
	}
}

func TestBoostsListReportsAProjectionReadFailure(t *testing.T) {
	cache := &fakeLookupCache{
		listByEntity: map[string]map[string][]map[string]any{
			"jp": {boostsEntity: {{"costBoost": 0, "eventPointRate": 1}}},
		},
		projectionErr: errors.New("store unavailable"),
	}
	status, code := serveBoostsList(t, newConfiguredLookupHandler(cache, "jp"), "jp")
	if status != http.StatusInternalServerError || code != "BOOST_QUERY_ERROR" {
		t.Fatalf("expected 500 BOOST_QUERY_ERROR, got %d %q", status, code)
	}
}
