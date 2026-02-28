package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

type GitHubMasterDataRepository struct {
	httpClient *http.Client
	token      string
}

type gitTreeResponse struct {
	Tree []gitTreeItem `json:"tree"`
}

type gitTreeItem struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

func NewGitHubMasterDataRepository(timeout time.Duration, token string) *GitHubMasterDataRepository {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	return &GitHubMasterDataRepository{
		httpClient: &http.Client{Timeout: timeout},
		token:      strings.TrimSpace(token),
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
	files := make(map[string]any)
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

		contentURL := repository.rawFileURL(source, item.Path)
		var parsed any
		if err := repository.getJSON(ctx, contentURL, &parsed); err != nil {
			return nil, fmt.Errorf("fetch file %s for region %s: %w", item.Path, source.Region, err)
		}

		files[item.Path] = parsed
	}

	return files, nil
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
