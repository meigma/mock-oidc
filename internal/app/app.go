// Package app is the composition root: it wires configuration, observability,
// and the kept chi+Huma transport into a runnable App. In this walking-skeleton
// slice it serves only the infrastructure routes; the OIDC hexagon
// (internal/oidc) and its adapters are mounted through the Registrar seam in
// later slices.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/time/rate"

	adapterhttp "github.com/meigma/mock-oidc/internal/adapter/http"
	"github.com/meigma/mock-oidc/internal/config"
	"github.com/meigma/mock-oidc/internal/observability"
	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/httpapi"
	"github.com/meigma/mock-oidc/internal/oidc/memory"
	"github.com/meigma/mock-oidc/internal/oidc/signing"
	"github.com/meigma/mock-oidc/internal/ratelimit"
)

// rateLimiterIdleTTL is how long an idle per-client bucket is kept before the
// in-process limiter evicts it, bounding memory under churning client keys.
const rateLimiterIdleTTL = 10 * time.Minute

// serviceName is the OpenTelemetry service.name reported by traces. It is a
// default; OTEL_SERVICE_NAME or OTEL_RESOURCE_ATTRIBUTES override it.
const serviceName = "mock-oidc"

// bootBanner is logged once at startup. mock-oidc mints real, signed tokens for
// any identity on request, so this line makes its for-testing-only positioning
// unmistakable in container logs (C10).
const bootBanner = "mock-oidc is FOR TESTING ONLY: it issues signed tokens for arbitrary identities and must never front production traffic"

// App is a fully wired API server ready to Run.
type App struct {
	server        *http.Server
	metricsServer *http.Server
	logger        *slog.Logger
	grace         time.Duration
	// rateLimiter is the in-process rate limiter whose janitor goroutine is
	// stopped during graceful shutdown. It is nil when rate limiting is disabled.
	rateLimiter *ratelimit.InMemory
	// traceShutdown flushes and shuts down the OpenTelemetry tracer provider on
	// graceful shutdown. It is a no-op when tracing is disabled.
	traceShutdown func(context.Context) error
}

// Option configures how New wires the application.
type Option func(*options)

// options collects the composition-root seams: the parsed OIDC seed plus the
// clock and signing injection points that let tests bypass the seed-derived
// defaults (a frozen clock, a fixed key set).
type options struct {
	seed    config.Seed
	clock   oidc.Clock      // nil → clock derived from the seed (frozen if systemTime set)
	signing signingProvider // nil → signing.NewProvider(seed)
}

// signingProvider is the crypto capability set the composition root consumes. It
// is declared here (consumer side) per the dependency rule; *signing.Provider
// satisfies it. The TokenVerifier facet backs the Slice 3 SessionService
// (/userinfo, /introspect).
type signingProvider interface {
	oidc.Signer
	oidc.KeyStore
	oidc.TokenVerifier
}

// WithSeed supplies the parsed OIDC seed. serve passes config.LoadSeed's result;
// tests pass a hand-built Seed. Absent → config.DefaultSeed().
func WithSeed(s config.Seed) Option { return func(o *options) { o.seed = s } }

// WithClock injects a clock, overriding the seed-derived one. Unit and
// functional tests pass a frozen or mutable memory.Clock to pin iat/nbf/exp.
func WithClock(c oidc.Clock) Option { return func(o *options) { o.clock = c } }

// WithSigning injects a signing provider with a fixed key set, bypassing the
// seed-driven construction (and its key generation) for stable kids in tests.
func WithSigning(p signingProvider) Option { return func(o *options) { o.signing = p } }

// New wires the application from cfg and logger. version is reported in the
// OpenAPI document served by the API. The server is DB-less, so New performs no
// I/O that can fail beyond initializing tracing. The caller owns running and
// shutting the App down.
func New(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	version string,
	opts ...Option,
) (*App, error) {
	o := options{seed: config.DefaultSeed()}
	for _, opt := range opts {
		opt(&o)
	}

	logger.WarnContext(ctx, bootBanner)

	// Build the OIDC hexagon over the in-memory + signing adapters. Signing
	// construction parses the seed's keys and validates the algorithm, so it is
	// the one fallible OIDC wiring step.
	registrar, err := buildRegistrar(o, logger)
	if err != nil {
		return nil, fmt.Errorf("init signing: %w", err)
	}

	metrics := observability.NewMetrics()

	rateLimiter, installRateLimit := buildRateLimiter(cfg, logger)

	// Configure tracing before serving so the global provider is in place when
	// requests start producing spans.
	traceShutdown, err := observability.NewTracerProvider(ctx, observability.TracingConfig{
		Enabled:        cfg.TracingEnabled,
		ServiceName:    serviceName,
		ServiceVersion: version,
	})
	if err != nil {
		return nil, fmt.Errorf("init tracing: %w", err)
	}

	// An empty metrics-addr co-locates /metrics on the API listener; otherwise a
	// dedicated metrics server (below) serves it off the API surface.
	serveMetricsInline := cfg.MetricsAddr == ""
	handler := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Logger:               logger,
		Metrics:              metrics,
		ServeMetricsEndpoint: serveMetricsInline,
		Version:              version,
		RequestTimeout:       cfg.RequestTimeout,
		CORSAllowedOrigins:   cfg.CORSAllowedOrigins,
		TrustedProxyHeader:   cfg.TrustedProxyHeader,
		// No DB ⇒ no readiness checks ⇒ /readyz is unconditionally ready.
		Readiness: nil,
		// Mount the OIDC protocol operations (discovery, JWKS, token) built above.
		Register: registrar,
		// Render wrong-method protocol routes (for example GET /{issuer}/token) as
		// the uniform OAuth2 error shape instead of RFC 9457, without the generic
		// transport substrate importing internal/oidc.
		FallbackWriter:   oidcFallbackWriter,
		Tracing:          cfg.TracingEnabled,
		InstallRateLimit: installRateLimit,
	})

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	var metricsServer *http.Server
	if !serveMetricsInline {
		metricsServer = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           adapterhttp.NewMetricsHandler(metrics),
			ReadTimeout:       cfg.ReadTimeout,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
			WriteTimeout:      cfg.WriteTimeout,
			IdleTimeout:       cfg.IdleTimeout,
		}
	}

	return &App{
		server:        server,
		metricsServer: metricsServer,
		logger:        logger,
		grace:         cfg.ShutdownGrace,
		rateLimiter:   rateLimiter,
		traceShutdown: traceShutdown,
	}, nil
}

