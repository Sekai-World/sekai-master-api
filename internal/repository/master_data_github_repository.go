package repository

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sekai-master-api/internal/domain/masterdata"
	"sekai-master-api/internal/logging"
)

type GitHubMasterDataRepository struct {
	httpClient      *http.Client
	token           string
	fileConcurrency int
	retryCount      int
	retryBackoff    time.Duration
	apiBaseURL      string
	rawBaseURL      string
	resumeBaseDir   string
}

const defaultGitHubAPIBaseURL = "https://api.github.com"
const defaultGitHubRawBaseURL = "https://raw.githubusercontent.com"
const defaultMasterDataResumeBaseDir = "tmp/master-data-sync-resume"

type gitTreeResponse struct {
	Tree []gitTreeItem `json:"tree"`
}

type gitTreeItem struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type gitCommitResponse struct {
	SHA string `json:"sha"`
}

func NewGitHubMasterDataRepository(timeout time.Duration, token string, fileConcurrency int, retryCount int, retryBackoff time.Duration) *GitHubMasterDataRepository {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if fileConcurrency <= 0 {
		fileConcurrency = 8
	}
	if retryCount <= 0 {
		retryCount = 3
	}
	if retryBackoff <= 0 {
		retryBackoff = 300 * time.Millisecond
	}

	return &GitHubMasterDataRepository{
		httpClient:      &http.Client{Timeout: timeout},
		token:           strings.TrimSpace(token),
		fileConcurrency: fileConcurrency,
		retryCount:      retryCount,
		retryBackoff:    retryBackoff,
		apiBaseURL:      defaultGitHubAPIBaseURL,
		rawBaseURL:      defaultGitHubRawBaseURL,
		resumeBaseDir:   defaultMasterDataResumeBaseDir,
	}
}

