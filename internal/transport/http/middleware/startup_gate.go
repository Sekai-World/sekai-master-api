package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/startup"
	"sekai-master-api/internal/transport/http/response"
)

func StartupGate(state *startup.State) gin.HandlerFunc {
	return func(c *gin.Context) {
		if state == nil || state.Ready() || startupRouteAllowed(c.Request) {
			c.Next()
			return
		}

		response.Error(c, http.StatusServiceUnavailable, "STARTUP_IN_PROGRESS", "service startup is still in progress")
		c.Abort()
	}
}

func startupRouteAllowed(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}

	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false
	}

	path := strings.TrimSpace(request.URL.Path)
	if path == "" {
		return false
	}

	switch path {
	case "/api/v1/health", "/admin", "/admin/login", "/api/v1/admin/login", "/api/v1/admin/login/callback", "/api/v1/admin/profile", "/api/v1/admin/master-data/status":
		return true
	}

	return strings.HasPrefix(path, "/admin/assets/")
}
