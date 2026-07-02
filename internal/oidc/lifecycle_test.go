package oidc_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestTokenTypeHintValid covers the closed token_type_hint enum: access_token and
// refresh_token are recognized; the empty value and any other string are not.
func TestTokenTypeHintValid(t *testing.T) {
	t.Parallel()

	assert.True(t, oidc.TokenHintRefreshToken.Valid())
	assert.True(t, oidc.TokenHintAccessToken.Valid())
	assert.False(t, oidc.TokenTypeHint("").Valid())
	assert.False(t, oidc.TokenTypeHint("id_token").Valid())
}

// TestRefreshCrossIssuerText pins the CORRECTED client-visible cross-issuer text
// exactly — the parity anchor forbids any drift from this string.
func TestRefreshCrossIssuerText(t *testing.T) {
	t.Parallel()

	err := oidc.RefreshCrossIssuer()
	assert.Equal(t, oidc.CodeInvalidGrant, err.Code)
	assert.Equal(t, 400, err.HTTPStatus)
	assert.Equal(t, "refresh_token was issued by a different issuer", err.Description)
}

// TestNewInvalidToken covers the /userinfo verify-fail constructor: invalid_token
// at 401, wrapping the verifier cause so [errors.Is] reaches it.
func TestNewInvalidToken(t *testing.T) {
	t.Parallel()

	cause := errors.New("signature mismatch")
	err := oidc.NewInvalidToken(cause)
	assert.Equal(t, oidc.CodeInvalidToken, err.Code)
	assert.Equal(t, 401, err.HTTPStatus)
	require.ErrorIs(t, err, cause)
}

// TestUnsupportedTokenType covers the /revoke bad-hint constructor:
// unsupported_token_type at 400 carrying the hint in its description.
func TestUnsupportedTokenType(t *testing.T) {
	t.Parallel()

	err := oidc.UnsupportedTokenType(oidc.TokenHintAccessToken)
	assert.Equal(t, oidc.CodeUnsupportedTokenType, err.Code)
	assert.Equal(t, 400, err.HTTPStatus)
	assert.Equal(t, "unsupported token type: access_token", err.Description)
}

// TestIntrospectionFrom covers the active-result mapping: active=true with the
// verified claims and a default token_type of Bearer.
func TestIntrospectionFrom(t *testing.T) {
	t.Parallel()

	claims := oidc.ClaimSet{Subject: "alice", Audience: oidc.Audience{"app"}}
	res := oidc.IntrospectionFrom(claims)
	assert.True(t, res.Active)
	assert.Equal(t, oidc.TokenTypeBearer, res.TokenType)
	assert.Equal(t, claims, res.Claims)
}

// TestInactiveIntrospection covers the unverifiable-token result: active=false,
// carrying no claims (reported at 200, never as an error).
func TestInactiveIntrospection(t *testing.T) {
	t.Parallel()

	res := oidc.InactiveIntrospection()
	assert.False(t, res.Active)
	assert.Equal(t, oidc.ClaimSet{}, res.Claims)
}

// TestNewEndSessionResult covers the logout-outcome constructor: a redirect URI
// yields Redirect()==true; an empty URI yields the plain logged-out page.
func TestNewEndSessionResult(t *testing.T) {
	t.Parallel()

	withURI := oidc.NewEndSessionResult("https://rp.example/after", "xyz")
	assert.True(t, withURI.Redirect())
	assert.Equal(t, "https://rp.example/after", withURI.RedirectURI)
	assert.Equal(t, "xyz", withURI.State)

	none := oidc.NewEndSessionResult("", "")
	assert.False(t, none.Redirect())
}
