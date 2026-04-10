package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/transport/http/response"
)

func Auth(tokenVerifier auth.TokenVerifier) gin.HandlerFunc {
	return AuthWithAuthorizer(tokenVerifier, nil)
}

func AuthWithAuthorizer(tokenVerifier auth.TokenVerifier, authorizer *auth.AdminClaimAuthorizer) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawToken, errMessage := bearerToken(c)
		if rawToken == "" {
			response.Error(c, http.StatusUnauthorized, "UNAUTHORIZED", errMessage)
			c.Abort()
			return
		}

		claims, err := tokenVerifier.Verify(c.Request.Context(), rawToken)
		if err != nil {
			response.Error(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
			c.Abort()
			return
		}

		if authorizer != nil {
			if err := authorizer.Authorize(claims); err != nil {
				response.Error(c, http.StatusForbidden, "ADMIN_ACCESS_DENIED", "authenticated user is not allowed to access admin endpoints")
				c.Abort()
				return
			}
		}

		c.Set("claims", claims)
		c.Next()
	}
}

func bearerToken(c *gin.Context) (string, string) {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if header != "" {
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return "", "invalid authorization header"
		}

		rawToken := strings.TrimSpace(parts[1])
		if rawToken == "" {
			return "", "invalid authorization header"
		}

		return rawToken, ""
	}

	if c.Request != nil && strings.EqualFold(c.Request.Method, http.MethodGet) {
		rawToken := strings.TrimSpace(c.Query("access_token"))
		if rawToken != "" {
			return rawToken, ""
		}
	}

	return "", "missing authorization header"
}
