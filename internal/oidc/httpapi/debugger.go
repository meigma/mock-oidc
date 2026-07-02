package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// debuggerCookieName is the flow-state cookie the front-channel POST sets and the
// callback reads back. It carries the state, nonce, PKCE verifier, client_id, and
// redirect_uri across the /authorize redirect round trip.
const debuggerCookieName = "mock_oidc_debugger"

// debuggerCookieMaxAge bounds the flow cookie's lifetime (seconds) — long enough
// for a human to complete the redirect, short enough not to linger.
const debuggerCookieMaxAge = 600

// DebuggerInput is GET /{issuer}/debugger: the issuer path segment plus optional
// query prefills for the interactive form (client_id, scope, subject).
type DebuggerInput struct {
	Issuer   string `path:"issuer"`
	ClientID string `              query:"client_id"`
	Scope    string `              query:"scope"`
	Subject  string `              query:"subject"`
}

func (i *DebuggerInput) issuerID() string { return i.Issuer }

// DebuggerSubmitInput is POST /{issuer}/debugger: the issuer path segment and the
// url-encoded form the browser submits (client_id, scope, subject).
type DebuggerSubmitInput struct {
	Issuer  string `path:"issuer"`
	RawBody []byte `              contentType:"application/x-www-form-urlencoded"`
}

func (i *DebuggerSubmitInput) issuerID() string { return i.Issuer }

// DebuggerCallbackInput is GET|POST /{issuer}/debugger/callback: the return leg of
// the code flow. code/state/error arrive on the query (default query response
// mode); the flow cookie carries the PKCE verifier and client_id. It is read from
// the raw Cookie header (no framework cookie binding) so the parsing stays
// explicit at the edge.
type DebuggerCallbackInput struct {
	Issuer           string `path:"issuer"`
	Code             string `              query:"code"`
	State            string `              query:"state"`
	Error            string `              query:"error"`
	ErrorDescription string `              query:"error_description"`
	Cookie           string `                                        header:"Cookie"`
}

func (i *DebuggerCallbackInput) issuerID() string { return i.Issuer }

// debuggerSubmitOutput is the front-channel POST envelope: a 302 into /authorize
// carrying the flow-state Set-Cookie.
type debuggerSubmitOutput struct {
	Status    int
	Location  string `header:"Location"`
	SetCookie string `header:"Set-Cookie"`
}

// debuggerFlow is the flow state stashed in the cookie across the redirect round
// trip. It is base64url(JSON)-encoded; the values are non-secret PKCE/flow data.
type debuggerFlow struct {
	State       string `json:"state"`
	Nonce       string `json:"nonce"`
	Verifier    string `json:"verifier"`
	ClientID    string `json:"clientId"`
	RedirectURI string `json:"redirectUri"`
}

// registerDebugger mounts the four debugger operations per the N.3 inventory: the
// pre-filled form (GET), the front-channel submit (POST), and the callback return
// leg on BOTH GET and POST as two registrations sharing one handler (distinct
// OperationIDs), mirroring the endsession pattern.
func (h *handlers) registerDebugger(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "debugger",
		Method:      http.MethodGet,
		Path:        "/{issuer}/debugger",
		Summary:     "Interactive OIDC debugger (pre-filled authorize form)",
		Tags:        []string{tagOIDC},
	}, h.debuggerForm)
	huma.Register(api, huma.Operation{
		OperationID:   "debugger-submit",
		Method:        http.MethodPost,
		Path:          "/{issuer}/debugger",
		Summary:       "Debugger: start the authorization-code flow",
		Tags:          []string{tagOIDC},
		DefaultStatus: http.StatusFound,
	}, h.debuggerSubmit)
	stampFormSchema(api, "/{issuer}/debugger", debuggerFormSchema())
	huma.Register(api, huma.Operation{
		OperationID: "debugger-callback-get",
		Method:      http.MethodGet,
		Path:        "/{issuer}/debugger/callback",
		Summary:     "Debugger: authorization-code callback (back-channel exchange)",
		Tags:        []string{tagOIDC},
	}, h.debuggerCallback)
	huma.Register(api, huma.Operation{
		OperationID: "debugger-callback-post",
		Method:      http.MethodPost,
		Path:        "/{issuer}/debugger/callback",
		Summary:     "Debugger: authorization-code callback (back-channel exchange)",
		Tags:        []string{tagOIDC},
	}, h.debuggerCallback)
}

