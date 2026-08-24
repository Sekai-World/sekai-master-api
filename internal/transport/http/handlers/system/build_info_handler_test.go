package system

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/version"
)

func TestBuildInfoReturnsBuildMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handler := NewBuildInfoHandler()
	router := gin.New()
	router.GET("/api/v1/build-info", handler.BuildInfo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/build-info", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["version"] != version.Version {
		t.Fatalf("expected version %q, got %q", version.Version, body["version"])
	}
	if body["commit"] != version.Commit {
		t.Fatalf("expected commit %q, got %q", version.Commit, body["commit"])
	}
	if body["buildDate"] != version.BuildDate {
		t.Fatalf("expected buildDate %q, got %q", version.BuildDate, body["buildDate"])
	}
}
