package oidc

// TokenResponse is the typed result of a successful token issuance. The
// client_credentials shape carries only the access token; the authorization_code
// (and, in later slices, refresh/password) shapes also carry IDToken and
// RefreshToken. The httpapi adapter maps this to the wire DTO, applying omitempty
// so the id_token/refresh_token keys are absent when unset.
type TokenResponse struct {
	TokenType    TokenType
	AccessToken  SignedToken
	IDToken      SignedToken  // set for authorization_code (and other id-token grants)
	RefreshToken RefreshToken // set for grants that issue a refresh token
	ExpiresIn    int64        // seconds; derived from the same Clock as exp (parity correction)
	Scope        Scopes
}

// IntrospectionResult is the typed RFC 7662 introspection outcome. Active
// reports whether the token verified; when true, Claims carries the verified
// claim set and TokenType is the introspection token_type (default Bearer). The
// edge maps this to the wire DTO — collapsing a single-element aud to a scalar
// and emitting the registered claims — so the domain stays transport-free.
type IntrospectionResult struct {
	Active    bool
	TokenType TokenType
	Claims    ClaimSet
}

// InactiveIntrospection is the {active:false} result an unverifiable token
// yields. It is reported at 200, never as an error (parity: introspecting a bad
// token is not a failure).
func InactiveIntrospection() IntrospectionResult {
	return IntrospectionResult{Active: false, TokenType: "", Claims: ClaimSet{}}
}

// IntrospectionFrom builds an active introspection result from a verified claim
// set, defaulting token_type to Bearer. The single-element aud -> scalar collapse
// lives on the DTO at the edge; the domain result stays typed.
func IntrospectionFrom(claims ClaimSet) IntrospectionResult {
	return IntrospectionResult{Active: true, TokenType: TokenTypeBearer, Claims: claims}
}

// EndSessionResult is the typed RP-initiated-logout outcome. A non-empty
// RedirectURI means the edge issues a 302 to it (appending ?state=State only when
// State is non-empty); an empty RedirectURI means the edge renders the plain
// "logged out" page at 200. The domain never builds the URL — the edge shapes it.
type EndSessionResult struct {
	RedirectURI string
	State       string
}

// NewEndSessionResult builds the logout outcome from the query-supplied redirect
// URI and state (both may be empty).
func NewEndSessionResult(uri, state string) EndSessionResult {
	return EndSessionResult{RedirectURI: uri, State: state}
}

// Redirect reports whether a post-logout redirect URI was supplied (302) versus
// the plain logged-out page (200).
func (r EndSessionResult) Redirect() bool { return r.RedirectURI != "" }

// AuthorizeResultKind enumerates what /authorize decided. The adapter switches
// on it: ShowLogin renders the interactive login page; FormPost renders the
// auto-submit HTML; Redirect is a 302 with the code in the query or fragment
// (Mode distinguishes).
type AuthorizeResultKind int

// The three outcomes of an /authorize decision.
const (
	AuthorizeShowLogin AuthorizeResultKind = iota // render the interactive login page
	AuthorizeFormPost                             // response_mode=form_post: auto-submit HTML
	AuthorizeRedirect                             // response_mode=query|fragment: 302 with code
)

// AuthorizeResult is the typed outcome of an /authorize decision. It carries only
// typed fields — the domain never builds a redirect URL; the httpapi adapter
// renders query/fragment/form_post and owns url-encoding at the edge. Request is
// populated for the ShowLogin case (the login page re-submits it); Code/State/
// RedirectURI/Mode are populated for the FormPost and Redirect cases.
type AuthorizeResult struct {
	Kind        AuthorizeResultKind
	Request     AuthorizeRequest
	Code        AuthorizationCode
	State       string
	RedirectURI string
	Mode        ResponseMode
}
