package app

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	adapterhttp "github.com/meigma/mock-oidc/internal/adapter/http"
	"github.com/meigma/mock-oidc/internal/adapter/http/problem"
	"github.com/meigma/mock-oidc/internal/config"
	"github.com/meigma/mock-oidc/internal/oidc/controlapi"
)

const (
	// controlMarkerHeader stamps every control response so a caller can never mistake
	// the test-time surface for a production one (C10 safe-positioning). HTTP header
	// names are case-insensitive; the canonical form is used so it round-trips
	// through net/http's header canonicalization unchanged.
	controlMarkerHeader = "X-Mock-Oidc"
	controlMarkerValue  = "testing-only"
	// controlTokenHeader carries the optional control-plane bearer token.
	controlTokenHeader = "X-Mock-Control-Token" //nolint:gosec // G101: header name, not a credential.
	// controlAPIKeyScheme is the OpenAPI security-scheme name advertised on the
	// control operations when a control token is configured.
	controlAPIKeyScheme = "mockControlToken"
)

// isControlPath reports whether p addresses the reserved /_mock control plane.
func isControlPath(p string) bool {
	return p == controlapi.Prefix || strings.HasPrefix(p, controlapi.Prefix+"/")
}

// controlScope wraps a handler so the /_mock control plane is gated and marked
// without affecting the public protocol surface. For a control path it stamps the
// testing-only marker header and, when want is non-empty, enforces the
// X-Mock-Control-Token header with a constant-time comparison, returning a 401
// problem+json (never the OAuth2 shape) on mismatch. Non-control paths pass
// straight through. It is applied path-scoped when the plane is co-located and
// applies to the whole (all-/_mock) handler on the dedicated listener.
func controlScope(want string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isControlPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set(controlMarkerHeader, controlMarkerValue)
			if want != "" &&
				subtle.ConstantTimeCompare([]byte(r.Header.Get(controlTokenHeader)), []byte(want)) != 1 {
				problem.Write(w, http.StatusUnauthorized, "missing or invalid control token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// newControlServer builds the dedicated-listener control server: a bare chi mux +
// Huma API carrying ONLY the /_mock operations (no OIDC protocol routes, no
// request-recording middleware), behind the control gate and marker. It mirrors
// the metrics listener — a minimal, off-the-API-surface server. RFC 9457
// problem+json is used for its own 404/405 fallbacks, so even a stray path never
// leaks the OAuth2 shape.
func newControlServer(cfg config.Config, version string, deps controlapi.Deps) *http.Server {
	mux := chi.NewMux()
	mux.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		problem.Write(w, http.StatusNotFound, "the requested resource was not found")
	})
	mux.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		problem.Write(w, http.StatusMethodNotAllowed, "the method is not allowed for this resource")
	})

	api := adapterhttp.NewAPI(mux, version)
	controlapi.Register(api, deps)
	stampControlSecurity(api, cfg.ControlToken)

	return newHTTPServer(cfg, cfg.ControlAddr, controlScope(cfg.ControlToken)(mux))
}

// stampControlSecurity advertises an apiKey-in-header security scheme on the
// control operations when a control token is configured, walking OpenAPI.Paths
// (Huma exposes no Operations() accessor) and attaching the requirement to every
// /_mock operation. The OAuth2/OpenID schemes stamped on the protocol surface do
// NOT apply to /_mock. It is a no-op when no token is set (the committed spec).
func stampControlSecurity(api huma.API, token string) {
	if token == "" {
		return
	}
	doc := api.OpenAPI()
	comp := doc.Components
	if comp.SecuritySchemes == nil {
		comp.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	comp.SecuritySchemes[controlAPIKeyScheme] = &huma.SecurityScheme{
		Type: "apiKey",
		In:   "header",
		Name: controlTokenHeader,
	}
	requirement := []map[string][]string{{controlAPIKeyScheme: {}}}
	for path, item := range doc.Paths {
		if !isControlPath(path) || item == nil {
			continue
		}
		for _, op := range pathOperations(item) {
			op.Security = requirement
		}
	}
}

// pathOperations returns the non-nil operations registered on a path item.
func pathOperations(item *huma.PathItem) []*huma.Operation {
	candidates := []*huma.Operation{item.Get, item.Post, item.Put, item.Delete, item.Patch}
	ops := make([]*huma.Operation, 0, len(candidates))
	for _, op := range candidates {
		if op != nil {
			ops = append(ops, op)
		}
	}
	return ops
}

// logControlRoutes emits a Warning-level line announcing the enabled control
// plane, its listener location, and whether the token gate is active — so an
// operator who leaves it on in a shared environment sees it unmistakably (C10).
func logControlRoutes(ctx context.Context, logger *slog.Logger, cfg config.Config) {
	routes := []string{
		"POST " + controlapi.Prefix + "/mint",
		"POST,GET,DELETE " + controlapi.Prefix + "/scenarios",
		"POST,GET,DELETE " + controlapi.Prefix + "/requests",
		"POST " + controlapi.Prefix + "/requests/take",
		"GET,PUT " + controlapi.Prefix + "/clock",
		"POST " + controlapi.Prefix + "/clock/advance",
		"POST " + controlapi.Prefix + "/reset",
	}
	location := "co-located on " + cfg.Addr
	if cfg.ControlAddr != "" {
		location = "dedicated listener " + cfg.ControlAddr
	}
	logger.WarnContext(ctx,
		"test-time control plane ENABLED (/_mock): mint tokens, enqueue scenarios, inspect requests, steer the clock",
		slog.String("location", location),
		slog.Bool("token_gate", cfg.ControlToken != ""),
		slog.Any("routes", routes),
	)
}
