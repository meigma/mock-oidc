package oidc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestNewCapturedRequest confirms the edge constructor derives the path and
// first-segment issuer from a raw URL without net/url, and preserves the raw body
// bytes verbatim.
func TestNewCapturedRequest(t *testing.T) {
	t.Parallel()

	body := []byte("grant_type=client_credentials&client_id=svc")
	header := map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}}
	query := map[string][]string{"x": {"1"}}

	tests := []struct {
		name       string
		rawURL     string
		wantPath   string
		wantIssuer oidc.IssuerID
	}{
		{"relative path with query", "/default/token?x=1", "/default/token", "default"},
		{"absolute url", "http://host:8080/other/token?x=1", "/other/token", "other"},
		{"path with fragment", "/acme/authorize#frag", "/acme/authorize", "acme"},
		{"root only", "/", "/", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := oidc.NewCapturedRequest("POST", tc.rawURL, header, query, body)
			assert.Equal(t, "POST", got.Method)
			assert.Equal(t, tc.rawURL, got.URL)
			assert.Equal(t, tc.wantPath, got.Path)
			assert.Equal(t, tc.wantIssuer, got.Issuer)
			assert.Equal(t, body, got.Body, "raw body bytes are preserved verbatim")
			assert.Equal(t, header, got.Header)
			assert.Equal(t, query, got.Query)
		})
	}
}

// TestCaptureFilterMatches covers the issuer + endpoint narrowing (zero value =
// match everything).
func TestCaptureFilterMatches(t *testing.T) {
	t.Parallel()

	req := oidc.NewCapturedRequest("POST", "/default/token", nil, nil, nil)

	assert.True(t, oidc.CaptureFilter{}.Matches(req), "zero filter matches everything")
	assert.True(t, oidc.CaptureFilter{Issuer: "default"}.Matches(req))
	assert.True(t, oidc.CaptureFilter{Endpoint: "token"}.Matches(req))
	assert.True(t, oidc.CaptureFilter{Issuer: "default", Endpoint: "token"}.Matches(req))

	assert.False(t, oidc.CaptureFilter{Issuer: "other"}.Matches(req))
	assert.False(t, oidc.CaptureFilter{Endpoint: "authorize"}.Matches(req))
}
