package admin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/config"
	"sekai-master-api/internal/transport/http/response"
)

const (
	adminLoginStateCookie    = "sekai_admin_login_state"
	adminCodeVerifierCookie  = "sekai_admin_login_code_verifier"
	adminLoginCookiePath     = "/api/v1/admin/login"
	adminDashboardPath       = "/admin"
	adminLoginPagePath       = "/admin/login"
	adminSessionStorageToken = "sekai_admin_token"
)

type AdminLoginHandler struct {
	authURL        string
	tokenEndpoint  string
	clientID       string
	redirectURL    string
	scopes         []string
	privateKeyPath string
	privateKeyID   string
	httpClient     *http.Client
	publicClient   *http.Client
}

type oauthTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type oauthErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func NewAdminLoginHandler(cfg config.Config) (*AdminLoginHandler, error) {
	httpClient, err := auth.NewOIDCHTTPClient(cfg, 10*time.Second)
	if err != nil {
		return nil, err
	}

	authURL, tokenEndpoint, err := auth.ResolveOIDCEndpoints(context.Background(), cfg)
	if err != nil {
		return nil, err
	}

	return &AdminLoginHandler{
		authURL:        authURL,
		tokenEndpoint:  tokenEndpoint,
		clientID:       cfg.OIDCClientID,
		redirectURL:    cfg.OIDCRedirectURL,
		scopes:         append([]string(nil), cfg.OIDCScopes...),
		privateKeyPath: cfg.OIDCPrivateKeyPath,
		privateKeyID:   cfg.OIDCPrivateKeyID,
		httpClient:     httpClient,
		publicClient:   auth.NewPublicOIDCHTTPClient(10 * time.Second),
	}, nil
}

// Start godoc
// @Summary Start admin login with OIDC provider
// @Tags admin
// @Success 302 {string} string "Redirect to OIDC authorization endpoint"
// @Failure 500 {object} shared.ErrorResponse
// @Router /admin/login [get]
func (handler *AdminLoginHandler) Start(c *gin.Context) {
	if !handler.isConfigured() {
		redirectToLoginError(c, "auth_not_configured")
		return
	}

	state, err := auth.RandomToken(32)
	if err != nil {
		redirectToLoginError(c, "oauth_callback_failed")
		return
	}

	codeVerifier, err := auth.RandomToken(32)
	if err != nil {
		redirectToLoginError(c, "oauth_callback_failed")
		return
	}

	setLoginCookie(c, adminLoginStateCookie, state)
	setLoginCookie(c, adminCodeVerifierCookie, codeVerifier)

	params := url.Values{}
	params.Set("client_id", handler.clientID)
	params.Set("response_type", "code")
	params.Set("redirect_uri", handler.redirectURL)
	params.Set("scope", strings.Join(handler.scopes, " "))
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge(codeVerifier))
	params.Set("code_challenge_method", "S256")

	c.Redirect(http.StatusFound, handler.authURL+"?"+params.Encode())
}

func (handler *AdminLoginHandler) Callback(c *gin.Context) {
	clearLoginCookies(c)

	if providerError := strings.TrimSpace(c.Query("error")); providerError != "" {
		redirectToLoginError(c, "oauth_login_failed")
		return
	}

	state := strings.TrimSpace(c.Query("state"))
	code := strings.TrimSpace(c.Query("code"))
	expectedState, stateCookieErr := c.Cookie(adminLoginStateCookie)
	codeVerifier, verifierCookieErr := c.Cookie(adminCodeVerifierCookie)

	if state == "" || code == "" || stateCookieErr != nil || verifierCookieErr != nil || state != expectedState {
		redirectToLoginError(c, "oauth_state_mismatch")
		return
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", handler.clientID)
	form.Set("code", code)
	form.Set("redirect_uri", handler.redirectURL)
	form.Set("code_verifier", codeVerifier)

	if handler.usesPrivateKeyJWT() {
		signer, err := auth.NewPrivateKeyJWTSigner(handler.clientID, handler.tokenEndpoint, handler.privateKeyPath, handler.privateKeyID)
		if err != nil {
			logWithRequestID(c, "admin login signer init failed: endpoint=%s error=%v", handler.tokenEndpoint, err)
			redirectToLoginError(c, "oauth_callback_failed")
			return
		}

		clientAssertion, err := signer.SignAssertion(time.Now())
		if err != nil {
			logWithRequestID(c, "admin login signer assertion failed: endpoint=%s error=%v", handler.tokenEndpoint, err)
			redirectToLoginError(c, "oauth_callback_failed")
			return
		}

		form.Set("client_assertion_type", auth.ClientAssertionType())
		form.Set("client_assertion", clientAssertion)
	}

	resp, err := handler.exchangeCodeForToken(c.Request.Context(), form)
	if err != nil {
		logWithRequestID(c, "admin login token request failed: endpoint=%s error=%v", handler.tokenEndpoint, err)
		redirectToLoginError(c, "oauth_exchange_failed")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logWithRequestID(c, "admin login failed to read token response: status=%d error=%v", resp.StatusCode, err)
		redirectToLoginError(c, "oauth_exchange_failed")
		return
	}

	if resp.StatusCode >= http.StatusBadRequest {
		var oauthErr oauthErrorResponse
		if err := json.Unmarshal(body, &oauthErr); err == nil && (oauthErr.Error != "" || oauthErr.ErrorDescription != "") {
			logWithRequestID(c, "admin login rejected by oidc provider: status=%d error=%s description=%s", resp.StatusCode, oauthErr.Error, oauthErr.ErrorDescription)
		} else {
			logWithRequestID(c, "admin login rejected by oidc provider: status=%d body=%s", resp.StatusCode, sanitizeLogBody(string(body)))
		}

		redirectToLoginError(c, "oauth_exchange_failed")
		return
	}

	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		logWithRequestID(c, "admin login received invalid token response: status=%d error=%v body=%s", resp.StatusCode, err, sanitizeLogBody(string(body)))
		redirectToLoginError(c, "oauth_exchange_failed")
		return
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		logWithRequestID(c, "admin login token response missing access token: status=%d body=%s", resp.StatusCode, sanitizeLogBody(string(body)))
		redirectToLoginError(c, "oauth_exchange_failed")
		return
	}

	renderLoginSuccess(c, tokenResp.AccessToken)
}

