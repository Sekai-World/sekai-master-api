package auth

import (
	"context"
	"errors"

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

func NewOIDCVerifier(ctx context.Context, cfg config.Config) (*OIDCVerifier, error) {
	provider, err := oidc.NewProvider(ctx, cfg.ZitadelIssuerURL)
	if err != nil {
		return nil, err
	}

	oidcConfig := &oidc.Config{
		SkipIssuerCheck: cfg.ZitadelSkipIssuer,
	}

	if cfg.ZitadelSkipAudCheck {
		oidcConfig.SkipClientIDCheck = true
	} else {
		oidcConfig.ClientID = cfg.ZitadelAudience
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
