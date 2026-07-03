package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// LoginInput is the POST /{issuer}/authorize input: the issuer path segment, the
// authorize query parameters (the login form POSTs back to the same URL, so they
// ride the query string), and the url-encoded body carrying username + optional
// claims (parsed by the adapter, not Huma). The query fields are declared flat
// (not via an embedded struct) because Huma v2 does not bind embedded-struct
// params.
type LoginInput struct {
	Issuer              string `path:"issuer"`
	ResponseType        string `query:"response_type"`
	ClientID            string `query:"client_id"`
	RedirectURI         string `query:"redirect_uri"`
	Scope               string `query:"scope"`
	State               string `query:"state"`
	Nonce               string `query:"nonce"`
	ResponseMode        string `query:"response_mode"`
	Prompt              string `query:"prompt"`
	CodeChallenge       string `query:"code_challenge"`
	CodeChallengeMethod string `query:"code_challenge_method"`
	LoginHint           string `query:"login_hint"`
	RawBody             []byte `contentType:"application/x-www-form-urlencoded"`
}

func (i *LoginInput) issuerID() string { return i.Issuer }

// params projects the flat query fields into the shared authorizeParams value.
func (i *LoginInput) params() authorizeParams {
	return authorizeParams{
		ResponseType:        i.ResponseType,
		ClientID:            i.ClientID,
		RedirectURI:         i.RedirectURI,
		Scope:               i.Scope,
		State:               i.State,
		Nonce:               i.Nonce,
		ResponseMode:        i.ResponseMode,
		Prompt:              i.Prompt,
		CodeChallenge:       i.CodeChallenge,
		CodeChallengeMethod: i.CodeChallengeMethod,
		LoginHint:           i.LoginHint,
	}
}

// registerLogin mounts POST /{issuer}/authorize as a second operation on the same
// path (distinct OperationID, shared BrowserOutput). The url-encoded request-body
// schema is stamped onto the operation for OpenAPI fidelity, matching the token
// endpoint's RawBody treatment.
func (h *handlers) registerLogin(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "authorize-login",
		Method:        http.MethodPost,
		Path:          "/{issuer}/authorize",
		Summary:       "Interactive login submission",
		Tags:          []string{tagOIDC},
		DefaultStatus: http.StatusFound,
	}, h.login)
	stampFormSchema(api, "/{issuer}/authorize", loginFormSchema())
}

// login parses the login submission, hands it plus the (query-preserved)
// authorize request to the domain, and renders the resulting redirect/form_post.
// A blank username is MissingParameter (invalid_request), routed through
// authorizeError like any /authorize error.
func (h *handlers) login(ctx context.Context, in *LoginInput) (*BrowserOutput, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return h.authorizeError(in.RedirectURI, oidc.ResponseModeQuery, in.State, err), nil
	}
	req := decodeAuthorizeRequest(issuer, in.params())

	login, err := h.decodeLoginSubmission(in.RawBody)
	if err != nil {
		return h.authorizeError(in.RedirectURI, req.ResponseMode, req.State, err), nil
	}

	result, err := h.deps.Authorize.SubmitLogin(ctx, req, login)
	if err != nil {
		return h.authorizeError(in.RedirectURI, req.ResponseMode, req.State, err), nil
	}
	return h.renderAuthorizeResult(result), nil
}

// decodeLoginSubmission parses the url-encoded login body (flat, last-wins) into
// the typed oidc.LoginSubmission: username is required (→ MissingParameter), and
// the optional claims JSON is dropped-with-warning at the edge if malformed, so
// the domain only ever sees a well-formed, possibly-empty claim set.
func (h *handlers) decodeLoginSubmission(raw []byte) (oidc.LoginSubmission, error) {
	form, err := parseFormFlat(raw)
	if err != nil {
		return oidc.LoginSubmission{}, oidc.MalformedRequest("could not parse form body")
	}
	claims, ok := parseLoginClaims(form.Get("claims"))
	if !ok {
		h.logger.Warn("dropping malformed login claims JSON")
	}
	return oidc.NewLoginSubmission(form.Get("username"), claims)
}

// parseLoginClaims parses the optional login "claims" field as a flat JSON object
// into an insertion-ordered CustomClaims. Empty input yields an empty set (ok);
// anything that is not a JSON object yields (empty, false) so the caller can warn.
// Object member order is preserved via a token walk (encoding/json into a map
// would randomize it).
func parseLoginClaims(raw string) (oidc.CustomClaims, bool) {
	var claims oidc.CustomClaims
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return claims, true
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return oidc.CustomClaims{}, false
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return oidc.CustomClaims{}, false
		}
		key, ok := keyTok.(string)
		if !ok {
			return oidc.CustomClaims{}, false
		}
		var val any
		if err := dec.Decode(&val); err != nil {
			return oidc.CustomClaims{}, false
		}
		claims.Set(key, oidc.ClaimValue(val))
	}
	return claims, true
}

// claimsToJSON serializes an ordered claim set into a compact JSON object for
// the login-page template pre-fill — the inverse of parseLoginClaims. The object
// is built member-by-member so insertion order survives (marshaling a map would
// randomize it); json.Marshal escapes <, >, and & by default, so the result is
// safe for the attribute context it renders into. Empty claims yield "" (a blank
// textarea), and a marshal failure — unreachable for config-parsed, JSON-native
// values — degrades to "" rather than a partial object.
func claimsToJSON(claims oidc.CustomClaims) string {
	entries := claims.Entries()
	if len(entries) == 0 {
		return ""
	}
	var buf strings.Builder
	buf.WriteByte('{')
	for i, e := range entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(e.Name)
		if err != nil {
			return ""
		}
		value, err := json.Marshal(e.Value)
		if err != nil {
			return ""
		}
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.String()
}

// loginFormSchema documents the url-encoded login-submission fields for the
// OpenAPI document (username required; optional claims JSON blob).
func loginFormSchema() map[string]*huma.Schema {
	return map[string]*huma.Schema{
		"username": {Type: huma.TypeString},
		"claims":   {Type: huma.TypeString},
	}
}