func (repository *GitHubMasterDataRepository) LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error) {
	treeURL := fmt.Sprintf(
		"%s/repos/%s/%s/git/trees/%s?recursive=1",
		strings.TrimRight(repository.apiBaseURL, "/"),
		url.PathEscape(source.Owner),
		url.PathEscape(source.Repo),
		url.PathEscape(source.Ref),
	)

	var treeResp gitTreeResponse
	if err := repository.getJSON(ctx, treeURL, &treeResp); err != nil {
		return nil, fmt.Errorf("fetch repository tree for region %s: %w", source.Region, err)
	}

	basePath := strings.Trim(strings.TrimSpace(source.Path), "/")
	filePaths := make([]string, 0)
	for _, item := range treeResp.Tree {
		if item.Type != "blob" {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(item.Path), ".json") {
			continue
		}
		if basePath != "" && !strings.HasPrefix(item.Path, basePath+"/") && item.Path != basePath {
			continue
		}

		filePaths = append(filePaths, item.Path)
	}

	files := make(map[string]any, len(filePaths))
	if len(filePaths) == 0 {
		logging.DebugKV("master-data-loader", fmt.Sprintf("region=%s phase=load no_json_files_found=true", source.Region))
		repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
			Event:      "master_data_sync_progress",
			Status:     "running",
			Region:     source.Region,
			Phase:      "load_discovered",
			Message:    "no json files found",
			TotalFiles: 0,
			UpdatedAt:  time.Now().UTC(),
		})
		return files, nil
	}

	effectiveConcurrency := repository.fileConcurrency
	if effectiveConcurrency <= 0 {
		effectiveConcurrency = 1
	}
	if effectiveConcurrency > len(filePaths) {
		effectiveConcurrency = len(filePaths)
	}

	resumeDir := repository.resumeDir(source, filePaths)
	if err := repository.loadResumeFiles(resumeDir, filePaths, files); err != nil {
		logging.ErrorKV("master-data-loader", fmt.Sprintf("region=%s phase=resume status=load_failed error=%v", source.Region, err))
	}

	logging.DebugKV("master-data-loader", fmt.Sprintf("region=%s phase=load files=%d concurrency=%d", source.Region, len(filePaths), effectiveConcurrency))
	repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
		Event:      "master_data_sync_progress",
		Status:     "running",
		Region:     source.Region,
		Phase:      "load_discovered",
		Message:    "discovered json files",
		TotalFiles: len(filePaths),
		UpdatedAt:  time.Now().UTC(),
	})

	var (
		resultsMu    sync.Mutex
		wg           sync.WaitGroup
		processed    atomic.Int64
		successCount atomic.Int64
	)

	pendingPaths := make([]string, 0, len(filePaths))
	for _, filePath := range filePaths {
		if _, exists := files[filePath]; !exists {
			pendingPaths = append(pendingPaths, filePath)
		}
	}

	processed.Store(int64(len(files)))
	successCount.Store(int64(len(files)))
	failedFileErrors := make(map[string]error)

	for attempt := 1; attempt <= repository.retryCount && len(pendingPaths) > 0; attempt++ {
		attemptPaths := append([]string{}, pendingPaths...)
		pendingPaths = make([]string, 0)

		semaphore := make(chan struct{}, effectiveConcurrency)
		for _, filePath := range attemptPaths {
			semaphore <- struct{}{}
			filePath := filePath

			wg.Go(func() {
				defer func() {
					<-semaphore
				}()

				contentURL := repository.rawFileURL(source, filePath)
				var parsed any
				if err := repository.getJSON(ctx, contentURL, &parsed); err != nil {
					wrappedError := fmt.Errorf("fetch file %s for region %s: %w", filePath, source.Region, err)
					resultsMu.Lock()
					failedFileErrors[filePath] = wrappedError
					pendingPaths = append(pendingPaths, filePath)
					resultsMu.Unlock()
					logging.ErrorKV("master-data-loader", fmt.Sprintf("region=%s phase=load status=failed attempt=%d/%d file=%s error=%v", source.Region, attempt, repository.retryCount, filePath, err))
					repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
						Event:      "master_data_sync_progress",
						Status:     "running",
						Region:     source.Region,
						Phase:      "load_file",
						Message:    fmt.Sprintf("file load failed, retry attempt %d/%d", attempt, repository.retryCount),
						FilePath:   filePath,
						TotalFiles: len(filePaths),
						UpdatedAt:  time.Now().UTC(),
					})
					return
				}

				resultsMu.Lock()
				files[filePath] = parsed
				delete(failedFileErrors, filePath)
				resultsMu.Unlock()

				if saveErr := repository.saveResumeFile(resumeDir, filePath, parsed); saveErr != nil {
					logging.ErrorKV("master-data-loader", fmt.Sprintf("region=%s phase=resume status=save_failed file=%s error=%v", source.Region, filePath, saveErr))
				}
				successCount.Add(1)

				currentProcessed := processed.Add(1)
				totalFiles := int64(len(filePaths))
				failedCount := totalFiles - successCount.Load()
				if currentProcessed == totalFiles || currentProcessed%20 == 0 {
					logging.DebugKV("master-data-loader", fmt.Sprintf("region=%s phase=load progress=%d/%d success=%d failed=%d", source.Region, currentProcessed, totalFiles, successCount.Load(), failedCount))
					repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
						Event:          "master_data_sync_progress",
						Status:         "running",
						Region:         source.Region,
						Phase:          "load_progress",
						Message:        "loading files",
						ProcessedFiles: int(currentProcessed),
						TotalFiles:     int(totalFiles),
						FileCount:      int(successCount.Load()),
						FailedFiles:    int(failedCount),
						UpdatedAt:      time.Now().UTC(),
					})
				}
			})
		}

		wg.Wait()

		if len(pendingPaths) == 0 {
			break
		}

		if attempt < repository.retryCount {
			if sleepErr := repository.waitRetryBackoff(ctx, attempt); sleepErr != nil {
				return nil, sleepErr
			}
		}
	}

	if len(pendingPaths) > 0 {
		fetchErrors := make([]error, 0, len(pendingPaths))
		for _, filePath := range pendingPaths {
			if err, exists := failedFileErrors[filePath]; exists {
				fetchErrors = append(fetchErrors, err)
			}
		}
		repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
			Event:          "master_data_sync_progress",
			Status:         "failed",
			Region:         source.Region,
			Phase:          "load_done",
			Message:        "region file loading failed",
			ProcessedFiles: len(filePaths),
			TotalFiles:     len(filePaths),
			FileCount:      len(files),
			FailedFiles:    len(pendingPaths),
			UpdatedAt:      time.Now().UTC(),
		})
		return nil, fmt.Errorf("fetch files for region %s: %w", source.Region, errors.Join(fetchErrors...))
	}

	logging.InfoKV("master-data-loader", fmt.Sprintf("region=%s phase=load status=success files=%d", source.Region, len(files)))
	if err := os.RemoveAll(resumeDir); err != nil {
		logging.ErrorKV("master-data-loader", fmt.Sprintf("region=%s phase=resume status=cleanup_failed error=%v", source.Region, err))
	}
	repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
		Event:          "master_data_sync_progress",
		Status:         "running",
		Region:         source.Region,
		Phase:          "load_done",
		Message:        "region file loading completed",
		ProcessedFiles: len(filePaths),
		TotalFiles:     len(filePaths),
		FileCount:      len(files),
		UpdatedAt:      time.Now().UTC(),
	})

	return files, nil
}

func (repository *GitHubMasterDataRepository) ResolveRegionVersion(ctx context.Context, source masterdata.Source) (string, error) {
	commitURL := fmt.Sprintf(
		"%s/repos/%s/%s/commits/%s",
		strings.TrimRight(repository.apiBaseURL, "/"),
		url.PathEscape(source.Owner),
		url.PathEscape(source.Repo),
		url.PathEscape(source.Ref),
	)

	var commitResp gitCommitResponse
	if err := repository.getJSON(ctx, commitURL, &commitResp); err != nil {
		return "", fmt.Errorf("resolve commit for region %s: %w", source.Region, err)
	}

	return strings.TrimSpace(commitResp.SHA), nil
}

