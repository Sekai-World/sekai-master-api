package handler

import (
	"encoding/json"
	"io"
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
		response.Error(c, http.StatusBadGateway, "KEYCLOAK_UNAVAILABLE", "failed to connect keycloak")
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		response.Error(c, http.StatusBadGateway, "KEYCLOAK_RESPONSE_ERROR", "failed to read keycloak response")
		return
	}

	if resp.StatusCode >= http.StatusBadRequest {
		response.Error(c, http.StatusUnauthorized, "LOGIN_FAILED", "invalid username or password")
		return
	}

	var keycloakResp keycloakTokenResponse
	if err := json.Unmarshal(body, &keycloakResp); err != nil {
		response.Error(c, http.StatusBadGateway, "KEYCLOAK_RESPONSE_ERROR", "invalid keycloak token response")
		return
	}
	if strings.TrimSpace(keycloakResp.AccessToken) == "" {
		response.Error(c, http.StatusBadGateway, "KEYCLOAK_RESPONSE_ERROR", "keycloak token missing")
		return
	}

	response.JSON(c, http.StatusOK, gin.H{
		"access_token": keycloakResp.AccessToken,
		"token_type":   keycloakResp.TokenType,
		"expires_in":   keycloakResp.ExpiresIn,
	})
}
