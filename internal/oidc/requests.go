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

	// refresh_token grant parameter, attached via WithRefreshToken.
	RefreshToken RefreshToken // the refresh token being redeemed

	// password (ROPC) grant parameter, attached via WithPassword. The password
	// value itself is NEVER carried inward — it is captured at the edge and
	// discarded, never validated (catalog line 96).
	Username Subject

	// jwt-bearer grant parameter, attached via WithAssertion: the inbound
	// on-behalf-of assertion, PARSED (never signature-verified) by the delegation
	// path.
	Assertion SignedToken

	// token-exchange grant parameters, attached via WithSubjectToken. SubjectToken
	// is PARSED (never verified); SubjectTokenType is accepted but not enforced;
	// Audience carries the request `audience` param for the aud-when-none-configured
	// precedence rule (catalog line 98).
	SubjectToken     SignedToken
	SubjectTokenType string
	Audience         Audience
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

// WithRefreshToken returns a copy of the request carrying the refresh_token
// grant parameter: the opaque refresh token being redeemed.
func (r TokenRequest) WithRefreshToken(tok RefreshToken) TokenRequest {
	r.RefreshToken = tok
	return r
}

// WithPassword returns a copy of the request carrying the password (ROPC) grant
// parameters: the username (which becomes the subject) and the requested scopes.
// The password value is intentionally absent — it is captured and discarded at
// the edge, never validated (catalog line 96), so it never crosses inward.
func (r TokenRequest) WithPassword(username string, scopes Scopes) TokenRequest {
	r.Username = Subject(username)
	r.Scopes = scopes
	return r
}

// WithAssertion returns a copy of the request carrying the jwt-bearer assertion
// and requested scopes. A blank assertion is the one hard error at this edge —
// invalid_request "missing required parameter assertion" (catalog line 97) — so
// the missing-assertion case surfaces as a typed *ProtocolError, not a later
// nil-parse. The assertion is PARSED, never signature-verified, downstream.
func (r TokenRequest) WithAssertion(assertion string, scopes Scopes) (TokenRequest, error) {
	if assertion == "" {
		return TokenRequest{}, MissingParameter("assertion")
	}
	r.Assertion = SignedToken(assertion)
	r.Scopes = scopes
	return r, nil
}

// WithSubjectToken returns a copy of the request carrying the token-exchange
// parameters. A blank subject_token is invalid_request "missing required
// parameter subject_token". subjectTokenType is accepted but NOT enforced; a
// non-empty audience becomes the single-valued request audience candidate for the
// aud-when-none-configured precedence rule (catalog line 98). The subject token is
// PARSED, never signature-verified, downstream.
func (r TokenRequest) WithSubjectToken(subjectToken, subjectTokenType, audience string) (TokenRequest, error) {
	if subjectToken == "" {
		return TokenRequest{}, MissingParameter("subject_token")
	}
	r.SubjectToken = SignedToken(subjectToken)
	r.SubjectTokenType = subjectTokenType
	if audience != "" {
		r.Audience = Audience{audience}
	}
	return r, nil
}

// CallbackInput projects the request into the transport-free view a
// TokenCallback matches and templates against. Grant-specific paths populate
// Subject/Audience: password carries the username as the subject; token-exchange
// carries the request `audience` param as the audience candidate. The
// client_credentials path leaves them zero so the default callback derives sub
// from the client and aud from the 4-step chain.
func (r TokenRequest) CallbackInput() CallbackInput {
	return CallbackInput{
		Grant:    r.Grant,
		Client:   r.Client,
		Scopes:   r.Scopes,
		Subject:  r.Username,
		Params:   nil,
		Audience: r.Audience,
	}
}

// TokenTypeHint is the closed OAuth2 `token_type_hint` (RFC 7009/7662) the
// domain recognizes at /revoke and /introspect. The empty value means "no hint
// given"; any non-member string the edge parses is carried through verbatim so
// the service can reject it with unsupported_token_type. /revoke supports only
// TokenHintRefreshToken — access_token is a recognized hint but not a revocable
// one here.
type TokenTypeHint string

// The recognized token_type_hint values.
const (
	TokenHintAccessToken  TokenTypeHint = "access_token"
	TokenHintRefreshToken TokenTypeHint = "refresh_token"
)

// allTokenTypeHints is the authoritative membership list; Valid derives from it.
//
//nolint:gochecknoglobals // single source of truth for the recognized token_type_hint set.
var allTokenTypeHints = []TokenTypeHint{TokenHintAccessToken, TokenHintRefreshToken}

// Valid reports whether h is a recognized token_type_hint (access_token or
// refresh_token). The empty value and any other string are not.
func (h TokenTypeHint) Valid() bool {
	return slices.Contains(allTokenTypeHints, h)
}

// UserInfoRequest is the typed /userinfo command: the issuer and the bearer
// access token parsed from the Authorization header at the edge.
type UserInfoRequest struct {
	Issuer IssuerID
	Token  SignedToken
}

// IntrospectionRequest is the typed /introspect command (RFC 7662): the issuer,
// the token to inspect, and the optional token_type_hint. Client authentication
// is a presence-only check enforced at the edge, not carried here.
type IntrospectionRequest struct {
	Issuer IssuerID
	Token  SignedToken
	Hint   TokenTypeHint
}

// RevocationRequest is the typed /revoke command (RFC 7009). Token is a
// RefreshToken because /revoke only removes refresh tokens; Hint gates the
// operation (anything but refresh_token -> unsupported_token_type).
type RevocationRequest struct {
	Issuer IssuerID
	Token  RefreshToken
	Hint   TokenTypeHint
}

// EndSessionRequest is the typed RP-initiated-logout command. Both fields are
// read from the QUERY only (parity); State is appended to the redirect by the
// edge only when present.
type EndSessionRequest struct {
	Issuer                IssuerID
	PostLogoutRedirectURI string
	State                 string
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
