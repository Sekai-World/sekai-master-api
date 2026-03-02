package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

func TestLoadRegionRetriesFailedFilesAndResumes(t *testing.T) {
	var mu sync.Mutex
	requestCountByPath := make(map[string]int)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/repos/owner/repo/git/trees/main") {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"tree":[{"path":"data/file-a.json","type":"blob"},{"path":"data/file-b.json","type":"blob"}]}`))
			return
		}

		if strings.HasPrefix(request.URL.Path, "/owner/repo/main/data/") {
			mu.Lock()
			requestCountByPath[request.URL.Path]++
			count := requestCountByPath[request.URL.Path]
			mu.Unlock()

			if request.URL.Path == "/owner/repo/main/data/file-b.json" && count == 1 {
				writer.WriteHeader(http.StatusBadGateway)
				_, _ = writer.Write([]byte("temporary upstream error"))
				return
			}

			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"ok":true}`))
			return
		}

		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	repository := NewGitHubMasterDataRepository(2*time.Second, "", 2, 3, 10*time.Millisecond)
	repository.apiBaseURL = server.URL
	repository.rawBaseURL = server.URL

	payload, err := repository.LoadRegion(context.Background(), masterdata.Source{
		Region: "jp",
		Owner:  "owner",
		Repo:   "repo",
		Ref:    "main",
		Path:   "data",
	})
	if err != nil {
		t.Fatalf("expected load success, got %v", err)
	}

	if len(payload) != 2 {
		t.Fatalf("expected 2 files loaded, got %d", len(payload))
	}

	mu.Lock()
	defer mu.Unlock()
	if requestCountByPath["/owner/repo/main/data/file-a.json"] != 1 {
		t.Fatalf("expected file-a fetched once, got %d", requestCountByPath["/owner/repo/main/data/file-a.json"])
	}
	if requestCountByPath["/owner/repo/main/data/file-b.json"] != 2 {
		t.Fatalf("expected file-b retried once, got %d", requestCountByPath["/owner/repo/main/data/file-b.json"])
	}
}

func TestResolveRegionVersionRetriesTransientFailure(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/repos/owner/repo/commits/main") {
			writer.WriteHeader(http.StatusNotFound)
			return
		}

		mu.Lock()
		requestCount++
		current := requestCount
		mu.Unlock()

		if current == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte("temporary unavailable"))
			return
		}

		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"sha": "commit-sha"})
	}))
	defer server.Close()

	repository := NewGitHubMasterDataRepository(2*time.Second, "", 1, 3, 10*time.Millisecond)
	repository.apiBaseURL = server.URL

	version, err := repository.ResolveRegionVersion(context.Background(), masterdata.Source{
		Region: "jp",
		Owner:  "owner",
		Repo:   "repo",
		Ref:    "main",
	})
	if err != nil {
		t.Fatalf("expected resolve success, got %v", err)
	}
	if version != "commit-sha" {
		t.Fatalf("expected commit-sha, got %s", version)
	}

	mu.Lock()
	defer mu.Unlock()
	if requestCount != 2 {
		t.Fatalf("expected two requests due to one retry, got %d", requestCount)
	}
}

func TestLoadRegionResumesAcrossRunsUsingLocalCache(t *testing.T) {
	var mu sync.Mutex
	requestCountByPath := make(map[string]int)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/repos/owner/repo/git/trees/main") {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"tree":[{"path":"data/file-a.json","type":"blob"},{"path":"data/file-b.json","type":"blob"}]}`))
			return
		}

		if strings.HasPrefix(request.URL.Path, "/owner/repo/main/data/") {
			mu.Lock()
			requestCountByPath[request.URL.Path]++
			count := requestCountByPath[request.URL.Path]
			mu.Unlock()

			if request.URL.Path == "/owner/repo/main/data/file-b.json" && count == 1 {
				writer.WriteHeader(http.StatusBadGateway)
				_, _ = writer.Write([]byte("temporary upstream error"))
				return
			}

			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"ok":true}`))
			return
		}

		writer.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	resumeBaseDir := filepath.Join(t.TempDir(), "resume")

	repository := NewGitHubMasterDataRepository(2*time.Second, "", 2, 1, 10*time.Millisecond)
	repository.apiBaseURL = server.URL
	repository.rawBaseURL = server.URL
	repository.resumeBaseDir = resumeBaseDir

	_, firstErr := repository.LoadRegion(context.Background(), masterdata.Source{
		Region: "jp",
		Owner:  "owner",
		Repo:   "repo",
		Ref:    "main",
		Path:   "data",
	})
	if firstErr == nil {
		t.Fatalf("expected first load to fail")
	}

	repositorySecondRun := NewGitHubMasterDataRepository(2*time.Second, "", 2, 1, 10*time.Millisecond)
	repositorySecondRun.apiBaseURL = server.URL
	repositorySecondRun.rawBaseURL = server.URL
	repositorySecondRun.resumeBaseDir = resumeBaseDir

	payload, secondErr := repositorySecondRun.LoadRegion(context.Background(), masterdata.Source{
		Region: "jp",
		Owner:  "owner",
		Repo:   "repo",
		Ref:    "main",
		Path:   "data",
	})
	if secondErr != nil {
		t.Fatalf("expected second load success, got %v", secondErr)
	}

	if len(payload) != 2 {
		t.Fatalf("expected 2 files loaded on second run, got %d", len(payload))
	}

	mu.Lock()
	if requestCountByPath["/owner/repo/main/data/file-a.json"] != 1 {
		mu.Unlock()
		t.Fatalf("expected file-a fetched once across two runs, got %d", requestCountByPath["/owner/repo/main/data/file-a.json"])
	}
	if requestCountByPath["/owner/repo/main/data/file-b.json"] != 2 {
		mu.Unlock()
		t.Fatalf("expected file-b fetched twice across two runs, got %d", requestCountByPath["/owner/repo/main/data/file-b.json"])
	}
	mu.Unlock()

	cacheFiles := 0
	err := filepath.WalkDir(resumeBaseDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		cacheFiles++
		return nil
	})
	if err != nil {
		t.Fatalf("walk resume dir: %v", err)
	}
	if cacheFiles != 0 {
		t.Fatalf("expected no cached files after successful load, got %d", cacheFiles)
	}
}
