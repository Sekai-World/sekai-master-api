package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/config"
)

func TestAdminPagesDisableCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewAdminUIHandler(config.Config{AppEnv: "test"})
	router := gin.New()
	router.GET("/admin/login", handler.LoginPage)
	router.GET("/admin", handler.DashboardPage)
	router.GET("/admin/assets/*filepath", handler.Asset)

	testCases := []string{
		"/admin/login",
		"/admin",
		"/admin/assets/dashboard-page.js?v=20260410",
	}

	for _, path := range testCases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)

		if resp.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, resp.Code)
		}

		if cacheControl := resp.Header().Get("Cache-Control"); cacheControl != "no-store, no-cache, must-revalidate" {
			t.Fatalf("%s: Cache-Control = %q, want %q", path, cacheControl, "no-store, no-cache, must-revalidate")
		}
	}
}
