package responsecache

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/transport/http/cachehint"
)

type memoryStore struct {
	mu      sync.Mutex
	entries map[string][]byte
	ttls    map[string]time.Duration
	getErr  error
	setErr  error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{entries: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (store *memoryStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.getErr != nil {
		return nil, false, store.getErr
	}
	value, ok := store.entries[key]
	return value, ok, nil
}

func (store *memoryStore) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.setErr != nil {
		return store.setErr
	}
	store.entries[key] = value
	store.ttls[key] = ttl
	return nil
}

func (store *memoryStore) onlyTTL(t *testing.T) time.Duration {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.ttls) != 1 {
		t.Fatalf("expected one stored entry, got %d", len(store.ttls))
	}
	for _, ttl := range store.ttls {
		return ttl
	}
	return 0
}

type fakeRevisions struct {
	mu       sync.Mutex
	revision string
	ok       bool
	err      error
}

func (revisions *fakeRevisions) RegionCacheRevision(_ context.Context, _ string) (string, bool, error) {
	revisions.mu.Lock()
	defer revisions.mu.Unlock()
	return revisions.revision, revisions.ok, revisions.err
}

var testNow = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

// newTestRouter serves /api/v1/things/:region/list through the cache, and an
// uncached /api/v1/other/:region route. The handler's behavior comes from
// respond.
func newTestRouter(t *testing.T, store Store, revisions RevisionSource, respond func(c *gin.Context)) (*gin.Engine, *atomic.Int64) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cache, err := New(Options{
		Store:         store,
		Revisions:     revisions,
		Routes:        []string{"/api/v1/things/:region/list"},
		Build:         "test",
		TTL:           time.Hour,
		MaxEntryBytes: 64,
		Now:           func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int64{}
	router := gin.New()
	v1 := router.Group("/api/v1")
	v1.Use(cache.Middleware())
	handler := func(c *gin.Context) {
		calls.Add(1)
		respond(c)
	}
	v1.GET("/things/:region/list", handler)
	v1.GET("/other/:region", handler)
	return router, calls
}

func get(router http.Handler, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}

func okJSON(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"region": c.Param("region"), "page": c.Query("page")})
}

func TestCacheServesHitAfterMiss(t *testing.T) {
	store := newMemoryStore()
	router, calls := newTestRouter(t, store, &fakeRevisions{revision: "c1@t1", ok: true}, okJSON)

	first := get(router, "/api/v1/things/jp/list?page=2")
	second := get(router, "/api/v1/things/jp/list?page=2")

	if first.Header().Get(HeaderName) != "miss" || second.Header().Get(HeaderName) != "hit" {
		t.Fatalf("X-Cache = %q then %q, want miss then hit", first.Header().Get(HeaderName), second.Header().Get(HeaderName))
	}
	if first.Body.String() != second.Body.String() || first.Code != second.Code {
		t.Fatalf("hit %d %q differs from miss %d %q", second.Code, second.Body.String(), first.Code, first.Body.String())
	}
	if second.Header().Get("Content-Type") != first.Header().Get("Content-Type") {
		t.Fatalf("hit content type %q, miss %q", second.Header().Get("Content-Type"), first.Header().Get("Content-Type"))
	}
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", calls.Load())
	}
	if ttl := store.onlyTTL(t); ttl != time.Hour {
		t.Fatalf("ttl = %s, want the configured hour", ttl)
	}
}

