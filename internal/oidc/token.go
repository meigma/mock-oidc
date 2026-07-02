package oidc

// TokenType is the response `token_type` (closed: Bearer).
type TokenType string

// TokenTypeBearer is the only response token_type this server issues.
const TokenTypeBearer TokenType = "Bearer"

// IssuedTokenType is the token-exchange `issued_token_type` (closed: the RFC
// 8693 access_token URN).
type IssuedTokenType string

// IssuedTokenAccessToken is the RFC 8693 access-token type URN (a protocol
// identifier, not a credential).
//
//nolint:gosec // G101: OAuth2 token-type URN, not a credential.
const IssuedTokenAccessToken IssuedTokenType = "urn:ietf:params:oauth:token-type:access_token"

// JOSEType is the JWS "typ" header. It is intentionally OPEN — no closed Valid():
// the default is "JWT", but a TokenCallback may set any value (e.g. RFC 9068
// "at+jwt"), so ParseJOSEType never rejects; it only supplies the default for
// empty input. This is distinct from TokenType (response token_type) and
// IssuedTokenType (token-exchange), which are closed.
type JOSEType string

// DefaultJOSEType is the JWS typ used when a callback specifies none.
const DefaultJOSEType JOSEType = "JWT"

// ParseJOSEType returns the JOSE typ for s, defaulting empty input to "JWT". It
// never rejects — any custom callback value is representable.
func ParseJOSEType(s string) JOSEType {
	if s == "" {
		return DefaultJOSEType
	}
	return JOSEType(s)
}

// JWTHeader is the JWS header for a minted token: algorithm, key id (= issuer),
// and JOSE type (default "JWT"; e.g. "at+jwt" via a callback typeHeader).
type JWTHeader struct {
	Algorithm SigningAlgorithm
	KeyID     KeyID
	Type      JOSEType
}

// Token is an unsigned token model — header plus claims. It crosses to the
// Signer port; the domain performs no crypto and never serializes.
type Token struct {
	Header JWTHeader
	Claims ClaimSet
}

// NewToken assembles an unsigned Token, defaulting the JOSE typ to "JWT". The
// kid is derived from the issuer (kid == IssuerID), the algorithm is explicit,
// and the typ is the open JOSEType (so at+jwt is representable).
func NewToken(issuer IssuerID, alg SigningAlgorithm, typ JOSEType, claims ClaimSet) Token {
	return Token{
		Header: JWTHeader{Algorithm: alg, KeyID: issuer.KeyID(), Type: ParseJOSEType(string(typ))},
		Claims: claims,
	}
}

// SignedToken is the compact-serialized signed JWT: the wire artifact. It is
// opaque to the domain — produced by the Signer adapter, echoed in responses,
// and handed back to the Verifier adapter for /userinfo and /introspect.
type SignedToken string
