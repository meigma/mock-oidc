package authz

import (
	"log/slog"
	"net/http"

	"github.com/cedar-policy/cedar-go"
	"github.com/cedar-policy/cedar-go/types"
	"github.com/danielgtaylor/huma/v2"
)

// APIKeyHeader is the request header the API-key authenticator reads a key from,
// and the header named by the OpenAPI security scheme. It is defined here, in the
// base package, so the security-scheme declaration (declare.go) needs no
// dependency on the apikey adapter.
//
//nolint:gosec // G101: this is a header name, not a credential value.
const APIKeyHeader = "X-API-Key"

// genericForbidden is the client-facing detail for any authorization denial. The
// specific reason (diagnostic, missing declaration) is logged, not returned, so
// the API does not leak its policy structure.
const genericForbidden = "you are not authorized to perform this action"

// genericUnauthorized is the client-facing detail when an anonymous caller is
// denied — it signals that credentials are required.
const genericUnauthorized = "authentication is required to perform this action"

// Middleware bundles the authentication and authorization Huma middleware behind
// one switch. When disabled it is inert (pass-through), so the template stays
// green before any route carries an authorization declaration.
type Middleware struct {
	authenticator Authenticator
	authorizer    *Authorizer
	api           huma.API
	logger        *slog.Logger
	enabled       bool
}

// NewMiddleware builds the authz middleware over authenticator and authorizer.
// api is the Huma API used to write RFC 9457 problem responses; logger records
// denials and fail-closed errors. When enabled is false the middleware is a
// pass-through (Install is a no-op), the escape hatch for incremental adoption.
func NewMiddleware(
	api huma.API,
	authenticator Authenticator,
	authorizer *Authorizer,
	logger *slog.Logger,
	enabled bool,
) *Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return &Middleware{
		authenticator: authenticator,
		authorizer:    authorizer,
		api:           api,
		logger:        logger,
		enabled:       enabled,
	}
}

// Install registers the authn and authz middleware on the API. It MUST run
// before the resource operations are registered: Huma snapshots the API's
// middleware stack into each operation at huma.Register time, so middleware added
// afterward never runs for those operations. It is a no-op when the middleware is
// disabled, so all operations run unauthenticated and unauthorized — the escape
// hatch that bypasses authorization entirely.
//
// OpenAPI security stamping is deliberately NOT done here, because it must run
// after registration (ApplySecurity needs the operations present); the
// composition root calls Finalize for that, or the server-less exporter calls
// DocumentSecurity directly.
func (m *Middleware) Install() {
	if !m.enabled {
		return
	}

	m.api.UseMiddleware(m.authenticate, m.authorize)
}

// Finalize stamps the API-key security scheme and the per-operation security
// requirements onto the OpenAPI document. It MUST run after the resource
// operations are registered (ApplySecurity iterates the registered paths). It is
// a no-op when the middleware is disabled, so a bypassed API advertises no
// security it does not enforce. Pairing Install (pre-register) with Finalize
// (post-register) is required because Huma fixes an operation's middleware at
// registration time while its OpenAPI metadata can be mutated afterward.
func (m *Middleware) Finalize() {
	if !m.enabled {
		return
	}

	DocumentSecurity(m.api)
}

// authenticate runs the configured Authenticator and stores the resulting
// Principal in the request context for the downstream authz middleware. A
// missing credential yields an anonymous principal (authorization decides
// whether that is acceptable); a malformed credential is rejected with 401.
func (m *Middleware) authenticate(ctx huma.Context, next func(huma.Context)) {
	principal, err := m.authenticator.Authenticate(ctx)
	if err != nil {
		// A credential was present but invalid. Do not log the credential
		// itself; the access-log middleware redacts the carrying headers.
		m.logger.WarnContext(ctx.Context(), "authentication failed", slog.Any("error", err))
		m.writeErr(ctx, http.StatusUnauthorized, genericUnauthorized)

		return
	}

	next(huma.WithContext(ctx, WithPrincipal(ctx.Context(), principal)))
}