// debuggerForm renders the pre-filled interactive form. The action targets the
// same issuer's /{issuer}/debugger POST; the form fields seed the flow the submit
// leg kicks off. A malformed/reserved issuer renders the shared HTML error page.
func (h *handlers) debuggerForm(_ context.Context, in *DebuggerInput) (*BrowserOutput, error) {
	if _, err := issuerOf(in); err != nil {
		return h.debuggerError(err), nil
	}
	data := debuggerFormData{
		Action:   "/" + url.PathEscape(in.Issuer) + "/debugger",
		Issuer:   in.Issuer,
		ClientID: orDefault(in.ClientID, "debugger"),
		Scope:    orDefault(in.Scope, "openid profile email"),
		Subject:  in.Subject,
	}
	return htmlOutput(http.StatusOK, tmplDebugger, data), nil
}

// debuggerSubmit parses the form, generates the flow secrets (state, nonce, PKCE
// verifier + S256 challenge), stashes them in the flow cookie, and 302-redirects
// the browser into the issuer's own /authorize with an absolute callback
// redirect_uri — the front-channel leg. It never returns a Go error.
func (h *handlers) debuggerSubmit(
	ctx context.Context,
	in *DebuggerSubmitInput,
) (*debuggerSubmitOutput, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return h.debuggerSubmitError(err)
	}
	base, err := oidc.ResolveBaseURL(originFrom(ctx))
	if err != nil {
		return h.debuggerSubmitError(err)
	}
	form, err := parseFormFlat(in.RawBody)
	if err != nil {
		return h.debuggerSubmitError(oidc.MalformedRequest("could not parse form body"))
	}

	flow := debuggerFlow{
		State:       randToken(),
		Nonce:       randToken(),
		Verifier:    randToken(),
		ClientID:    orDefault(form.Get(clientIDParam), "debugger"),
		RedirectURI: base.IssuerURL(issuer) + "/debugger/callback",
	}

	query := debuggerAuthorizeQuery(flow, form.Get("scope")).Encode()
	authorizeURL := base.IssuerURL(issuer) + "/authorize?" + query
	return &debuggerSubmitOutput{
		Status:    http.StatusFound,
		Location:  authorizeURL,
		SetCookie: h.debuggerSetCookie(issuer, flow),
	}, nil
}

// debuggerCallback is the redirect return leg (GET and POST share it). It reads
// the flow cookie, rejects a state mismatch or an /authorize error, then performs
// the REAL back-channel POST /{issuer}/token code exchange against this server's
// own public surface and renders the resulting tokens plus the raw exchange
// request/response bytes. It never returns a Go error.
func (h *handlers) debuggerCallback(
	ctx context.Context,
	in *DebuggerCallbackInput,
) (*BrowserOutput, error) {
	issuer, err := issuerOf(in)
	if err != nil {
		return h.debuggerError(err), nil
	}
	if in.Error != "" {
		return h.debuggerErrorPage(in.Error, in.ErrorDescription), nil
	}
	// The back-channel token endpoint is recomputed from the REQUEST origin (as the
	// submit leg does), never from the cookie: the flow cookie is unauthenticated,
	// so deriving the exchange target from it would be an SSRF vector (an arbitrary
	// host) and could panic. The cookie's redirect_uri is only echoed to /token.
	base, err := oidc.ResolveBaseURL(originFrom(ctx))
	if err != nil {
		//nolint:nilerr // browser surface renders the failure as HTML, never a Go error.
		return h.debuggerErrorPage("server_error", "could not resolve the request origin"), nil
	}
	tokenURL := base.IssuerURL(issuer) + "/token"
	flow, ok := decodeDebuggerFlow(in.Cookie)
	if !ok {
		return h.debuggerErrorPage(
			"invalid_request",
			"missing or malformed debugger flow cookie",
		), nil
	}
	if in.State == "" || in.State != flow.State {
		return h.debuggerErrorPage(
			"invalid_request",
			"state mismatch — the flow cookie does not match the callback",
		), nil
	}
	if in.Code == "" {
		return h.debuggerErrorPage("invalid_request", "no authorization code on the callback"), nil
	}

	reqBody := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {in.Code},
		"redirect_uri":  {flow.RedirectURI},
		clientIDParam:   {flow.ClientID},
		"code_verifier": {flow.Verifier},
	}.Encode()

	status, respBody, err := h.debuggerExchange(ctx, tokenURL, reqBody)
	if err != nil {
		//nolint:nilerr // browser surface renders the failure as HTML, never a Go error.
		return h.debuggerErrorPage(
			"server_error",
			"back-channel token exchange failed: "+err.Error(),
		), nil
	}

	data := debuggerResultData{
		Issuer:         in.Issuer,
		TokenEndpoint:  tokenURL,
		RequestBody:    reqBody,
		ResponseStatus: status,
		ResponseBody:   respBody,
	}
	extractTokens(&data, respBody)
	return htmlOutput(http.StatusOK, tmplDebuggerResult, data), nil
}

// debuggerExchange performs the back-channel token POST and returns the response
// status line, the raw response body, and any transport error.
func (h *handlers) debuggerExchange(
	ctx context.Context,
	tokenURL, body string,
) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.debuggerClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	return resp.Status, string(raw), nil
}

