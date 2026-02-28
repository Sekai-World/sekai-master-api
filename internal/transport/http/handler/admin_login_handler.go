package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/transport/http/response"
)

type AdminLoginHandler struct {
	tokenEndpoint string
	clientID      string
	httpClient    *http.Client
}

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type keycloakTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

type keycloakErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func NewAdminLoginHandler(cfg config.Config) *AdminLoginHandler {
	baseURL := strings.TrimRight(cfg.KeycloakBaseURL, "/")
	realm := strings.TrimSpace(cfg.KeycloakRealm)

	tokenEndpoint := baseURL + "/realms/" + url.PathEscape(realm) + "/protocol/openid-connect/token"

	return &AdminLoginHandler{
		tokenEndpoint: tokenEndpoint,
		clientID:      cfg.KeycloakClientID,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (handler *AdminLoginHandler) Login(c *gin.Context) {
	var reqBody adminLoginRequest
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid login payload")
		return
	}

	username := strings.TrimSpace(reqBody.Username)
	password := strings.TrimSpace(reqBody.Password)
	if username == "" || password == "" {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "username and password are required")
		return
	}

	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", handler.clientID)
	form.Set("username", username)
	form.Set("password", password)

	request, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, handler.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to build login request")
		return
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := handler.httpClient.Do(request)
	if err != nil {
		logWithRequestID(c, "admin login keycloak request failed: endpoint=%s error=%v", handler.tokenEndpoint, err)
		response.Error(c, http.StatusBadGateway, "KEYCLOAK_UNAVAILABLE", "failed to connect keycloak")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logWithRequestID(c, "admin login failed to read keycloak response: status=%d error=%v", resp.StatusCode, err)
		response.Error(c, http.StatusBadGateway, "KEYCLOAK_RESPONSE_ERROR", "failed to read keycloak response")
		return
	}

	if resp.StatusCode >= http.StatusBadRequest {
		var keycloakErr keycloakErrorResponse
		if err := json.Unmarshal(body, &keycloakErr); err == nil && (keycloakErr.Error != "" || keycloakErr.ErrorDescription != "") {
			logWithRequestID(c, "admin login rejected by keycloak: status=%d error=%s description=%s", resp.StatusCode, keycloakErr.Error, keycloakErr.ErrorDescription)
		} else {
			logWithRequestID(c, "admin login rejected by keycloak: status=%d body=%s", resp.StatusCode, sanitizeLogBody(string(body)))
		}

		if resp.StatusCode >= http.StatusInternalServerError {
			response.Error(c, http.StatusBadGateway, "KEYCLOAK_UNAVAILABLE", "failed to connect keycloak")
			return
		}

		response.Error(c, http.StatusUnauthorized, "LOGIN_FAILED", "login failed")
		return
	}

	var keycloakResp keycloakTokenResponse
	if err := json.Unmarshal(body, &keycloakResp); err != nil {
		logWithRequestID(c, "admin login received invalid keycloak token response: status=%d error=%v body=%s", resp.StatusCode, err, sanitizeLogBody(string(body)))
		response.Error(c, http.StatusBadGateway, "KEYCLOAK_RESPONSE_ERROR", "invalid keycloak token response")
		return
	}
	if strings.TrimSpace(keycloakResp.AccessToken) == "" {
		logWithRequestID(c, "admin login keycloak response missing access token: status=%d body=%s", resp.StatusCode, sanitizeLogBody(string(body)))
		response.Error(c, http.StatusBadGateway, "KEYCLOAK_RESPONSE_ERROR", "keycloak token missing")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{
		"access_token": keycloakResp.AccessToken,
		"token_type":   keycloakResp.TokenType,
		"expires_in":   keycloakResp.ExpiresIn,
	})
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
