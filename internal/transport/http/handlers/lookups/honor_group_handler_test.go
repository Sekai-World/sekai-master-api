package lookups

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/transport/http/handlers/shared"
	"sekai-master-api/internal/usecase"
)

type honorGroupListCache struct {
	*fakeLookupCache
	listAllErrors map[string]error
}

func (cache *honorGroupListCache) ListAll(ctx context.Context, region string, entity string) ([]map[string]any, error) {
	if err := cache.listAllErrors[entity]; err != nil {
		return nil, err
	}

	return cache.fakeLookupCache.ListAll(ctx, region, entity)
}

func newHonorGroupListHandler(cache usecase.MasterDataCache) *LookupHandler {
	statusStore := &fakeLookupStatusStore{
		statuses: []masterdata.SyncStatus{
			{Region: "jp", Status: "success"},
		},
	}
	syncUsecase := usecase.NewMasterDataSyncUsecase(nil, nil, cache, statusStore, nil, 1)
	return NewLookupHandler(syncUsecase)
}

func newHonorGroupListRouter(handler *LookupHandler) *gin.Engine {
	router := gin.New()
	router.GET("/api/v1/honorGroups/:region/list", handler.HonorGroupsList)
	return router
}

func newHonorGroupListCache() *honorGroupListCache {
	return &honorGroupListCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {
					honorGroupsEntity: {
						{"id": 30, "name": "Group 30", "honorType": "degree"},
						{"id": 20, "name": "Empty group"},
						{"id": 10, "name": "Group 10", "frameName": "frame_10"},
						{"id": 40, "name": "Group 40"},
					},
					honorsEntity: {
						{
							"id":              301,
							"seq":             3,
							"groupId":         30,
							"name":            "Honor 301",
							"honorRarity":     "rarity_normal",
							"assetbundleName": "honor_301",
							"levels":          []any{map[string]any{"honorId": 301, "level": 1, "bonus": 0.5}},
						},
						{
							"id":              101,
							"seq":             1,
							"groupId":         10,
							"name":            "Honor 101",
							"honorRarity":     "rarity_rare",
							"assetbundleName": "honor_101",
							"levels":          []any{map[string]any{"honorId": 101, "level": 1}},
						},
						{
							"id":              302,
							"seq":             2,
							"groupId":         30,
							"name":            "Honor 302",
							"honorRarity":     "rarity_high",
							"assetbundleName": "honor_302",
							"levels":          []any{map[string]any{"honorId": 302, "level": 1}, map[string]any{"honorId": 302, "level": 2}},
						},
						{
							"id":              401,
							"seq":             4,
							"groupId":         40,
							"name":            "Honor 401",
							"honorRarity":     "rarity_normal",
							"assetbundleName": "honor_401",
							"levels":          []any{},
						},
						{"id": 501, "groupId": 0, "name": "Ungrouped honor"},
						{"id": 502, "groupId": -1, "name": "Negative group honor"},
						{"id": 503, "groupId": 99, "name": "Orphan honor"},
					},
				},
			},
		},
	}
}

func decodeHonorGroupList(t *testing.T, body []byte) shared.HonorGroupListResponse {
	t.Helper()

	var response shared.HonorGroupListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode honor group list: %v", err)
	}
	return response
}

func requireHonorGroupID(t *testing.T, group shared.HonorGroupObjectResponse, expected int64) {
	t.Helper()
	if group.ID != expected {
		t.Fatalf("expected group id %d, got %d", expected, group.ID)
	}
}

func TestHonorGroupsListPaginatesDisplayableGroupsAndPreservesMasterOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := newHonorGroupListHandler(newHonorGroupListCache())
	router := newHonorGroupListRouter(handler)

	firstPage := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honorGroups/jp/list?page=1&page_size=1")
	if firstPage.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", firstPage.Code, firstPage.Body.String())
	}
	firstBody := decodeHonorGroupList(t, firstPage.Body.Bytes())
	if firstBody.Pagination.Total != 3 || firstBody.Pagination.TotalPages != 3 || !firstBody.Pagination.HasNext {
		t.Fatalf("expected group-count pagination for three displayable groups, got %#v", firstBody.Pagination)
	}
	if len(firstBody.Items) != 1 {
		t.Fatalf("expected one group on page one, got %#v", firstBody.Items)
	}

	firstGroup := firstBody.Items[0]
	requireHonorGroupID(t, firstGroup, 30)
	if len(firstGroup.Honors) != 2 || firstGroup.Honors[0].ID != 301 || firstGroup.Honors[1].ID != 302 {
		t.Fatalf("expected all group 30 honors in source order, got %#v", firstGroup.Honors)
	}
	if firstGroup.Honors[0].Group == nil || firstGroup.Honors[0].Group.ID == nil || *firstGroup.Honors[0].Group.ID != 30 {
		t.Fatalf("expected honor group metadata on every honor, got %#v", firstGroup.Honors[0].Group)
	}
	if len(firstGroup.Honors[0].Levels) != 1 || firstGroup.Honors[0].Levels[0].HonorID == nil || *firstGroup.Honors[0].Levels[0].HonorID != 301 {
		t.Fatalf("expected honor levels to be included, got %#v", firstGroup.Honors[0].Levels)
	}
	if len(firstGroup.Honors[1].Levels) != 2 {
		t.Fatalf("expected all levels for every nested honor, got %#v", firstGroup.Honors[1].Levels)
	}

	secondPage := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honorGroups/jp/list?page=2&page_size=1")
	secondBody := decodeHonorGroupList(t, secondPage.Body.Bytes())
	if len(secondBody.Items) != 1 {
		t.Fatalf("expected one group on page two, got %#v", secondBody.Items)
	}
	requireHonorGroupID(t, secondBody.Items[0], 10)

	thirdPage := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honorGroups/jp/list?page=3&page_size=1")
	thirdBody := decodeHonorGroupList(t, thirdPage.Body.Bytes())
	if len(thirdBody.Items) != 1 {
		t.Fatalf("expected one group on page three, got %#v", thirdBody.Items)
	}
	requireHonorGroupID(t, thirdBody.Items[0], 40)
	if len(thirdBody.Items[0].Honors) != 1 || len(thirdBody.Items[0].Honors[0].Levels) != 0 {
		t.Fatalf("expected honors with empty levels to remain complete, got %#v", thirdBody.Items[0].Honors)
	}
	if thirdBody.Pagination.HasNext {
		t.Fatalf("expected no next page after final group, got %#v", thirdBody.Pagination)
	}
}

