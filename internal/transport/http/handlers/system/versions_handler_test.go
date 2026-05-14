package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type fakeVersionsBackupStore struct {
	payload                map[string]any
	payloadByRegion        map[string]map[string]any
	versionPayload         any
	versionPayloadByRegion map[string]any
	found                  bool
	foundByRegion          map[string]bool
	versionFound           bool
	versionFoundByRegion   map[string]bool
	err                    error
	errByRegion            map[string]error
	versionErr             error
	versionErrByRegion     map[string]error
	source                 masterdata.Source
	loadLatestCalls        int
	loadLatestVersionCalls int
}

func (store *fakeVersionsBackupStore) SaveRegionPayload(context.Context, masterdata.Source, string, map[string]any) error {
	return nil
}

func (store *fakeVersionsBackupStore) LoadRegionPayload(context.Context, masterdata.Source, string) (map[string]any, bool, error) {
	return nil, false, nil
}

func (store *fakeVersionsBackupStore) LoadLatestRegionPayload(_ context.Context, source masterdata.Source) (map[string]any, string, time.Time, bool, error) {
	store.source = source
	store.loadLatestCalls++

	if store.payloadByRegion != nil {
		payload, payloadFound := store.payloadByRegion[source.Region]
		found := false
		if store.foundByRegion != nil {
			found = store.foundByRegion[source.Region]
		} else {
			found = payloadFound
		}

		var err error
		if store.errByRegion != nil {
			err = store.errByRegion[source.Region]
		}

		return payload, "commit", time.Now().UTC(), found, err
	}

	return store.payload, "commit", time.Now().UTC(), store.found, store.err
}

func (store *fakeVersionsBackupStore) LoadLatestRegionVersionPayload(_ context.Context, source masterdata.Source) (any, string, time.Time, bool, error) {
	store.source = source
	store.loadLatestVersionCalls++

	if store.versionPayloadByRegion != nil {
		payload, payloadFound := store.versionPayloadByRegion[source.Region]
		found := false
		if store.versionFoundByRegion != nil {
			found = store.versionFoundByRegion[source.Region]
		} else {
			found = payloadFound
		}

		var err error
		if store.versionErrByRegion != nil {
			err = store.versionErrByRegion[source.Region]
		}

		return payload, "commit", time.Now().UTC(), found, err
	}

	return store.versionPayload, "commit", time.Now().UTC(), store.versionFound, store.versionErr
}

func TestVersionsAllRegionsReturnsConfiguredVersions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncUsecase := usecase.NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "en", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "tw", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "kr", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
		{Region: "cn", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
	}, nil, nil, nil, nil, 1)

	backupStore := &fakeVersionsBackupStore{
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
		versionFoundByRegion: map[string]bool{
			"jp": true,
			"en": true,
			"tw": true,
			"kr": true,
			"cn": true,
		},
	}
	syncUsecase.SetBackupStore(backupStore)

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

	if backupStore.loadLatestVersionCalls != 5 {
		t.Fatalf("expected 5 version-only loads, got %d", backupStore.loadLatestVersionCalls)
	}
}

func TestVersionsByRegionReturnsCachedVersionPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncUsecase := usecase.NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main", Path: "data"},
	}, nil, nil, nil, nil, 1)
	backupStore := &fakeVersionsBackupStore{
		versionFound: true,
		versionPayload: map[string]any{
			"appVersion":  "3.2.1",
			"dataVersion": "20260419",
		},
	}
	syncUsecase.SetBackupStore(backupStore)

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
	if backupStore.source.Region != "jp" {
		t.Fatalf("expected source region jp, got %s", backupStore.source.Region)
	}
	if backupStore.loadLatestCalls != 0 {
		t.Fatalf("expected no full payload load, got %d", backupStore.loadLatestCalls)
	}
	if backupStore.loadLatestVersionCalls != 1 {
		t.Fatalf("expected one version-only load, got %d", backupStore.loadLatestVersionCalls)
	}
}

func TestVersionsByRegionReturnsNotFoundWhenVersionMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncUsecase := usecase.NewMasterDataSyncUsecase([]masterdata.Source{
		{Region: "jp", Owner: "owner", Repo: "repo", Ref: "main"},
	}, nil, nil, nil, nil, 1)
	syncUsecase.SetBackupStore(&fakeVersionsBackupStore{
		found:   true,
		payload: map[string]any{"cards.json": []any{}},
	})

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
