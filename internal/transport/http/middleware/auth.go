package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/auth"
	"sekai-master-api/internal/transport/http/response"
)

func Auth(tokenVerifier auth.TokenVerifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			response.Error(c, http.StatusUnauthorized, "UNAUTHORIZED", "missing authorization header")
			c.Abort()
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			response.Error(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authorization header")
			c.Abort()
			return
		}

		claims, err := tokenVerifier.Verify(c.Request.Context(), parts[1])
		if err != nil {
			response.Error(c, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
			c.Abort()
			return
		}

		c.Set("claims", claims)
		c.Next()
	}
}
