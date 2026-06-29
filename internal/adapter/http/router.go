package http

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/meigma/template-go-api/internal/adapter/http/middleware"
	"github.com/meigma/template-go-api/internal/adapter/http/problem"
	"github.com/meigma/template-go-api/internal/observability"
)

// Infrastructure route paths. They are raw chi routes outside the Huma API and
// the OpenAPI spec, and are excluded from tracing.
const (
	pathHealthz = "/healthz"
	pathReadyz  = "/readyz"
	pathMetrics = "/metrics"
)

// RouterDeps carries the dependencies needed to assemble the HTTP handler.
type RouterDeps struct {
	// Logger is the base logger for the recover and access-log middleware.
	Logger *slog.Logger
	// Metrics provides the metrics middleware and, when ServeMetricsEndpoint is
	// set, the /metrics handler.
	Metrics *observability.Metrics
	// ServeMetricsEndpoint mounts /metrics on this router. Leave it false when a
	// dedicated metrics listener serves /metrics instead; the metrics middleware
	// runs either way, so API requests are always recorded.
	ServeMetricsEndpoint bool
	// Version is reported in the OpenAPI document.
	Version string
	// RequestTimeout bounds per-request processing in the timeout middleware.
	RequestTimeout time.Duration
	// CORSAllowedOrigins lists origins for the CORS middleware; empty disables it.
	CORSAllowedOrigins []string
	// TrustedProxyHeader names the proxy header to read the client IP from; empty
	// trusts only the direct TCP peer.
	TrustedProxyHeader string
	// Readiness lists checks evaluated by /readyz; empty means always ready.
	Readiness []ReadinessCheck
	// Register mounts resource operations onto the Huma API.
	Register Registrar
	// Tracing wraps the handler with the OpenTelemetry HTTP server-span
	// instrumentation (otelhttp) and installs the span-naming Huma middleware.
	// The infrastructure routes (/healthz, /readyz, /metrics) are filtered out so
	// health checks and metrics scrapes do not generate spans. False adds no
	// tracing overhead.
	Tracing bool
	// InstallRateLimit installs the rate-limit Huma middleware on the API. Like
	// InstallAuthz it MUST run before the resource operations are registered (Huma
	// snapshots the middleware stack per operation at registration), and it runs
	// BEFORE InstallAuthz so an over-limit request is rejected before
	// authentication touches the credential store. Nil (or a disabled middleware)
	// leaves the API unthrottled. The infrastructure routes bypass Huma, so they
	// are never rate limited.
	InstallRateLimit func(huma.API)
	// InstallAuthz installs the authentication/authorization Huma middleware on
	// the API. It MUST run before the resource operations are registered: Huma
	// snapshots the API's middleware stack into each operation at registration
	// time, so middleware added afterward never runs. Nil (or a disabled
	// middleware) leaves the API unauthenticated — the escape hatch.
	InstallAuthz func(huma.API)
	// FinalizeAuthz stamps the OpenAPI security scheme and per-operation security
	// requirements onto the document. It MUST run after registration (it iterates
	// the registered operations). Nil (or a disabled middleware) leaves the spec
	// without security. It is the post-register counterpart to InstallAuthz,
	// split because Huma fixes an operation's middleware at registration time
	// while its OpenAPI metadata can be mutated afterward.
	FinalizeAuthz func(huma.API)
}

