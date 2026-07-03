package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/config"
	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestDefaultSeed verifies the zero-config seed: live clock, RS256, no keys.
func TestDefaultSeed(t *testing.T) {
	t.Parallel()

	seed := config.DefaultSeed()
	assert.False(t, seed.SystemTimeFixed)
	assert.Equal(t, oidc.DefaultSigningAlgorithm, seed.Algorithm)
	assert.Empty(t, seed.InitialKeys)
}

// TestLoadSeedNoSource verifies that with no JSON source configured (and no
// ./config.json present) LoadSeed returns DefaultSeed.
func TestLoadSeedNoSource(t *testing.T) {
	// No t.Parallel: t.Chdir is incompatible with parallel tests.

	// Isolate from any stray ./config.json in the working tree.
	t.Chdir(t.TempDir())

	seed, err := config.LoadSeed(viper.New())
	require.NoError(t, err)
	assert.Equal(t, config.DefaultSeed(), seed)
}

// TestLoadSeedInlineJSON verifies the inline JSON_CONFIG source parses the first
// three fields into the typed seed.
func TestLoadSeedInlineJSON(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{
		"tokenProvider": {
			"systemTime": "2020-01-01T00:00:00Z",
			"keyProvider": {
				"algorithm": "RS512",
				"initialKeys": [{"kty":"RSA","kid":"seed-a"}, {"kty":"RSA","kid":"seed-b"}]
			}
		}
	}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)

	assert.True(t, seed.SystemTimeFixed)
	assert.Equal(t, int64(1577836800), seed.SystemTime.Unix())
	assert.Equal(t, oidc.RS512, seed.Algorithm)
	require.Len(t, seed.InitialKeys, 2)
	// InitialKeys stay opaque (unparsed) bytes at the config edge.
	assert.JSONEq(t, `{"kty":"RSA","kid":"seed-a"}`, string(seed.InitialKeys[0]))
}

