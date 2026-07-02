package oidc_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestDefaultTokenCallbackSubject confirms client_credentials resolves sub to the
// client_id, while other grants use the pre-resolved input subject.
func TestDefaultTokenCallbackSubject(t *testing.T) {
	t.Parallel()

	cb := oidc.NewDefaultTokenCallback("default")
	assert.Equal(t, oidc.IssuerID("default"), cb.IssuerID())

	ccSub := cb.Subject(oidc.CallbackInput{
		Grant:  oidc.GrantClientCredentials,
		Client: oidc.Client{ID: "app", Auth: oidc.ClientAuthNone},
	})
	assert.Equal(t, oidc.Subject("app"), ccSub)

	otherSub := cb.Subject(oidc.CallbackInput{Grant: oidc.GrantPassword, Subject: "alice"})
	assert.Equal(t, oidc.Subject("alice"), otherSub)
}

// TestDefaultTokenCallbackAudience walks the 4-step precedence chain.
func TestDefaultTokenCallbackAudience(t *testing.T) {
	t.Parallel()

	cb := oidc.NewDefaultTokenCallback("default")

	t.Run("fallback to default when nothing configured", func(t *testing.T) {
		t.Parallel()

		aud := cb.Audience(oidc.CallbackInput{Grant: oidc.GrantClientCredentials})
		assert.Equal(t, oidc.Audience{"default"}, aud)
	})

	t.Run("token-exchange audience param wins over scopes", func(t *testing.T) {
		t.Parallel()

		aud := cb.Audience(oidc.CallbackInput{
			Audience: oidc.Audience{"api://target"},
			Scopes:   oidc.ParseScopes("openid resourceX"),
		})
		assert.Equal(t, oidc.Audience{"api://target"}, aud)
	})

	t.Run("non-OIDC scopes when no configured or exchange audience", func(t *testing.T) {
		t.Parallel()

		aud := cb.Audience(oidc.CallbackInput{Scopes: oidc.ParseScopes("openid profile resourceX resourceY")})
		assert.Equal(t, oidc.Audience{"resourceX", "resourceY"}, aud)
	})
}

// TestDefaultTokenCallbackDefaults pins the JWS typ and default expiry.
func TestDefaultTokenCallbackDefaults(t *testing.T) {
	t.Parallel()

	cb := oidc.NewDefaultTokenCallback("default")
	assert.Equal(t, oidc.DefaultJOSEType, cb.TypeHeader(oidc.CallbackInput{}))
	assert.Equal(t, 3600*time.Second, cb.Expiry())
	assert.True(t, cb.Matches(oidc.CallbackInput{}), "default callback always matches")
	assert.Equal(t, 0, cb.ExtraClaims(oidc.CallbackInput{}).Custom.Len())
}

// TestFormParams covers last-wins Get, All, and SpaceJoined.
func TestFormParams(t *testing.T) {
	t.Parallel()

	p := oidc.FormParams{"scope": {"openid", "api"}, "grant_type": {"client_credentials"}}
	assert.Equal(t, "api", p.Get("scope"), "last value wins")
	assert.Equal(t, "client_credentials", p.Get("grant_type"))
	assert.Empty(t, p.Get("missing"))
	assert.Equal(t, []string{"openid", "api"}, p.All("scope"))
	assert.Equal(t, "openid api", p.SpaceJoined("scope"))
}

// TestClientRequireClientID covers the effective-client_id invariant.
func TestClientRequireClientID(t *testing.T) {
	t.Parallel()

	_, err := oidc.Client{ID: "", Auth: oidc.ClientAuthNone}.RequireClientID()
	var pe *oidc.ProtocolError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, oidc.CodeInvalidClient, pe.Code)
	assert.Equal(t, "client_id cannot be null", pe.Description)

	id, err := oidc.Client{ID: "app"}.RequireClientID()
	require.NoError(t, err)
	assert.Equal(t, oidc.ClientID("app"), id)
}