// NewRouter assembles the chi router: the core middleware stack, RFC 9457 error
// fallbacks, the Huma API with its registered resource operations (which appear
// in the OpenAPI spec), and the raw infrastructure routes (/healthz, /readyz,
// and — when ServeMetricsEndpoint is set — /metrics) that bypass the spec.
func NewRouter(deps RouterDeps) http.Handler {
	mux := chi.NewMux()

	// Core chi middleware, outermost first. The rate-limit and authn/authz
	// middleware are Huma middleware (installed on the API below), not chi
	// middleware, so they run only for API operations and never for the
	// infrastructure routes.
	//
	// Client-IP runs first so the request id, access log, metrics, and the
	// rate limiter all see the resolved IP. CORS sits after the access log (so
	// preflight responses are logged and metered) and is installed only when
	// origins are configured.
	mux.Use(middleware.ClientIP(deps.TrustedProxyHeader))
	mux.Use(chimiddleware.RequestID)
	mux.Use(middleware.Recoverer(deps.Logger))
	mux.Use(observability.RequestLogger(deps.Logger))
	if len(deps.CORSAllowedOrigins) > 0 {
		mux.Use(middleware.CORS(deps.CORSAllowedOrigins))
	}
	mux.Use(deps.Metrics.Middleware())
	mux.Use(middleware.Timeout(deps.RequestTimeout))

	// Error fallbacks: emit RFC 9457 problem+json instead of chi's text/plain 404
	// and empty 405, so every API error response shares Huma's error shape.
	mux.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		problem.Write(w, http.StatusNotFound, "the requested resource was not found")
	})
	mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		// chi does not pass the allowed methods to a custom handler, so rebuild
		// the Allow header (required on a 405 by RFC 9110) by probing the routes.
		if allow := allowedMethods(mux, r.URL.Path); allow != "" {
			w.Header().Set("Allow", allow)
		}
		problem.Write(w, http.StatusMethodNotAllowed, "the method is not allowed for this resource")
	})

	api := NewAPI(mux, deps.Version)
	// The tracing, rate-limit, and authn/authz Huma middleware are installed
	// BEFORE the operations are registered: Huma bakes the API's middleware stack
	// into each operation at registration time, so middleware added afterward
	// would never run. The span namer is installed first so it runs within the
	// otelhttp server span; rate limiting next so an over-limit request is
	// rejected before authentication runs. Each is a no-op when its feature is
	// disabled.
	if deps.Tracing {
		api.UseMiddleware(observability.TraceSpanNamer)
	}
	if deps.InstallRateLimit != nil {
		deps.InstallRateLimit(api)
	}
	if deps.InstallAuthz != nil {
		deps.InstallAuthz(api)
	}
	// Resource operations are mounted by their adapter packages via the Registrar.
	if deps.Register != nil {
		deps.Register(api)
	}
	// OpenAPI security is stamped AFTER registration, once the operations exist;
	// it only mutates document metadata, so it is safe post-register. No-op when
	// authorization is disabled.
	if deps.FinalizeAuthz != nil {
		deps.FinalizeAuthz(api)
	}

	// Infrastructure routes stay raw chi and are excluded from the spec.
	mountInfra(mux, deps.Metrics, deps.Readiness, deps.ServeMetricsEndpoint)

	if deps.Tracing {
		// Wrap the whole handler in the OpenTelemetry HTTP server span, extracting
		// any propagated trace context. The filter excludes the infrastructure
		// routes so health checks and metrics scrapes are not traced.
		return otelhttp.NewHandler(mux, "http.server", otelhttp.WithFilter(traceableRequest))
	}

	return mux
}

// traceableRequest reports whether a request should be traced. The
// infrastructure routes (/healthz, /readyz, /metrics) are excluded so routine
// health checks and metrics scrapes do not flood the trace backend.
func traceableRequest(r *http.Request) bool {
	switch r.URL.Path {
	case pathHealthz, pathReadyz, pathMetrics:
		return false
	default:
		return true
	}
}

func mountInfra(
	mux chi.Router,
	metrics *observability.Metrics,
	readiness []ReadinessCheck,
	serveMetrics bool,
) {
	mux.Get(pathHealthz, handleHealthz)
	mux.Get(pathReadyz, handleReadyz(readiness))
	if serveMetrics {
		mux.Handle(pathMetrics, metrics.Handler())
	}
}

// allowedMethods returns a comma-separated Allow header value for path by probing
// which standard methods the router has registered for it.
func allowedMethods(routes chi.Routes, path string) string {
	probe := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	}

	allowed := make([]string, 0, len(probe))
	for _, method := range probe {
		if routes.Match(chi.NewRouteContext(), method, path) {
			allowed = append(allowed, method)
		}
	}

	return strings.Join(allowed, ", ")
}
