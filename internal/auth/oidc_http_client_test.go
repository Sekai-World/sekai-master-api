package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"sekai-master-api/internal/config"
)

func TestNewOIDCHTTPClientRewritesToInternalBaseAndPreservesHost(t *testing.T) {
	var gotHost string
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewOIDCHTTPClient(config.Config{
		OIDCIssuerURL:   "http://localhost:18081",
		OIDCInternalURL: server.URL,
	}, time.Second)
	if err != nil {
		t.Fatalf("NewOIDCHTTPClient() error = %v", err)
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

func TestOIDCRoutingTransportMatchesHostOnly(t *testing.T) {
	// Simulates k8s scenario: issuer is https but Keycloak discovery returns
	// http:// endpoints for JWKS. The transport must still rewrite based on
	// host match even when the scheme differs.
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	transport, err := newOIDCHTTPTransport(config.Config{
		OIDCIssuerURL:   "https://issuer.example.com/realms/r",
		OIDCInternalURL: server.URL,
	}, http.DefaultTransport)
	if err != nil {
		t.Fatalf("newOIDCHTTPTransport() error = %v", err)
	}

	// Scheme drift: issuer is https but incoming request is http — should still rewrite
	req, err := http.NewRequest(http.MethodGet, "http://issuer.example.com/realms/r/protocol/openid-connect/certs", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	// If the request reached the test server, it was rewritten (otherwise DNS
	// resolution of issuer.example.com would fail). Verify the path survived.
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if gotPath != "/realms/r/protocol/openid-connect/certs" {
		t.Fatalf("path = %q, want %q", gotPath, "/realms/r/protocol/openid-connect/certs")
	}

	// Port drift: issuer has an explicit port but the discovered endpoint omits
	// it (URL.Host includes the port; Hostname() does not) — should still rewrite.
	gotPath = ""
	transportPortDrift, err := newOIDCHTTPTransport(config.Config{
		OIDCIssuerURL:   "https://issuer.example.com:8443/realms/r",
		OIDCInternalURL: server.URL,
	}, http.DefaultTransport)
	if err != nil {
		t.Fatalf("newOIDCHTTPTransport() error = %v", err)
	}

	portDriftReq, err := http.NewRequest(http.MethodGet, "https://issuer.example.com/realms/r/protocol/openid-connect/token", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	portDriftResp, err := (&http.Client{Transport: transportPortDrift}).Do(portDriftReq)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer portDriftResp.Body.Close()
	_, _ = io.Copy(io.Discard, portDriftResp.Body)

	if portDriftResp.StatusCode != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", portDriftResp.StatusCode, http.StatusNoContent)
	}
	if gotPath != "/realms/r/protocol/openid-connect/token" {
		t.Fatalf("path = %q, want %q", gotPath, "/realms/r/protocol/openid-connect/token")
	}

	// Verify: a request to an unrelated host is NOT rewritten.
	// Use a capturing round tripper to avoid DNS resolution of the fake host.
	var capturedReq *http.Request
	capturingTransport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		capturedReq = req
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})
	transportUnrelated, err := newOIDCHTTPTransport(config.Config{
		OIDCIssuerURL:   "https://issuer.example.com/realms/r",
		OIDCInternalURL: server.URL,
	}, capturingTransport)
	if err != nil {
		t.Fatalf("newOIDCHTTPTransport() error = %v", err)
	}

	unrelatedReq, err := http.NewRequest(http.MethodGet, "https://other.example.com/realms/r/.well-known/openid-configuration", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	_, _ = (&http.Client{Transport: transportUnrelated}).Do(unrelatedReq)

	// The request should NOT have been rewritten — scheme, host, path unchanged
	if capturedReq.URL.Scheme != "https" {
		t.Fatalf("scheme = %q, want %q", capturedReq.URL.Scheme, "https")
	}
	if capturedReq.URL.Host != "other.example.com" {
		t.Fatalf("host = %q, want %q", capturedReq.URL.Host, "other.example.com")
	}
	if capturedReq.URL.Path != "/realms/r/.well-known/openid-configuration" {
		t.Fatalf("path = %q, want %q", capturedReq.URL.Path, "/realms/r/.well-known/openid-configuration")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewOIDCHTTPClientPreservesIssuerPathWhenInternalBaseHasNoPath(t *testing.T) {
	var gotPath string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewOIDCHTTPClient(config.Config{
		OIDCIssuerURL:   "http://localhost:18081/application/o/sekai-admin-web/",
		OIDCInternalURL: server.URL,
	}, time.Second)
	if err != nil {
		t.Fatalf("NewOIDCHTTPClient() error = %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, "http://localhost:18081/application/o/sekai-admin-web/.well-known/openid-configuration", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if gotPath != "/application/o/sekai-admin-web/.well-known/openid-configuration" {
		t.Fatalf("request path = %q, want %q", gotPath, "/application/o/sekai-admin-web/.well-known/openid-configuration")
	}
}
