package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/startup"
)

type mockVerifier struct{}

func (mockVerifier) Verify(_ context.Context, rawToken string) (map[string]any, error) {
	if rawToken == "valid-token" {
		return map[string]any{
			"sub":                "123",
			"preferred_username": "alice",
		}, nil
	}
	if rawToken == "admin-token" {
		return map[string]any{
			"sub":                "456",
			"preferred_username": "sekai-admin",
			"groups":             []any{"sekai-admin"},
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
	return setupRouterWithEnvAndStartupReady(t, appEnv, true)
}

func setupRouterWithEnvAndStartupReady(t *testing.T, appEnv string, ready bool) http.Handler {
	t.Helper()

	cfg := config.Config{
		Port:               "8080",
		AppEnv:             appEnv,
		OIDCIssuerURL:      "https://auth.example.com",
		OIDCInternalURL:    "https://auth-internal.example.com",
		OIDCAudience:       "https://api.example.com",
		OIDCClientID:       "web-client",
		OIDCAuthURL:        "https://auth.example.com/application/o/authorize/",
		OIDCTokenURL:       "https://auth.example.com/application/o/token/",
		OIDCRedirectURL:    "http://localhost:8080/api/v1/admin/login/callback",
		OIDCScopes:         []string{"openid", "profile", "email"},
		OIDCPrivateKeyPath: "/tmp/oidc-test-key.pem",
	}

	startupState := startup.NewState()
	if ready {
		startupState.MarkReady()
	}

	router, err := NewRouter(cfg, nil, mockVerifier{}, nil, nil, startupState)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	return router
}

func setupRouterWithAdminClaim(t *testing.T, claim string, values []string) http.Handler {
	t.Helper()
	return setupRouterWithEnvAndAdminClaim(t, "test", claim, values)
}

func setupRouterWithEnvAndAdminClaim(t *testing.T, appEnv string, claim string, values []string) http.Handler {
	t.Helper()

	cfg := config.Config{
		Port:                 "8080",
		AppEnv:               appEnv,
		OIDCIssuerURL:        "https://auth.example.com",
		OIDCInternalURL:      "https://auth-internal.example.com",
		OIDCAudience:         "https://api.example.com",
		OIDCClientID:         "web-client",
		OIDCAuthURL:          "https://auth.example.com/application/o/authorize/",
		OIDCTokenURL:         "https://auth.example.com/application/o/token/",
		OIDCRedirectURL:      "http://localhost:8080/api/v1/admin/login/callback",
		OIDCScopes:           []string{"openid", "profile", "email"},
		OIDCPrivateKeyPath:   "/tmp/oidc-test-key.pem",
		OIDCAdminClaim:       claim,
		OIDCAdminClaimValues: append([]string(nil), values...),
	}

	startupState := startup.NewState()
	startupState.MarkReady()

	router, err := NewRouter(cfg, nil, mockVerifier{}, nil, nil, startupState)
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	return router
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

func TestAdminMasterDataEventsUnauthorized(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/master-data/events", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on admin master-data events without token, got %d", resp.Code)
	}
}

func TestPublicMasterDataStatusNotFound(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/master-data/status", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on public master-data status, got %d", resp.Code)
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

func TestCardAvailableRegionsUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/regions/1001/availability", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on card available regions when service unavailable, got %d", resp.Code)
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

func TestMusicAvailableRegionsUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/musics/regions/1001/availability", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on music available regions when service unavailable, got %d", resp.Code)
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

func TestEventListUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list?page=1&page_size=20", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on event list when service unavailable, got %d", resp.Code)
	}
}

func TestEventSearchUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/search?q=test", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on event search when service unavailable, got %d", resp.Code)
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

func TestEventAvailableRegionsUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events/regions/101/availability", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on event available regions when service unavailable, got %d", resp.Code)
	}
}

func TestMasterDataEventsUnavailable(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/master-data/events", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on public master-data events, got %d", resp.Code)
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

func TestProfileForbiddenWithoutRequiredAdminClaim(t *testing.T) {
	router := setupRouterWithAdminClaim(t, "groups", []string{"sekai-admin"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/profile", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on profile without required admin claim, got %d", resp.Code)
	}
}

func TestProfileAuthorizedWithRequiredAdminClaim(t *testing.T) {
	router := setupRouterWithAdminClaim(t, "groups", []string{"sekai-admin"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/profile", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 on profile with required admin claim, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	authDebug, ok := body["auth_debug"].(map[string]any)
	if !ok {
		t.Fatalf("expected auth_debug object in test env response, got %T", body["auth_debug"])
	}
	if authDebug["admin_claim"] != "groups" {
		t.Fatalf("expected admin_claim=groups, got %v", authDebug["admin_claim"])
	}
}

func TestProfileHidesAuthDebugInProduction(t *testing.T) {
	router := setupRouterWithEnvAndAdminClaim(t, "production", "groups", []string{"sekai-admin"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/profile", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 on profile in production, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if _, exists := body["auth_debug"]; exists {
		t.Fatalf("expected auth_debug to be hidden in production response")
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

func TestAdminMasterDataEventsAuthorizedByQueryToken(t *testing.T) {
	router := setupRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/master-data/events?access_token=valid-token", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on admin master-data events without hub, got %d", resp.Code)
	}
}

func TestStartupGateAllowsAdminLoginBeforeReady(t *testing.T) {
	router := setupRouterWithEnvAndStartupReady(t, "test", false)

	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 on admin login page during startup, got %d", resp.Code)
	}
}

func TestStartupGateAllowsAdminProfileBeforeReady(t *testing.T) {
	router := setupRouterWithEnvAndStartupReady(t, "test", false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/profile", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 on admin profile during startup, got %d", resp.Code)
	}
}

func TestStartupGateBlocksPublicDataEndpointsBeforeReady(t *testing.T) {
	router := setupRouterWithEnvAndStartupReady(t, "test", false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/jp/1001", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on public data endpoint during startup, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errorPayload, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %T", body["error"])
	}
	if errorPayload["code"] != "STARTUP_IN_PROGRESS" {
		t.Fatalf("expected STARTUP_IN_PROGRESS code, got %v", errorPayload["code"])
	}
}

func TestStartupStatusReturnsBootstrapPayloadBeforeReady(t *testing.T) {
	router := setupRouterWithEnvAndStartupReady(t, "test", false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/master-data/status", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 on admin master-data status during startup, got %d", resp.Code)
	}

	body := map[string]any{}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["startup_ready"] != false {
		t.Fatalf("expected startup_ready=false, got %v", body["startup_ready"])
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
	if !strings.HasPrefix(location, "https://auth.example.com/application/o/authorize/?") {
		t.Fatalf("expected redirect to oidc authorize endpoint, got %q", location)
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
