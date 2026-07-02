package oidc

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
