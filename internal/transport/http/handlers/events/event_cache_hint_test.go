package events

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
)

func captureHint(captured **cachehint.Hint) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, hint := cachehint.WithHint(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		*captured = hint
		c.Next()
	}
}

func TestEventListReportsWhenAHiddenEventIsRevealed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	startAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Millisecond)
	cache := &fakeEventHandlerCache{listByEntity: map[string]map[string][]map[string]any{"jp": {"events": {
		{"id": 1, "name": "past", "startAt": 1000, "closedAt": 2000},
		{"id": 2, "name": "upcoming", "startAt": startAt.UnixMilli(), "closedAt": startAt.Add(time.Hour).UnixMilli()},
	}}}}
	var hint *cachehint.Hint
	router := gin.New()
	router.GET("/api/v1/events/:region/list", captureHint(&hint), newReadyEventHandler(cache).List)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/events/jp/list", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	if until, ok := hint.Until(); !ok || !until.Equal(startAt) {
		t.Fatalf("reported %v %t, want the upcoming event's start %v", until, ok, startAt)
	}
}

func TestIsCurrentEventReportsWhenTheCurrentEventCanChange(t *testing.T) {
	now := time.Now().UTC()
	closedAt := now.Add(time.Hour).Truncate(time.Millisecond)
	current := map[string]any{"id": 101, "startAt": now.Add(-time.Hour).UnixMilli(), "closedAt": closedAt.UnixMilli()}
	cache := &fakeEventHandlerCache{
		byID:         map[string]map[string]map[string]map[string]any{"jp": {"events": {"101": current}}},
		listByEntity: map[string]map[string][]map[string]any{"jp": {"events": {current}}},
	}
	ctx, hint := cachehint.WithHint(context.Background())

	if !newReadyEventHandler(cache).isCurrentEvent(ctx, "jp", "101") {
		t.Fatal("expected event 101 to be current")
	}
	if until, ok := hint.Until(); !ok || !until.Equal(closedAt.Add(time.Millisecond)) {
		t.Fatalf("reported %v %t, want just after closedAt %v", until, ok, closedAt)
	}
}