func (repository *GitHubMasterDataRepository) reportProgress(ctx context.Context, event masterdata.SyncUpdatedEvent) {
	reporter := masterdata.ProgressReporterFromContext(ctx)
	if reporter == nil {
		return
	}

	reporter(event)
}

func (repository *GitHubMasterDataRepository) rawFileURL(source masterdata.Source, filePath string) string {
	segments := strings.Split(strings.Trim(filePath, "/"), "/")
	escapedPath := make([]string, 0, len(segments))
	for _, segment := range segments {
		escapedPath = append(escapedPath, url.PathEscape(segment))
	}

	return strings.TrimRight(strings.TrimSpace(repository.rawBaseURL), "/") + "/" +
		url.PathEscape(source.Owner) + "/" +
		url.PathEscape(source.Repo) + "/" +
		url.PathEscape(source.Ref) + "/" +
		path.Join(escapedPath...)
}

func (repository *GitHubMasterDataRepository) resumeDir(source masterdata.Source, filePaths []string) string {
	sortedPaths := append([]string{}, filePaths...)
	sort.Strings(sortedPaths)

	hasher := sha1.New()
	_, _ = hasher.Write([]byte(strings.ToLower(strings.TrimSpace(source.Region))))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Owner)))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Repo)))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Ref)))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Path)))
	for _, filePath := range sortedPaths {
		_, _ = hasher.Write([]byte("|" + filePath))
	}

	resumeKey := hex.EncodeToString(hasher.Sum(nil))
	regionKey := strings.ToLower(strings.TrimSpace(source.Region))
	if regionKey == "" {
		regionKey = "default"
	}

	return filepath.Join(repository.resumeBaseDir, regionKey, resumeKey)
}

func (repository *GitHubMasterDataRepository) resumeFilePath(resumeDir string, filePath string) (string, error) {
	cleanPath := path.Clean("/" + strings.TrimSpace(filePath))
	if cleanPath == "/" {
		return "", fmt.Errorf("empty file path")
	}
	relativePath := strings.TrimPrefix(cleanPath, "/")
	if strings.HasPrefix(relativePath, "../") || relativePath == ".." {
		return "", fmt.Errorf("invalid file path: %s", filePath)
	}

	return filepath.Join(resumeDir, "files", filepath.FromSlash(relativePath)), nil
}

func (repository *GitHubMasterDataRepository) loadResumeFiles(resumeDir string, filePaths []string, files map[string]any) error {
	for _, filePath := range filePaths {
		cacheFilePath, err := repository.resumeFilePath(resumeDir, filePath)
		if err != nil {
			return err
		}

		body, readErr := os.ReadFile(cacheFilePath)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read resume file %s: %w", cacheFilePath, readErr)
		}

		var parsed any
		if err := json.Unmarshal(body, &parsed); err != nil {
			return fmt.Errorf("decode resume file %s: %w", cacheFilePath, err)
		}

		files[filePath] = parsed
	}

	return nil
}

func (repository *GitHubMasterDataRepository) saveResumeFile(resumeDir string, filePath string, parsed any) error {
	cacheFilePath, err := repository.resumeFilePath(resumeDir, filePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cacheFilePath), 0o755); err != nil {
		return fmt.Errorf("create resume directory: %w", err)
	}

	body, err := json.Marshal(parsed)
	if err != nil {
		return fmt.Errorf("marshal resume payload: %w", err)
	}

	if err := os.WriteFile(cacheFilePath, body, 0o644); err != nil {
		return fmt.Errorf("write resume file: %w", err)
	}

	return nil
}

func (repository *GitHubMasterDataRepository) getJSON(ctx context.Context, targetURL string, out any) error {
	var lastErr error
	for attempt := 1; attempt <= repository.retryCount; attempt++ {
		err := repository.doJSONRequest(ctx, targetURL, out)
		if err == nil {
			return nil
		}

		lastErr = err
		if !isRetriableRequestError(err) || attempt >= repository.retryCount {
			break
		}

		if sleepErr := repository.waitRetryBackoff(ctx, attempt); sleepErr != nil {
			return sleepErr
		}
	}

	return lastErr
}

func (repository *GitHubMasterDataRepository) doJSONRequest(ctx context.Context, targetURL string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "sekai-master-api/1.0")
	if repository.token != "" {
		request.Header.Set("Authorization", "Bearer "+repository.token)
	}

	resp, err := repository.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return &httpStatusError{statusCode: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode json response: %w", err)
	}

	return nil
}

func (repository *GitHubMasterDataRepository) waitRetryBackoff(ctx context.Context, attempt int) error {
	backoff := repository.retryBackoff * time.Duration(attempt)
	timer := time.NewTimer(backoff)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type httpStatusError struct {
	statusCode int
	body       string
}

func (err *httpStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", err.statusCode, err.body)
}

func isRetriableRequestError(err error) bool {
	if err == nil {
		return false
	}

	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		if statusErr.statusCode == http.StatusTooManyRequests {
			return true
		}

		return statusErr.statusCode >= http.StatusInternalServerError
	}

	return true
}
