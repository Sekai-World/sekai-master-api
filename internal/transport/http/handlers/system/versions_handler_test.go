package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/usecase"
)

type fakeVersionsBackupStore struct {
	payload                map[string]any
	versionPayload         any
	found                  bool
	versionFound           bool
	err                    error
	versionErr             error
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
	return store.payload, "commit", time.Now().UTC(), store.found, store.err
}

func (store *fakeVersionsBackupStore) LoadLatestRegionVersionPayload(_ context.Context, source masterdata.Source) (any, string, time.Time, bool, error) {
	store.source = source
	store.loadLatestVersionCalls++
	return store.versionPayload, "commit", time.Now().UTC(), store.versionFound, store.versionErr
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