// TestLoadSeedTLSFromHTTPServer verifies that an httpServer.ssl object (even
// empty) sets TLSFromHTTPServer, while a bare-string or ssl-less httpServer, and
// an absent httpServer, leave it off (upstream ssl:{} parity).
func TestLoadSeedTLSFromHTTPServer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		json string
		want bool
	}{
		{"empty ssl object enables TLS", `{"httpServer":{"ssl":{}}}`, true},
		{"populated ssl object enables TLS", `{"httpServer":{"type":"NettyWrapper","ssl":{"keyPassword":"x"}}}`, true},
		{"httpServer without ssl stays off", `{"httpServer":{"type":"MockWebServerWrapper"}}`, false},
		{"bare-string httpServer stays off", `{"httpServer":"MockWebServerWrapper"}`, false},
		{"null ssl stays off", `{"httpServer":{"ssl":null}}`, false},
		{"absent httpServer stays off", `{"interactiveLogin":true}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vp := viper.New()
			vp.Set("json-config", tc.json)
			seed, err := config.LoadSeed(vp)
			require.NoError(t, err)
			assert.Equal(t, tc.want, seed.TLSFromHTTPServer)
		})
	}
}

// TestDefaultSeedTLSOff verifies the zero-config seed does not request TLS.
func TestDefaultSeedTLSOff(t *testing.T) {
	t.Parallel()

	assert.False(t, config.DefaultSeed().TLSFromHTTPServer)
}

// TestLoadSeedInteractiveLogin verifies the top-level interactiveLogin flag
// parses into the seed, forcing GET /authorize to render the login page.
func TestLoadSeedInteractiveLogin(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"interactiveLogin": true}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.True(t, seed.InteractiveLogin, "interactiveLogin honored from JSON config")
}

// TestLoadSeedInteractiveLoginDefaultsFalse verifies interactiveLogin defaults to
// false (zero-config = non-interactive authorize issues codes directly).
func TestLoadSeedInteractiveLoginDefaultsFalse(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"tokenProvider":{"keyProvider":{"algorithm":"RS256"}}}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.False(t, seed.InteractiveLogin, "absent interactiveLogin defaults to false")
}

// TestDefaultSeedInteractiveLoginFalse verifies the zero-config seed leaves
// interactive login off.
func TestDefaultSeedInteractiveLoginFalse(t *testing.T) {
	t.Parallel()

	assert.False(t, config.DefaultSeed().InteractiveLogin)
}

// TestLoadSeedRotateRefreshToken verifies the top-level rotateRefreshToken flag
// parses into the seed, enabling refresh-token rotation on redemption.
func TestLoadSeedRotateRefreshToken(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"rotateRefreshToken": true}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.True(t, seed.RotateRefreshToken, "rotateRefreshToken honored from JSON config")
}

// TestLoadSeedRotateRefreshTokenDefaultsFalse verifies rotateRefreshToken
// defaults to false (the same refresh token keeps redeeming).
func TestLoadSeedRotateRefreshTokenDefaultsFalse(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"interactiveLogin": true}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.False(t, seed.RotateRefreshToken, "absent rotateRefreshToken defaults to false")
}

// TestLoadSeedStaticAssetsPath verifies the top-level staticAssetsPath parses
// into the seed (trimmed) so the composition root can mount the /static tree.
func TestLoadSeedStaticAssetsPath(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"staticAssetsPath": "  /srv/assets  "}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.Equal(t, "/srv/assets", seed.StaticAssetsPath, "staticAssetsPath honored and trimmed")
}

// TestLoadSeedStaticAssetsPathDefaultsEmpty verifies an absent staticAssetsPath
// leaves the /static tree unmounted (zero-config default).
func TestLoadSeedStaticAssetsPathDefaultsEmpty(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"interactiveLogin": true}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.Empty(t, seed.StaticAssetsPath, "absent staticAssetsPath stays empty")
}

// TestDefaultSeedRotateRefreshTokenFalse verifies the zero-config seed leaves
// refresh-token rotation off.
func TestDefaultSeedRotateRefreshTokenFalse(t *testing.T) {
	t.Parallel()

	assert.False(t, config.DefaultSeed().RotateRefreshToken)
}

// TestLoadSeedTokenCallbacks verifies the tokenCallbacks parser groups callbacks
// by issuer in declared order and selects the RequestMapping vs Default shape from
// the presence of requestMappings — the same anti-corruption path the control
// scenario DTO uses.
func TestLoadSeedTokenCallbacks(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{
		"tokenCallbacks": [
			{"issuer": "default", "subject": "alice", "claims": {"acr": "Level4"}},
			{"issuer": "default", "requestMappings": [
				{"param": "client_id", "match": "web", "claims": {"aud": "web-api"}}
			]},
			{"issuer": "other", "audience": ["svc"]}
		]
	}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	require.Len(t, seed.IssuerRecords, 2, "two distinct issuers, grouped")

	def := seed.IssuerRecords[0]
	assert.Equal(t, oidc.IssuerID("default"), def.ID)
	require.Len(t, def.Callbacks, 2, "default's two callbacks kept in declared order")
	assert.IsType(t, oidc.DefaultTokenCallback{}, def.Callbacks[0], "no requestMappings -> default callback")
	assert.IsType(t, oidc.RequestMappingCallback{}, def.Callbacks[1], "requestMappings -> mapping callback")

	other := seed.IssuerRecords[1]
	assert.Equal(t, oidc.IssuerID("other"), other.ID)
	require.Len(t, other.Callbacks, 1)
}

// TestLoadSeedTokenCallbacksRejectsReservedIssuer verifies a reserved "_mock"
// issuer in a tokenCallbacks entry is a hard error, not a silently-materialized
// issuer.
func TestLoadSeedTokenCallbacksRejectsReservedIssuer(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"tokenCallbacks": [{"issuer": "_mock"}]}`)

	_, err := config.LoadSeed(vp)
	require.Error(t, err)
	assert.ErrorIs(t, err, oidc.ErrReservedIssuer)
}

// TestLoadSeedLoginTemplates verifies the loginTemplates parser maps entries
// through the domain constructors, preserving declaration order and claims.
func TestLoadSeedLoginTemplates(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{
		"loginTemplates": [
			{"name": "admin-alice", "subject": "alice", "claims": {"email": "alice@example.com"}},
			{"name": "basic-bob", "subject": "bob"}
		]
	}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	require.Equal(t, 2, seed.LoginTemplates.Len())

	all := seed.LoginTemplates.All()
	assert.Equal(t, oidc.LoginTemplateName("admin-alice"), all[0].Name, "declaration order preserved")
	assert.Equal(t, oidc.Subject("alice"), all[0].Subject)
	email, ok := all[0].Claims.Get("email")
	require.True(t, ok)
	assert.Equal(t, "alice@example.com", email)
	assert.Equal(t, oidc.LoginTemplateName("basic-bob"), all[1].Name)
	assert.Equal(t, 0, all[1].Claims.Len(), "claimless template parses to an empty set")
}

