package oidc

import "slices"

// GrantType is the closed set of OAuth2 grants the token endpoint dispatches on.
type GrantType string

// The six OAuth2 grant types. Only client_credentials is dispatched in Slice 1;
// the rest are declared so the closed set and its predicate matrix are complete.
// The URN grant types are protocol identifiers, not credentials.
const (
	GrantAuthorizationCode GrantType = "authorization_code"
	GrantClientCredentials GrantType = "client_credentials"
	GrantPassword          GrantType = "password"
	GrantRefreshToken      GrantType = "refresh_token"
	GrantJWTBearer         GrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"     //nolint:gosec // G101: OAuth2 grant-type URN, not a credential.
	GrantTokenExchange     GrantType = "urn:ietf:params:oauth:grant-type:token-exchange" //nolint:gosec // G101: OAuth2 grant-type URN, not a credential.
)

// allGrantTypes is the authoritative membership list; Valid and the
// exhaustiveness test both derive from it.
//
//nolint:gochecknoglobals // single source of truth for the closed grant set (TDD §4).
var allGrantTypes = []GrantType{
	GrantAuthorizationCode, GrantClientCredentials, GrantPassword,
	GrantRefreshToken, GrantJWTBearer, GrantTokenExchange,
}

// Valid reports whether g is a member of the closed grant set, derived from the
// single-source allGrantTypes list.
func (g GrantType) Valid() bool {
	return slices.Contains(allGrantTypes, g)
}

// IssuesRefreshToken reports whether this grant returns a refresh_token
// (authorization_code and refresh_token only — the per-grant token matrix).
func (g GrantType) IssuesRefreshToken() bool {
	switch g {
	case GrantAuthorizationCode, GrantRefreshToken:
		return true
	case GrantClientCredentials, GrantPassword, GrantJWTBearer, GrantTokenExchange:
		return false
	default:
		return false
	}
}

// IssuesIDToken reports whether this grant mints an id_token (authorization_code,
// refresh_token, password). client_credentials, jwt-bearer, and token-exchange
// do not.
func (g GrantType) IssuesIDToken() bool {
	switch g {
	case GrantAuthorizationCode, GrantRefreshToken, GrantPassword:
		return true
	case GrantClientCredentials, GrantJWTBearer, GrantTokenExchange:
		return false
	default:
		return false
	}
}

// ParseGrantType parses the form `grant_type` into a GrantType. It is on the
// request path, so it returns a typed *ProtocolError: blank -> invalid_request
// (missing parameter); unknown -> invalid_grant "grant_type <x> not supported."
// (wrapping ErrUnsupportedGrantType), matching upstream's code and text.
func ParseGrantType(s string) (GrantType, error) {
	if s == "" {
		return "", MissingParameter("grant_type")
	}
	g := GrantType(s)
	if !g.Valid() {
		return "", UnsupportedGrant(s)
	}
	return g, nil
}
