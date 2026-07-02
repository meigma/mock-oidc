package oidc

// KeyID is a JWS key identifier. For this server kid == IssuerID by
// construction.
type KeyID string

// KeyType is the closed JWK key family.
type KeyType string

// The supported JWK key families.
const (
	KeyTypeRSA KeyType = "RSA"
	KeyTypeEC  KeyType = "EC"
)

// PublicParams is the sealed set of public-JWK parameter shapes (closed via the
// unexported marker method). It replaces an opaque map[string]string so the
// domain carries typed key parameters, not stringly-typed open keys.
type PublicParams interface{ isPublicParams() }

// RSAPublicParams carries the public RSA parameters (n, e).
type RSAPublicParams struct{ N, E string }

func (RSAPublicParams) isPublicParams() {}

// ECPublicParams carries the public EC parameters (crv, x, y).
type ECPublicParams struct{ Crv, X, Y string }

func (ECPublicParams) isPublicParams() {}

// SigningKey is the public metadata of an issuer's signing key. No private
// material ever enters the domain; the signing adapter owns that. kid equals the
// issuer id by construction.
type SigningKey struct {
	KeyID     KeyID
	Algorithm SigningAlgorithm
	Public    JWK // public parameters only (use=sig)
}

// JWK is a single public JSON Web Key (public parameters only). The signing
// adapter materializes it; the jwks adapter renders it. The domain holds typed
// parameters via the sealed PublicParams union, never an open string map.
type JWK struct {
	KeyID     KeyID
	Algorithm SigningAlgorithm
	KeyType   KeyType
	Use       string       // always "sig"
	Params    PublicParams // RSAPublicParams | ECPublicParams
}

// JWKS (alias KeySet) is the public key set served at /{issuer}/jwks.
type JWKS struct {
	Keys []JWK
}

// KeySet is an alias for JWKS, matching the glossary's naming.
type KeySet = JWKS
