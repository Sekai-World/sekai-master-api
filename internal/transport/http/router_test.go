package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	return setupRouterWithEnv(t, "test")
}

func setupRouterWithEnv(t *testing.T, appEnv string) http.Handler {
	t.Helper()

	cfg := config.Config{
		Port:                  "8080",
		AppEnv:                appEnv,
		ZitadelIssuerURL:      "https://zitadel.example.com",
		ZitadelAudience:       "https://api.example.com",
		ZitadelClientID:       "web-client",
		ZitadelRedirectURL:    "http://localhost:8080/api/v1/admin/login/callback",
		ZitadelScopes:         []string{"openid", "profile", "email"},
		ZitadelPrivateKeyPath: "/tmp/zitadel-test-key.pem",
	}

	return NewRouter(cfg, nil, mockVerifier{}, nil, nil)
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

func TestDocsPage(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/docs/index.html", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
}

func TestDocsJSON(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/docs/doc.json", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
}

func TestDocsDisabledInProduction(t *testing.T) {
	router := setupRouterWithEnv(t, "production")

	req := httptest.NewRequest(http.MethodGet, "/docs/index.html", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 in production, got %d", resp.Code)
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

func TestMasterDataForceSyncUnauthorized(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/master-data/sync/force", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on master-data force sync without token, got %d", resp.Code)
	}
}

func TestAdminMasterDataStatusUnauthorized(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/master-data/status", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on admin master-data status without token, got %d", resp.Code)
	}
}

func TestCardByIDUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on card by id when service unavailable, got %d", resp.Code)
	}
}

func TestCardParamsUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/params", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on card params when service unavailable, got %d", resp.Code)
	}
}

func TestCardEpisodesUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001/episodes", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on card episodes when service unavailable, got %d", resp.Code)
	}
}

func TestCardSearchUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/search?q=クール", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on card search when service unavailable, got %d", resp.Code)
	}
}

func TestCardListUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/list?page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on card list when service unavailable, got %d", resp.Code)
	}
}

func TestMusicByIDUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/1001", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on music by id when service unavailable, got %d", resp.Code)
	}
}

func TestMusicSearchUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/search?title=hello", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on music search when service unavailable, got %d", resp.Code)
	}
}

func TestMusicListUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/jp/list?page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on music list when service unavailable, got %d", resp.Code)
	}
}

func TestEventByIDUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on event by id when service unavailable, got %d", resp.Code)
	}
}

func TestCurrentEventUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/current", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on current event when service unavailable, got %d", resp.Code)
	}
}

func TestEventRewardsUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/101/rewards", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on event rewards when service unavailable, got %d", resp.Code)
	}
}

func TestMasterDataEventsUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/master-data/events", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on master-data events when service unavailable, got %d", resp.Code)
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

func TestAdminMasterDataStatusAuthorized(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/master-data/status", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 on admin master-data status, got %d", resp.Code)
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

func TestAdminLoginStart(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/login", nil)
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusFound {
		t.Fatalf("expected 302 on admin login start, got %d", resp.Code)
	}

	location := resp.Header().Get("Location")
	if !strings.HasPrefix(location, "https://zitadel.example.com/oauth/v2/authorize?") {
		t.Fatalf("expected redirect to zitadel authorize endpoint, got %q", location)
	}

	if len(resp.Result().Cookies()) == 0 {
		t.Fatal("expected login cookies to be set")
	}
}

func TestAdminLoginCallbackInvalidState(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/login/callback?code=demo-code&state=wrong-state", nil)
	req.AddCookie(&http.Cookie{Name: "sekai_admin_login_state", Value: "expected-state", Path: "/api/v1/admin/login"})
	req.AddCookie(&http.Cookie{Name: "sekai_admin_login_code_verifier", Value: "code-verifier", Path: "/api/v1/admin/login"})
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusFound {
		t.Fatalf("expected 302 on invalid callback state, got %d", resp.Code)
	}

	if location := resp.Header().Get("Location"); location != "/admin/login?error=oauth_state_mismatch" {
		t.Fatalf("expected redirect to login error page, got %q", location)
	}
}
