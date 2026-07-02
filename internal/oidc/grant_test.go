package oidc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestGrantTypeExhaustive pins the closed grant set: every member is Valid and
// round-trips through ParseGrantType, and the count tripwire fails if a grant is
// added without updating the set. It also asserts the typed-error contract for
// the request-path parser (blank vs unknown).
func TestGrantTypeExhaustive(t *testing.T) {
	t.Parallel()

	all := []oidc.GrantType{
		oidc.GrantAuthorizationCode, oidc.GrantClientCredentials, oidc.GrantPassword,
		oidc.GrantRefreshToken, oidc.GrantJWTBearer, oidc.GrantTokenExchange,
	}
	require.Len(t, all, 6) // tripwire: a new grant must update this and Valid()/Parse

	for _, g := range all {
		assert.Truef(t, g.Valid(), "%s missing from Valid()", g)
		got, err := oidc.ParseGrantType(string(g))
		require.NoErrorf(t, err, "ParseGrantType(%q)", g)
		assert.Equal(t, g, got) // round-trip
	}

	var pe *oidc.ProtocolError

	_, err := oidc.ParseGrantType("nonsense")
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, oidc.CodeInvalidGrant, pe.Code)       // unknown -> invalid_grant
	require.ErrorIs(t, err, oidc.ErrUnsupportedGrantType) // wrapped sentinel still matches
	assert.Equal(t, "grant_type nonsense not supported.", pe.Description)

	_, err = oidc.ParseGrantType("")
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, oidc.CodeInvalidRequest, pe.Code) // blank -> invalid_request
}

// TestGrantTokenMatrix pins the per-grant id_token/refresh_token matrix.
func TestGrantTokenMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		grant     oidc.GrantType
		idToken   bool
		refresh   bool
		echoScope bool
		issuedTyp oidc.IssuedTokenType
	}{
		{oidc.GrantAuthorizationCode, true, true, true, ""},
		{oidc.GrantClientCredentials, false, false, true, ""},
		{oidc.GrantPassword, true, false, true, ""},
		{oidc.GrantRefreshToken, true, true, true, ""},
		{oidc.GrantJWTBearer, false, false, true, ""},
		{oidc.GrantTokenExchange, false, false, false, oidc.IssuedTokenAccessToken},
	}
	for _, tc := range tests {
		assert.Equalf(t, tc.idToken, tc.grant.IssuesIDToken(), "%s IssuesIDToken", tc.grant)
		assert.Equalf(t, tc.refresh, tc.grant.IssuesRefreshToken(), "%s IssuesRefreshToken", tc.grant)
		assert.Equalf(t, tc.echoScope, tc.grant.EchoesScope(), "%s EchoesScope", tc.grant)
		assert.Equalf(t, tc.issuedTyp, tc.grant.IssuedTokenType(), "%s IssuedTokenType", tc.grant)
	}

	assert.False(t, oidc.GrantType("bogus").Valid())
}
