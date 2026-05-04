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

func TestGitHubWebhookPushWithVersionFileTriggersRegionSync(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncer := &fakeWebhookSyncer{
		calls: make(chan string, 1),
	}
	handler := NewGitHubWebhookHandler(map[string]config.MasterDataSource{
		"jp": {Region: "jp", Owner: "Sekai-World", Repo: "sekai-master-data-jp", Ref: "main"},
	}, syncer, 5*time.Second, "top-secret")

	router := gin.New()
	router.POST("/api/v1/internal/github/webhooks/master-data", handler.MasterData)

	body := `{
		"ref":"refs/heads/main",
		"repository":{"name":"sekai-master-data-jp","full_name":"Sekai-World/sekai-master-data-jp","owner":{"login":"Sekai-World"}},
		"commits":[{"modified":["data/versions.json"]}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/github/webhooks/master-data", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signGitHubWebhookBody("top-secret", body))

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.Code)
	}

	select {
	case region := <-syncer.calls:
		if region != "jp" {
			t.Fatalf("expected region jp, got %s", region)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected region sync to be triggered")
	}

	if syncer.lastCtx == nil {
		t.Fatal("expected sync context to be set")
	}
}

func TestGitHubWebhookIgnoresPushWithoutVersionFile(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncer := &fakeWebhookSyncer{
		calls: make(chan string, 1),
	}
	handler := NewGitHubWebhookHandler(map[string]config.MasterDataSource{
		"jp": {Region: "jp", Owner: "Sekai-World", Repo: "sekai-master-data-jp", Ref: "main"},
	}, syncer, 0, "top-secret")

	router := gin.New()
	router.POST("/api/v1/internal/github/webhooks/master-data", handler.MasterData)

	body := `{
		"ref":"refs/heads/main",
		"repository":{"name":"sekai-master-data-jp","full_name":"Sekai-World/sekai-master-data-jp","owner":{"login":"Sekai-World"}},
		"commits":[{"modified":["data/cards.json"]}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/github/webhooks/master-data", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signGitHubWebhookBody("top-secret", body))

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.Code)
	}

	select {
	case region := <-syncer.calls:
		t.Fatalf("expected no sync call, got region %s", region)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGitHubWebhookIgnoresNonPushEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncer := &fakeWebhookSyncer{
		calls: make(chan string, 1),
	}
	handler := NewGitHubWebhookHandler(nil, syncer, 0, "top-secret")

	router := gin.New()
	router.POST("/api/v1/internal/github/webhooks/master-data", handler.MasterData)

	body := `{"zen":"keep it logically awesome"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/github/webhooks/master-data", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", signGitHubWebhookBody("top-secret", body))

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.Code)
	}

	select {
	case region := <-syncer.calls:
		t.Fatalf("expected no sync call, got region %s", region)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGitHubWebhookRejectsMissingSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncer := &fakeWebhookSyncer{
		calls: make(chan string, 1),
	}
	handler := NewGitHubWebhookHandler(map[string]config.MasterDataSource{
		"jp": {Region: "jp", Owner: "Sekai-World", Repo: "sekai-master-data-jp", Ref: "main"},
	}, syncer, 0, "")

	router := gin.New()
	router.POST("/api/v1/internal/github/webhooks/master-data", handler.MasterData)

	body := `{
		"ref":"refs/heads/main",
		"repository":{"name":"sekai-master-data-jp","full_name":"Sekai-World/sekai-master-data-jp","owner":{"login":"Sekai-World"}},
		"commits":[{"modified":["data/versions.json"]}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/github/webhooks/master-data", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.Code)
	}

	select {
	case region := <-syncer.calls:
		t.Fatalf("expected no sync call, got region %s", region)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestGitHubWebhookRejectsInvalidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncer := &fakeWebhookSyncer{
		calls: make(chan string, 1),
	}
	handler := NewGitHubWebhookHandler(map[string]config.MasterDataSource{
		"jp": {Region: "jp", Owner: "Sekai-World", Repo: "sekai-master-data-jp", Ref: "main"},
	}, syncer, 0, "top-secret")

	router := gin.New()
	router.POST("/api/v1/internal/github/webhooks/master-data", handler.MasterData)

	body := `{
		"ref":"refs/heads/main",
		"repository":{"name":"sekai-master-data-jp","full_name":"Sekai-World/sekai-master-data-jp","owner":{"login":"Sekai-World"}},
		"commits":[{"modified":["data/versions.json"]}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/github/webhooks/master-data", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.Code)
	}
}

func TestGitHubWebhookAcceptsValidSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	syncer := &fakeWebhookSyncer{
		calls: make(chan string, 1),
	}
	handler := NewGitHubWebhookHandler(map[string]config.MasterDataSource{
		"jp": {Region: "jp", Owner: "Sekai-World", Repo: "sekai-master-data-jp", Ref: "main"},
	}, syncer, 0, "top-secret")

	router := gin.New()
	router.POST("/api/v1/internal/github/webhooks/master-data", handler.MasterData)

	body := `{
		"ref":"refs/heads/main",
		"repository":{"name":"sekai-master-data-jp","full_name":"Sekai-World/sekai-master-data-jp","owner":{"login":"Sekai-World"}},
		"commits":[{"modified":["data/versions.json"]}]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/github/webhooks/master-data", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", signGitHubWebhookBody("top-secret", body))

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.Code)
	}

	select {
	case region := <-syncer.calls:
		if region != "jp" {
			t.Fatalf("expected region jp, got %s", region)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected region sync to be triggered")
	}
}

func signGitHubWebhookBody(secret string, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