func TestCacheKeysOnQueryRegionAndRevision(t *testing.T) {
	store := newMemoryStore()
	revisions := &fakeRevisions{revision: "c1@t1", ok: true}
	router, calls := newTestRouter(t, store, revisions, okJSON)

	get(router, "/api/v1/things/jp/list?a=1&b=2")
	if response := get(router, "/api/v1/things/jp/list?b=2&a=1"); response.Header().Get(HeaderName) != "hit" {
		t.Fatalf("reordered query: X-Cache = %q, want hit", response.Header().Get(HeaderName))
	}
	if response := get(router, "/api/v1/things/en/list?a=1&b=2"); response.Header().Get(HeaderName) != "miss" {
		t.Fatalf("other region: X-Cache = %q, want miss", response.Header().Get(HeaderName))
	}

	revisions.mu.Lock()
	revisions.revision = "c1@t2"
	revisions.mu.Unlock()
	if response := get(router, "/api/v1/things/jp/list?a=1&b=2"); response.Header().Get(HeaderName) != "miss" {
		t.Fatalf("new sync revision: X-Cache = %q, want miss", response.Header().Get(HeaderName))
	}
	if calls.Load() != 3 {
		t.Fatalf("handler ran %d times, want 3", calls.Load())
	}
}

func TestCacheBypassesWhileRegionIsNotSynced(t *testing.T) {
	store := newMemoryStore()
	router, calls := newTestRouter(t, store, &fakeRevisions{ok: false}, okJSON)

	for range 2 {
		if response := get(router, "/api/v1/things/jp/list"); response.Header().Get(HeaderName) != "bypass" || response.Code != http.StatusOK {
			t.Fatalf("X-Cache = %q code %d, want bypass 200", response.Header().Get(HeaderName), response.Code)
		}
	}
	if calls.Load() != 2 || len(store.entries) != 0 {
		t.Fatalf("handler ran %d times with %d entries stored, want 2 and 0", calls.Load(), len(store.entries))
	}
}

