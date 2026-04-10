package middleware

import "testing"

func TestSanitizeQueryStringRedactsAccessToken(t *testing.T) {
	query := sanitizeQueryString("access_token=secret-token&foo=bar")

	if query != "access_token=%5BREDACTED%5D&foo=bar" && query != "foo=bar&access_token=%5BREDACTED%5D" {
		t.Fatalf("expected redacted access token query, got %q", query)
	}
}

func TestSanitizeQueryStringKeepsPlainQuery(t *testing.T) {
	query := sanitizeQueryString("foo=bar&baz=qux")

	if query != "baz=qux&foo=bar" && query != "foo=bar&baz=qux" {
		t.Fatalf("expected unchanged query values, got %q", query)
	}
}
