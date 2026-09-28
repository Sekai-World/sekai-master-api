package system

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

// fakeVersionsCache is a store that keeps a version payload per region.
type fakeVersionsCache struct {
	versionPayloadByRegion map[string]any
	versionErr             error
	loadedRegions          []string
}

func (cache *fakeVersionsCache) StoreRegion(context.Context, string, map[string]any) error {
	return nil
}

func (cache *fakeVersionsCache) GetByID(context.Context, string, string, string) (map[string]any, bool, error) {
	return nil, false, nil
}

func (cache *fakeVersionsCache) ListAll(context.Context, string, string) ([]map[string]any, error) {
	return nil, nil
}

func (cache *fakeVersionsCache) ListByPage(context.Context, string, string, int, int) ([]map[string]any, int, error) {
	return nil, 0, nil
}

func (cache *fakeVersionsCache) LoadRegionVersionPayload(_ context.Context, region string) (any, bool, error) {
	cache.loadedRegions = append(cache.loadedRegions, region)
	if cache.versionErr != nil {
		return nil, false, cache.versionErr
	}
	payload, found := cache.versionPayloadByRegion[region]
	return payload, found, nil
}

func TestVersionsAllRegionsReturnsConfiguredVersions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVersionsCache{
		versionPayloadByRegion: map[string]any{
			"jp": map[string]any{
				"appVersion":   "3.2.1",
				"assetVersion": "3.2.1.10",
				"dataVersion":  "3.2.1.10",
				"cdnVersion":   1,
				"ignored":      "should-not-be-present",
			},
			"en": map[string]any{
				"appVersion":   "3.2.0",
				"assetVersion": "3.2.0.9",
				"dataVersion":  "3.2.0.9",
			},
			"tw": map[string]any{
				"appVersion":   "3.2.1",
				"assetVersion": "3.2.1.10",
				"dataVersion":  "3.2.1.10",
				"cdnVersion":   1,
			},
			"kr": map[string]any{
				"appVersion":   "3.2.1",
				"assetVersion": "3.2.1.10",
				"dataVersion":  "3.2.1.10",
				"cdnVersion":   2.0,
			},
			"cn": map[string]any{
				"appVersion":   "3.2.1",
				"assetVersion": "3.2.1.10",
				"dataVersion":  "3.2.1.10",
				"cdnVersion":   "3",
			},
		},
	}
	syncUsecase := usecase.NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "en", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "tw", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "kr", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "cn", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
	}, nil, cache, nil, nil, 1)

	handler := NewVersionsHandler(syncUsecase)
	router := gin.New()
	router.GET("/api/v1/versions", handler.AllRegions)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/versions", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body map[string]map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body) != 5 {
		t.Fatalf("expected versions for 5 regions, got %d", len(body))
	}

	jp, ok := body["jp"]
	if !ok {
		t.Fatalf("expected jp region in response")
	}
	if jp["appVersion"] != "3.2.1" {
		t.Fatalf("expected jp appVersion 3.2.1, got %v", jp["appVersion"])
	}
	if jp["assetVersion"] != "3.2.1.10" {
		t.Fatalf("expected jp assetVersion 3.2.1.10, got %v", jp["assetVersion"])
	}
	if jp["dataVersion"] != "3.2.1.10" {
		t.Fatalf("expected jp dataVersion 3.2.1.10, got %v", jp["dataVersion"])
	}
	if jp["cdnVersion"] != float64(1) {
		t.Fatalf("expected jp cdnVersion, got %v", jp["cdnVersion"])
	}
	if _, exists := jp["ignored"]; exists {
		t.Fatalf("unexpected field ignored in jp payload")
	}

	en, ok := body["en"]
	if !ok {
		t.Fatalf("expected en region in response")
	}
	if en["appVersion"] != "3.2.0" {
		t.Fatalf("expected en appVersion 3.2.0, got %v", en["appVersion"])
	}
	if en["assetVersion"] != "3.2.0.9" {
		t.Fatalf("expected en assetVersion 3.2.0.9, got %v", en["assetVersion"])
	}
	if en["dataVersion"] != "3.2.0.9" {
		t.Fatalf("expected en dataVersion 3.2.0.9, got %v", en["dataVersion"])
	}
	if _, exists := en["cdnVersion"]; exists {
		t.Fatalf("did not expect en cdnVersion when source payload does not include it")
	}

	tw, ok := body["tw"]
	if !ok {
		t.Fatalf("expected tw region in response")
	}
	if tw["cdnVersion"] != float64(1) {
		t.Fatalf("expected tw cdnVersion 1, got %v", tw["cdnVersion"])
	}

	kr, ok := body["kr"]
	if !ok {
		t.Fatalf("expected kr region in response")
	}
	if kr["cdnVersion"] != float64(2) {
		t.Fatalf("expected kr cdnVersion 2, got %v", kr["cdnVersion"])
	}

	cn, ok := body["cn"]
	if !ok {
		t.Fatalf("expected cn region in response")
	}
	if cn["cdnVersion"] != float64(3) {
		t.Fatalf("expected cn cdnVersion 3, got %v", cn["cdnVersion"])
	}

	versionPattern := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
	for region, payload := range body {
		asset, ok := payload["assetVersion"].(string)
		if !ok {
			t.Fatalf("expected %s assetVersion to be string, got %T", region, payload["assetVersion"])
		}
		if !versionPattern.MatchString(asset) {
			t.Fatalf("expected %s assetVersion to match x.x.x.x, got %q", region, asset)
		}

		data, ok := payload["dataVersion"].(string)
		if !ok {
			t.Fatalf("expected %s dataVersion to be string, got %T", region, payload["dataVersion"])
		}
		if !versionPattern.MatchString(data) {
			t.Fatalf("expected %s dataVersion to match x.x.x.x, got %q", region, data)
		}
	}

	if len(cache.loadedRegions) != 5 {
		t.Fatalf("expected 5 version payload loads, got %v", cache.loadedRegions)
	}
}

func TestVersionsByRegionReturnsCachedVersionPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := &fakeVersionsCache{
		versionPayloadByRegion: map[string]any{
			"jp": map[string]any{
				"appVersion":  "3.2.1",
				"dataVersion": "20260419",
			},
		},
	}
	syncUsecase := usecase.NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
	}, nil, cache, nil, nil, 1)

	handler := NewVersionsHandler(syncUsecase)
	router := gin.New()
	router.GET("/api/v1/versions/:region", handler.ByRegion)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/versions/jp", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["appVersion"] != "3.2.1" {
		t.Fatalf("expected appVersion 3.2.1, got %v", body["appVersion"])
	}
	if len(cache.loadedRegions) != 1 || cache.loadedRegions[0] != "jp" {
		t.Fatalf("expected one version payload load for jp, got %v", cache.loadedRegions)
	}
}

func TestVersionsByRegionReturnsNotFoundWhenVersionMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncUsecase := usecase.NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main"},
	}, nil, &fakeVersionsCache{}, nil, nil, 1)

	handler := NewVersionsHandler(syncUsecase)
	router := gin.New()
	router.GET("/api/v1/versions/:region", handler.ByRegion)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/versions/jp", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Code)
	}
}

func TestVersionsEndpointsHandleVersionLoadErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncUsecase := usecase.NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main"},
	}, nil, &fakeVersionsCache{versionErr: errors.New("store read failed")}, nil, nil, 1)

	handler := NewVersionsHandler(syncUsecase)
	router := gin.New()
	router.GET("/api/v1/versions", handler.AllRegions)
	router.GET("/api/v1/versions/:region", handler.ByRegion)

	// All regions leaves the failing region out.
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/versions", nil))
	if resp.Code != http.StatusOK || strings.TrimSpace(resp.Body.String()) != "{}" {
		t.Fatalf("expected 200 without the failing region, got %d: %s", resp.Code, resp.Body.String())
	}

	// One region reports the error.
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/versions/jp", nil))
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "VERSION_QUERY_ERROR" {
		t.Fatalf("expected VERSION_QUERY_ERROR, got %q", body.Error.Code)
	}
}
