package system

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"sekai-master-api/internal/config"
	"sekai-master-api/internal/transport/http/response"
	"sekai-master-api/internal/usecase"
)

type masterDataRegionSyncer interface {
	SyncRegion(ctx context.Context, region string) error
}

type GitHubWebhookHandler struct {
	sources     map[string]config.MasterDataSource
	syncer      masterDataRegionSyncer
	syncTimeout time.Duration
	secret      string
	enabled     bool
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
) *GitHubWebhookHandler {
	copiedSources := make(map[string]config.MasterDataSource, len(sources))
	for region, source := range sources {
		copiedSources[strings.ToLower(strings.TrimSpace(region))] = source
	}

	return &GitHubWebhookHandler{
		sources:     copiedSources,
		syncer:      syncer,
		syncTimeout: syncTimeout,
		secret:      strings.TrimSpace(secret),
		enabled:     !isNilMasterDataSyncer(syncer),
	}
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

	go handler.triggerRegionSync(region, owner, repo, ref)

	response.JSON(c, http.StatusAccepted, gin.H{
		"status": "accepted",
		"region": region,
	})
}

func (handler *GitHubWebhookHandler) triggerRegionSync(region string, owner string, repo string, ref string) {
	ctx := context.Background()
	cancel := func() {}
	if handler.syncTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, handler.syncTimeout)
	}
	defer cancel()

	zap.S().Infow("github webhook triggered master data sync", "region", region, "owner", owner, "repo", repo, "ref", ref)

	if err := handler.syncer.SyncRegion(ctx, region); err != nil {
		if err == usecase.ErrSyncInProgress {
			zap.S().Infow("github webhook skipped because master data sync already running", "region", region)
			return
		}

		zap.S().Warnw("github webhook master data sync failed", "region", region, "error", err)
		return
	}

	zap.S().Infow("github webhook master data sync completed", "region", region)
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
		return true
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
