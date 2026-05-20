package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
)

func TestAdminLoginCallbackFallsBackToPublicTokenEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var tokenRequests int
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++

		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want %s", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/application/o/token/" {
			t.Fatalf("path = %q, want %q", r.URL.Path, "/application/o/token/")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if r.Form.Get("code") != "auth-code" {
			t.Fatalf("code = %q, want %q", r.Form.Get("code"), "auth-code")
		}
		if r.Form.Get("code_verifier") != "code-verifier" {
			t.Fatalf("code_verifier = %q, want %q", r.Form.Get("code_verifier"), "code-verifier")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"token-123","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	primaryClient, err := auth.NewOIDCHTTPClient(config.Config{
		OIDCIssuerURL:   tokenServer.URL,
		OIDCInternalURL: "http://127.0.0.1:1",
	}, time.Second)
	if err != nil {
		t.Fatalf("NewOIDCHTTPClient() error = %v", err)
	}

	handler := &AdminLoginHandler{
		tokenEndpoint: tokenServer.URL + "/application/o/token/",
		clientID:      "sekai-admin-web",
		redirectURL:   "http://localhost:18080/api/v1/admin/login/callback",
		scopes:        []string{"openid", "profile", "email"},
		httpClient:    primaryClient,
		publicClient:  auth.NewPublicOIDCHTTPClient(time.Second),
	}

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/login/callback?state=login-state&code=auth-code", nil)
	request.AddCookie(&http.Cookie{Name: adminLoginStateCookie, Value: "login-state", Path: adminLoginCookiePath})
	request.AddCookie(&http.Cookie{Name: adminCodeVerifierCookie, Value: "code-verifier", Path: adminLoginCookiePath})
	context.Request = request

	handler.Callback(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if tokenRequests != 1 {
		t.Fatalf("token requests = %d, want %d", tokenRequests, 1)
	}
	if !strings.Contains(recorder.Body.String(), `sessionStorage.setItem("sekai_admin_token", "token-123")`) {
		t.Fatalf("response body missing access token: %s", recorder.Body.String())
	}
}