func TestCacheBypassesOnErrors(t *testing.T) {
	for name, setup := range map[string]func(*memoryStore, *fakeRevisions){
		"revision error":  func(_ *memoryStore, revisions *fakeRevisions) { revisions.err = errors.New("db down") },
		"redis get error": func(store *memoryStore, _ *fakeRevisions) { store.getErr = errors.New("redis down") },
	} {
		t.Run(name, func(t *testing.T) {
			store := newMemoryStore()
			revisions := &fakeRevisions{revision: "c1@t1", ok: true}
			setup(store, revisions)
			router, calls := newTestRouter(t, store, revisions, okJSON)

			response := get(router, "/api/v1/things/jp/list")
			if response.Header().Get(HeaderName) != "bypass" || response.Code != http.StatusOK || calls.Load() != 1 {
				t.Fatalf("X-Cache = %q code %d calls %d, want bypass 200 1", response.Header().Get(HeaderName), response.Code, calls.Load())
			}
		})
	}

	t.Run("redis set error", func(t *testing.T) {
		store := newMemoryStore()
		store.setErr = errors.New("redis down")
		router, _ := newTestRouter(t, store, &fakeRevisions{revision: "c1@t1", ok: true}, okJSON)
		if response := get(router, "/api/v1/things/jp/list"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"region":"jp"`) {
			t.Fatalf("store error changed the response: %d %q", response.Code, response.Body.String())
		}
	})
}

func TestCacheStoresOnlyCompleteOKResponses(t *testing.T) {
	for name, respond := range map[string]func(*gin.Context){
		"not ready":  func(c *gin.Context) { c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not ready"}) },
		"not found":  func(c *gin.Context) { c.JSON(http.StatusNotFound, gin.H{"error": "missing"}) },
		"over limit": func(c *gin.Context) { c.String(http.StatusOK, strings.Repeat("x", 65)) },
	} {
		t.Run(name, func(t *testing.T) {
			store := newMemoryStore()
			router, calls := newTestRouter(t, store, &fakeRevisions{revision: "c1@t1", ok: true}, respond)
			first := get(router, "/api/v1/things/jp/list")
			second := get(router, "/api/v1/things/jp/list")
			if second.Header().Get(HeaderName) != "miss" || calls.Load() != 2 || len(store.entries) != 0 {
				t.Fatalf("X-Cache %q, calls %d, entries %d: want miss, 2, 0", second.Header().Get(HeaderName), calls.Load(), len(store.entries))
			}
			if first.Body.String() != second.Body.String() {
				t.Fatalf("responses differ: %q vs %q", first.Body.String(), second.Body.String())
			}
		})
	}
}

func TestCacheBoundsTTLByReportedValidity(t *testing.T) {
	store := newMemoryStore()
	router, _ := newTestRouter(t, store, &fakeRevisions{revision: "c1@t1", ok: true}, func(c *gin.Context) {
		cachehint.ValidUntil(c.Request.Context(), testNow.Add(10*time.Minute))
		cachehint.ValidUntil(c.Request.Context(), testNow.Add(20*time.Minute))
		okJSON(c)
	})

	get(router, "/api/v1/things/jp/list")
	if ttl := store.onlyTTL(t); ttl != 10*time.Minute {
		t.Fatalf("ttl = %s, want the earliest reported 10m", ttl)
	}
}

func TestCacheSkipsResponsesAboutToChange(t *testing.T) {
	store := newMemoryStore()
	router, calls := newTestRouter(t, store, &fakeRevisions{revision: "c1@t1", ok: true}, func(c *gin.Context) {
		cachehint.ValidUntil(c.Request.Context(), testNow.Add(200*time.Millisecond))
		okJSON(c)
	})

	get(router, "/api/v1/things/jp/list")
	get(router, "/api/v1/things/jp/list")
	if calls.Load() != 2 || len(store.entries) != 0 {
		t.Fatalf("calls %d, entries %d: want 2 and 0", calls.Load(), len(store.entries))
	}
}

func TestCacheLeavesOtherRoutesAlone(t *testing.T) {
	store := newMemoryStore()
	router, calls := newTestRouter(t, store, &fakeRevisions{revision: "c1@t1", ok: true}, okJSON)

	for range 2 {
		if response := get(router, "/api/v1/other/jp"); response.Header().Get(HeaderName) != "" {
			t.Fatalf("uncached route got X-Cache %q", response.Header().Get(HeaderName))
		}
	}
	if calls.Load() != 2 || len(store.entries) != 0 {
		t.Fatalf("calls %d, entries %d: want 2 and 0", calls.Load(), len(store.entries))
	}
}

func TestCacheRunsOneHandlerForConcurrentMisses(t *testing.T) {
	store := newMemoryStore()
	release := make(chan struct{})
	entered := make(chan struct{}, 8)
	router, calls := newTestRouter(t, store, &fakeRevisions{revision: "c1@t1", ok: true}, func(c *gin.Context) {
		entered <- struct{}{}
		<-release
		okJSON(c)
	})

	var wg sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 5)
	wg.Add(1)
	go func() {
		defer wg.Done()
		responses[0] = get(router, "/api/v1/things/jp/list")
	}()
	<-entered
	for index := 1; index < len(responses); index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses[index] = get(router, "/api/v1/things/jp/list")
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", calls.Load())
	}
	for index, response := range responses {
		if response.Code != http.StatusOK || response.Body.String() != responses[0].Body.String() {
			t.Fatalf("response %d = %d %q, want %q", index, response.Code, response.Body.String(), responses[0].Body.String())
		}
	}
}

func TestEntryRoundTrip(t *testing.T) {
	for _, body := range []string{"", `{"a":1}`, strings.Repeat(`{"large":true}`, 500)} {
		value := entry{status: http.StatusOK, contentType: "application/json; charset=utf-8", body: []byte(body)}
		decoded, err := decodeEntry(encodeEntry(value))
		if err != nil || decoded.status != value.status || decoded.contentType != value.contentType || string(decoded.body) != body {
			t.Fatalf("round trip of %d bytes: %+v, %v", len(body), decoded, err)
		}
	}
	if len(encodeEntry(entry{status: 200, body: []byte(strings.Repeat("a", 4096))})) >= 4096 {
		t.Fatal("expected a large body to be compressed")
	}
	if _, err := decodeEntry([]byte{9, 0, 0}); err == nil {
		t.Fatal("expected a malformed entry to fail")
	}
}
