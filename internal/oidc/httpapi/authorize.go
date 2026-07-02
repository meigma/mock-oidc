package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// authorizeParams is the decoded, transport-agnostic view of the /authorize
// query parameters that the GET and login-POST handlers share. It is NOT a Huma
// input struct: Huma v2 does not bind or document embedded-struct query params,
// so both concrete inputs declare the fields flat and project into this value
// via params().
type authorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	Scope               string
	State               string
	Nonce               string
	ResponseMode        string
	Prompt              string
	CodeChallenge       string
	CodeChallengeMethod string
}

// AuthorizeInput is the GET /{issuer}/authorize input: the issuer path segment
// plus the authorize query parameters, taken as PERMISSIVE strings (no Huma
// required/enum/min/max) so the handler — not the framework — decides whether a
// defect becomes a redirect-with-error or a direct error.
type AuthorizeInput struct {
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
}

func (i *AuthorizeInput) issuerID() string { return i.Issuer }

// params projects the flat query fields into the shared authorizeParams value.
func (i *AuthorizeInput) params() authorizeParams {
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
	}
}

// registerAuthorize mounts GET /{issuer}/authorize with DefaultStatus 302 (the
// redirect outcome; the handler overrides Status for the HTML outcomes). The
// login POST shares the path under a distinct OperationID (login.go).
func (h *handlers) registerAuthorize(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "authorize",
		Method:        http.MethodGet,
		Path:          "/{issuer}/authorize",
		Summary:       "OAuth2/OIDC authorization endpoint",
		Tags:          []string{tagOIDC},
		DefaultStatus: http.StatusFound,
	}, h.authorize)
}

// authorize decodes the permissive query into a typed AuthorizeRequest, asks the
// domain to decide (interactive login vs issue code), and renders the outcome:
// an HTML login page, a self-submitting form_post page, or a 302 with the code
// in the redirect. Every error routes to authorizeError, which redirects it into
// redirect_uri when usable, else renders the direct error page. It NEVER returns
// a Go error, so the browser surface has one error contract.
func (h *handlers) authorize(ctx context.Context, in *AuthorizeInput) (*BrowserOutput, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return h.authorizeError(in.RedirectURI, oidc.ResponseModeQuery, in.State, err), nil
	}
	req := decodeAuthorizeRequest(issuer, in.params())

	result, err := h.deps.Authorize.Authorize(ctx, req)
	if err != nil {
		return h.authorizeError(in.RedirectURI, req.ResponseMode, req.State, err), nil
	}

	if result.Kind == oidc.AuthorizeShowLogin {
		return h.loginPage(in.Issuer, in.params()), nil
	}
	return h.renderAuthorizeResult(result), nil
}

// renderAuthorizeResult renders the code-issued outcomes shared by GET (direct
// code) and POST (after login): a form_post self-submitting page, or a 302 with
// the code appended per response_mode.
func (h *handlers) renderAuthorizeResult(result oidc.AuthorizeResult) *BrowserOutput {
	if result.Kind == oidc.AuthorizeFormPost {
		return htmlOutput(http.StatusOK, tmplFormPost, formPostData{
			RedirectURI: result.RedirectURI,
			Code:        string(result.Code),
			State:       result.State,
		})
	}
	return &BrowserOutput{
		Status:   http.StatusFound,
		Location: appendCode(result.RedirectURI, result.Mode, result.Code, result.State),
	}
}

// loginPage renders the interactive login form, whose action is the same
// /authorize URL with the authorize query string preserved (so the POST carries
// the parameters back). The URL is auto-escaped in the template's attribute
// context.
func (h *handlers) loginPage(issuer string, p authorizeParams) *BrowserOutput {
	return htmlOutput(http.StatusOK, tmplLogin, loginData{
		Action: authorizeActionURL(issuer, p),
	})
}

// authorizeError renders a protocol error on the /authorize surface: it appends
// the OAuth2 error into redirect_uri (per response_mode) when a usable
// redirect_uri is present, else it renders the direct HTML error page at the
// mapped status. Empty state is tolerated (omitted) — upstream's form_post
// missing-state 500 is NOT replicated.
func (h *handlers) authorizeError(redirectURI string, mode oidc.ResponseMode, state string, err error) *BrowserOutput {
	status, body := oauth2Error(err)
	if usableRedirect(redirectURI) {
		return &BrowserOutput{
			Status:   http.StatusFound,
			Location: appendError(redirectURI, mode, body, state),
		}
	}
	return htmlOutput(status, tmplError, errorData{Error: body.Code, Description: body.Description})
}

