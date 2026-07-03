// Package config defines the mock-oidc server's runtime configuration, loaded
// from flags and MOCK_OIDC_* environment variables (plus upstream-parity env
// aliases) via Viper.
package config

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	defaultAddr = ":8080"
	// defaultServerPort is the listen port when neither --addr nor a hostname/port
	// pair is set. It composes with the empty default hostname into ":8080".
	defaultServerPort        = 8080
	defaultMetricsAddr       = ":9090"
	defaultReadTimeout       = 5 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultWriteTimeout      = 10 * time.Second
	defaultIdleTimeout       = 120 * time.Second
	defaultRequestTimeout    = 15 * time.Second
	defaultShutdownGrace     = 15 * time.Second
	defaultLogLevel          = "info"
	defaultLogFormat         = "json"
	// defaultRateLimitEnabled is false: a for-testing OIDC server is hammered by
	// Testcontainers suites, so throttling legitimate test traffic is a parity
	// defect. Opt in via config when wanted.
	defaultRateLimitEnabled = false
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
	// defaultControlEnabled is true: the /_mock test-time control plane is the
	// headline capability of a for-testing OIDC server, so it is ON by default.
	// Turn it OFF (--control-enabled=false) to serve only the public protocol
	// surface, making every /_mock route return the ordinary OIDC 404.
	defaultControlEnabled = true
	// defaultControlAddr is empty, co-locating the /_mock control plane on the main
	// API listener (Addr). Set a host:port to serve it on its own dedicated
	// listener instead (no request-recording middleware, mirroring metrics-addr).
	defaultControlAddr = ""
	// defaultControlToken is empty, which disables the control-token gate: every
	// /_mock request is accepted. Set a token to require it in the
	// X-Mock-Control-Token header (constant-time compared) or get a 401.
	defaultControlToken = ""
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
	// TLSEnabled turns on HTTPS. It is set from the JSON config's `ssl` object
	// (even empty) by runServe, ORed after Validate. When enabled with empty
	// TLSCertFile/TLSKeyFile, the composition root generates an in-process
	// self-signed localhost certificate (upstream ssl:{} parity).
	TLSEnabled bool
	// TLSCertFile is the PEM certificate served for HTTPS. Empty with TLS enabled
	// triggers self-signed localhost generation; a non-empty value must be paired
	// with TLSKeyFile.
	TLSCertFile string
	// TLSKeyFile is the PEM private key paired with TLSCertFile.
	TLSKeyFile string
	// RateLimitEnabled is the rate-limiting master switch. It defaults to false:
	// a for-testing OIDC server should not throttle Testcontainers traffic. When
	// false the rate-limit middleware is inert (pass-through).
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
	// ControlEnabled turns on the /_mock test-time control plane (direct mint,
	// scenario enqueue, request inspection, clock control). It defaults to true;
	// false serves only the public protocol surface, so every /_mock route returns
	// the ordinary OIDC 404.
	ControlEnabled bool
	// ControlAddr is the host:port of a dedicated listener for the /_mock control
	// plane. Empty (the default) co-locates /_mock on Addr; a non-empty value moves
	// it to its own listener with no request-recording middleware.
	ControlAddr string
	// ControlToken, when set, gates every /_mock request behind the
	// X-Mock-Control-Token header (constant-time compared). Empty (the default)
	// disables the gate, accepting every control request.
	ControlToken string
}

// RegisterFlags declares the server configuration flags on flags. Binding them
// to a Viper instance makes flags, environment variables, and defaults compose.
func RegisterFlags(flags *pflag.FlagSet) {
	flags.String("addr", "", "host:port to listen on; overrides --server-hostname/--server-port when set")
	flags.String(
		"server-hostname",
		"",
		"bind interface; empty binds the wildcard address (env SERVER_HOSTNAME)",
	)
	flags.Int("server-port", defaultServerPort, "listen port (env SERVER_PORT > PORT > 8080)")
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
	flags.String(
		"tls-cert-file",
		"",
		"PEM certificate for HTTPS; empty with TLS enabled generates a self-signed localhost cert",
	)
	flags.String("tls-key-file", "", "PEM private key for HTTPS; paired with --tls-cert-file")
	flags.Bool(
		"rate-limit-enabled",
		defaultRateLimitEnabled,
		"enable per-client (IP) rate limiting; OFF by default for test traffic",
	)
	flags.Float64("rate-limit-rps", defaultRateLimitRPS, "sustained per-client request rate (requests per second)")
	flags.Int("rate-limit-burst", defaultRateLimitBurst, "per-client burst size (token-bucket depth)")
	flags.Bool(
		"tracing-enabled",
		defaultTracingEnabled,
		"enable OpenTelemetry tracing (OTLP); configure the exporter via the standard OTEL_* env vars",
	)
	flags.Bool(
		"control-enabled",
		defaultControlEnabled,
		"enable the /_mock test-time control plane; false serves only the public protocol surface",
	)
	flags.String(
		"control-addr",
		defaultControlAddr,
		"host:port for a dedicated /_mock control listener; empty co-locates /_mock on --addr",
	)
	flags.String(
		"control-token",
		defaultControlToken,
		"require this token in the X-Mock-Control-Token header on /_mock requests; empty disables the gate",
	)
}

