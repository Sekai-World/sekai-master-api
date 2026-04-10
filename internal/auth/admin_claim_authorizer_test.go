package auth

import (
	"errors"
	"testing"
)

func TestAdminClaimAuthorizerDisabledWithoutConfig(t *testing.T) {
	authorizer := NewAdminClaimAuthorizer("", nil)

	if err := authorizer.Authorize(map[string]any{}); err != nil {
		t.Fatalf("Authorize() error = %v, want nil", err)
	}
}

func TestAdminClaimAuthorizerMatchesStringArrayClaim(t *testing.T) {
	authorizer := NewAdminClaimAuthorizer("groups", []string{"sekai-admin"})

	err := authorizer.Authorize(map[string]any{
		"groups": []any{"sekai-admin", "other"},
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil", err)
	}
}

func TestAdminClaimAuthorizerMatchesNestedClaimPath(t *testing.T) {
	authorizer := NewAdminClaimAuthorizer("realm_access.roles", []string{"admin"})

	err := authorizer.Authorize(map[string]any{
		"realm_access": map[string]any{
			"roles": []any{"user", "admin"},
		},
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil", err)
	}
}

func TestAdminClaimAuthorizerMatchesScopeString(t *testing.T) {
	authorizer := NewAdminClaimAuthorizer("scope", []string{"sekai-admin"})

	err := authorizer.Authorize(map[string]any{
		"scope": "openid profile sekai-admin",
	})
	if err != nil {
		t.Fatalf("Authorize() error = %v, want nil", err)
	}
}

func TestAdminClaimAuthorizerRejectsMissingClaimValue(t *testing.T) {
	authorizer := NewAdminClaimAuthorizer("groups", []string{"sekai-admin"})

	err := authorizer.Authorize(map[string]any{
		"groups": []any{"viewer"},
	})
	if !errors.Is(err, ErrInsufficientClaims) {
		t.Fatalf("Authorize() error = %v, want %v", err, ErrInsufficientClaims)
	}
}