func TestHonorGroupsListUsesDefaultAndBoundedPageSizes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := newHonorGroupListHandler(newHonorGroupListCache())
	router := newHonorGroupListRouter(handler)

	defaultPage := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honorGroups/jp/list")
	defaultBody := decodeHonorGroupList(t, defaultPage.Body.Bytes())
	if defaultBody.Pagination.PageSize != honorGroupsDefaultPageSize || len(defaultBody.Items) != 3 {
		t.Fatalf("expected default page size %d and all three groups, got %#v", honorGroupsDefaultPageSize, defaultBody)
	}

	maximumPage := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honorGroups/jp/list?page_size=24")
	maximumBody := decodeHonorGroupList(t, maximumPage.Body.Bytes())
	if maximumBody.Pagination.PageSize != honorGroupsMaximumPageSize {
		t.Fatalf("expected maximum page size %d, got %#v", honorGroupsMaximumPageSize, maximumBody.Pagination)
	}

	for _, pageSize := range []string{"0", "25", "not-a-number"} {
		response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honorGroups/jp/list?page_size="+pageSize)
		if response.Code != http.StatusBadRequest {
			t.Errorf("page_size=%s: expected 400, got %d: %s", pageSize, response.Code, response.Body.String())
		}
	}

	for _, page := range []string{"0", "-1", "not-a-number"} {
		response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honorGroups/jp/list?page="+page)
		if response.Code != http.StatusBadRequest {
			t.Errorf("page=%s: expected 400, got %d: %s", page, response.Code, response.Body.String())
		}
	}
}

func TestHonorGroupsListReturnsEmptyPageForOutOfRangePage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := newHonorGroupListHandler(newHonorGroupListCache())
	router := newHonorGroupListRouter(handler)
	response := serveLookupRequest(t, router, http.MethodGet, "/api/v1/honorGroups/jp/list?page=4&page_size=1")
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}

	body := decodeHonorGroupList(t, response.Body.Bytes())
	if len(body.Items) != 0 || body.Pagination.Total != 3 || body.Pagination.TotalPages != 3 || body.Pagination.HasNext {
		t.Fatalf("expected authoritative empty out-of-range page, got %#v", body)
	}
}

func TestHonorGroupsListPreservesQueryAndReadinessErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	queryErrorCache := &honorGroupListCache{
		fakeLookupCache: &fakeLookupCache{
			listByEntity: map[string]map[string][]map[string]any{
				"jp": {honorsEntity: {{"id": 1, "groupId": 1}}},
			},
		},
		listAllErrors: map[string]error{honorGroupsEntity: errors.New("honor groups unavailable")},
	}
	queryErrorResponse := serveLookupRequest(
		t,
		newHonorGroupListRouter(newHonorGroupListHandler(queryErrorCache)),
		http.MethodGet,
		"/api/v1/honorGroups/jp/list",
	)
	if queryErrorResponse.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for group query failure, got %d: %s", queryErrorResponse.Code, queryErrorResponse.Body.String())
	}
	var queryErrorBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(queryErrorResponse.Body.Bytes(), &queryErrorBody); err != nil {
		t.Fatalf("decode query error: %v", err)
	}
	if queryErrorBody.Error.Code != "HONOR_QUERY_ERROR" {
		t.Fatalf("expected HONOR_QUERY_ERROR, got %q", queryErrorBody.Error.Code)
	}

	readinessCache := &honorGroupListCache{
		fakeLookupCache: &fakeLookupCache{hasIndexSet: true, hasIndex: false},
	}
	readinessResponse := serveLookupRequest(
		t,
		newHonorGroupListRouter(newHonorGroupListHandler(readinessCache)),
		http.MethodGet,
		"/api/v1/honorGroups/jp/list",
	)
	if readinessResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when honor data is not ready, got %d: %s", readinessResponse.Code, readinessResponse.Body.String())
	}
}
