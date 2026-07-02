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
