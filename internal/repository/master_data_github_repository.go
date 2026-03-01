package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

type GitHubMasterDataRepository struct {
	httpClient      *http.Client
	token           string
	fileConcurrency int
}

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

func NewGitHubMasterDataRepository(timeout time.Duration, token string, fileConcurrency int) *GitHubMasterDataRepository {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	if fileConcurrency <= 0 {
		fileConcurrency = 8
	}

	return &GitHubMasterDataRepository{
		httpClient:      &http.Client{Timeout: timeout},
		token:           strings.TrimSpace(token),
		fileConcurrency: fileConcurrency,
	}
}

func (repository *GitHubMasterDataRepository) LoadRegion(ctx context.Context, source masterdata.Source) (map[string]any, error) {
	treeURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1",
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
		log.Printf("component=master-data-loader region=%s phase=load no_json_files_found=true", source.Region)
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

	log.Printf(
		"component=master-data-loader region=%s phase=load files=%d concurrency=%d",
		source.Region,
		len(filePaths),
		effectiveConcurrency,
	)
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
		fetchErrors  []error
		wg           sync.WaitGroup
		processed    atomic.Int64
		successCount atomic.Int64
	)

	semaphore := make(chan struct{}, effectiveConcurrency)
	for _, filePath := range filePaths {
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
				fetchErrors = append(fetchErrors, wrappedError)
				resultsMu.Unlock()
				log.Printf("component=master-data-loader region=%s phase=load status=failed file=%s error=%v", source.Region, filePath, err)
				repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
					Event:      "master_data_sync_progress",
					Status:     "running",
					Region:     source.Region,
					Phase:      "load_file",
					Message:    "file load failed",
					FilePath:   filePath,
					TotalFiles: len(filePaths),
					UpdatedAt:  time.Now().UTC(),
				})
			} else {
				resultsMu.Lock()
				files[filePath] = parsed
				resultsMu.Unlock()
				successCount.Add(1)
			}

			currentProcessed := processed.Add(1)
			totalFiles := int64(len(filePaths))
			failedCount := currentProcessed - successCount.Load()
			if currentProcessed == totalFiles || currentProcessed%20 == 0 {
				log.Printf(
					"component=master-data-loader region=%s phase=load progress=%d/%d success=%d failed=%d",
					source.Region,
					currentProcessed,
					totalFiles,
					successCount.Load(),
					failedCount,
				)
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

	if len(fetchErrors) > 0 {
		repository.reportProgress(ctx, masterdata.SyncUpdatedEvent{
			Event:          "master_data_sync_progress",
			Status:         "failed",
			Region:         source.Region,
			Phase:          "load_done",
			Message:        "region file loading failed",
			ProcessedFiles: len(filePaths),
			TotalFiles:     len(filePaths),
			FileCount:      len(files),
			FailedFiles:    len(fetchErrors),
			UpdatedAt:      time.Now().UTC(),
		})
		return nil, fmt.Errorf("fetch files for region %s: %w", source.Region, errors.Join(fetchErrors...))
	}

	log.Printf(
		"component=master-data-loader region=%s phase=load status=success files=%d",
		source.Region,
		len(files),
	)
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
		"https://api.github.com/repos/%s/%s/commits/%s",
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

	return "https://raw.githubusercontent.com/" +
		url.PathEscape(source.Owner) + "/" +
		url.PathEscape(source.Repo) + "/" +
		url.PathEscape(source.Ref) + "/" +
		path.Join(escapedPath...)
}

func (repository *GitHubMasterDataRepository) getJSON(ctx context.Context, targetURL string, out any) error {
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
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode json response: %w", err)
	}

	return nil
}
