package oidc

// TokenResponse is the typed result of a successful token issuance. This is the
// client_credentials shape (access token only); id_token and refresh_token
// fields are added by the slices that issue them. The httpapi adapter maps this
// to the wire DTO (omitempty on optional fields).
type TokenResponse struct {
	TokenType   TokenType
	AccessToken SignedToken
	ExpiresIn   int64 // seconds; derived from the same Clock as exp (parity correction)
	Scope       Scopes
}
