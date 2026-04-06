package repository

import (
	"archive/tar"
	"compress/gzip"
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
	"strings"
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
	resumeBaseDir   string
}

const defaultGitHubAPIBaseURL = "https://api.github.com"
const defaultMasterDataResumeBaseDir = "tmp/master-data-sync-resume"

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
		resumeBaseDir:   defaultMasterDataResumeBaseDir,
	}
}

func (repository *GitHubMasterDataRepository) LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error) {
	archiveURL := repository.tarballURL(source)
	workspaceDir := repository.resumeDir(source)
	archivePath := filepath.Join(workspaceDir, "source.tar.gz")

	if err := os.RemoveAll(workspaceDir); err != nil {
		return nil, fmt.Errorf("clear archive workspace for region %s: %w", source.Region, err)
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return nil, fmt.Errorf("create archive workspace for region %s: %w", source.Region, err)
	}
	defer func() {
		if err := os.RemoveAll(workspaceDir); err != nil {
			logging.ErrorKV("master-data-loader", fmt.Sprintf("region=%s phase=archive status=cleanup_failed error=%v", source.Region, err))
		}
	}()

	repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
		Event:     "master_data_sync_progress",
		Status:    "running",
		Region:    source.Region,
		Phase:     "load_archive",
		Message:   "downloading source archive",
		UpdatedAt: time.Now().UTC(),
	})

	if err := repository.downloadArchiveToFile(ctx, archiveURL, archivePath); err != nil {
		return nil, fmt.Errorf("download archive for region %s: %w", source.Region, err)
	}

	repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
		Event:     "master_data_sync_progress",
		Status:    "running",
		Region:    source.Region,
		Phase:     "load_extract",
		Message:   "extracting source archive",
		UpdatedAt: time.Now().UTC(),
	})

	files, err := repository.extractArchivePayload(archivePath, source)
	if err != nil {
		return nil, fmt.Errorf("extract archive for region %s: %w", source.Region, err)
	}

	logging.InfoKV("master-data-loader", fmt.Sprintf("region=%s phase=load status=success files=%d", source.Region, len(files)))
	repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
		Event:      "master_data_sync_progress",
		Status:     "running",
		Region:     source.Region,
		Phase:      "load_done",
		Message:    "region archive loading completed",
		TotalFiles: len(files),
		FileCount:  len(files),
		UpdatedAt:  time.Now().UTC(),
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

func (repository *GitHubMasterDataRepository) tarballURL(source masterdata.Source) string {
	return fmt.Sprintf(
		"%s/repos/%s/%s/tarball/%s",
		strings.TrimRight(repository.apiBaseURL, "/"),
		url.PathEscape(source.Owner),
		url.PathEscape(source.Repo),
		url.PathEscape(source.Ref),
	)
}

func (repository *GitHubMasterDataRepository) resumeDir(source masterdata.Source) string {
	hasher := sha1.New()
	_, _ = hasher.Write([]byte(strings.ToLower(strings.TrimSpace(source.Region))))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Owner)))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Repo)))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Ref)))
	_, _ = hasher.Write([]byte("|" + strings.TrimSpace(source.Path)))

	resumeKey := hex.EncodeToString(hasher.Sum(nil))
	regionKey := strings.ToLower(strings.TrimSpace(source.Region))
	if regionKey == "" {
		regionKey = "default"
	}

	return filepath.Join(repository.resumeBaseDir, regionKey, resumeKey)
}

func (repository *GitHubMasterDataRepository) downloadArchiveToFile(ctx context.Context, archiveURL string, targetPath string) error {
	body, err := repository.getBytes(ctx, archiveURL)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create archive directory: %w", err)
	}

	tempPath := targetPath + ".tmp"
	if err := os.WriteFile(tempPath, body, 0o644); err != nil {
		return fmt.Errorf("write archive file: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("move archive file into place: %w", err)
	}

	return nil
}

func (repository *GitHubMasterDataRepository) extractArchivePayload(archivePath string, source masterdata.Source) (map[string]any, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive file: %w", err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("open gzip reader: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	files := make(map[string]any)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive entry: %w", err)
		}

		if header.FileInfo().IsDir() || !header.FileInfo().Mode().IsRegular() {
			continue
		}

		relativePath, ok := archiveRelativeJSONPath(header.Name, source.Path)
		if !ok {
			continue
		}

		body, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("read archive file %s: %w", relativePath, err)
		}

		var parsed any
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("decode archive file %s: %w", relativePath, err)
		}

		files[relativePath] = parsed
	}

	return files, nil
}

func archiveRelativeJSONPath(entryName string, basePath string) (string, bool) {
	trimmedEntryName := strings.Trim(strings.TrimSpace(entryName), "/")
	if trimmedEntryName == "" {
		return "", false
	}

	parts := strings.SplitN(trimmedEntryName, "/", 2)
	if len(parts) < 2 {
		return "", false
	}

	relativePath := strings.Trim(parts[1], "/")
	if relativePath == "" {
		return "", false
	}

	cleanPath := strings.TrimPrefix(path.Clean("/"+relativePath), "/")
	if cleanPath == "" || cleanPath == "." || strings.HasPrefix(cleanPath, "../") {
		return "", false
	}
	if !strings.HasSuffix(strings.ToLower(cleanPath), ".json") {
		return "", false
	}

	normalizedBasePath := strings.Trim(strings.TrimSpace(basePath), "/")
	if normalizedBasePath != "" && cleanPath != normalizedBasePath && !strings.HasPrefix(cleanPath, normalizedBasePath+"/") {
		return "", false
	}

	return cleanPath, true
}

func (repository *GitHubMasterDataRepository) getBytes(ctx context.Context, targetURL string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= repository.retryCount; attempt++ {
		body, err := repository.doBytesRequest(ctx, targetURL)
		if err == nil {
			return body, nil
		}

		lastErr = err
		if !isRetriableRequestError(err) || attempt >= repository.retryCount {
			break
		}

		if sleepErr := repository.waitRetryBackoff(ctx, attempt); sleepErr != nil {
			return nil, sleepErr
		}
	}

	return nil, lastErr
}

func (repository *GitHubMasterDataRepository) doBytesRequest(ctx context.Context, targetURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	request.Header.Set("User-Agent", "sekai-master-api/1.0")
	if repository.token != "" {
		request.Header.Set("Authorization", "Bearer "+repository.token)
	}

	resp, err := repository.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, &httpStatusError{statusCode: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}

	return body, nil
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
