package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sekai-master-api/internal/config"
)

func TestNewZitadelHTTPClientRewritesToInternalBaseAndPreservesHost(t *testing.T) {
	var gotHost string
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewZitadelHTTPClient(config.Config{
		ZitadelIssuerURL:   "http://localhost:18081",
		ZitadelInternalURL: server.URL,
	}, time.Second)
	if err != nil {
		t.Fatalf("NewZitadelHTTPClient() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, "http://localhost:18081/.well-known/openid-configuration", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if gotHost != "localhost:18081" {
		t.Fatalf("host header = %q, want %q", gotHost, "localhost:18081")
	}
	if gotPath != "/.well-known/openid-configuration" {
		t.Fatalf("request path = %q, want %q", gotPath, "/.well-known/openid-configuration")
	}
}
