package signing_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/signing"
)

// TestParseUnverifiedDecodesClaims asserts the unverified-decode facet returns the
// payload's claims WITHOUT any signature check: a token whose signature has been
// corrupted still parses, and its registered + custom claims are recovered.
func TestParseUnverifiedDecodesClaims(t *testing.T) {
	t.Parallel()

	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	// Mint a real token, then corrupt its signature — ParseUnverified must not care.
	token := mintAccessToken(t, p, "default", oidc.RS256, oidc.DefaultJOSEType, "https://issuer.example/default")
	parts := strings.Split(string(token), ".")
	require.Len(t, parts, 3)
	corrupted := oidc.SignedToken(parts[0] + "." + parts[1] + ".tampered")

	claims, err := p.ParseUnverified(context.Background(), corrupted)
	require.NoError(t, err)

	assert.Equal(t, oidc.Subject("alice"), claims.Subject)
	assert.Equal(t, "https://issuer.example/default", claims.Issuer)
	role, ok := claims.Custom.Get("role")
	assert.True(t, ok)
	assert.Equal(t, "admin", role)
}

// TestParseUnverifiedRejectsMalformed asserts every structurally-bad input maps to
// a typed invalid_request *ProtocolError (never a panic, never a bare error).
func TestParseUnverifiedRejectsMalformed(t *testing.T) {
	t.Parallel()

	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	badPayload := base64.RawURLEncoding.EncodeToString([]byte("not-json"))

	tests := []struct {
		name  string
		token oidc.SignedToken
	}{
		{"garbage single segment", "this-is-not-a-jws"},
		{"two segments", "aaaa.bbbb"},
		{"oversized input", oidc.SignedToken("h." + strings.Repeat("a", 64*1024) + ".s")},
		{"undecodable base64 payload", "aaaa.$$$$.bbbb"},
		{"payload is not JSON", oidc.SignedToken("aaaa." + badPayload + ".bbbb")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := p.ParseUnverified(context.Background(), tc.token)
			var perr *oidc.ProtocolError
			require.ErrorAs(t, err, &perr)
			assert.Equal(t, oidc.CodeInvalidRequest, perr.Code)
			assert.Equal(t, 400, perr.HTTPStatus)
		})
	}
}
