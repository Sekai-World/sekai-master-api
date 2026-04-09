package auth

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sekai-master-api/internal/config"
)

func NewZitadelHTTPClient(cfg config.Config, timeout time.Duration) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()

	rewrittenTransport, err := newZitadelHTTPTransport(cfg, transport)
	if err != nil {
		return nil, err
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: rewrittenTransport,
	}, nil
}

func newZitadelHTTPTransport(cfg config.Config, base http.RoundTripper) (http.RoundTripper, error) {
	publicIssuer := cfg.NormalizedZitadelIssuerURL()
	internalBase := cfg.NormalizedZitadelInternalURL()
	if strings.TrimSpace(internalBase) == "" {
		return base, nil
	}

	publicBaseURL, err := parseAbsoluteURL(publicIssuer, "ZITADEL_ISSUER_URL")
	if err != nil {
		return nil, err
	}

	internalBaseURL, err := parseAbsoluteURL(internalBase, "ZITADEL_INTERNAL_URL")
	if err != nil {
		return nil, err
	}

	if sameURLOrigin(publicBaseURL, internalBaseURL) && sameURLPath(publicBaseURL.Path, internalBaseURL.Path) {
		return base, nil
	}

	return &zitadelRoutingTransport{
		base:            base,
		publicBaseURL:   publicBaseURL,
		internalBaseURL: internalBaseURL,
	}, nil
}

type zitadelRoutingTransport struct {
	base            http.RoundTripper
	publicBaseURL   *url.URL
	internalBaseURL *url.URL
}

func (transport *zitadelRoutingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil || !transport.matches(req.URL) {
		return transport.base.RoundTrip(req)
	}

	rewrittenReq := req.Clone(req.Context())
	rewrittenReq.URL = cloneURL(req.URL)
	rewrittenReq.Host = req.Host
	if rewrittenReq.Host == "" {
		rewrittenReq.Host = req.URL.Host
	}

	rewrittenReq.URL.Scheme = transport.internalBaseURL.Scheme
	rewrittenReq.URL.Host = transport.internalBaseURL.Host
	rewrittenReq.URL.Path = rewriteURLPath(transport.publicBaseURL.Path, transport.internalBaseURL.Path, req.URL.Path)
	if req.URL.RawPath != "" {
		rewrittenReq.URL.RawPath = rewriteURLPath(transport.publicBaseURL.EscapedPath(), transport.internalBaseURL.EscapedPath(), req.URL.RawPath)
	}

	return transport.base.RoundTrip(rewrittenReq)
}

func (transport *zitadelRoutingTransport) matches(target *url.URL) bool {
	return strings.EqualFold(target.Scheme, transport.publicBaseURL.Scheme) &&
		strings.EqualFold(target.Host, transport.publicBaseURL.Host)
}

func parseAbsoluteURL(raw string, name string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", name, err)
	}
	if parsed == nil || !parsed.IsAbs() || strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("invalid %s: expected absolute URL", name)
	}
	return parsed, nil
}

func sameURLOrigin(left *url.URL, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Host, right.Host)
}

func sameURLPath(left string, right string) bool {
	return strings.TrimRight(left, "/") == strings.TrimRight(right, "/")
}

func cloneURL(original *url.URL) *url.URL {
	if original == nil {
		return nil
	}

	cloned := *original
	return &cloned
}

func rewriteURLPath(publicBasePath string, internalBasePath string, requestPath string) string {
	normalizedPublic := strings.TrimRight(publicBasePath, "/")
	normalizedInternal := strings.TrimRight(internalBasePath, "/")

	suffix := requestPath
	if normalizedPublic != "" && strings.HasPrefix(requestPath, normalizedPublic) {
		suffix = strings.TrimPrefix(requestPath, normalizedPublic)
	}

	if normalizedInternal == "" {
		if suffix == "" {
			return "/"
		}
		if strings.HasPrefix(suffix, "/") {
			return suffix
		}
		return "/" + suffix
	}

	if suffix == "" {
		return normalizedInternal
	}
	if strings.HasPrefix(suffix, "/") {
		return normalizedInternal + suffix
	}
	return normalizedInternal + "/" + suffix
}
