package config

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg := Load(viper.New())
	assert.Equal(t, defaultAddr, cfg.Addr)
	assert.Equal(t, defaultMetricsAddr, cfg.MetricsAddr)
	assert.Equal(t, defaultLogLevel, cfg.LogLevel)
	assert.Equal(t, defaultLogFormat, cfg.LogFormat)
	assert.Equal(t, defaultRequestTimeout, cfg.RequestTimeout)
	assert.Empty(t, cfg.CORSAllowedOrigins)
	assert.Empty(t, cfg.TrustedProxyHeader)
	assert.Empty(t, cfg.DatabaseURL)
	assert.Zero(t, cfg.DBMaxConns)
	assert.True(t, cfg.AuthzEnabled, "authz is enabled by default now that the routes are tagged")
	assert.Empty(t, cfg.AuthzPolicyDir)
	assert.True(t, cfg.RateLimitEnabled, "rate limiting is enabled by default")
	assert.InDelta(t, defaultRateLimitRPS, cfg.RateLimitRPS, 0.0001)
	assert.Equal(t, defaultRateLimitBurst, cfg.RateLimitBurst)
	assert.False(t, cfg.TracingEnabled, "tracing is opt-in (needs an external collector)")
}

func TestLoadAuthzFromFlags(t *testing.T) {
	t.Parallel()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("authz-enabled", "true"))
	require.NoError(t, flags.Set("authz-policy-dir", "/etc/policies"))

	vp := viper.New()
	require.NoError(t, vp.BindPFlags(flags))

	cfg := Load(vp)
	assert.True(t, cfg.AuthzEnabled)
	assert.Equal(t, "/etc/policies", cfg.AuthzPolicyDir)
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("TEMPLATE_GO_API_ADDR", ":9999")
	t.Setenv("TEMPLATE_GO_API_LOG_LEVEL", "debug")
	t.Setenv("TEMPLATE_GO_API_TRUSTED_PROXY_HEADER", "X-Real-IP")

	vp := viper.New()
	vp.SetEnvPrefix("TEMPLATE_GO_API")
	vp.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	vp.AutomaticEnv()

	cfg := Load(vp)
	assert.Equal(t, ":9999", cfg.Addr)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, "X-Real-IP", cfg.TrustedProxyHeader)
}

func TestLoadCORSOriginsFromFlags(t *testing.T) {
	t.Parallel()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("cors-allowed-origins", "https://a.example,https://b.example"))

	vp := viper.New()
	require.NoError(t, vp.BindPFlags(flags))

	cfg := Load(vp)
	assert.Equal(t, []string{"https://a.example", "https://b.example"}, cfg.CORSAllowedOrigins)
}

func TestValidate(t *testing.T) {
	t.Parallel()

	base := Config{
		Addr:           ":8080",
		RequestTimeout: time.Second,
		ShutdownGrace:  time.Second,
		LogFormat:      "json",
		DatabaseURL:    "postgres://localhost:5432/app",
	}
	require.NoError(t, base.Validate())

	emptyAddr := base
	emptyAddr.Addr = ""
	require.Error(t, emptyAddr.Validate())

	badFormat := base
	badFormat.LogFormat = "xml"
	require.Error(t, badFormat.Validate())

	metricsSameAsAddr := base
	metricsSameAsAddr.MetricsAddr = base.Addr
	require.Error(t, metricsSameAsAddr.Validate())

	negativeTimeout := base
	negativeTimeout.RequestTimeout = -time.Second
	require.Error(t, negativeTimeout.Validate())

	missingDatabaseURL := base
	missingDatabaseURL.DatabaseURL = ""
	require.Error(t, missingDatabaseURL.Validate())

	// Rate-limit settings are validated only when rate limiting is enabled.
	rateLimited := base
	rateLimited.RateLimitEnabled = true
	rateLimited.RateLimitRPS = 10
	rateLimited.RateLimitBurst = 20
	require.NoError(t, rateLimited.Validate())

	zeroRPS := rateLimited
	zeroRPS.RateLimitRPS = 0
	require.Error(t, zeroRPS.Validate())

	zeroBurst := rateLimited
	zeroBurst.RateLimitBurst = 0
	require.Error(t, zeroBurst.Validate())

	// With rate limiting disabled, non-positive values are ignored.
	disabledIgnoresValues := base
	disabledIgnoresValues.RateLimitEnabled = false
	disabledIgnoresValues.RateLimitRPS = 0
	disabledIgnoresValues.RateLimitBurst = 0
	require.NoError(t, disabledIgnoresValues.Validate())
}
