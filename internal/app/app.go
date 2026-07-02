// Package app is the composition root: it wires the domain service, the
// PostgreSQL persistence adapter, the authorization engine and API-key
// authenticator, observability, and the HTTP server into a runnable App.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	adapterhttp "github.com/meigma/mock-oidc/internal/adapter/http"
	"github.com/meigma/mock-oidc/internal/adapter/postgres"
	"github.com/meigma/mock-oidc/internal/authz"
	"github.com/meigma/mock-oidc/internal/authz/apikey"
	"github.com/meigma/mock-oidc/internal/config"
	"github.com/meigma/mock-oidc/internal/observability"
	"github.com/meigma/mock-oidc/internal/ratelimit"
	"github.com/meigma/mock-oidc/internal/todo"
	todoauthz "github.com/meigma/mock-oidc/internal/todo/authz"
	"github.com/meigma/mock-oidc/internal/todo/httpapi"
	todopostgres "github.com/meigma/mock-oidc/internal/todo/postgres"
)

// rateLimiterIdleTTL is how long an idle per-client bucket is kept before the
// in-process limiter evicts it, bounding memory under churning client keys.
const rateLimiterIdleTTL = 10 * time.Minute

// serviceName is the OpenTelemetry service.name reported by traces. It is a
// default; OTEL_SERVICE_NAME or OTEL_RESOURCE_ATTRIBUTES override it.
const serviceName = "template-go-api"

// App is a fully wired API server ready to Run.
type App struct {
	server        *http.Server
	metricsServer *http.Server
	logger        *slog.Logger
	grace         time.Duration
	// pool is the PostgreSQL connection pool, closed during graceful shutdown.
	// It is nil when a repository is injected with WithRepository (tests).
	pool *pgxpool.Pool
	// rateLimiter is the in-process rate limiter whose janitor goroutine is
	// stopped during graceful shutdown. It is nil when rate limiting is disabled.
	rateLimiter *ratelimit.InMemory
	// traceShutdown flushes and shuts down the OpenTelemetry tracer provider on
	// graceful shutdown. It is a no-op when tracing is disabled.
	traceShutdown func(context.Context) error
}

// Option configures how New wires the application.
type Option func(*options)

type options struct {
	repo          todo.Repository
	authenticator authz.Authenticator
}

// WithRepository injects a ready-made todo.Repository instead of connecting the
// PostgreSQL adapter. It lets tests wire the full server without a database, and
// gives integrators a seam to plug in an alternative adapter without editing the
// composition root.
func WithRepository(repo todo.Repository) Option {
	return func(o *options) {
		o.repo = repo
	}
}

// WithAuthenticator injects an authz.Authenticator instead of wiring the shipped
// PostgreSQL-backed API-key authenticator. It mirrors WithRepository: tests use
// it to authenticate a request without a database (so authz can run with
// AuthzEnabled true and no api_keys table), and integrators use it to plug in a
// real verifier (JWT/OIDC/session) without editing the composition root.
func WithAuthenticator(authenticator authz.Authenticator) Option {
	return func(o *options) {
		o.authenticator = authenticator
	}
}

// New wires the application from cfg and logger. version is reported in the
// OpenAPI document served by the API. Unless a repository is injected with
// WithRepository, it connects a PostgreSQL connection pool, which can fail. The
// caller owns running and shutting the App down, which closes the pool.
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

	repo, pool, readiness, err := resolveStore(ctx, cfg, o.repo)
	if err != nil {
		return nil, err
	}

	service := todo.NewService(repo, logger)
	metrics := observability.NewMetrics()

	installAuthz, finalizeAuthz, err := authzInstaller(cfg, repo, pool, logger, o.authenticator)
	if err != nil {
		return nil, err
	}

	rateLimiter, installRateLimit := buildRateLimiter(cfg, logger)

	// Configure tracing before serving so the global provider is in place when
	// requests (and their pgx queries) start producing spans.
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
		// The postgres store contributes a real connectivity check here; an
		// injected repository (tests) contributes none, so /readyz is always ready.
		Readiness:        readiness,
		Register:         registerResources(service),
		Tracing:          cfg.TracingEnabled,
		InstallRateLimit: installRateLimit,
		InstallAuthz:     installAuthz,
		FinalizeAuthz:    finalizeAuthz,
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
		pool:          pool,
		rateLimiter:   rateLimiter,
		traceShutdown: traceShutdown,
	}, nil
}