// buildRateLimiter constructs the rate limiter and the hook that installs the
// rate-limit middleware on the API. When rate limiting is disabled it returns a
// nil limiter and a nil hook, so NewRouter leaves the API unthrottled. The
// limiter is keyed by client IP (adapterhttp.ClientIPKeyFunc); swap that key
// function for a principal-based one to limit authenticated callers instead.
// The returned limiter runs a janitor goroutine the App stops on shutdown.
func buildRateLimiter(cfg config.Config, logger *slog.Logger) (*ratelimit.InMemory, func(huma.API)) {
	if !cfg.RateLimitEnabled {
		return nil, nil
	}

	limiter := ratelimit.NewInMemory(rate.Limit(cfg.RateLimitRPS), cfg.RateLimitBurst, rateLimiterIdleTTL)
	install := func(api huma.API) {
		ratelimit.NewMiddleware(api, limiter, adapterhttp.ClientIPKeyFunc, logger, true).Install()
	}

	return limiter, install
}

// buildRegistrar wires the OIDC hexagon — the mutable clock, the signing
// adapter, and the in-memory issuer registry — into the provider, token, and
// authorize services, and returns the httpapi Registrar that mounts their
// operations. It is the single OIDC-wiring path shared by New and the server-less
// OpenAPIYAML. The CodeStore is shared: AuthorizeService writes codes, the
// TokenService burns them at the authorization_code exchange.
func buildRegistrar(o options, logger *slog.Logger) (adapterhttp.Registrar, error) {
	clock := resolveClock(o)
	sign, err := resolveSigning(o)
	if err != nil {
		return nil, err
	}

	registry := memory.NewIssuerRegistry()
	codes := memory.NewCodeStore()
	refresh := memory.NewRefreshTokenStore()

	provider := oidc.NewProviderService(registry, sign, oidc.WithProviderLogger(logger))
	tokens := oidc.NewTokenService(registry, sign, sign, clock,
		oidc.WithTokenLogger(logger),
		oidc.WithCodeStore(codes),
		oidc.WithRefreshStore(refresh),
		oidc.WithRefreshRotation(o.seed.RotateRefreshToken),
	)
	authorize := oidc.NewAuthorizeService(codes, clock, o.seed.InteractiveLogin)
	session := oidc.NewSessionService(sign, refresh, clock)

	return httpapi.Registrar(httpapi.Deps{
		Provider:  provider,
		Tokens:    tokens,
		Authorize: authorize,
		Session:   session,
		Logger:    logger,
	}), nil
}

// resolveClock returns the injected clock when present; otherwise it derives one
// from the seed: a clock frozen at systemTime when configured, else a mutable
// clock reading wall time.
func resolveClock(o options) oidc.Clock {
	switch {
	case o.clock != nil:
		return o.clock
	case o.seed.SystemTimeFixed:
		return memory.NewFrozenClock(o.seed.SystemTime)
	default:
		return memory.NewClock()
	}
}

// resolveSigning returns the injected signing provider when present; otherwise it
// constructs the RSA signing adapter from the seed (parsing the initial keys and
// validating the algorithm — the fallible step).
func resolveSigning(o options) (signingProvider, error) {
	if o.signing != nil {
		return o.signing, nil
	}
	sign, err := signing.NewProvider(o.seed.Algorithm, o.seed.InitialKeys)
	if err != nil {
		return nil, err
	}
	return sign, nil
}

// oidcFallbackWriter renders the uniform OAuth2 error shape for a wrong-method
// protocol-family route (for example GET /{issuer}/token → 405), returning false
// for non-protocol paths so the RFC 9457 problem+json fallback stays in place. It
// is the composition-root strategy installed into RouterDeps.FallbackWriter.
func oidcFallbackWriter(w http.ResponseWriter, r *http.Request) bool {
	if !httpapi.IsProtocolPath(r.URL.Path) {
		return false
	}
	httpapi.WriteOAuth2Error(w, http.StatusMethodNotAllowed,
		"invalid_request", "the method is not allowed for this resource")
	return true
}

// Handler returns the assembled HTTP handler, primarily for functional tests.
func (a *App) Handler() http.Handler {
	return a.server.Handler
}

// OpenAPIYAML builds the API without binding a listener and returns the
// OpenAPI 3.0.3 specification as YAML. It registers the OIDC protocol operations
// (discovery, JWKS, token) through the same Registrar the server uses, so the
// committed spec matches the running surface. The operations' shapes are
// seed-independent, so a default seed is used.
func OpenAPIYAML(version string) ([]byte, error) {
	registrar, err := buildRegistrar(options{seed: config.DefaultSeed()}, slog.New(slog.DiscardHandler))
	if err != nil {
		return nil, fmt.Errorf("build oidc registrar: %w", err)
	}

	spec, err := adapterhttp.SpecYAML(version, registrar, nil)
	if err != nil {
		return nil, fmt.Errorf("build openapi spec: %w", err)
	}

	return spec, nil
}