// authorize enforces the operation's authorization declaration. Deny-by-default:
// an operation with no declaration is denied and logged; Public proceeds; Require
// evaluates the Cedar request and proceeds only on Allow. A captured entity-load
// error fails closed with 500.
func (m *Middleware) authorize(ctx huma.Context, next func(huma.Context)) {
	principal := m.principal(ctx)

	decl, ok := declarationFrom(ctx.Operation())
	if !ok {
		// Fail-closed: a route that declared neither Require nor Public is a
		// server-side programming omission, not a missing-credential case, so it
		// is always 403 (never 401) — credentials can never satisfy a route that
		// declares no requirement, and a 401 would invite a pointless retry. This
		// matches the design's undeclared = deny-403 framing.
		m.logger.WarnContext(ctx.Context(), "denying undeclared operation",
			slog.String("operation", ctx.Operation().OperationID))
		m.writeErr(ctx, http.StatusForbidden, genericForbidden)

		return
	}

	if decl.kind == kindPublic {
		next(ctx)

		return
	}

	allowed := m.evaluate(ctx, principal, decl)
	switch allowed {
	case decisionAllow:
		next(ctx)
	case decisionDeny:
		m.deny(ctx, principal)
	case decisionError:
		m.writeErr(ctx, http.StatusInternalServerError, "authorization is temporarily unavailable")
	default:
		// Fail closed: an unrecognized outcome (including the zero value) is
		// treated as an error rather than allowed.
		m.writeErr(ctx, http.StatusInternalServerError, "authorization is temporarily unavailable")
	}
}

// outcome is the resolved authorization result the middleware acts on. The zero
// value is decisionError so any unset or unhandled outcome fails closed, keeping
// the decision pipeline deny-by-default by construction.
type outcome int

const (
	decisionError outcome = iota
	decisionAllow
	decisionDeny
)

// evaluate builds the Cedar request for decl, runs the authorizer over a
// request-scoped composite getter, and resolves the outcome. A getter load
// failure is reported as decisionError so the caller fails closed; a Cedar Deny
// (or an evaluation diagnostic) is decisionDeny.
func (m *Middleware) evaluate(ctx huma.Context, principal Principal, decl *declaration) outcome {
	getter := newGetter(ctx.Context(), principal, m.authorizer.Contributions())

	req := cedar.Request{
		Principal: principal.UID,
		Action:    decl.action,
		Resource:  resourceFor(decl, ctx),
		Context:   principal.Claims,
	}

	decision, diag := m.authorizer.Authorize(getter, req)

	if err := getter.Err(); err != nil {
		m.logger.ErrorContext(ctx.Context(), "authorization entity load failed",
			slog.String("operation", ctx.Operation().OperationID),
			slog.Any("error", err))

		return decisionError
	}

	if decision == cedar.Allow {
		return decisionAllow
	}

	m.logger.InfoContext(ctx.Context(), "authorization denied",
		slog.String("operation", ctx.Operation().OperationID),
		slog.String("principal", principal.UID.String()),
		slog.String("action", decl.action.String()),
		slog.Any("reasons", diag.Reasons))

	return decisionDeny
}

// resourceFor builds the Cedar resource entity for decl. When the declaration
// binds a path parameter (Require(action, idParam)), the resource is the
// instance Type::"<id>", read straight from the matched route's path value — no
// database load — so policies can decide on the specific instance. Without a
// bound parameter (collection operations), the resource is the type-level entity
// derived from the action's resource segment.
//
// The route is matched before the Huma middleware runs, so ctx.Param(idParam)
// returns the matched path value here. An empty value (a missing or unmatched
// parameter) falls back to the type-level resource rather than minting a
// Type::"" instance, keeping a misconfiguration coarse rather than nonsensical.
func resourceFor(decl *declaration, ctx huma.Context) cedar.EntityUID {
	resourceType := resourceTypeFromAction(decl.action)
	if decl.idParam == "" {
		return resourceType
	}

	id := ctx.Param(decl.idParam)
	if id == "" {
		return resourceType
	}

	return cedar.NewEntityUID(resourceType.Type, types.String(id))
}

// principal returns the request principal, defaulting to anonymous when the
// authn middleware did not store one (for example, when authz runs without authn
// in a test).
func (m *Middleware) principal(ctx huma.Context) Principal {
	if p, ok := PrincipalFrom(ctx.Context()); ok {
		return p
	}

	return Anonymous()
}

// deny writes a 403 for an authenticated caller and a 401 for an anonymous one,
// signalling that credentials are required rather than insufficient.
func (m *Middleware) deny(ctx huma.Context, principal Principal) {
	if principal.IsAnonymous() {
		m.writeErr(ctx, http.StatusUnauthorized, genericUnauthorized)

		return
	}

	m.writeErr(ctx, http.StatusForbidden, genericForbidden)
}

// writeErr emits an RFC 9457 problem response through Huma's content negotiation,
// matching the error shape every other surface returns.
func (m *Middleware) writeErr(ctx huma.Context, status int, detail string) {
	_ = huma.WriteErr(m.api, ctx, status, detail)
}
