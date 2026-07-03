package httpapi

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// debuggerExchangeTimeout bounds the debugger's back-channel /token exchange so a
// stuck self-call can never pin the request goroutine.
const debuggerExchangeTimeout = 10 * time.Second

// OpenAPI tags grouping the protocol operations in the document.
const (
	tagOIDC  = "OIDC"
	tagOAuth = "OAuth2"
)

// ProviderPort is the provider-metadata use case the driving adapter consumes:
// discovery/RFC 8414 metadata and the JWK set. It is satisfied by
// *oidc.ProviderService; the interface is declared here (consumer side) so the
// adapter depends on behavior, not the concrete service.
type ProviderPort interface {
	Discovery(ctx context.Context, id oidc.IssuerID, origin oidc.RequestOrigin) (oidc.DiscoveryDocument, error)
	JWKS(ctx context.Context, id oidc.IssuerID) (oidc.JWKS, error)
}

// TokenPort is the token-issuance use case the driving adapter consumes. It is
// satisfied by *oidc.TokenService.
type TokenPort interface {
	Issue(ctx context.Context, origin oidc.RequestOrigin, req oidc.TokenRequest) (oidc.TokenResponse, error)
}

// AuthorizePort is the interactive-authorization use case the driving adapter
// consumes: decide between the login page and issuing a code (Authorize), and
// finish a submitted login (SubmitLogin). It is satisfied by
// *oidc.AuthorizeService.
type AuthorizePort interface {
	Authorize(ctx context.Context, req oidc.AuthorizeRequest) (oidc.AuthorizeResult, error)
	SubmitLogin(
		ctx context.Context,
		req oidc.AuthorizeRequest,
		login oidc.LoginSubmission,
	) (oidc.AuthorizeResult, error)
}

// SessionPort is the post-issuance lifecycle use case the driving adapter
// consumes: /userinfo, /introspect, /revoke, and /endsession. It is satisfied by
// *oidc.SessionService.
type SessionPort interface {
	UserInfo(ctx context.Context, req oidc.UserInfoRequest) (oidc.ClaimSet, error)
	Introspect(ctx context.Context, req oidc.IntrospectionRequest) (oidc.IntrospectionResult, error)
	Revoke(ctx context.Context, req oidc.RevocationRequest) error
	EndSession(ctx context.Context, req oidc.EndSessionRequest) (oidc.EndSessionResult, error)
}

// Deps are the core services the protocol handlers orchestrate. The composition
// root builds them from the real signing/memory adapters and passes them here.
// Logger is optional; when nil the handlers discard the edge warnings (malformed
// login claims).
type Deps struct {
	Provider  ProviderPort
	Tokens    TokenPort
	Authorize AuthorizePort
	Session   SessionPort
	Logger    *slog.Logger
	// HTTPClient is the client the debugger uses for its real back-channel /token
	// exchange. Optional; nil installs a bounded default.
	HTTPClient *http.Client
	// SelfAddr is this server's bound listen address (e.g. ":8080"), supplied by the
	// composition root from cfg.Addr. When set, the debugger's back-channel /token
	// exchange dials THIS process's own loopback listener rather than the
	// browser-facing origin, so it stays reachable behind a remapped/mapped container
	// port. Empty preserves the origin-derived dial (in-process httptest tests).
	SelfAddr string
	// LoginTemplates are the config-declared login templates the login page offers
	// as a pre-fill dropdown. The rendering is an adapter concern; the domain
	// consumes the same collection separately for headless login_hint resolution.
	// Empty leaves the login page dropdown-free.
	LoginTemplates oidc.LoginTemplates
}

// handlers binds the dependencies for the operation handler methods.
type handlers struct {
	deps   Deps
	logger *slog.Logger
	// debuggerClient performs the debugger's real back-channel /token exchange
	// against this server's own public surface. It is Deps.HTTPClient when set,
	// else a bounded default.
	debuggerClient *http.Client
	// selfAddr is the bound listen address (Deps.SelfAddr) the debugger loopback-dials
	// its own /token on; empty falls back to the origin-derived front-channel target.
	selfAddr string
}

// Register mounts the Slice 1 protocol operations onto api. It installs the
// request-origin middleware BEFORE any huma.Register (Huma snapshots the
// middleware stack per operation at registration time, so middleware added after
// would never run), then registers discovery, JWKS, and the token endpoint, and
// finally stamps the security schemes onto the document.
func Register(api huma.API, deps Deps) {
	api.UseMiddleware(requestOriginMiddleware) // edge: derive RequestOrigin

	logger := deps.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	client := deps.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: debuggerExchangeTimeout}
	}
	h := &handlers{deps: deps, logger: logger, debuggerClient: client, selfAddr: deps.SelfAddr}
	h.registerDiscovery(api)
	h.registerJWKS(api)
	h.registerToken(api)
	h.registerAuthorize(api)
	h.registerLogin(api)
	h.registerUserinfo(api)
	h.registerIntrospect(api)
	h.registerRevoke(api)
	h.registerEndSession(api)
	h.registerDebugger(api)
	h.registerFavicon(api)

	stampSecuritySchemes(api)
}

