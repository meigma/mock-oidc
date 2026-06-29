// Package config defines the API server's runtime configuration, loaded from
// flags and TEMPLATE_GO_API_* environment variables via Viper.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	defaultAddr              = ":8080"
	defaultMetricsAddr       = ":9090"
	defaultReadTimeout       = 5 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultWriteTimeout      = 10 * time.Second
	defaultIdleTimeout       = 120 * time.Second
	defaultRequestTimeout    = 15 * time.Second
	defaultShutdownGrace     = 15 * time.Second
	defaultLogLevel          = "info"
	defaultLogFormat         = "json"
	defaultDBMaxConns        = 0
	// defaultAuthzEnabled is true: the todo routes now carry their authorization
	// declarations and the engine merges the base policies with each slice's
	// Contribution, so the deny-by-default middleware protects the API out of the
	// box. Operators set it false as an escape hatch to bypass authorization
	// entirely (incremental adoption or local debugging).
	defaultAuthzEnabled = true
	// defaultRateLimitEnabled is true: the API is rate limited out of the box
	// (per client IP, pre-auth), a secure default that also shields the
	// credential store from anonymous floods. Operators set it false to disable
	// throttling entirely.
	defaultRateLimitEnabled = true
	// defaultRateLimitRPS is the sustained per-client request rate (requests per
	// second). It is deliberately generous so local development and the demo
	// stack are not throttled; tune it down for production.
	defaultRateLimitRPS = 10.0
	// defaultRateLimitBurst is the per-client token-bucket depth: how many
	// requests a client may make in a burst before the sustained rate applies.
	defaultRateLimitBurst = 20
	// defaultTracingEnabled is false: distributed tracing requires an external
	// OpenTelemetry collector, so it is opt-in. Enable it and configure the
	// exporter via the standard OTEL_* environment variables.
	defaultTracingEnabled = false
)

// Config holds runtime settings for the API server.
type Config struct {
	// Addr is the host:port the HTTP server listens on.
	Addr string
	// MetricsAddr is the host:port of the dedicated listener that serves /metrics
	// off the main API surface and its middleware. Empty co-locates /metrics on Addr.
	MetricsAddr string
	// ReadTimeout bounds the time spent reading an entire request.
	ReadTimeout time.Duration
	// ReadHeaderTimeout bounds the time spent reading request headers.
	ReadHeaderTimeout time.Duration
	// WriteTimeout bounds the time spent writing the response.
	WriteTimeout time.Duration
	// IdleTimeout bounds how long an idle keep-alive connection is kept open.
	IdleTimeout time.Duration
	// RequestTimeout bounds per-request processing in the timeout middleware.
	RequestTimeout time.Duration
	// ShutdownGrace bounds graceful shutdown before in-flight requests are dropped.
	ShutdownGrace time.Duration
	// LogLevel is the minimum slog level (debug, info, warn, error).
	LogLevel string
	// LogFormat selects the slog handler (json or text).
	LogFormat string
	// CORSAllowedOrigins lists the origins permitted by the CORS middleware.
	// Empty (the default) disables CORS entirely.
	CORSAllowedOrigins []string
	// TrustedProxyHeader names a proxy-set header (for example X-Real-IP) to
	// read the client IP from. Empty (the default) trusts only the direct TCP
	// peer, which cannot be spoofed.
	TrustedProxyHeader string
	// DatabaseURL is the PostgreSQL connection string. It is required: the
	// template persists exclusively to PostgreSQL.
	DatabaseURL string
	// DBMaxConns caps the PostgreSQL connection pool size. Zero leaves the
	// driver default in place.
	DBMaxConns int32
	// AuthzEnabled is the authorization master switch. It defaults to true now
	// that the routes carry their authorization declarations: the deny-by-default
	// middleware protects the API out of the box. When false the authz middleware
	// is inert (pass-through), the escape hatch for incremental adoption or local
	// debugging.
	AuthzEnabled bool
	// AuthzPolicyDir optionally loads .cedar policy files from a directory
	// instead of the embedded set. Empty (the default) uses the embedded
	// policies.
	AuthzPolicyDir string
	// RateLimitEnabled is the rate-limiting master switch. It defaults to true:
	// the API is throttled per client IP before authentication runs. When false
	// the rate-limit middleware is inert (pass-through).
	RateLimitEnabled bool
	// RateLimitRPS is the sustained per-client request rate, in requests per
	// second, when rate limiting is enabled.
	RateLimitRPS float64
	// RateLimitBurst is the per-client token-bucket depth: the number of requests
	// a client may make in a burst before the sustained RateLimitRPS applies.
	RateLimitBurst int
	// TracingEnabled turns on OpenTelemetry distributed tracing. It defaults to
	// false because tracing needs an external collector; the exporter is then
	// configured via the standard OTEL_* environment variables.
	TracingEnabled bool
}

