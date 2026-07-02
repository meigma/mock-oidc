package httpapi

// DiscoveryDTO is the provider-metadata wire shape. Field order is the contract's
// FIXED serialization order (catalog correction: token_endpoint is 5th) expressed
// purely by struct declaration order — encoding/json honors declaration order
// even when the value travels through ProtocolJSON.Body any, so no manual
// ordering is needed. A unit test pins the JSON field order to this declaration.
type DiscoveryDTO struct {
	Issuer                 string   `json:"issuer"`
	AuthorizationEndpoint  string   `json:"authorization_endpoint"`
	EndSessionEndpoint     string   `json:"end_session_endpoint"`
	RevocationEndpoint     string   `json:"revocation_endpoint"`
	TokenEndpoint          string   `json:"token_endpoint"`
	UserinfoEndpoint       string   `json:"userinfo_endpoint"`
	JwksURI                string   `json:"jwks_uri"`
	IntrospectionEndpoint  string   `json:"introspection_endpoint"`
	ResponseTypesSupported []string `json:"response_types_supported"`
	ResponseModesSupported []string `json:"response_modes_supported"`
	SubjectTypesSupported  []string `json:"subject_types_supported"`
	IDTokenSigningAlgs     []string `json:"id_token_signing_alg_values_supported"`
	CodeChallengeMethods   []string `json:"code_challenge_methods_supported"`
}

// JWKSDTO is the JWK set served at /{issuer}/jwks.
type JWKSDTO struct {
	Keys []JWKDTO `json:"keys"`
}

// JWKDTO is a single public JSON Web Key. Only public parameters are ever
// serialized; the RSA (n,e) and EC (crv,x,y) unions are omitempty so exactly the
// key type's parameters appear.
type JWKDTO struct {
	Kty string `json:"kty"`
	Use string `json:"use"` // "sig"
	Kid string `json:"kid"` // == issuer id
	Alg string `json:"alg"`
	N   string `json:"n,omitempty"`   // RSA modulus
	E   string `json:"e,omitempty"`   // RSA exponent
	Crv string `json:"crv,omitempty"` // EC curve
	X   string `json:"x,omitempty"`   // EC x
	Y   string `json:"y,omitempty"`   // EC y
}

// TokenResponseDTO is the /{issuer}/token success body. Optional per-grant fields
// are omitempty (matching upstream's NON_NULL), so client_credentials emits only
// token_type/access_token/expires_in (+scope when echoed). ExpiresIn is present
// even when 0 because it is domain-computed from the same Clock as exp.
type TokenResponseDTO struct {
	TokenType       string `json:"token_type"`                  // always "Bearer"
	IssuedTokenType string `json:"issued_token_type,omitempty"` // token-exchange only
	IDToken         string `json:"id_token,omitempty"`
	AccessToken     string `json:"access_token"`
	RefreshToken    string `json:"refresh_token,omitempty"`
	ExpiresIn       int    `json:"expires_in"` // present even when 0; domain-computed
	Scope           string `json:"scope,omitempty"`
}

// OAuth2Error is the RFC 6749 §5.2 error body. It is returned as a success-shaped
// output value (never a Go error) so it bypasses Huma's RFC 9457 path. Text is
// emitted correct-case — upstream lowercases the whole body, mangling
// error_description; this project does not replicate that defect.
type OAuth2Error struct {
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
	URI         string `json:"error_uri,omitempty"`
}

// ContentType pins the body to application/json regardless of the Accept header.
func (OAuth2Error) ContentType(string) string { return "application/json" }

// ProtocolJSON is the success-shaped JSON envelope every protocol JSON endpoint
// returns (never as a Go error), so it bypasses Huma's global RFC 9457 error
// path. Body is a success DTO on 2xx or an OAuth2Error on 4xx/5xx; both marshal
// as application/json, and the handler sets Status. WWWAuth is emitted only for
// invalid_token responses.
type ProtocolJSON struct {
	Status  int
	WWWAuth string `header:"WWW-Authenticate"`
	Body    any
}
