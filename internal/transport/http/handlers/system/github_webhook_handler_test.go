package system

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/config"
)

type fakeWebhookSyncer struct {
	calls   chan string
	regions []string
	lastCtx context.Context
}

func (syncer *fakeWebhookSyncer) SyncRegion(ctx context.Context, region string) error {
	syncer.lastCtx = ctx
	syncer.regions = append(syncer.regions, region)
	if syncer.calls != nil {
		syncer.calls <- region
	}
	return nil
}

const webhookTestSecret = "top-secret"

// Shared webhook push payloads so the repeated request bodies are defined once
// instead of copied into every test.
const (
	webhookPushBodyWithVersionFile = `{
	"ref":"refs/heads/main",
	"repository":{"name":"sekai-master-data-jp","full_name":"Sekai-World/sekai-master-data-jp","owner":{"login":"Sekai-World"}},
	"commits":[{"modified":["data/versions.json"]}]
}`
	webhookPushBodyWithCardsFile = `{
	"ref":"refs/heads/main",
	"repository":{"name":"sekai-master-data-jp","full_name":"Sekai-World/sekai-master-data-jp","owner":{"login":"Sekai-World"}},
	"commits":[{"modified":["data/cards.json"]}]
}`
	webhookPingBody = `{"zen":"keep it logically awesome"}`
)

// newTestWebhookHandler builds the jp datasource handler used by most tests,
// collapsing the repeated constructor arguments into one place.
func newTestWebhookHandler(t *testing.T, syncer masterDataRegionSyncer, timeout time.Duration, secret string) *GitHubWebhookHandler {
	t.Helper()
	return NewGitHubWebhookHandler(map[string]config.MasterDataSource{
		"jp": {Region: "jp", Owner: "Sekai-World", Repo: "sekai-master-data-jp", Ref: "main"},
	}, syncer, timeout, secret)
}

// serveWebhook performs a signed (or unsigned, when signature is empty) POST to
// the webhook route and returns the recorder, removing the per-test request
// plumbing.
func serveWebhook(t *testing.T, router *gin.Engine, event, body, signature string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/github/webhooks/master-data", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func assertWebhookStatus(t *testing.T, resp *httptest.ResponseRecorder, want int) {
	t.Helper()
	if resp.Code != want {
		t.Fatalf("expected status %d, got %d", want, resp.Code)
	}
}

func assertNoWebhookSyncCall(t *testing.T, syncer *fakeWebhookSyncer) {
	t.Helper()
	select {
	case region := <-syncer.calls:
		t.Fatalf("expected no sync call, got region %s", region)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertWebhookSyncedRegion(t *testing.T, syncer *fakeWebhookSyncer, want string) {
	t.Helper()
	select {
	case region := <-syncer.calls:
		if region != want {
			t.Fatalf("expected region %s, got %s", want, region)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected region sync to be triggered")
	}
	if syncer.lastCtx == nil {
		t.Fatal("expected sync context to be set")
	}
}

func TestGitHubWebhookPushWithVersionFileTriggersRegionSync(t *testing.T) {
	syncer := &fakeWebhookSyncer{calls: make(chan string, 1)}
	handler := newTestWebhookHandler(t, syncer, 5*time.Second, webhookTestSecret)
	router := newWebhookTestRouter(handler, context.Background())

	resp := serveWebhook(t, router, "push", webhookPushBodyWithVersionFile, signGitHubWebhookBody(webhookTestSecret, webhookPushBodyWithVersionFile))
	assertWebhookStatus(t, resp, http.StatusAccepted)
	assertWebhookSyncedRegion(t, syncer, "jp")
}

func TestGitHubWebhookIgnoresPushWithoutVersionFile(t *testing.T) {
	syncer := &fakeWebhookSyncer{calls: make(chan string, 1)}
	handler := newTestWebhookHandler(t, syncer, 0, webhookTestSecret)
	router := newWebhookTestRouter(handler, context.Background())

	resp := serveWebhook(t, router, "push", webhookPushBodyWithCardsFile, signGitHubWebhookBody(webhookTestSecret, webhookPushBodyWithCardsFile))
	assertWebhookStatus(t, resp, http.StatusAccepted)
	assertNoWebhookSyncCall(t, syncer)
}

func TestGitHubWebhookIgnoresNonPushEvent(t *testing.T) {
	syncer := &fakeWebhookSyncer{calls: make(chan string, 1)}
	handler := NewGitHubWebhookHandler(nil, syncer, 0, webhookTestSecret)
	router := newWebhookTestRouter(handler, context.Background())

	resp := serveWebhook(t, router, "ping", webhookPingBody, signGitHubWebhookBody(webhookTestSecret, webhookPingBody))
	assertWebhookStatus(t, resp, http.StatusAccepted)
	assertNoWebhookSyncCall(t, syncer)
}

func TestGitHubWebhookRejectsMissingSecret(t *testing.T) {
	syncer := &fakeWebhookSyncer{calls: make(chan string, 1)}
	handler := newTestWebhookHandler(t, syncer, 0, "")
	router := newWebhookTestRouter(handler, context.Background())

	resp := serveWebhook(t, router, "push", webhookPushBodyWithVersionFile, "")
	assertWebhookStatus(t, resp, http.StatusServiceUnavailable)
	assertNoWebhookSyncCall(t, syncer)
}

func TestGitHubWebhookRejectsInvalidSignature(t *testing.T) {
	syncer := &fakeWebhookSyncer{calls: make(chan string, 1)}
	handler := newTestWebhookHandler(t, syncer, 0, webhookTestSecret)
	router := newWebhookTestRouter(handler, context.Background())

	resp := serveWebhook(t, router, "push", webhookPushBodyWithVersionFile, "sha256=invalid")
	assertWebhookStatus(t, resp, http.StatusUnauthorized)
}

func TestGitHubWebhookAcceptsValidSignature(t *testing.T) {
	syncer := &fakeWebhookSyncer{calls: make(chan string, 1)}
	handler := newTestWebhookHandler(t, syncer, 0, webhookTestSecret)
	router := newWebhookTestRouter(handler, context.Background())

	resp := serveWebhook(t, router, "push", webhookPushBodyWithVersionFile, signGitHubWebhookBody(webhookTestSecret, webhookPushBodyWithVersionFile))
	assertWebhookStatus(t, resp, http.StatusAccepted)
	assertWebhookSyncedRegion(t, syncer, "jp")
}

func signGitHubWebhookBody(secret string, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// newWebhookTestRouter wires the webhook handler to its route, injecting the
// lifecycle context the way the production router does, so tests share the same
// HTTP setup without repeating it.
func newWebhookTestRouter(handler *GitHubWebhookHandler, lifecycleCtx context.Context) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/internal/github/webhooks/master-data", func(c *gin.Context) {
		handler.MasterData(c, lifecycleCtx)
	})
	return router
}
