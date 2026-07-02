package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// introspectClientAuthStatus is the HTTP status for a missing client credential
// at /introspect: upstream answers presence-only auth with 400 (not 401).
const introspectClientAuthStatus = http.StatusBadRequest

// IntrospectInput is the POST /{issuer}/introspect input: the issuer path
// segment, the Authorization header (presence-only client auth), and the raw
// url-encoded body carrying token/token_type_hint (parsed by the adapter).
type IntrospectInput struct {
	Issuer        string `path:"issuer"`
	Authorization string `              header:"Authorization"`
	RawBody       []byte `                                     contentType:"application/x-www-form-urlencoded"`
}

func (i *IntrospectInput) issuerID() string { return i.Issuer }

// registerIntrospect mounts POST /{issuer}/introspect and documents the
// url-encoded request fields plus the RFC 7662 response shape.
func (h *handlers) registerIntrospect(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "introspect",
		Method:      http.MethodPost,
		Path:        "/{issuer}/introspect",
		Summary:     "OAuth2 token introspection endpoint",
		Tags:        []string{tagOAuth},
	}, h.introspect)
	stampFormSchema(api, "/{issuer}/introspect", tokenHintFormSchema())
	stampJSONResponse(api, "/{issuer}/introspect", http.MethodPost, schemaOf(api, IntrospectionDTO{}))
}

// introspect enforces presence-only client authentication at the edge (a missing
// Authorization header is invalid_client at 400), flat-parses token/
// token_type_hint, and serializes the introspection result. An unverifiable token
// is {active:false} at 200 — never an error. It NEVER returns a Go error; every
// failure routes through protocolError.
func (h *handlers) introspect(ctx context.Context, in *IntrospectInput) (*ProtocolJSON, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return protocolError(err), nil
	}
	if strings.TrimSpace(in.Authorization) == "" {
		return protocolError(
			oidc.InvalidClientStatus(introspectClientAuthStatus, "client authentication required"),
		), nil
	}
	form, err := parseFormFlat(in.RawBody)
	if err != nil {
		//nolint:nilerr // a form-parse failure is surfaced as an OAuth2 error body, never a Go error.
		return protocolError(oidc.MalformedRequest("could not parse form body")), nil
	}
	req := oidc.IntrospectionRequest{
		Issuer: issuer,
		Token:  oidc.SignedToken(form.Get("token")),
		Hint:   oidc.TokenTypeHint(form.Get("token_type_hint")),
	}
	result, err := h.deps.Session.Introspect(ctx, req)
	if err != nil {
		return protocolError(err), nil
	}
	return &ProtocolJSON{Status: http.StatusOK, Body: toIntrospectionBody(result)}, nil
}

// tokenHintFormSchema documents the token/token_type_hint url-encoded fields
// shared by the /introspect and /revoke request bodies.
func tokenHintFormSchema() map[string]*huma.Schema {
	str := &huma.Schema{Type: huma.TypeString}
	return map[string]*huma.Schema{
		"token":           str,
		"token_type_hint": str,
	}
}
