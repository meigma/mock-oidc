package oidc

// DiscoveryDocument is the provider metadata served at
// /{issuer}/.well-known/openid-configuration (and the identical RFC 8414
// oauth-authorization-server body). Field order is FIXED by struct declaration
// order and mirrors upstream's corrected order (catalog §Discovery): the eight
// endpoints first, then the *_supported lists.
type DiscoveryDocument struct {
	Issuer                           string
	AuthorizationEndpoint            string
	EndSessionEndpoint               string
	RevocationEndpoint               string
	TokenEndpoint                    string
	UserinfoEndpoint                 string
	JWKSURI                          string
	IntrospectionEndpoint            string
	ResponseTypesSupported           []string
	ResponseModesSupported           []string
	SubjectTypesSupported            []string
	IDTokenSigningAlgValuesSupported []SigningAlgorithm
	CodeChallengeMethodsSupported    []string
}

// Endpoint path suffixes joined onto the issuer URL to form each endpoint.
const (
	suffixAuthorize  = "/authorize"
	suffixEndSession = "/endsession"
	suffixRevoke     = "/revoke"
	suffixToken      = "/token"
	suffixUserinfo   = "/userinfo"
	suffixJWKS       = "/jwks"
	suffixIntrospect = "/introspect"
)

// NewDiscoveryDocument builds the discovery document for issuer id at base,
// joining each endpoint onto the issuer URL (base.IssuerURL(id) — the host root
// with the id segment). algs is the advertised signing-algorithm set, sourced
// from SupportedSigningAlgorithms() so discovery advertises exactly what the
// signer can produce (the §6 constant-sync invariant). The advertised response
// types / modes / subject types / PKCE methods mirror upstream.
func NewDiscoveryDocument(base BaseURL, id IssuerID, algs []SigningAlgorithm) DiscoveryDocument {
	issuer := base.IssuerURL(id)
	return DiscoveryDocument{
		Issuer:                           issuer,
		AuthorizationEndpoint:            issuer + suffixAuthorize,
		EndSessionEndpoint:               issuer + suffixEndSession,
		RevocationEndpoint:               issuer + suffixRevoke,
		TokenEndpoint:                    issuer + suffixToken,
		UserinfoEndpoint:                 issuer + suffixUserinfo,
		JWKSURI:                          issuer + suffixJWKS,
		IntrospectionEndpoint:            issuer + suffixIntrospect,
		ResponseTypesSupported:           []string{"code", "none", "id_token", "token"},
		ResponseModesSupported:           []string{"query", "fragment", "form_post"},
		SubjectTypesSupported:            []string{"public"},
		IDTokenSigningAlgValuesSupported: algs,
		CodeChallengeMethodsSupported:    []string{"plain", "S256"},
	}
}
