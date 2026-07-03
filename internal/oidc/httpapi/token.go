package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TokenInput is the /token operation input: the issuer path segment, the raw
// url-encoded body (parsed by the adapter, not Huma), and the Authorization
// header carrying client auth.
type TokenInput struct {
	Issuer        string `path:"issuer"`
	Authorization string `header:"Authorization"` // Basic / Bearer client-auth carrier
	RawBody       []byte `contentType:"application/x-www-form-urlencoded"`
}

func (i *TokenInput) issuerID() string { return i.Issuer }

// registerToken mounts ONLY POST /{issuer}/token. A GET is intentionally not
// registered: the router's protocol-family 405 fallback emits the uniform OAuth2
// error shape (Decision D-2), so no bespoke "unsupported method" body is served.
func (h *handlers) registerToken(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "token",
		Method:      http.MethodPost,
		Path:        "/{issuer}/token",
		Summary:     "OAuth2 token endpoint",
		Tags:        []string{tagOAuth},
	}, h.token)
	stampFormSchema(api, "/{issuer}/token", tokenFormSchema())
	stampJSONResponse(api, "/{issuer}/token", http.MethodPost, schemaOf(api, TokenResponseDTO{}))
}

// token parses the raw form (flat, last-wins), decodes the typed command at the
// anti-corruption boundary, issues via the token service, and serializes either
// the token DTO (200) or an OAuth2Error (4xx/5xx). It NEVER returns a Go error for
// a protocol failure — every failure routes through protocolError.
func (h *handlers) token(ctx context.Context, in *TokenInput) (*ProtocolJSON, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return protocolError(err), nil
	}
	form, err := parseFormFlat(in.RawBody)
	if err != nil {
		//nolint:nilerr // a form-parse failure is surfaced as an OAuth2 error body, never a Go error.
		return protocolError(oidc.MalformedRequest("could not parse form body")), nil
	}
	cmd, err := decodeTokenRequest(issuer, form, in.Authorization)
	if err != nil {
		return protocolError(err), nil
	}
	// Attach the multi-valued form view so a RequestMappingCallback can match and
	// ${...}-template against arbitrary submitted params, not just the grant fields.
	// A parse error here is non-fatal: the flat parse above already succeeded, so
	// the grant proceeds without request-mapping params rather than failing.
	if params, perr := parseFormMulti(in.RawBody); perr == nil {
		cmd = cmd.WithParams(params)
	}
	resp, err := h.deps.Tokens.Issue(ctx, originFrom(ctx), cmd)
	if err != nil {
		return protocolError(err), nil
	}
	return &ProtocolJSON{Status: http.StatusOK, Body: toTokenResponseDTO(resp)}, nil
}

// tokenFormSchema documents the url-encoded token request fields for the OpenAPI
// document. It covers every wired grant's fields plus the grant-agnostic and
// private_key_jwt client-auth fields.
func tokenFormSchema() map[string]*huma.Schema {
	str := &huma.Schema{Type: huma.TypeString}
	return map[string]*huma.Schema{
		"grant_type":            str,
		"scope":                 str,
		clientIDParam:           str,
		"client_secret":         str,
		"code":                  str,
		"code_verifier":         str,
		"redirect_uri":          str,
		"refresh_token":         str,
		"username":              str,
		"password":              str,
		"assertion":             str,
		"subject_token":         str,
		"subject_token_type":    str,
		"audience":              str,
		"client_assertion":      str,
		"client_assertion_type": str,
	}
}
