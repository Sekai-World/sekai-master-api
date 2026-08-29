package system

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/logging"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type masterDataRegionSyncer interface {
	SyncRegion(ctx context.Context, region string) error
}

type GitHubWebhookHandler struct {
	sources      map[string]config.MasterDataSource
	syncer       masterDataRegionSyncer
	syncTimeout  time.Duration
	secret       string
	enabled      bool
	lifecycleCtx context.Context

	// admissionMu guards the rejecting flag together with the inflight WaitGroup
	// Add so that RejectNewSubmissions cannot be observed between the admission
	// check and the WaitGroup Add. Holding it makes the two operations atomic:
	// either a submission is admitted (and counted) before shutdown begins, or
	// shutdown sets rejecting first and the submission is refused. It also lets
	// WaitForInflight ensure no in-progress admission is still pending before it
	// starts waiting, preventing a WaitGroup Add/Wait race when the counter is
	// zero.
	admissionMu sync.Mutex
	// rejecting is set when the application begins graceful shutdown; new webhook
	// submissions are then refused so no sync is started after teardown begins.
	rejecting bool
	// inflight tracks in-flight webhook-triggered sync goroutines so shutdown can
	// wait for them before tearing down dependencies. The Add happens under
	// admissionMu, before the goroutine is spawned, to avoid a WaitGroup Add/Wait
	// race.
	inflight sync.WaitGroup
}

type gitHubPushWebhookPayload struct {
	Ref        string                `json:"ref"`
	Repository gitHubWebhookRepo     `json:"repository"`
	Commits    []gitHubWebhookCommit `json:"commits"`
	HeadCommit *gitHubWebhookCommit  `json:"head_commit"`
}

type gitHubWebhookRepo struct {
	Name     string                 `json:"name"`
	FullName string                 `json:"full_name"`
	Owner    gitHubWebhookRepoOwner `json:"owner"`
}

type gitHubWebhookRepoOwner struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

type gitHubWebhookCommit struct {
	Added    []string `json:"added"`
	Modified []string `json:"modified"`
	Removed  []string `json:"removed"`
}

func NewGitHubWebhookHandler(
	sources map[string]config.MasterDataSource,
	syncer masterDataRegionSyncer,
	syncTimeout time.Duration,
	secret string,
	lifecycleContexts ...context.Context,
) *GitHubWebhookHandler {
	var lifecycleCtx context.Context
	if len(lifecycleContexts) > 0 {
		lifecycleCtx = lifecycleContexts[0]
	}
	copiedSources := make(map[string]config.MasterDataSource, len(sources))
	for region, source := range sources {
		copiedSources[strings.ToLower(strings.TrimSpace(region))] = source
	}

	return &GitHubWebhookHandler{
		sources:      copiedSources,
		syncer:       syncer,
		syncTimeout:  syncTimeout,
		secret:       strings.TrimSpace(secret),
		enabled:      !isNilMasterDataSyncer(syncer),
		lifecycleCtx: lifecycleCtx,
	}
}

// RejectNewSubmissions closes the admission gate so further webhook requests are
// refused during graceful shutdown. It is idempotent.
func (handler *GitHubWebhookHandler) RejectNewSubmissions() {
	if handler == nil {
		return
	}
	handler.admissionMu.Lock()
	defer handler.admissionMu.Unlock()
	handler.rejecting = true
}

// WaitForInflight blocks until all webhook-triggered sync goroutines have stopped
// or the provided context is done (shutdown deadline). It returns a non-nil error
// if the wait times out so callers can surface a shutdown error.
func (handler *GitHubWebhookHandler) WaitForInflight(ctx context.Context) error {
	if handler == nil {
		return nil
	}
	// Acquire and release the admission gate so any in-progress admission (between
	// the rejecting check and the WaitGroup Add) completes before we start waiting.
	// Without this, Wait could observe a zero counter and then race with a
	// subsequent inflight.Add.
	handler.admissionMu.Lock()
	handler.admissionMu.Unlock()
	done := make(chan struct{})
	go func() {
		handler.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("github webhook inflight sync did not finish: %w", ctx.Err())
	}
}