// RegisterFlags declares the server configuration flags on flags. Binding them
// to a Viper instance makes flags, environment variables, and defaults compose.
func RegisterFlags(flags *pflag.FlagSet) {
	flags.String("addr", defaultAddr, "host:port the HTTP server listens on")
	flags.String(
		"metrics-addr",
		defaultMetricsAddr,
		"host:port for the dedicated /metrics listener; empty serves /metrics on --addr",
	)
	flags.String("log-level", defaultLogLevel, "log level: debug, info, warn, or error")
	flags.String("log-format", defaultLogFormat, "log format: json or text")
	flags.Duration("read-timeout", defaultReadTimeout, "maximum duration for reading an entire request")
	flags.Duration("read-header-timeout", defaultReadHeaderTimeout, "maximum duration for reading request headers")
	flags.Duration("write-timeout", defaultWriteTimeout, "maximum duration before timing out response writes")
	flags.Duration("idle-timeout", defaultIdleTimeout, "maximum time to wait for the next keep-alive request")
	flags.Duration("request-timeout", defaultRequestTimeout, "per-request processing timeout")
	flags.Duration("shutdown-grace", defaultShutdownGrace, "maximum duration to await graceful shutdown")
	flags.StringSlice("cors-allowed-origins", nil, "allowed CORS origins (comma-separated); empty disables CORS")
	flags.String(
		"trusted-proxy-header",
		"",
		"proxy header to read the client IP from (for example X-Real-IP); empty trusts the TCP peer",
	)
	flags.String("database-url", "", "PostgreSQL connection URL (required)")
	flags.Int32("db-max-conns", defaultDBMaxConns, "maximum PostgreSQL pool connections; 0 uses the driver default")
	flags.Bool(
		"authz-enabled",
		defaultAuthzEnabled,
		"enable the authorization middleware (deny-by-default); false bypasses it entirely",
	)
	flags.String(
		"authz-policy-dir",
		"",
		"directory of .cedar policy files to load instead of the embedded policies; empty uses the embedded set",
	)
	flags.Bool(
		"rate-limit-enabled",
		defaultRateLimitEnabled,
		"enable per-client (IP) rate limiting; false disables throttling entirely",
	)
	flags.Float64("rate-limit-rps", defaultRateLimitRPS, "sustained per-client request rate (requests per second)")
	flags.Int("rate-limit-burst", defaultRateLimitBurst, "per-client burst size (token-bucket depth)")
	flags.Bool(
		"tracing-enabled",
		defaultTracingEnabled,
		"enable OpenTelemetry tracing (OTLP); configure the exporter via the standard OTEL_* env vars",
	)
}

// Load reads the server configuration from vp, applying defaults for unset keys.
func Load(vp *viper.Viper) Config {
	setDefaults(vp)

	return Config{
		Addr:               vp.GetString("addr"),
		MetricsAddr:        vp.GetString("metrics-addr"),
		ReadTimeout:        vp.GetDuration("read-timeout"),
		ReadHeaderTimeout:  vp.GetDuration("read-header-timeout"),
		WriteTimeout:       vp.GetDuration("write-timeout"),
		IdleTimeout:        vp.GetDuration("idle-timeout"),
		RequestTimeout:     vp.GetDuration("request-timeout"),
		ShutdownGrace:      vp.GetDuration("shutdown-grace"),
		LogLevel:           vp.GetString("log-level"),
		LogFormat:          vp.GetString("log-format"),
		CORSAllowedOrigins: vp.GetStringSlice("cors-allowed-origins"),
		TrustedProxyHeader: vp.GetString("trusted-proxy-header"),
		DatabaseURL:        vp.GetString("database-url"),
		DBMaxConns:         vp.GetInt32("db-max-conns"),
		AuthzEnabled:       vp.GetBool("authz-enabled"),
		AuthzPolicyDir:     vp.GetString("authz-policy-dir"),
		RateLimitEnabled:   vp.GetBool("rate-limit-enabled"),
		RateLimitRPS:       vp.GetFloat64("rate-limit-rps"),
		RateLimitBurst:     vp.GetInt("rate-limit-burst"),
		TracingEnabled:     vp.GetBool("tracing-enabled"),
	}
}

// Validate checks that the configuration is internally consistent.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return errors.New("addr must not be empty")
	}
	if c.MetricsAddr != "" && c.MetricsAddr == c.Addr {
		return errors.New("metrics-addr must differ from addr")
	}
	if c.RequestTimeout <= 0 {
		return errors.New("request-timeout must be positive")
	}
	if c.ShutdownGrace <= 0 {
		return errors.New("shutdown-grace must be positive")
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		return fmt.Errorf("log-format must be %q or %q, got %q", "json", "text", c.LogFormat)
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return errors.New("database-url is required")
	}
	if c.RateLimitEnabled {
		if c.RateLimitRPS <= 0 {
			return errors.New("rate-limit-rps must be positive when rate limiting is enabled")
		}
		if c.RateLimitBurst <= 0 {
			return errors.New("rate-limit-burst must be positive when rate limiting is enabled")
		}
	}

	return nil
}

func setDefaults(vp *viper.Viper) {
	vp.SetDefault("addr", defaultAddr)
	vp.SetDefault("metrics-addr", defaultMetricsAddr)
	vp.SetDefault("read-timeout", defaultReadTimeout)
	vp.SetDefault("read-header-timeout", defaultReadHeaderTimeout)
	vp.SetDefault("write-timeout", defaultWriteTimeout)
	vp.SetDefault("idle-timeout", defaultIdleTimeout)
	vp.SetDefault("request-timeout", defaultRequestTimeout)
	vp.SetDefault("shutdown-grace", defaultShutdownGrace)
	vp.SetDefault("log-level", defaultLogLevel)
	vp.SetDefault("log-format", defaultLogFormat)
	vp.SetDefault("cors-allowed-origins", []string{})
	vp.SetDefault("trusted-proxy-header", "")
	vp.SetDefault("database-url", "")
	vp.SetDefault("db-max-conns", defaultDBMaxConns)
	vp.SetDefault("authz-enabled", defaultAuthzEnabled)
	vp.SetDefault("authz-policy-dir", "")
	vp.SetDefault("rate-limit-enabled", defaultRateLimitEnabled)
	vp.SetDefault("rate-limit-rps", defaultRateLimitRPS)
	vp.SetDefault("rate-limit-burst", defaultRateLimitBurst)
	vp.SetDefault("tracing-enabled", defaultTracingEnabled)
}