// decodeAuthorizeRequest builds the typed AuthorizeRequest from the permissive
// query strings. It is intentionally infallible: nothing is rejected here
// (parse-don't-validate), and redirect_uri is captured but NOT validated
// (parity). response_type membership is decided by the domain (AuthorizeService),
// not the edge, so a bad response_type still routes through the domain error path
// with a usable redirect. An absent nonce/PKCE stays nil (presence is semantic).
func decodeAuthorizeRequest(issuer oidc.IssuerID, p authorizeParams) oidc.AuthorizeRequest {
	req := oidc.AuthorizeRequest{
		Issuer:       issuer,
		Client:       oidc.Client{ID: oidc.ClientID(p.ClientID), Auth: oidc.ClientAuthNone},
		ResponseType: oidc.ResponseType(p.ResponseType),
		RedirectURI:  p.RedirectURI,
		Scopes:       oidc.ParseScopes(p.Scope),
		State:        p.State,
		Prompt:       oidc.Prompt(p.Prompt),
		ResponseMode: oidc.ResponseMode(p.ResponseMode),
	}
	if p.Nonce != "" {
		n := oidc.Nonce(p.Nonce)
		req.Nonce = &n
	}
	if p.CodeChallenge != "" {
		method := oidc.CodeChallengeMethod(p.CodeChallengeMethod)
		if method == "" {
			method = oidc.ChallengePlain // RFC 7636: default method is plain when omitted
		}
		req.PKCE = &oidc.PKCEChallenge{Challenge: p.CodeChallenge, Method: method}
	}
	return req
}

// authorizeActionURL reconstructs the /{issuer}/authorize URL with the authorize
// query parameters preserved, so the login form POSTs the identity while the
// authorize parameters ride the query string.
func authorizeActionURL(issuer string, p authorizeParams) string {
	q := url.Values{}
	setNonEmpty(q, "response_type", p.ResponseType)
	setNonEmpty(q, clientIDParam, p.ClientID)
	setNonEmpty(q, "redirect_uri", p.RedirectURI)
	setNonEmpty(q, "scope", p.Scope)
	setNonEmpty(q, "state", p.State)
	setNonEmpty(q, "nonce", p.Nonce)
	setNonEmpty(q, "response_mode", p.ResponseMode)
	setNonEmpty(q, "prompt", p.Prompt)
	setNonEmpty(q, "code_challenge", p.CodeChallenge)
	setNonEmpty(q, "code_challenge_method", p.CodeChallengeMethod)

	path := "/" + url.PathEscape(issuer) + "/authorize"
	if enc := q.Encode(); enc != "" {
		return path + "?" + enc
	}
	return path
}

// appendCode is the ONLY owner of success redirect-URL construction: it appends
// code (+ state when present) to redirect_uri, in the query string or — for
// response_mode=fragment — after the fragment '#'. url-encoding happens here at
// the edge; the domain never builds a redirect URL.
func appendCode(redirectURI string, mode oidc.ResponseMode, code oidc.AuthorizationCode, state string) string {
	params := url.Values{}
	params.Set("code", string(code))
	setNonEmpty(params, "state", state)
	return appendParams(redirectURI, mode, params)
}

// appendError is the symmetric error builder: it appends the OAuth2 error (+
// state when present) to redirect_uri the same way appendCode appends the code.
func appendError(redirectURI string, mode oidc.ResponseMode, e OAuth2Error, state string) string {
	params := url.Values{}
	params.Set("error", e.Code)
	setNonEmpty(params, "error_description", e.Description)
	setNonEmpty(params, "state", state)
	return appendParams(redirectURI, mode, params)
}

// appendParams appends the url-encoded params to redirectURI: after '#' for
// fragment mode (choosing '&' when a fragment already exists), else on the query
// string. It concatenates the already-encoded set rather than re-parsing the URL,
// so an existing query/fragment is preserved verbatim without re-sorting or
// double-encoding.
func appendParams(redirectURI string, mode oidc.ResponseMode, params url.Values) string {
	enc := params.Encode()
	if mode == oidc.ResponseModeFragment {
		sep := "#"
		if strings.Contains(redirectURI, "#") {
			sep = "&"
		}
		return redirectURI + sep + enc
	}
	sep := "?"
	if strings.Contains(redirectURI, "?") {
		sep = "&"
	}
	return redirectURI + sep + enc
}

// usableRedirect reports whether redirect_uri is present and absolute enough to
// carry an error back (a parseable URI with a scheme). When it is not, the
// authorize error is shown as a direct HTML page instead of a broken redirect.
func usableRedirect(redirectURI string) bool {
	if redirectURI == "" {
		return false
	}
	u, err := url.Parse(redirectURI)
	return err == nil && u.Scheme != ""
}

// setNonEmpty sets key=value on q only when value is non-empty, so absent
// parameters stay absent from the reconstructed URL (empty state is omitted, not
// serialized as state=).
func setNonEmpty(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}
