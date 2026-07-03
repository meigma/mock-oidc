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
	assert.False(t, cfg.RateLimitEnabled, "rate limiting is disabled by default for test traffic")
	assert.InDelta(t, defaultRateLimitRPS, cfg.RateLimitRPS, 0.0001)
	assert.Equal(t, defaultRateLimitBurst, cfg.RateLimitBurst)
	assert.False(t, cfg.TracingEnabled, "tracing is opt-in (needs an external collector)")
	assert.True(t, cfg.ControlEnabled, "the /_mock control plane is ON by default")
	assert.Empty(t, cfg.ControlAddr, "control plane co-locates on the API listener by default")
	assert.Empty(t, cfg.ControlToken, "the control-token gate is disabled by default")
	assert.False(t, cfg.TLSEnabled, "plain HTTP by default")
	assert.Empty(t, cfg.TLSCertFile, "no TLS cert by default")
	assert.Empty(t, cfg.TLSKeyFile, "no TLS key by default")
}

// TestLoadListenAddrComposition verifies the SERVER_PORT/PORT precedence composes
// the listen address, and that an explicit --addr overrides it.
func TestLoadListenAddrComposition(t *testing.T) {
	t.Parallel()

	t.Run("server-hostname and server-port compose", func(t *testing.T) {
		t.Parallel()
		vp := viper.New()
		vp.Set("server-hostname", "0.0.0.0")
		vp.Set("server-port", 9123)
		assert.Equal(t, "0.0.0.0:9123", Load(vp).Addr)
	})

	t.Run("empty hostname yields wildcard host", func(t *testing.T) {
		t.Parallel()
		vp := viper.New()
		vp.Set("server-port", 7000)
		assert.Equal(t, ":7000", Load(vp).Addr)
	})

	t.Run("explicit addr wins over port", func(t *testing.T) {
		t.Parallel()
		vp := viper.New()
		vp.Set("addr", ":5555")
		vp.Set("server-port", 9123)
		assert.Equal(t, ":5555", Load(vp).Addr)
	})
}

// TestValidateTLSPairing verifies the cert-and-key-together rule.
func TestValidateTLSPairing(t *testing.T) {
	t.Parallel()

	base := Config{Addr: ":8080", RequestTimeout: time.Second, ShutdownGrace: time.Second, LogFormat: "json"}

	certOnly := base
	certOnly.TLSCertFile = "/x/cert.pem"
	require.Error(t, certOnly.Validate(), "cert without key must fail")

	keyOnly := base
	keyOnly.TLSKeyFile = "/x/key.pem"
	require.Error(t, keyOnly.Validate(), "key without cert must fail")

	both := base
	both.TLSCertFile = "/x/cert.pem"
	both.TLSKeyFile = "/x/key.pem"
	require.NoError(t, both.Validate(), "cert and key together is valid")

	// TLSEnabled with no files is valid at this layer: the OR happens after
	// Validate and self-signs in New.
	enabledNoFiles := base
	enabledNoFiles.TLSEnabled = true
	require.NoError(t, enabledNoFiles.Validate())
}

func TestLoadControlFromFlags(t *testing.T) {
	t.Parallel()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("control-enabled", "false"))
	require.NoError(t, flags.Set("control-addr", ":9100"))
	require.NoError(t, flags.Set("control-token", "s3cret"))

	vp := viper.New()
	require.NoError(t, vp.BindPFlags(flags))

	cfg := Load(vp)
	assert.False(t, cfg.ControlEnabled)
	assert.Equal(t, ":9100", cfg.ControlAddr)
	assert.Equal(t, "s3cret", cfg.ControlToken)
}

func TestLoadControlEnvOverride(t *testing.T) {
	t.Setenv("MOCK_OIDC_CONTROL_ENABLED", "false")
	t.Setenv("MOCK_OIDC_CONTROL_TOKEN", "envtoken")

	vp := viper.New()
	vp.SetEnvPrefix("MOCK_OIDC")
	vp.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	vp.AutomaticEnv()

	cfg := Load(vp)
	assert.False(t, cfg.ControlEnabled, "MOCK_OIDC_CONTROL_ENABLED=false disables the control plane")
	assert.Equal(t, "envtoken", cfg.ControlToken)
}

func TestLoadRateLimitFromFlags(t *testing.T) {
	t.Parallel()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterFlags(flags)
	require.NoError(t, flags.Set("rate-limit-enabled", "true"))

	vp := viper.New()
	require.NoError(t, vp.BindPFlags(flags))

	cfg := Load(vp)
	assert.True(t, cfg.RateLimitEnabled)
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("MOCK_OIDC_ADDR", ":9999")
	t.Setenv("MOCK_OIDC_LOG_LEVEL", "debug")
	t.Setenv("MOCK_OIDC_TRUSTED_PROXY_HEADER", "X-Real-IP")

	vp := viper.New()
	vp.SetEnvPrefix("MOCK_OIDC")
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

	controlSameAsAddr := base
	controlSameAsAddr.ControlAddr = base.Addr
	require.Error(t, controlSameAsAddr.Validate())

	controlSameAsMetrics := base
	controlSameAsMetrics.MetricsAddr = ":9090"
	controlSameAsMetrics.ControlAddr = ":9090"
	require.Error(t, controlSameAsMetrics.Validate())

	controlDistinct := base
	controlDistinct.ControlAddr = ":9100"
	require.NoError(t, controlDistinct.Validate())

	negativeTimeout := base
	negativeTimeout.RequestTimeout = -time.Second
	require.Error(t, negativeTimeout.Validate())

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
