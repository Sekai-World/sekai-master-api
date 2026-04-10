package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sekai-master-api/internal/config"
)

func TestResolveOIDCEndpointsFallsBackToPublicIssuerWhenInternalURLFails(t *testing.T) {
	var discoveryHits int

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			t.Fatalf("unexpected discovery path %q", r.URL.Path)
		}

		discoveryHits++
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 server.URL,
			"authorization_endpoint": server.URL + "/application/o/authorize/",
			"token_endpoint":         server.URL + "/application/o/token/",
			"jwks_uri":               server.URL + "/jwks/",
		}); err != nil {
			t.Fatalf("encode discovery response: %v", err)
		}
	}))
	defer server.Close()

	authURL, tokenURL, err := ResolveOIDCEndpoints(context.Background(), config.Config{
		OIDCIssuerURL:   server.URL,
		OIDCInternalURL: "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("ResolveOIDCEndpoints() error = %v", err)
	}

	if authURL != server.URL+"/application/o/authorize/" {
		t.Fatalf("authorization endpoint = %q, want %q", authURL, server.URL+"/application/o/authorize/")
	}
	if tokenURL != server.URL+"/application/o/token/" {
		t.Fatalf("token endpoint = %q, want %q", tokenURL, server.URL+"/application/o/token/")
	}
	if discoveryHits != 1 {
		t.Fatalf("discovery hits = %d, want %d", discoveryHits, 1)
	}
}

func TestDiscoverOIDCProviderWithRetryWaitsForTransient404(t *testing.T) {
	var discoveryHits int

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/application/o/sekai-admin-web/.well-known/openid-configuration" {
			t.Fatalf("unexpected discovery path %q", r.URL.Path)
		}

		discoveryHits++
		if discoveryHits == 1 {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 server.URL + "/application/o/sekai-admin-web/",
			"authorization_endpoint": server.URL + "/application/o/authorize/",
			"token_endpoint":         server.URL + "/application/o/token/",
			"jwks_uri":               server.URL + "/jwks/",
		}); err != nil {
			t.Fatalf("encode discovery response: %v", err)
		}
	}))
	defer server.Close()

	provider, err := discoverOIDCProviderWithRetry(context.Background(), config.Config{
		OIDCIssuerURL:   server.URL + "/application/o/sekai-admin-web/",
		OIDCInternalURL: server.URL,
	}, 3*time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("discoverOIDCProviderWithRetry() error = %v", err)
	}
	if provider == nil {
		t.Fatal("discoverOIDCProviderWithRetry() returned nil provider")
	}
	if discoveryHits != 2 {
		t.Fatalf("discovery hits = %d, want %d", discoveryHits, 2)
	}
}
