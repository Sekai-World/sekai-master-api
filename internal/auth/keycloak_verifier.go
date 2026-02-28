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

type KeycloakVerifier struct {
	verifier *oidc.IDTokenVerifier
}

func NewKeycloakVerifier(ctx context.Context, cfg config.Config) (*KeycloakVerifier, error) {
	provider, err := oidc.NewProvider(ctx, cfg.KeycloakIssuerURL)
	if err != nil {
		return nil, err
	}

	oidcConfig := &oidc.Config{
		SkipIssuerCheck: cfg.KeycloakSkipIssuer,
	}

	if cfg.KeycloakSkipAudCheck {
		oidcConfig.SkipClientIDCheck = true
	} else {
		oidcConfig.ClientID = cfg.KeycloakAudience
	}

	return &KeycloakVerifier{
		verifier: provider.Verifier(oidcConfig),
	}, nil
}

func (verifier *KeycloakVerifier) Verify(ctx context.Context, rawToken string) (map[string]any, error) {
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
