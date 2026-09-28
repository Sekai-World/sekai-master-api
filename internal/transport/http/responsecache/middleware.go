package responsecache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	apimetric "go.opentelemetry.io/otel/metric"
	"golang.org/x/sync/singleflight"

	"sekai-master-api/internal/logging"
	"sekai-master-api/internal/transport/http/cachehint"
)

// HeaderName tells clients how the cache answered: hit, miss, or bypass.
const HeaderName = "X-Cache"

const keyPrefix = "sekai-master-api:cache:v1:"

// minTTL is the shortest lifetime worth storing; a response that changes
// sooner is served uncached.
const minTTL = time.Second

// RevisionSource identifies a region's stored data. It reports false while
// the region may be changing or unavailable.
type RevisionSource interface {
	RegionCacheRevision(ctx context.Context, region string) (string, bool, error)
}

// Options configures a Cache.
type Options struct {
	Store     Store
	Revisions RevisionSource
	// Routes are the gin route patterns (FullPath) that are cached. Each must
	// have a :region parameter, and its response must depend only on its
	// path, query, the region's stored data, the build, and time reported
	// through cachehint.
	Routes []string
	// Build identifies the binary, so a deploy never serves another build's
	// responses.
	Build         string
	TTL           time.Duration
	MaxEntryBytes int
	// Now defaults to time.Now.
	Now func() time.Time
}

// Cache is the response cache middleware.
type Cache struct {
	options  Options
	routes   map[string]struct{}
	flight   singleflight.Group
	requests apimetric.Int64Counter
}

// New returns a Cache for options.
func New(options Options) (*Cache, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.MaxEntryBytes <= 0 {
		options.MaxEntryBytes = 1 << 20
	}
	requests, err := otel.Meter("sekai-master-api/http").Int64Counter(
		"sekai_http_cache_requests_total",
		apimetric.WithDescription("Response cache lookups by route and result (hit, miss, bypass, error)."),
	)
	if err != nil {
		return nil, err
	}
	routes := make(map[string]struct{}, len(options.Routes))
	for _, route := range options.Routes {
		routes[route] = struct{}{}
	}
	return &Cache{options: options, routes: routes, requests: requests}, nil
}

// Middleware caches the allowlisted routes and passes every other request on.
func (cache *Cache) Middleware() gin.HandlerFunc {
	return cache.handle
}

func (cache *Cache) handle(c *gin.Context) {
	route := c.FullPath()
	if _, allowed := cache.routes[route]; !allowed || c.Request.Method != http.MethodGet {
		c.Next()
		return
	}

	ctx := c.Request.Context()
	region := strings.ToLower(strings.TrimSpace(c.Param("region")))
	revision, ok, err := cache.options.Revisions.RegionCacheRevision(ctx, region)
	if err != nil || !ok {
		cache.bypass(c, route, err)
		return
	}

	key := cache.key(route, region, revision, c.Request.URL)
	raw, found, err := cache.options.Store.Get(ctx, key)
	if err != nil {
		cache.bypass(c, route, err)
		return
	}
	if found {
		if value, decodeErr := decodeEntry(raw); decodeErr == nil {
			cache.count(ctx, route, "hit")
			writeEntry(c, value, "hit")
			return
		}
	}

	// Concurrent misses for one key share a single handler run: the first
	// request runs the handler, the others replay its response.
	leader := false
	shared, _, _ := cache.flight.Do(key, func() (any, error) {
		leader = true
		return cache.fill(c, route, key), nil
	})
	if leader {
		return
	}
	cache.count(ctx, route, "miss")
	if value, ok := shared.(*entry); ok && value != nil {
		writeEntry(c, *value, "miss")
		return
	}
	c.Header(HeaderName, "miss")
	c.Next()
}

// fill runs the handler, stores a cacheable response, and returns it for
// requests waiting on the same key (nil when it is not cacheable).
func (cache *Cache) fill(c *gin.Context, route, key string) *entry {
	hintCtx, hint := cachehint.WithHint(c.Request.Context())
	c.Request = c.Request.WithContext(hintCtx)
	recorder := &recorder{ResponseWriter: c.Writer, limit: cache.options.MaxEntryBytes}
	c.Writer = recorder
	c.Header(HeaderName, "miss")

	c.Next()

	cache.count(hintCtx, route, "miss")
	if recorder.Status() != http.StatusOK || recorder.overflow {
		return nil
	}
	value := &entry{
		status:      http.StatusOK,
		contentType: recorder.Header().Get("Content-Type"),
		body:        bytes.Clone(recorder.body.Bytes()),
	}

	ttl := cache.options.TTL
	if until, ok := hint.Until(); ok {
		if remaining := until.Sub(cache.options.Now()); remaining < ttl {
			ttl = remaining
		}
	}
	if ttl < minTTL {
		return value
	}
	// The response is already sent; storing it must not depend on the client
	// staying connected.
	if err := cache.options.Store.Set(context.WithoutCancel(hintCtx), key, encodeEntry(*value), ttl); err != nil {
		cache.count(hintCtx, route, "error")
		logging.FromContext(hintCtx).Warnw("response cache store failed", "component", "response-cache", "route", route, "error", err)
	}
	return value
}

func (cache *Cache) bypass(c *gin.Context, route string, err error) {
	result := "bypass"
	if err != nil {
		result = "error"
		logging.FromContext(c.Request.Context()).Warnw("response cache bypassed", "component", "response-cache", "route", route, "error", err)
	}
	cache.count(c.Request.Context(), route, result)
	c.Header(HeaderName, "bypass")
	c.Next()
}

func (cache *Cache) count(ctx context.Context, route, result string) {
	cache.requests.Add(ctx, 1, apimetric.WithAttributes(
		attribute.String("route", route),
		attribute.String("result", result),
	))
}

// key hashes everything a cached response depends on.
func (cache *Cache) key(route, region, revision string, requestURL *url.URL) string {
	hash := sha256.New()
	for _, part := range []string{cache.options.Build, region, revision, route, requestURL.Path, normalizedQuery(requestURL.Query())} {
		hash.Write([]byte(part))
		hash.Write([]byte{0})
	}
	return keyPrefix + hex.EncodeToString(hash.Sum(nil))
}

// normalizedQuery sorts the keys and keeps each key's values in order.
func normalizedQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		for _, value := range values[key] {
			parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
		}
	}
	return strings.Join(parts, "&")
}

func writeEntry(c *gin.Context, value entry, result string) {
	c.Header(HeaderName, result)
	c.Data(value.status, value.contentType, value.body)
	c.Abort()
}

// recorder copies the response body, up to limit bytes, while writing it.
type recorder struct {
	gin.ResponseWriter
	body     bytes.Buffer
	limit    int
	overflow bool
}

func (writer *recorder) Write(data []byte) (int, error) {
	writer.capture(len(data), func() { writer.body.Write(data) })
	return writer.ResponseWriter.Write(data)
}

func (writer *recorder) WriteString(data string) (int, error) {
	writer.capture(len(data), func() { writer.body.WriteString(data) })
	return writer.ResponseWriter.WriteString(data)
}

func (writer *recorder) capture(size int, write func()) {
	if writer.overflow {
		return
	}
	if writer.body.Len()+size > writer.limit {
		writer.overflow = true
		writer.body.Reset()
		return
	}
	write()
}
