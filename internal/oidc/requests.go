package oidc

import "slices"

// TokenRequest is the typed, transport-free command the token endpoint consumes.
// The httpapi edge parses raw form bytes into it (parse-don't-validate); the
// service reads only typed values. Grant-specific fields beyond the
// client_credentials set (authorization code, refresh token, assertions) are
// added by their slices.
type TokenRequest struct {
	Issuer IssuerID
	Grant  GrantType
	Client Client
	Scopes Scopes

	// authorization_code grant parameters, attached via WithAuthorizationCode.
	Code         AuthorizationCode // the code being redeemed
	CodeVerifier string            // PKCE code_verifier (may be empty when no challenge was registered)
	RedirectURI  string            // captured, intentionally NOT validated (parity)
}

// NewTokenRequest builds a TokenRequest for the given issuer, grant, and client.
// Grant-specific parameters are attached through the With* builders.
func NewTokenRequest(issuer IssuerID, grant GrantType, client Client) TokenRequest {
	return TokenRequest{Issuer: issuer, Grant: grant, Client: client, Scopes: nil}
}

// WithScopes returns a copy of the request carrying the parsed scopes.
func (r TokenRequest) WithScopes(s Scopes) TokenRequest {
	r.Scopes = s
	return r
}

// WithAuthorizationCode returns a copy of the request carrying the
// authorization_code grant parameters: the code being redeemed, the optional
// PKCE code_verifier, and the captured (never validated) redirect_uri.
func (r TokenRequest) WithAuthorizationCode(code AuthorizationCode, verifier, redirectURI string) TokenRequest {
	r.Code = code
	r.CodeVerifier = verifier
	r.RedirectURI = redirectURI
	return r
}

// CallbackInput projects the request into the transport-free view a
// TokenCallback matches and templates against. Grant-specific paths populate
// Subject/Params/Audience; the client_credentials path leaves them zero so the
// default callback derives sub from the client and aud from the 4-step chain.
func (r TokenRequest) CallbackInput() CallbackInput {
	return CallbackInput{
		Grant:    r.Grant,
		Client:   r.Client,
		Scopes:   r.Scopes,
		Subject:  "",
		Params:   nil,
		Audience: nil,
	}
}

// ResponseType is the closed OAuth2 `response_type`. Only ResponseTypeCode is
// dispatched by /authorize; the hybrid/implicit members are advertised in
// discovery but not implemented, so they exist as named values, not as behavior.
type ResponseType string

// The advertised response types (discovery response_types_supported). Only
// ResponseTypeCode drives a flow.
const (
	ResponseTypeCode    ResponseType = "code"
	ResponseTypeNone    ResponseType = "none"
	ResponseTypeIDToken ResponseType = "id_token"
	ResponseTypeToken   ResponseType = "token"
)

// allResponseTypes is the authoritative membership list; Valid derives from it.
//
//nolint:gochecknoglobals // single source of truth for the advertised response-type set.
var allResponseTypes = []ResponseType{
	ResponseTypeCode, ResponseTypeNone, ResponseTypeIDToken, ResponseTypeToken,
}

// Valid reports whether t is a member of the advertised response-type set.
func (t ResponseType) Valid() bool {
	return slices.Contains(allResponseTypes, t)
}

// ResponseMode is the closed OAuth2 `response_mode` that decides how the code is
// delivered: appended to the redirect query, to its fragment, or auto-POSTed via
// a self-submitting form (form_post). The domain only selects the mode; the
// httpapi adapter renders the redirect string.
type ResponseMode string

// The three delivery modes advertised in discovery.
const (
	ResponseModeQuery    ResponseMode = "query"
	ResponseModeFragment ResponseMode = "fragment"
	ResponseModeFormPost ResponseMode = "form_post"
)

// allResponseModes is the authoritative membership list; Valid derives from it.
//
//nolint:gochecknoglobals // single source of truth for the advertised response-mode set.
var allResponseModes = []ResponseMode{
	ResponseModeQuery, ResponseModeFragment, ResponseModeFormPost,
}

// Valid reports whether m is a member of the advertised response-mode set.
func (m ResponseMode) Valid() bool {
	return slices.Contains(allResponseModes, m)
}

// Prompt is the OIDC `prompt` parameter. Its only domain-relevant behavior is
// RequiresLogin: login/consent/select_account force the interactive page; none
// and the empty value do not.
type Prompt string

// The OIDC prompt values the domain distinguishes.
const (
	PromptNone          Prompt = "none"
	PromptLogin         Prompt = "login"
	PromptConsent       Prompt = "consent"
	PromptSelectAccount Prompt = "select_account"
)

// RequiresLogin reports whether this prompt forces the interactive login page.
// It is true for login/consent/select_account and false for none and the empty
// value (and any unrecognized value, which is treated as no forced login).
func (p Prompt) RequiresLogin() bool {
	switch p {
	case PromptLogin, PromptConsent, PromptSelectAccount:
		return true
	case PromptNone, "":
		return false
	default:
		return false
	}
}

// AuthorizeRequest is the typed, transport-free command the /authorize endpoint
// consumes. The httpapi edge parses raw query params into it (parse-don't-
// validate); RedirectURI is captured but intentionally NOT validated (parity).
type AuthorizeRequest struct {
	Issuer       IssuerID
	Client       Client
	ResponseType ResponseType
	RedirectURI  string
	Scopes       Scopes
	State        string
	Nonce        *Nonce
	Prompt       Prompt
	PKCE         *PKCEChallenge
	ResponseMode ResponseMode
	LoginHint    string
}

// AuthorizeSnapshot is the subset of an AuthorizeRequest cached in a CodeRecord:
// the fields the token exchange reproduces, minus the transient nonce/PKCE/login
// which NewCodeRecord takes separately.
type AuthorizeSnapshot struct {
	Issuer       IssuerID
	Client       Client
	RedirectURI  string
	Scope        Scopes
	ResponseMode ResponseMode
}

// Snapshot projects the request into the AuthorizeSnapshot cached against the
// issued code.
func (r AuthorizeRequest) Snapshot() AuthorizeSnapshot {
	return AuthorizeSnapshot{
		Issuer:       r.Issuer,
		Client:       r.Client,
		RedirectURI:  r.RedirectURI,
		Scope:        r.Scopes,
		ResponseMode: r.ResponseMode,
	}
}