func (handler *AdminLoginHandler) isConfigured() bool {
	return isAbsoluteURL(handler.authURL) &&
		isAbsoluteURL(handler.tokenEndpoint) &&
		isAbsoluteURL(handler.redirectURL) &&
		strings.TrimSpace(handler.clientID) != "" &&
		len(handler.scopes) > 0
}

func (handler *AdminLoginHandler) usesPrivateKeyJWT() bool {
	return strings.TrimSpace(handler.privateKeyPath) != ""
}

func (handler *AdminLoginHandler) exchangeCodeForToken(ctx context.Context, form url.Values) (*http.Response, error) {
	encodedForm := form.Encode()

	resp, err := handler.doTokenRequest(ctx, handler.httpClient, encodedForm)
	if err == nil || handler.publicClient == nil {
		return resp, err
	}

	fallbackResp, fallbackErr := handler.doTokenRequest(ctx, handler.publicClient, encodedForm)
	if fallbackErr == nil {
		return fallbackResp, nil
	}

	return nil, fmt.Errorf("primary request failed: %w; public fallback failed: %v", err, fallbackErr)
}

func (handler *AdminLoginHandler) doTokenRequest(ctx context.Context, client *http.Client, encodedForm string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, handler.tokenEndpoint, strings.NewReader(encodedForm))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return client.Do(request)
}

func setLoginCookie(c *gin.Context, name string, value string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     adminLoginCookiePath,
		HttpOnly: true,
		Secure:   requestUsesHTTPS(c),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
}

func clearLoginCookies(c *gin.Context) {
	for _, name := range []string{adminLoginStateCookie, adminCodeVerifierCookie} {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     adminLoginCookiePath,
			HttpOnly: true,
			Secure:   requestUsesHTTPS(c),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
}

func requestUsesHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}

	return strings.EqualFold(strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")), "https")
}

func codeChallenge(codeVerifier string) string {
	sum := sha256.Sum256([]byte(codeVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func redirectToLoginError(c *gin.Context, code string) {
	c.Redirect(http.StatusFound, adminLoginPagePath+"?error="+url.QueryEscape(code))
}

func isAbsoluteURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.IsAbs()
}

func renderLoginSuccess(c *gin.Context, accessToken string) {
	tokenJSON, err := json.Marshal(accessToken)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to finalize login")
		return
	}

	html := fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <meta http-equiv="refresh" content="0;url=%s" />
    <title>Admin Login</title>
  </head>
  <body>
    <script>
      sessionStorage.setItem(%q, %s);
      window.location.replace(%q);
    </script>
  </body>
</html>`, adminDashboardPath, adminSessionStorageToken, string(tokenJSON), adminDashboardPath)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

func sanitizeLogBody(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) <= 512 {
		return trimmed
	}

	return trimmed[:512] + "..."
}

func logWithRequestID(c *gin.Context, format string, args ...any) {
	requestID := strings.TrimSpace(c.GetString("request_id"))
	if requestID == "" {
		requestID = "missing"
	}

	log.Printf("request_id=%s %s", requestID, fmt.Sprintf(format, args...))
}
