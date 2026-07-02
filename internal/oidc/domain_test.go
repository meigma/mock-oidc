package oidc_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestInstant covers UTC normalization, RFC 3339 parsing, and Add/Unix.
func TestInstant(t *testing.T) {
	t.Parallel()

	loc := time.FixedZone("EST", -5*60*60)
	got := oidc.NewInstant(time.Date(2026, 6, 29, 12, 0, 0, 0, loc))
	assert.Equal(t, time.UTC, got.Time().Location(), "Instant normalizes to UTC")

	parsed, err := oidc.ParseInstant("2026-06-29T12:00:00Z")
	require.NoError(t, err)
	want := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC).Unix()
	assert.Equal(t, want, parsed.Unix())
	assert.Equal(t, parsed.Unix()+3600, parsed.Add(time.Hour).Unix())

	_, err = oidc.ParseInstant("not-a-timestamp")
	require.Error(t, err)
}

// TestFixedClock confirms the pinned clock returns its instant and satisfies the
// Clock port.
func TestFixedClock(t *testing.T) {
	t.Parallel()

	at := oidc.NewInstant(time.Unix(1000, 0))
	var clock oidc.Clock = oidc.NewFixedClock(at)
	assert.Equal(t, at, clock.Now())
}

// TestScopes covers ordered dedup, non-OIDC filtering, and String round-trip.
func TestScopes(t *testing.T) {
	t.Parallel()

	s := oidc.ParseScopes("  openid  api  openid   read ")
	assert.Equal(t, oidc.Scopes{"openid", "api", "read"}, s, "blanks dropped, dups removed, order kept")
	assert.Equal(t, "openid api read", s.String())
	assert.Equal(t, oidc.Scopes{"api", "read"}, s.NonOIDC(), "OIDC scopes stripped")
	assert.Empty(t, oidc.ParseScopes(""))
}

// TestCustomClaims covers ordered insertion, replace-in-place, SetIfAbsent, and
// Clone isolation.
func TestCustomClaims(t *testing.T) {
	t.Parallel()

	var c oidc.CustomClaims
	c.Set("acr", "level1")
	c.Set("roles", []oidc.ClaimValue{"admin"})
	c.Set("acr", "level2") // replace preserves original position

	entries := c.Entries()
	require.Len(t, entries, 2)
	assert.Equal(t, "acr", entries[0].Name)
	assert.Equal(t, "level2", entries[0].Value)
	assert.Equal(t, "roles", entries[1].Name)

	assert.False(t, c.SetIfAbsent("acr", "level3"), "present key not overwritten")
	got, ok := c.Get("acr")
	assert.True(t, ok)
	assert.Equal(t, "level2", got)
	assert.True(t, c.SetIfAbsent("tenant", "contoso"))
	assert.Equal(t, 3, c.Len())

	clone := c.Clone()
	clone.Set("acr", "mutated")
	orig, _ := c.Get("acr")
	assert.Equal(t, "level2", orig, "clone mutation must not alias the source")
}

// TestClaimSetBasics confirms the typed registered fields and the pointer-optional
// claims behave as values.
func TestClaimSetBasics(t *testing.T) {
	t.Parallel()

	nonce := oidc.Nonce("5678")
	azp := oidc.ClientID("app")
	tid := "default"
	cs := oidc.ClaimSet{
		Subject:  "app",
		Audience: oidc.Audience{"default"},
		Issuer:   "http://localhost:8080/default",
		Nonce:    &nonce,
		Azp:      &azp,
		Tenant:   &tid,
		Scope:    oidc.ParseScopes("openid api"),
	}
	assert.Equal(t, oidc.Subject("app"), cs.Subject)
	assert.Equal(t, "http://localhost:8080/default", cs.Issuer)
	assert.Equal(t, oidc.Audience{"default"}, cs.Audience)
	assert.Equal(t, oidc.ParseScopes("openid api"), cs.Scope)
	require.NotNil(t, cs.Nonce)
	assert.Equal(t, oidc.Nonce("5678"), *cs.Nonce)
	require.NotNil(t, cs.Azp)
	assert.Equal(t, oidc.ClientID("app"), *cs.Azp)
	require.NotNil(t, cs.Tenant)
	assert.Equal(t, "default", *cs.Tenant)

	var zero oidc.ClaimSet
	assert.Nil(t, zero.Nonce, "absent nonce is nil, not empty string")
	assert.Nil(t, zero.Azp)
}

// TestNewTokenDefaultsType covers the open JOSEType default and kid derivation.
func TestNewTokenDefaultsType(t *testing.T) {
	t.Parallel()

	tok := oidc.NewToken("default", oidc.RS256, "", oidc.ClaimSet{})
	assert.Equal(t, oidc.DefaultJOSEType, tok.Header.Type)
	assert.Equal(t, oidc.KeyID("default"), tok.Header.KeyID)
	assert.Equal(t, oidc.RS256, tok.Header.Algorithm)

	custom := oidc.NewToken("default", oidc.RS256, "at+jwt", oidc.ClaimSet{})
	assert.Equal(t, oidc.JOSEType("at+jwt"), custom.Header.Type, "open typ preserved")
}