// Load reads the server configuration from vp, applying defaults for unset keys.
func Load(vp *viper.Viper) Config {
	setDefaults(vp)

	return Config{
		Addr:               resolveListenAddr(vp),
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
		TLSCertFile:        vp.GetString("tls-cert-file"),
		TLSKeyFile:         vp.GetString("tls-key-file"),
		RateLimitEnabled:   vp.GetBool("rate-limit-enabled"),
		RateLimitRPS:       vp.GetFloat64("rate-limit-rps"),
		RateLimitBurst:     vp.GetInt("rate-limit-burst"),
		TracingEnabled:     vp.GetBool("tracing-enabled"),
		ControlEnabled:     vp.GetBool("control-enabled"),
		ControlAddr:        vp.GetString("control-addr"),
		ControlToken:       vp.GetString("control-token"),
	}
}

// resolveListenAddr applies the address precedence: an explicit --addr (or
// MOCK_OIDC_ADDR) wins; otherwise the effective address is composed from
// server-hostname and server-port (fed by SERVER_HOSTNAME and the
// SERVER_PORT > PORT alias chain). viper's IsSet is false for values that come
// only from defaults, so an unset --addr falls through to composition, yielding
// ":8080" with the stock defaults.
func resolveListenAddr(vp *viper.Viper) string {
	if vp.IsSet("addr") {
		return vp.GetString("addr")
	}
	return net.JoinHostPort(vp.GetString("server-hostname"), strconv.Itoa(vp.GetInt("server-port")))
}

// Validate checks that the configuration is internally consistent.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Addr) == "" {
		return errors.New("addr must not be empty")
	}
	if c.MetricsAddr != "" && c.MetricsAddr == c.Addr {
		return errors.New("metrics-addr must differ from addr")
	}
	if c.ControlAddr != "" {
		if c.ControlAddr == c.Addr {
			return errors.New("control-addr must differ from addr")
		}
		if c.ControlAddr == c.MetricsAddr {
			return errors.New("control-addr must differ from metrics-addr")
		}
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
	if err := c.validateTLS(); err != nil {
		return err
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

// validateTLS enforces that an explicit cert and key are supplied together. TLS
// may still be enabled with neither (the self-signed path); only a lone cert or
// lone key is invalid.
func (c Config) validateTLS() error {
	certSet, keySet := c.TLSCertFile != "", c.TLSKeyFile != ""
	if certSet != keySet {
		return errors.New("tls-cert-file and tls-key-file must be set together")
	}
	return nil
}

func setDefaults(vp *viper.Viper) {
	// addr has no default: an unset addr composes from server-hostname/server-port
	// in resolveListenAddr, which yields ":8080" from the port default below.
	vp.SetDefault("server-hostname", "")
	vp.SetDefault("server-port", defaultServerPort)
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
	vp.SetDefault("tls-cert-file", "")
	vp.SetDefault("tls-key-file", "")
	vp.SetDefault("rate-limit-enabled", defaultRateLimitEnabled)
	vp.SetDefault("rate-limit-rps", defaultRateLimitRPS)
	vp.SetDefault("rate-limit-burst", defaultRateLimitBurst)
	vp.SetDefault("tracing-enabled", defaultTracingEnabled)
	vp.SetDefault("control-enabled", defaultControlEnabled)
	vp.SetDefault("control-addr", defaultControlAddr)
	vp.SetDefault("control-token", defaultControlToken)
}