// admit records a new in-flight submission under the admission gate. It returns
// false (and does not increment inflight) when graceful shutdown has begun. The
// rejecting check and the WaitGroup Add run atomically under admissionMu so a
// concurrent RejectNewSubmissions cannot be observed between them.
func (handler *GitHubWebhookHandler) admit() bool {
	if handler == nil {
		return false
	}
	handler.admissionMu.Lock()
	defer handler.admissionMu.Unlock()
	if handler.rejecting {
		return false
	}
	handler.inflight.Add(1)
	return true
}

// MasterData godoc
// @Summary Receive GitHub master-data webhook
// @Tags system
// @Accept json
// @Produce json
// @Param X-GitHub-Event header string true "GitHub event type"
// @Param X-Hub-Signature-256 header string false "GitHub HMAC SHA-256 signature"
// @Param payload body map[string]interface{} true "GitHub webhook payload"
// @Success 202 {object} shared.GitHubWebhookResponse
// @Failure 400 {object} shared.ErrorResponse
// @Failure 401 {object} shared.ErrorResponse
// @Failure 503 {object} shared.ErrorResponse
// @Router /internal/github/webhooks/master-data [post]
func (handler *GitHubWebhookHandler) MasterData(c *gin.Context) {
	if handler == nil || !handler.enabled {
		response.Error(c, http.StatusServiceUnavailable, "MASTER_DATA_SYNC_DISABLED", "master data sync is not configured")
		return
	}

	if handler.secret == "" {
		response.Error(c, http.StatusServiceUnavailable, "GITHUB_WEBHOOK_DISABLED", "github webhook secret is not configured")
		return
	}

	body, err := c.GetRawData()
	if err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "failed to read webhook payload")
		return
	}

	if !handler.verifySignature(body, c.GetHeader("X-Hub-Signature-256")) {
		response.Error(c, http.StatusUnauthorized, "INVALID_WEBHOOK_SIGNATURE", "invalid github webhook signature")
		return
	}

	eventType := strings.ToLower(strings.TrimSpace(c.GetHeader("X-GitHub-Event")))
	if eventType != "push" {
		response.JSON(c, http.StatusAccepted, gin.H{
			"status": "ignored",
			"reason": "unsupported_event",
		})
		return
	}

	payload := gitHubPushWebhookPayload{}
	if err := json.Unmarshal(body, &payload); err != nil {
		response.Error(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid github webhook payload")
		return
	}

	owner := payload.Repository.OwnerName()
	repo := strings.TrimSpace(payload.Repository.Name)
	ref := normalizeGitHubRef(payload.Ref)
	region, matched := handler.matchRegion(owner, repo, ref)
	if !matched {
		response.JSON(c, http.StatusAccepted, gin.H{
			"status": "ignored",
			"reason": "region_not_matched",
		})
		return
	}

	if !payload.hasVersionFileChange() {
		response.JSON(c, http.StatusAccepted, gin.H{
			"status": "ignored",
			"reason": "version_file_not_changed",
			"region": region,
		})
		return
	}

	// Admission gate: once graceful shutdown has begun, refuse new submissions so
	// no sync is spawned after dependency teardown starts. The rejecting check and
	// the WaitGroup Add are performed atomically under admissionMu so a concurrent
	// RejectNewSubmissions cannot slip between them.
	if !handler.admit() {
		response.Error(c, http.StatusServiceUnavailable, "SHUTTING_DOWN", "master data sync is shutting down")
		return
	}

	go func() {
		defer handler.inflight.Done()
		handler.triggerRegionSync(logging.DetachedTraceContext(c.Request.Context()), region, owner, repo, ref)
	}()

	response.JSON(c, http.StatusAccepted, gin.H{
		"status": "accepted",
		"region": region,
	})
}

