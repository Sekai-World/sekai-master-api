package lookups

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
)

func TestLookupListReportsWhenAHiddenRecordIsRevealed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	publishedAt := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Millisecond)
	cache := &fakeLookupCache{listByEntity: map[string]map[string][]map[string]any{"jp": {"cardepisodes": {
		{"id": 1, "cardId": 10, "title": "past"},
		{"id": 2, "cardId": 11, "title": "upcoming", "publishedAt": publishedAt.UnixMilli()},
	}}}}
	var hint *cachehint.Hint
	router := gin.New()
	router.GET("/api/v1/cardEpisodes/:region/list", func(c *gin.Context) {
		ctx, captured := cachehint.WithHint(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		hint = captured
		c.Next()
	}, newReadyLookupHandler(cache).CardEpisodesList)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/cardEpisodes/jp/list", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if until, ok := hint.Until(); !ok || !until.Equal(publishedAt) {
		t.Fatalf("reported %v %t, want the hidden episode's publish time %v", until, ok, publishedAt)
	}
}
