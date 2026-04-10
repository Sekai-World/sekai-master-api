package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"sekai-master-api/internal/config"
)

var ErrInvalidToken = errors.New("invalid token")

type TokenVerifier interface {
	Verify(ctx context.Context, rawToken string) (map[string]any, error)
}

type OIDCVerifier struct {
	verifier *oidc.IDTokenVerifier
}

const (
	oidcDiscoveryAttemptTimeout = 5 * time.Second
	oidcDiscoveryRetryTimeout   = 30 * time.Second
	oidcDiscoveryRetryInterval  = 2 * time.Second
)

func discoverOIDCProvider(ctx context.Context, cfg config.Config) (*oidc.Provider, error) {
	return discoverOIDCProviderWithRetry(ctx, cfg, oidcDiscoveryRetryTimeout, oidcDiscoveryRetryInterval)
}

func discoverOIDCProviderWithRetry(ctx context.Context, cfg config.Config, totalTimeout time.Duration, retryInterval time.Duration) (*oidc.Provider, error) {
	deadline := time.Now().Add(totalTimeout)
	var lastErr error

	for {
		provider, err := discoverOIDCProviderOnce(ctx, cfg)
		if err == nil {
			return provider, nil
		}
		if !shouldRetryOIDCDiscovery(err) {
			return nil, err
		}

		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, ctx.Err()
		case <-time.After(retryInterval):
		}
	}
}

func discoverOIDCProviderOnce(ctx context.Context, cfg config.Config) (*oidc.Provider, error) {
	provider, err := discoverOIDCProviderWithClient(ctx, cfg, oidcDiscoveryAttemptTimeout, true)
	if err == nil {
		return provider, nil
	}
	if !shouldFallbackToPublicOIDC(cfg) {
		return nil, err
	}

	fallbackProvider, fallbackErr := discoverOIDCProviderWithClient(ctx, cfg, oidcDiscoveryAttemptTimeout, false)
	if fallbackErr == nil {
		return fallbackProvider, nil
	}

	return nil, fmt.Errorf("oidc discovery via internal url failed: %w; fallback via issuer url failed: %v", err, fallbackErr)
}

func discoverOIDCProviderWithClient(ctx context.Context, cfg config.Config, timeout time.Duration, preferInternal bool) (*oidc.Provider, error) {
	client, err := newOIDCDiscoveryClient(cfg, timeout, preferInternal)
	if err != nil {
		return nil, err
	}

	discoveryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	discoveryCtx = oidc.ClientContext(discoveryCtx, client)
	provider, err := oidc.NewProvider(discoveryCtx, cfg.NormalizedOIDCIssuerURL())
	if err != nil {
		return nil, err
	}
	return provider, nil
}

func newOIDCDiscoveryClient(cfg config.Config, timeout time.Duration, preferInternal bool) (*http.Client, error) {
	if !preferInternal {
		return NewPublicOIDCHTTPClient(timeout), nil
	}

	return NewOIDCHTTPClient(cfg, timeout)
}

func shouldFallbackToPublicOIDC(cfg config.Config) bool {
	internal := strings.TrimSpace(cfg.NormalizedOIDCInternalURL())
	if internal == "" {
		return false
	}

	public := strings.TrimSpace(cfg.NormalizedOIDCIssuerURL())
	if public == "" {
		return false
	}

	publicBaseURL, err := parseAbsoluteURL(public, "OIDC_ISSUER_URL")
	if err != nil {
		return false
	}

	internalBaseURL, err := parseAbsoluteURL(internal, "OIDC_INTERNAL_URL")
	if err != nil {
		return false
	}

	return !sameURLOrigin(publicBaseURL, internalBaseURL) || !sameURLPath(publicBaseURL.Path, internalBaseURL.Path)
}

func shouldRetryOIDCDiscovery(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "invalid oidc_issuer_url") || strings.Contains(message, "invalid oidc_internal_url") {
		return false
	}

	return true
}

func ResolveOIDCEndpoints(ctx context.Context, cfg config.Config) (string, string, error) {
	authURL := cfg.OIDCAuthorizationURL()
	tokenURL := cfg.OIDCTokenEndpoint()
	if authURL != "" && tokenURL != "" {
		return authURL, tokenURL, nil
	}

	provider, err := discoverOIDCProvider(ctx, cfg)
	if err != nil {
		return "", "", err
	}

	endpoint := provider.Endpoint()
	if authURL == "" {
		authURL = endpoint.AuthURL
	}
	if tokenURL == "" {
		tokenURL = endpoint.TokenURL
	}

	return authURL, tokenURL, nil
}

func NewOIDCVerifier(ctx context.Context, cfg config.Config) (*OIDCVerifier, error) {
	provider, err := discoverOIDCProvider(ctx, cfg)
	if err != nil {
		return nil, err
	}

	oidcConfig := &oidc.Config{
		SkipIssuerCheck: cfg.OIDCSkipIssuer,
	}

	if cfg.OIDCSkipAudCheck {
		oidcConfig.SkipClientIDCheck = true
	} else {
		oidcConfig.ClientID = cfg.OIDCAudience
	}

	return &OIDCVerifier{
		verifier: provider.Verifier(oidcConfig),
	}, nil
}

func (verifier *OIDCVerifier) Verify(ctx context.Context, rawToken string) (map[string]any, error) {
	token, err := verifier.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, ErrInvalidToken
	}

	claims := map[string]any{}
	if err := token.Claims(&claims); err != nil {
		return nil, ErrInvalidToken
	}

	return claims, nil
}
