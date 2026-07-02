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

// options collects the composition-root seams. It is intentionally empty in the
// skeleton slice; later slices add OIDC seams (clock, signing, seed) here.
type options struct{}

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
	var o options
	for _, opt := range opts {
		opt(&o)
	}

	logger.WarnContext(ctx, bootBanner)

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
		// The OIDC services do not exist yet; a nil Registrar mounts no operations,
		// so the server serves only the infrastructure routes.
		Register:         nil,
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

// Handler returns the assembled HTTP handler, primarily for functional tests.
func (a *App) Handler() http.Handler {
	return a.server.Handler
}

// OpenAPIYAML builds the API without binding a listener and returns the
// OpenAPI 3.0.3 specification as YAML. In the skeleton slice no OIDC operations
// are registered, so the document describes only the base API surface; later
// slices register the protocol operations through the Registrar.
func OpenAPIYAML(version string) ([]byte, error) {
	spec, err := adapterhttp.SpecYAML(version, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("build openapi spec: %w", err)
	}

	return spec, nil
}