// Registrar adapts Register to the internal/adapter/http.Registrar seam
// (func(huma.API)) by binding deps, so the composition root can pass the returned
// closure as RouterDeps.Register without this package knowing the router type.
func Registrar(deps Deps) func(huma.API) {
	return func(api huma.API) { Register(api, deps) }
}

// originCtxKey keys the per-request oidc.RequestOrigin on the context.
type originCtxKey struct{}

// requestOriginMiddleware captures the externally visible origin (scheme, host,
// port, and X-Forwarded-* overrides) once per request and stashes the typed
// oidc.RequestOrigin on the context. This is the ONLY place transport header
// names live; the domain resolver consumes the typed value.
func requestOriginMiddleware(ctx huma.Context, next func(huma.Context)) {
	scheme := oidc.SchemeHTTP
	if ctx.TLS() != nil { // HTTPS terminated at this process
		scheme = oidc.SchemeHTTPS
	}
	host, port := splitHostPort(ctx.Host())
	origin := oidc.RequestOrigin{
		Scheme:   scheme,
		Host:     host,
		Port:     port,
		FwdProto: ctx.Header("X-Forwarded-Proto"),
		FwdHost:  ctx.Header("X-Forwarded-Host"),
		FwdPort:  ctx.Header("X-Forwarded-Port"),
	}
	next(huma.WithValue(ctx, originCtxKey{}, origin))
}

// originFrom recovers the request origin the middleware stashed; a zero value is
// returned when it is absent (the domain resolver then rejects it).
func originFrom(ctx context.Context) oidc.RequestOrigin {
	o, _ := ctx.Value(originCtxKey{}).(oidc.RequestOrigin)
	return o
}

// splitHostPort splits an authority (host[:port]) into its host and numeric
// port, returning port 0 when none is present or it is unparseable.
func splitHostPort(hostport string) (string, int) {
	if hostport == "" {
		return "", 0
	}
	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport, 0 // no port component
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return host, 0
	}
	return host, port
}

// issuerInput is implemented by every issuer-scoped operation input, exposing the
// raw {issuer} path segment for the single issuer smart constructor.
type issuerInput interface{ issuerID() string }

// issuerOf parses the path parameter into the domain identity. A malformed or
// reserved issuer is a typed *oidc.ProtocolError, surfaced through the endpoint's
// normal OAuth2 error path.
func issuerOf(in issuerInput) (oidc.IssuerID, error) {
	return oidc.ParseIssuerID(in.issuerID())
}

// stampSecuritySchemes declares the OAuth2 (client_credentials) and
// openIdConnect security schemes on the document after registration, so the
// server-less export matches the running protocol surface.
func stampSecuritySchemes(api huma.API) {
	comp := api.OpenAPI().Components
	if comp.SecuritySchemes == nil {
		comp.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	//nolint:gosec // G101: OAuth2 token endpoint URL, not a credential.
	ccFlow := &huma.OAuthFlow{
		TokenURL: "/{issuer}/token",
		Scopes:   map[string]string{},
	}
	comp.SecuritySchemes["oauth2"] = &huma.SecurityScheme{
		Type:  "oauth2",
		Flows: &huma.OAuthFlows{ClientCredentials: ccFlow},
	}
	comp.SecuritySchemes["openIdConnect"] = &huma.SecurityScheme{
		Type:             "openIdConnect",
		OpenIDConnectURL: "/{issuer}/.well-known/openid-configuration",
	}
}

// stampJSONResponse documents the 2xx application/json body shape that
// ProtocolJSON.Body any would otherwise erase from the spec, located via Paths
// (Huma exposes no Operations() map).
func stampJSONResponse(api huma.API, path, method string, schema *huma.Schema) {
	op := operationAt(api, path, method)
	if op == nil {
		return
	}
	if op.Responses == nil {
		op.Responses = map[string]*huma.Response{}
	}
	op.Responses["200"] = &huma.Response{
		Description: "Success",
		Content: map[string]*huma.MediaType{
			"application/json": {Schema: schema},
		},
	}
}

// stampFormSchema attaches a typed url-encoded request-body schema to the POST
// operation at path whose handler consumed RawBody, so the committed spec
// documents the real fields. Huma already created the content key for the RawBody
// field, so this overwrites its opaque {type:string,format:binary} schema.
func stampFormSchema(api huma.API, path string, props map[string]*huma.Schema) {
	op := operationAt(api, path, "POST")
	if op == nil || op.RequestBody == nil {
		return
	}
	const ct = "application/x-www-form-urlencoded"
	media := op.RequestBody.Content[ct]
	if media == nil {
		return
	}
	media.Schema = &huma.Schema{Type: huma.TypeObject, Properties: props}
}

// operationAt returns the operation registered at path+method, or nil. Huma
// exposes no Operations() accessor, so the lookup walks OpenAPI.Paths.
func operationAt(api huma.API, path, method string) *huma.Operation {
	item := api.OpenAPI().Paths[path]
	if item == nil {
		return nil
	}
	switch method {
	case "GET":
		return item.Get
	case "POST":
		return item.Post
	default:
		return nil
	}
}

// schemaOf builds a non-ref schema for a DTO type, used to document the JSON
// response bodies.
func schemaOf(api huma.API, v any) *huma.Schema {
	return huma.SchemaFromType(api.OpenAPI().Components.Schemas, reflect.TypeOf(v))
}
