package auth

import (
	"errors"
	"fmt"
	"strings"
)

var ErrInsufficientClaims = errors.New("insufficient claims")

type AdminClaimAuthorizer struct {
	claimPath     string
	allowedValues map[string]struct{}
}

type AdminClaimDebug struct {
	ClaimPath     string
	ClaimValues   []string
	MatchedValues []string
}

func NewAdminClaimAuthorizer(claimPath string, allowedValues []string) *AdminClaimAuthorizer {
	normalizedPath := strings.TrimSpace(claimPath)
	normalizedValues := make(map[string]struct{}, len(allowedValues))
	for _, value := range allowedValues {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalizedValues[trimmed] = struct{}{}
	}

	return &AdminClaimAuthorizer{
		claimPath:     normalizedPath,
		allowedValues: normalizedValues,
	}
}

func (authorizer *AdminClaimAuthorizer) Enabled() bool {
	return authorizer != nil && authorizer.claimPath != "" && len(authorizer.allowedValues) > 0
}

func (authorizer *AdminClaimAuthorizer) Authorize(claims map[string]any) error {
	debug, ok := authorizer.debug(claims)
	if !authorizer.Enabled() {
		return nil
	}
	if !ok {
		return ErrInsufficientClaims
	}

	if len(debug.MatchedValues) > 0 {
		return nil
	}

	return ErrInsufficientClaims
}

func (authorizer *AdminClaimAuthorizer) Debug(claims map[string]any) *AdminClaimDebug {
	debug, ok := authorizer.debug(claims)
	if !ok {
		return nil
	}
	return debug
}

func (authorizer *AdminClaimAuthorizer) debug(claims map[string]any) (*AdminClaimDebug, bool) {
	if !authorizer.Enabled() {
		return nil, false
	}

	rawClaim, ok := claimValueByPath(claims, authorizer.claimPath)
	if !ok {
		return nil, false
	}

	values := claimValues(rawClaim, authorizer.claimPath)
	matched := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := authorizer.allowedValues[value]; ok {
			matched = append(matched, value)
		}
	}

	return &AdminClaimDebug{
		ClaimPath:     authorizer.claimPath,
		ClaimValues:   values,
		MatchedValues: matched,
	}, true
}

func claimValueByPath(claims map[string]any, path string) (any, bool) {
	if len(claims) == 0 {
		return nil, false
	}

	current := any(claims)
	for _, part := range strings.Split(strings.TrimSpace(path), ".") {
		key := strings.TrimSpace(part)
		if key == "" {
			return nil, false
		}

		asMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		next, ok := asMap[key]
		if !ok {
			return nil, false
		}

		current = next
	}

	return current, true
}

func claimValues(raw any, claimPath string) []string {
	switch value := raw.(type) {
	case string:
		return splitClaimString(value, claimPath)
	case []string:
		values := make([]string, 0, len(value))
		for _, item := range value {
			values = append(values, splitClaimString(item, claimPath)...)
		}
		return values
	case []any:
		values := make([]string, 0, len(value))
		for _, item := range value {
			values = append(values, claimValues(item, claimPath)...)
		}
		return values
	default:
		if raw == nil {
			return nil
		}

		trimmed := strings.TrimSpace(fmt.Sprintf("%v", raw))
		if trimmed == "" {
			return nil
		}
		return []string{trimmed}
	}
}

func splitClaimString(value string, claimPath string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	if !scopeLikeClaim(claimPath) {
		return []string{trimmed}
	}

	fields := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	values := make([]string, 0, len(fields))
	for _, field := range fields {
		part := strings.TrimSpace(field)
		if part == "" {
			continue
		}
		values = append(values, part)
	}
	return values
}

func scopeLikeClaim(claimPath string) bool {
	parts := strings.Split(strings.TrimSpace(claimPath), ".")
	if len(parts) == 0 {
		return false
	}

	last := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	return last == "scope" || last == "scp"
}
