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
