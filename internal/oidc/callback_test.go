package oidc_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestDefaultTokenCallbackSubject confirms client_credentials resolves sub to the
// client_id, other grants use the pre-resolved input subject, and a fully
// unresolved subject falls back to a per-callback UUID (never a sub-less token).
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

	// Non-interactive authorization_code has no login subject and nothing
	// configured: the callback must still mint a subject (upstream parity is
	// a UUID default), stable for this callback and distinct across callbacks.
	empty := oidc.CallbackInput{Grant: oidc.GrantAuthorizationCode, Client: oidc.Client{ID: "app"}}
	fallback := cb.Subject(empty)
	assert.NotEmpty(t, fallback)
	assert.Equal(t, fallback, cb.Subject(empty), "fallback is stable per callback")
	assert.NotEqual(t, fallback, oidc.NewDefaultTokenCallback("other").Subject(empty),
		"fallback differs across callbacks")
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

// mappingClaims builds an ordered CustomClaims template from name/value pairs.
func mappingClaims(t *testing.T, pairs ...any) oidc.CustomClaims {
	t.Helper()
	require.Zero(t, len(pairs)%2, "pairs must be name,value,...")
	var c oidc.CustomClaims
	for i := 0; i < len(pairs); i += 2 {
		name, ok := pairs[i].(string)
		require.True(t, ok, "claim name must be a string")
		c.Set(name, oidc.ClaimValue(pairs[i+1]))
	}
	return c
}

// mappingCallback builds a single-mapping RequestMappingCallback for issuer
// "default".
func mappingCallback(t *testing.T, m oidc.RequestMapping) oidc.RequestMappingCallback {
	t.Helper()
	cb, err := oidc.NewRequestMappingCallback("default", time.Hour, []oidc.RequestMapping{m})
	require.NoError(t, err)
	return cb
}

// TestRequestMappingMatchMatrix exercises the matcher: "*" wildcard, exact-string
// and full-match regex, a silently-swallowed invalid regex, and the
// un-shadowable client_id param.
func TestRequestMappingMatchMatrix(t *testing.T) {
	t.Parallel()

	claims := mappingClaims(t, "sub", "matched")

	tests := []struct {
		name  string
		param string
		match string
		in    oidc.CallbackInput
		want  bool
	}{
		{
			name:  "wildcard matches a present form param",
			param: "scope", match: "*",
			in:   oidc.CallbackInput{Params: oidc.FormParams{"scope": {"api"}}},
			want: true,
		},
		{
			name:  "wildcard does not match an absent param",
			param: "scope", match: "*",
			in:   oidc.CallbackInput{Params: oidc.FormParams{}},
			want: false,
		},
		{
			name:  "exact-string match",
			param: "acr", match: "Level4",
			in:   oidc.CallbackInput{Params: oidc.FormParams{"acr": {"Level4"}}},
			want: true,
		},
		{
			name:  "regex full-match succeeds",
			param: "acr", match: "Level[0-9]",
			in:   oidc.CallbackInput{Params: oidc.FormParams{"acr": {"Level4"}}},
			want: true,
		},
		{
			name:  "regex is anchored (no partial match)",
			param: "acr", match: "Level",
			in:   oidc.CallbackInput{Params: oidc.FormParams{"acr": {"Level4"}}},
			want: false,
		},
		{
			name:  "invalid regex is swallowed and falls back to exact (no match)",
			param: "acr", match: "Level[4",
			in:   oidc.CallbackInput{Params: oidc.FormParams{"acr": {"Level4"}}},
			want: false,
		},
		{
			name:  "invalid regex still matches on exact equality",
			param: "acr", match: "Level[4",
			in:   oidc.CallbackInput{Params: oidc.FormParams{"acr": {"Level[4"}}},
			want: true,
		},
		{
			name:  "client_id matches the authenticated client, not a form param",
			param: "client_id", match: "real",
			in: oidc.CallbackInput{
				Client: oidc.Client{ID: "real"},
				Params: oidc.FormParams{"client_id": {"evil"}},
			},
			want: true,
		},
		{
			name:  "client_id form param cannot shadow the authenticated client",
			param: "client_id", match: "evil",
			in: oidc.CallbackInput{
				Client: oidc.Client{ID: "real"},
				Params: oidc.FormParams{"client_id": {"evil"}},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cb := mappingCallback(t, oidc.RequestMapping{Param: tc.param, Match: tc.match, Claims: claims})
			assert.Equal(t, tc.want, cb.Matches(tc.in))
		})
	}
}