// resolveStore returns the todo.Repository to wire. An injected repository is
// used as-is with no pool or readiness check (tests, or an integrator-supplied
// adapter); otherwise it connects the PostgreSQL adapter and returns the pool
// (for shutdown) and a connectivity readiness check.
func resolveStore(
	ctx context.Context,
	cfg config.Config,
	injected todo.Repository,
) (todo.Repository, *pgxpool.Pool, []adapterhttp.ReadinessCheck, error) {
	if injected != nil {
		return injected, nil, nil, nil
	}

	pool, err := postgres.Connect(ctx, postgres.Config{
		URL:      cfg.DatabaseURL,
		MaxConns: cfg.DBMaxConns,
		Tracing:  cfg.TracingEnabled,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connect postgres: %w", err)
	}

	repo := todopostgres.NewTodoRepository(pool)
	readiness := []adapterhttp.ReadinessCheck{{Name: "postgres", Check: repo.Ping}}

	return repo, pool, readiness, nil
}

// authzInstaller builds the authorization engine and returns a hook that
// installs the authn/authz Huma middleware on the API. The Authorizer merges the
// base cross-cutting policies with each domain slice's Contribution — the todo
// slice contributes its policies, actions, and a repo-backed fact resolver, so
// an attribute policy can load a todo lazily through repo. Adding a resource adds
// one Contribution here. cfg.AuthzPolicyDir, when set, loads the base policies
// from that directory instead of the embedded base.cedar.
//
// Authentication is resolved through WithAuthenticator when one is injected
// (tests, or an integrator-supplied verifier); otherwise the shipped
// PostgreSQL-backed API-key authenticator is wired, which needs a pool when
// authorization is enabled. The middleware is inert when cfg.AuthzEnabled is
// false, keeping every route working without authentication.
func authzInstaller(
	cfg config.Config,
	repo todo.Repository,
	pool *pgxpool.Pool,
	logger *slog.Logger,
	injected authz.Authenticator,
) (func(huma.API), func(huma.API), error) {
	authorizer, err := authz.New(
		[]authz.Contribution{todoauthz.Contribution(repo)},
		authz.WithPolicyDir(cfg.AuthzPolicyDir),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build authorizer: %w", err)
	}

	authenticator, err := resolveAuthenticator(cfg, pool, injected)
	if err != nil {
		return nil, nil, err
	}

	// install registers the middleware before resources are mounted; finalize
	// stamps the OpenAPI security after — the split Huma's registration-time
	// middleware snapshot requires.
	install := func(api huma.API) {
		authz.NewMiddleware(api, authenticator, authorizer, logger, cfg.AuthzEnabled).Install()
	}
	finalize := func(api huma.API) {
		authz.NewMiddleware(api, authenticator, authorizer, logger, cfg.AuthzEnabled).Finalize()
	}

	return install, finalize, nil
}

// resolveAuthenticator selects the authenticator the middleware runs. An injected
// authenticator (WithAuthenticator) is used as-is, the seam tests use to satisfy
// authz without a database and integrators use to plug in a real verifier.
// Otherwise the shipped PostgreSQL-backed API-key authenticator is wired, which
// requires a pool when authorization is enabled; when disabled the middleware
// never runs, so a nil authenticator is harmless.
func resolveAuthenticator(
	cfg config.Config,
	pool *pgxpool.Pool,
	injected authz.Authenticator,
) (authz.Authenticator, error) {
	if injected != nil {
		return injected, nil
	}

	if cfg.AuthzEnabled && pool == nil {
		return nil, errors.New("authz-enabled requires a database connection for the api-key store")
	}

	if pool == nil {
		return nil, nil //nolint:nilnil // a disabled middleware never invokes the authenticator.
	}

	return apikey.NewAuthenticator(apikey.NewStore(pool)), nil
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
// OpenAPI 3.0.3 specification as YAML. The repository is never invoked while
// generating the spec, so a no-op stub stands in for the real adapter and no
// database connection is required.
//
// The routes are tagged with their authorization declarations, so the export
// also stamps the security scheme and per-operation requirements via
// authz.DocumentSecurity — independently of the runtime --authz-enabled flag, so
// the committed spec always advertises the protection the routes declare.
func OpenAPIYAML(version string) ([]byte, error) {
	service := todo.NewService(noopRepository{}, nil)

	spec, err := adapterhttp.SpecYAML(version, registerResources(service), authz.DocumentSecurity)
	if err != nil {
		return nil, fmt.Errorf("build openapi spec: %w", err)
	}

	return spec, nil
}

// noopRepository is a todo.Repository that performs no persistence. It exists
// solely to construct the service when generating the OpenAPI document
// server-lessly, where the repository is never invoked.
type noopRepository struct{}

func (noopRepository) Save(_ context.Context, _ todo.Todo) error { return nil }

func (noopRepository) FindByID(_ context.Context, _ string) (todo.Todo, error) {
	return todo.Todo{}, todo.ErrNotFound
}

func (noopRepository) List(_ context.Context, _ todo.PageQuery) (todo.PageResult, error) {
	return todo.PageResult{}, nil
}

// apiVersionV1 is the URL path prefix for version 1 of the resource API. Every
// resource operation is mounted beneath it (so the version is explicit in the
// URL and the OpenAPI paths); the infrastructure routes (/healthz, /readyz,
// /metrics, and the OpenAPI/docs endpoints) are operational, not part of the
// versioned API contract, so they stay unprefixed at the root.
const apiVersionV1 = "/v1"

// registerResources composes the per-resource HTTP adapters mounted on the API,
// grouped under the current API version prefix.
//
// Add a new resource by constructing its service above and adding one Register
// call here, onto the same version group.
//
// Introduce a breaking revision by adding a sibling group — v2 :=
// huma.NewGroup(api, "/v2") — and registering the changed resources on it;
// unchanged resources keep registering on v1. Both versions then serve side by
// side and share the one OpenAPI document. huma.NewGroup returns a huma.API, so
// a resource's Register call is identical whether it mounts on a version group
// or the root API, and the authz middleware installed on the root API (before
// this runs) is inherited by every grouped route.
func registerResources(todoService *todo.Service) adapterhttp.Registrar {
	return func(api huma.API) {
		v1 := huma.NewGroup(api, apiVersionV1)
		httpapi.Register(v1, todoService)
	}
}
