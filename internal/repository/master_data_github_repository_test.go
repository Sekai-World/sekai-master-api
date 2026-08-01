package repository

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"sekai-master-api/internal/domain/masterdata"
)

func TestLoadRegionDownloadsArchiveAndFiltersBasePath(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/repos/owner/repo/tarball/main") {
			writer.WriteHeader(http.StatusNotFound)
			return
		}

		mu.Lock()
		requestCount++
		mu.Unlock()

		writeTarball(t, writer, map[string]string{
			"repo-commit/data/file-a.json":  `{"id":1}`,
			"repo-commit/data/file-b.json":  `{"id":2}`,
			"repo-commit/data/readme.txt":   `skip`,
			"repo-commit/other/file-c.json": `{"id":3}`,
		})
	}))
	defer server.Close()

	repository := NewGitHubMasterDataRepository(2*time.Second, "", 2, 2, 10*time.Millisecond)
	repository.apiBaseURL = server.URL

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
		t.Fatalf("expected 2 json files loaded, got %d", len(payload))
	}
	if _, ok := payload["data/file-a.json"]; !ok {
		t.Fatalf("expected data/file-a.json in payload")
	}
	if _, ok := payload["data/file-b.json"]; !ok {
		t.Fatalf("expected data/file-b.json in payload")
	}
	if _, ok := payload["other/file-c.json"]; ok {
		t.Fatalf("did not expect other/file-c.json in filtered payload")
	}

	mu.Lock()
	defer mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("expected one archive request, got %d", requestCount)
	}
}

func TestLoadRegionRetriesArchiveDownload(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/repos/owner/repo/tarball/main") {
			writer.WriteHeader(http.StatusNotFound)
			return
		}

		mu.Lock()
		requestCount++
		current := requestCount
		mu.Unlock()

		if current == 1 {
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte("temporary upstream error"))
			return
		}

		writeTarball(t, writer, map[string]string{
			"repo-commit/data/file-a.json": `{"ok":true}`,
		})
	}))
	defer server.Close()

	repository := NewGitHubMasterDataRepository(2*time.Second, "", 1, 3, 10*time.Millisecond)
	repository.apiBaseURL = server.URL

	payload, err := repository.LoadRegion(context.Background(), masterdata.Source{
		Region: "jp",
		Owner:  "owner",
		Repo:   "repo",
		Ref:    "main",
		Path:   "data",
	})
	if err != nil {
		t.Fatalf("expected load success after retry, got %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected 1 file loaded, got %d", len(payload))
	}

	mu.Lock()
	defer mu.Unlock()
	if requestCount != 2 {
		t.Fatalf("expected two archive requests due to one retry, got %d", requestCount)
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

func TestResolveRegionVersionFallsBackToGitSmartHTTP(t *testing.T) {
	const mainSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const headSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/owner/repo/commits/main":
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte("rate limit exceeded"))
		case "/owner/repo.git/info/refs":
			if request.URL.Query().Get("service") != "git-upload-pack" {
				t.Fatalf("expected git-upload-pack service query, got %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(
				gitPktLine("# service=git-upload-pack\n") +
					"0000" +
					gitPktLine(headSHA+" HEAD\x00symref=HEAD:refs/heads/main\n") +
					gitPktLine(mainSHA+" refs/heads/main\n"),
			))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	repository := NewGitHubMasterDataRepository(2*time.Second, "", 1, 1, 10*time.Millisecond)
	repository.apiBaseURL = server.URL
	repository.gitBaseURL = server.URL

	version, err := repository.ResolveRegionVersion(context.Background(), masterdata.Source{
		Region: "jp",
		Owner:  "owner",
		Repo:   "repo",
		Ref:    "main",
	})
	if err != nil {
		t.Fatalf("expected smart HTTP fallback success, got %v", err)
	}
	if version != mainSHA {
		t.Fatalf("expected %s, got %s", mainSHA, version)
	}
}

func TestParseGitUploadPackRefsSelectsRequestedRefOrHead(t *testing.T) {
	const mainSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const headSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	refs, err := parseGitUploadPackRefs([]byte(
		gitPktLine("# service=git-upload-pack\n") +
			"0000" +
			gitPktLine(headSHA+" HEAD\x00symref=HEAD:refs/heads/main\n") +
			gitPktLine(mainSHA+" refs/heads/main\n"),
	))
	if err != nil {
		t.Fatalf("expected pkt-line parsing success, got %v", err)
	}

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "short branch", ref: "main", want: mainSHA},
		{name: "full branch", ref: "refs/heads/main", want: mainSHA},
		{name: "head", ref: "HEAD", want: headSHA},
		{name: "empty ref uses head", ref: "", want: headSHA},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, err := selectGitUploadPackRef(refs, test.ref)
			if err != nil {
				t.Fatalf("expected ref selection success, got %v", err)
			}
			if version != test.want {
				t.Fatalf("expected %s, got %s", test.want, version)
			}
		})
	}
}

func TestResolveRegionVersionReturnsRESTAndSmartHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/owner/repo/commits/main":
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte("rate limit exceeded"))
		case "/owner/repo.git/info/refs":
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte("smart HTTP unavailable"))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	repository := NewGitHubMasterDataRepository(2*time.Second, "", 1, 1, 10*time.Millisecond)
	repository.apiBaseURL = server.URL
	repository.gitBaseURL = server.URL

	_, err := repository.ResolveRegionVersion(context.Background(), masterdata.Source{
		Region: "jp",
		Owner:  "owner",
		Repo:   "repo",
		Ref:    "main",
	})
	if err == nil {
		t.Fatal("expected smart HTTP fallback failure")
	}
	if !strings.Contains(err.Error(), "smart HTTP fallback failed") {
		t.Fatalf("expected smart HTTP fallback error, got %v", err)
	}
	if !strings.Contains(err.Error(), "status 502") {
		t.Fatalf("expected smart HTTP status in error, got %v", err)
	}
}

func TestArchiveRelativeJSONPathRejectsTraversal(t *testing.T) {
	relativePath, ok := archiveRelativeJSONPath("repo-commit/../evil.json", "")
	if ok {
		t.Fatalf("expected traversal path to be rejected, got %s", relativePath)
	}
}

func writeTarball(t *testing.T, writer http.ResponseWriter, files map[string]string) {
	t.Helper()

	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)

	for name, content := range files {
		header := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatalf("write tar content: %v", err)
		}
	}

	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}

	writer.Header().Set("Content-Type", "application/gzip")
	_, _ = writer.Write(buffer.Bytes())
}

func gitPktLine(payload string) string {
	return fmt.Sprintf("%04x%s", len(payload)+4, payload)
}