// TestRequestMappingTemplating covers ${...} substitution: string leaves are
// templated (multi-values space-joined), nested list leaves are templated,
// non-string leaves pass through, unknown keys stay literal, and ${client_id}
// resolves to the authenticated client. subject and audience are derived from the
// templated sub/aud claims.
func TestRequestMappingTemplating(t *testing.T) {
	t.Parallel()

	claims := mappingClaims(t,
		"sub", "${subject}",
		"aud", "${resource}",
		"roles", []oidc.ClaimValue{"${role}"},
		"level", float64(4), // non-string leaf: not templated
		"owner", "${client_id}",
		"missing", "${nope}", // unknown key stays literal
	)
	cb := mappingCallback(t, oidc.RequestMapping{Param: "resource", Match: "*", Claims: claims})

	in := oidc.CallbackInput{
		Client:  oidc.Client{ID: "the-client"},
		Subject: "alice",
		Params: oidc.FormParams{
			"resource": {"api://one"},
			"role":     {"admin", "teller"}, // space-joined
		},
	}

	assert.Equal(t, oidc.Subject("alice"), cb.Subject(in))
	assert.Equal(t, oidc.Audience{"api://one"}, cb.Audience(in))

	extra := cb.ExtraClaims(in).Custom
	roles, ok := extra.Get("roles")
	require.True(t, ok)
	assert.Equal(t, []oidc.ClaimValue{"admin teller"}, roles)

	level, ok := extra.Get("level")
	require.True(t, ok)
	levelNum, ok := level.(float64)
	require.True(t, ok, "non-string leaves keep their type")
	assert.InDelta(t, 4.0, levelNum, 0, "non-string leaves are not templated")

	owner, ok := extra.Get("owner")
	require.True(t, ok)
	assert.Equal(t, "the-client", owner, "${client_id} resolves to the authenticated client")

	missing, ok := extra.Get("missing")
	require.True(t, ok)
	assert.Equal(t, "${nope}", missing, "unknown template keys are left literal")
}

// TestRequestMappingCallbackNoTidNoAzp confirms the request-mapping callback's
// extra claims never carry tid or azp — only DefaultTokenCallback stamps those.
func TestRequestMappingCallbackNoTidNoAzp(t *testing.T) {
	t.Parallel()

	claims := mappingClaims(t, "tid", "forged", "azp", "forged", "acr", "Level4")
	cb := mappingCallback(t, oidc.RequestMapping{Param: "scope", Match: "*", Claims: claims})
	in := oidc.CallbackInput{Params: oidc.FormParams{"scope": {"api"}}}

	extra := cb.ExtraClaims(in).Custom
	_, hasTid := extra.Get("tid")
	_, hasAzp := extra.Get("azp")
	assert.False(t, hasTid, "tid is a registered claim, never added by a mapping")
	assert.False(t, hasAzp, "azp is a registered claim, never added by a mapping")
	acr, ok := extra.Get("acr")
	require.True(t, ok)
	assert.Equal(t, "Level4", acr)
}

// TestNewScenario covers the one-shot scenario constructor: nil is rejected and a
// wrapped callback exposes its issuer.
func TestNewScenario(t *testing.T) {
	t.Parallel()

	_, err := oidc.NewScenario(nil)
	require.ErrorIs(t, err, oidc.ErrNilScenarioCallback)

	sc, err := oidc.NewScenario(oidc.NewDefaultTokenCallback("acme"))
	require.NoError(t, err)
	assert.Equal(t, oidc.IssuerID("acme"), sc.IssuerID())
	assert.Equal(t, oidc.IssuerID("acme"), sc.Callback.IssuerID())
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