// TestLoadSeedLoginTemplatesRejectsInvalid verifies a bad template — blank name,
// blank subject, or a duplicate name — fails LoadSeed (fail-fast startup).
func TestLoadSeedLoginTemplatesRejectsInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		json    string
		wantErr error
		wantIdx string
	}{
		{
			"blank name",
			`{"loginTemplates": [{"name": "", "subject": "alice"}]}`,
			oidc.ErrBlankTemplateName,
			"loginTemplates[0]",
		},
		{
			"blank subject",
			`{"loginTemplates": [{"name": "a", "subject": "x"}, {"name": "b", "subject": "  "}]}`,
			oidc.ErrBlankTemplateSubject,
			"loginTemplates[1]",
		},
		{
			"duplicate name",
			`{"loginTemplates": [{"name": "dup", "subject": "alice"}, {"name": "dup", "subject": "bob"}]}`,
			oidc.ErrDuplicateTemplateName,
			"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			vp := viper.New()
			vp.Set("json-config", tc.json)

			_, err := config.LoadSeed(vp)
			require.Error(t, err)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantIdx != "" {
				assert.Contains(t, err.Error(), tc.wantIdx, "index-tagged error")
			}
		})
	}
}

// TestLoadSeedNoLoginTemplates verifies an absent loginTemplates leaves the seed
// empty (login_hint ignored, no dropdown).
func TestLoadSeedNoLoginTemplates(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"interactiveLogin": true}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.Equal(t, 0, seed.LoginTemplates.Len())
}

// TestLoadSeedNoTokenCallbacks verifies an absent tokenCallbacks leaves the seed's
// IssuerRecords empty (every issuer is zero-config, materialized on demand).
func TestLoadSeedNoTokenCallbacks(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"interactiveLogin": true}`)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.Empty(t, seed.IssuerRecords)
}

// TestLoadSeedInlineOverridesPath verifies JSON_CONFIG (inline) wins over
// JSON_CONFIG_PATH.
func TestLoadSeedInlineOverridesPath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"tokenProvider":{"keyProvider":{"algorithm":"PS256"}}}`), 0o600))

	vp := viper.New()
	vp.Set("json-config", `{"tokenProvider":{"keyProvider":{"algorithm":"RS384"}}}`)
	vp.Set("json-config-path", path)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.Equal(t, oidc.RS384, seed.Algorithm)
}

// TestLoadSeedFromPath verifies a JSON_CONFIG_PATH file is read and parsed.
func TestLoadSeedFromPath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"tokenProvider":{"keyProvider":{"algorithm":"PS512"}}}`), 0o600))

	vp := viper.New()
	vp.Set("json-config-path", path)

	seed, err := config.LoadSeed(vp)
	require.NoError(t, err)
	assert.Equal(t, oidc.PS512, seed.Algorithm)
}

// TestLoadSeedMissingPathFile verifies a named-but-absent JSON_CONFIG_PATH is a
// hard error (it was named explicitly).
func TestLoadSeedMissingPathFile(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config-path", filepath.Join(t.TempDir(), "does-not-exist.json"))

	_, err := config.LoadSeed(vp)
	require.Error(t, err)
}

// TestLoadSeedDefaultConfigFile verifies the implicit ./config.json fallback is
// consulted when neither env source is set.
func TestLoadSeedDefaultConfigFile(t *testing.T) {
	// No t.Parallel: t.Chdir is incompatible with parallel tests.

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"tokenProvider":{"keyProvider":{"algorithm":"RS384"}}}`), 0o600))
	t.Chdir(dir)

	seed, err := config.LoadSeed(viper.New())
	require.NoError(t, err)
	assert.Equal(t, oidc.RS384, seed.Algorithm)
}

// TestLoadSeedRejectsBadAlgorithm verifies a refused algorithm surfaces a typed
// error (never a silent default).
func TestLoadSeedRejectsBadAlgorithm(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"tokenProvider":{"keyProvider":{"algorithm":"EdDSA"}}}`)

	_, err := config.LoadSeed(vp)
	require.Error(t, err)
	assert.ErrorIs(t, err, oidc.ErrUnsupportedAlgorithm)
}

// TestLoadSeedRejectsBadSystemTime verifies an unparseable systemTime is a hard
// error.
func TestLoadSeedRejectsBadSystemTime(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{"tokenProvider":{"systemTime":"not-a-time"}}`)

	_, err := config.LoadSeed(vp)
	require.Error(t, err)
}

// TestLoadSeedRejectsMalformedJSON verifies invalid JSON is a hard error.
func TestLoadSeedRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("json-config", `{not json`)

	_, err := config.LoadSeed(vp)
	require.Error(t, err)
}
