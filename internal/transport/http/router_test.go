package http

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sekai-master-api/internal/config"
)

type mockVerifier struct{}

func (mockVerifier) Verify(_ context.Context, rawToken string) (map[string]any, error) {
	if rawToken == "valid-token" {
		return map[string]any{
			"sub":                "123",
			"preferred_username": "alice",
		}, nil
	}
	return nil, errors.New("invalid token")
}

func setupRouter(t *testing.T) http.Handler {
	t.Helper()

	cfg := config.Config{
		Port:   "8080",
		AppEnv: "test",
	}

	return NewRouter(cfg, nil, mockVerifier{}, nil)
}

func TestHealth(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
}

func TestProfileUnauthorized(t *testing.T) {
	router := setupRouter(t)

	profileReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/profile", nil)
	profileResp := httptest.NewRecorder()
	router.ServeHTTP(profileResp, profileReq)

	if profileResp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on profile without token, got %d", profileResp.Code)
	}
}

func TestMasterDataSyncUnauthorized(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/master-data/sync", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on master-data sync without token, got %d", resp.Code)
	}
}

func TestProfileAuthorized(t *testing.T) {
	router := setupRouter(t)

	profileReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/profile", nil)
	profileReq.Header.Set("Authorization", "Bearer valid-token")
	profileResp := httptest.NewRecorder()
	router.ServeHTTP(profileResp, profileReq)

	if profileResp.Code != http.StatusOK {
		t.Fatalf("expected 200 on profile, got %d", profileResp.Code)
	}
}

func TestAdminLoginPage(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 on /admin/login, got %d", resp.Code)
	}
}

func TestAdminLoginInvalidPayload(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/login", bytes.NewBufferString("{invalid-json"))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid admin login payload, got %d", resp.Code)
	}
}