func (handler *GitHubWebhookHandler) triggerRegionSync(ctx context.Context, region string, owner string, repo string, ref string) {
	baseCtx := ctx
	if handler.syncTimeout > 0 {
		var stopTimeout context.CancelFunc
		baseCtx, stopTimeout = context.WithTimeout(baseCtx, handler.syncTimeout)
		defer stopTimeout()
	}

	// Also stop the sync when the application lifecycle ends (graceful
	// shutdown), so a webhook-triggered sync is bounded like every other
	// background worker.
	lifecycleCtx := handler.lifecycleCtx
	if lifecycleCtx != nil {
		derivedCtx, stopLifecycle := context.WithCancel(baseCtx)
		stop := make(chan struct{})
		go func() {
			select {
			case <-lifecycleCtx.Done():
				stopLifecycle()
			case <-stop:
			}
		}()
		defer func() {
			close(stop)
			stopLifecycle()
		}()
		baseCtx = derivedCtx
	}

	logger := logging.FromContext(baseCtx)
	logger.Infow("github webhook triggered master data sync", "region", region, "owner", owner, "repo", repo, "ref", ref)

	if err := handler.syncer.SyncRegion(baseCtx, region); err != nil {
		if err == usecase.ErrSyncInProgress {
			logger.Infow("github webhook skipped because master data sync already running", "region", region)
			return
		}

		logger.Warnw("github webhook master data sync failed", "region", region, "error", err)
		return
	}

	logger.Infow("github webhook master data sync completed", "region", region)
}

func (handler *GitHubWebhookHandler) matchRegion(owner string, repo string, ref string) (string, bool) {
	normalizedOwner := strings.TrimSpace(owner)
	normalizedRepo := strings.TrimSpace(repo)
	normalizedRef := normalizeGitHubRef(ref)
	if normalizedOwner == "" || normalizedRepo == "" || normalizedRef == "" {
		return "", false
	}

	for region, source := range handler.sources {
		if !strings.EqualFold(strings.TrimSpace(source.Owner), normalizedOwner) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(source.Repo), normalizedRepo) {
			continue
		}
		if normalizeGitHubRef(source.Ref) != normalizedRef {
			continue
		}

		return region, true
	}

	return "", false
}

func (handler *GitHubWebhookHandler) verifySignature(body []byte, signature string) bool {
	if handler == nil || handler.secret == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(handler.secret))
	_, _ = mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.TrimSpace(signature)))
}

func (payload gitHubPushWebhookPayload) hasVersionFileChange() bool {
	changedFiles := make([]string, 0)
	for _, commit := range payload.Commits {
		changedFiles = append(changedFiles, commit.Added...)
		changedFiles = append(changedFiles, commit.Modified...)
		changedFiles = append(changedFiles, commit.Removed...)
	}
	if payload.HeadCommit != nil {
		changedFiles = append(changedFiles, payload.HeadCommit.Added...)
		changedFiles = append(changedFiles, payload.HeadCommit.Modified...)
		changedFiles = append(changedFiles, payload.HeadCommit.Removed...)
	}

	for _, filePath := range changedFiles {
		baseName := strings.ToLower(strings.TrimSpace(path.Base(filePath)))
		if baseName == "versions.json" {
			return true
		}
	}

	return false
}

func (repo gitHubWebhookRepo) OwnerName() string {
	if owner := strings.TrimSpace(repo.Owner.Login); owner != "" {
		return owner
	}
	if owner := strings.TrimSpace(repo.Owner.Name); owner != "" {
		return owner
	}
	if fullName := strings.TrimSpace(repo.FullName); fullName != "" {
		owner, _, found := strings.Cut(fullName, "/")
		if found {
			return strings.TrimSpace(owner)
		}
	}

	return ""
}

func normalizeGitHubRef(ref string) string {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "refs/") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "heads/") {
		return "refs/" + trimmed
	}

	return "refs/heads/" + trimmed
}

func isNilMasterDataSyncer(syncer masterDataRegionSyncer) bool {
	if syncer == nil {
		return true
	}

	value := reflect.ValueOf(syncer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