// debuggerSetCookie renders the flow-state Set-Cookie header value: HttpOnly +
// SameSite=Lax, scoped to the issuer's debugger subtree so it rides only the
// callback and not the wider surface.
func (h *handlers) debuggerSetCookie(issuer oidc.IssuerID, flow debuggerFlow) string {
	raw, _ := json.Marshal(flow)
	// Secure is intentionally omitted: the debugger drives the local/dev protocol
	// surface over plain http, and the cookie carries only non-secret PKCE/flow
	// state. HttpOnly + SameSite=Lax are set.
	//nolint:gosec // G124: non-secret test-flow cookie; Secure omitted to work over http.
	cookie := &http.Cookie{
		Name:     debuggerCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     "/" + string(issuer) + "/debugger",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   debuggerCookieMaxAge,
	}
	return cookie.String()
}

// debuggerSubmitError renders a submit-leg failure as the direct HTML error page,
// packaged in the submit output envelope (200, no redirect/cookie).
func (h *handlers) debuggerSubmitError(err error) (*debuggerSubmitOutput, error) {
	_, body := oauth2Error(err)
	page := h.debuggerErrorPage(body.Code, body.Description)
	return &debuggerSubmitOutput{Status: page.Status, SetCookie: ""}, nil
}

// debuggerError renders a protocol error (malformed issuer) as the HTML error page.
func (h *handlers) debuggerError(err error) *BrowserOutput {
	status, body := oauth2Error(err)
	page := htmlOutput(
		status,
		tmplDebuggerResult,
		debuggerResultData{Issuer: "", Error: body.Code, ErrorDescription: body.Description},
	)
	return page
}

// debuggerErrorPage renders the debugger result page in its error state.
func (h *handlers) debuggerErrorPage(code, desc string) *BrowserOutput {
	return htmlOutput(
		http.StatusOK,
		tmplDebuggerResult,
		debuggerResultData{Error: code, ErrorDescription: desc},
	)
}

// debuggerAuthorizeQuery builds the front-channel /authorize query: the code flow
// with the PKCE S256 challenge and the flow's state/nonce.
func debuggerAuthorizeQuery(flow debuggerFlow, scope string) url.Values {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set(clientIDParam, flow.ClientID)
	q.Set("redirect_uri", flow.RedirectURI)
	q.Set("scope", orDefault(scope, "openid profile email"))
	q.Set("state", flow.State)
	q.Set("nonce", flow.Nonce)
	q.Set("code_challenge", pkceS256(flow.Verifier))
	q.Set("code_challenge_method", "S256")
	return q
}

// decodeDebuggerFlow extracts and decodes the flow cookie from a raw Cookie
// header, returning ok=false when it is absent or malformed.
func decodeDebuggerFlow(cookieHeader string) (debuggerFlow, bool) {
	value, ok := cookieValue(cookieHeader, debuggerCookieName)
	if !ok {
		return debuggerFlow{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return debuggerFlow{}, false
	}
	var flow debuggerFlow
	if err := json.Unmarshal(raw, &flow); err != nil {
		return debuggerFlow{}, false
	}
	return flow, true
}

// cookieValue extracts a single cookie value from a raw Cookie request header
// (name=value; name2=value2), returning ok=false when the name is absent.
func cookieValue(header, name string) (string, bool) {
	for part := range strings.SplitSeq(header, ";") {
		k, v, found := strings.Cut(strings.TrimSpace(part), "=")
		if found && k == name {
			return v, true
		}
	}
	return "", false
}

// extractTokens best-effort parses the token response JSON to surface the tokens
// in dedicated fields; a non-JSON/error body leaves them empty (the raw response
// is always shown regardless).
func extractTokens(data *debuggerResultData, respBody string) {
	var tok struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal([]byte(respBody), &tok); err != nil {
		return
	}
	data.AccessToken = tok.AccessToken
	data.IDToken = tok.IDToken
	data.RefreshToken = tok.RefreshToken
	data.TokenType = tok.TokenType
	data.ExpiresIn = tok.ExpiresIn
	data.Scope = tok.Scope
}

// pkceS256 computes the RFC 7636 S256 code_challenge for a verifier:
// base64url(SHA-256(verifier)), no padding.
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randToken returns a URL-safe random token (32 bytes of entropy) for the state,
// nonce, and PKCE verifier. It panics only on a crypto/rand failure, which is
// unrecoverable.
func randToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// orDefault returns v when non-empty, else the fallback.
func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// debuggerFormSchema documents the url-encoded debugger submit fields for the
// OpenAPI document.
func debuggerFormSchema() map[string]*huma.Schema {
	str := &huma.Schema{Type: huma.TypeString}
	return map[string]*huma.Schema{
		clientIDParam: str,
		"scope":       str,
		"subject":     str,
	}
}
