package virtuallives

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
)

func TestVirtualLiveListReportsWhenAHiddenLiveIsRevealed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	startAt := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Millisecond)
	cache := &fakeVirtualLiveHandlerCache{
		listItems: []map[string]any{
			{"id": 1, "name": "past", "startAt": 1000, "endAt": 2000},
			{"id": 2, "name": "upcoming", "startAt": startAt.UnixMilli(), "endAt": startAt.Add(time.Hour).UnixMilli()},
		},
		listTotal: 2,
	}
	var hint *cachehint.Hint
	router := gin.New()
	router.GET("/api/v1/virtualLives/:region/list", func(c *gin.Context) {
		ctx, captured := cachehint.WithHint(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		hint = captured
		c.Next()
	}, newReadyVirtualLiveHandler(cache).List)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/virtualLives/jp/list", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if until, ok := hint.Until(); !ok || !until.Equal(startAt) {
		t.Fatalf("reported %v %t, want the upcoming live's start %v", until, ok, startAt)
	}
}
