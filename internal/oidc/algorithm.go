package oidc

import (
	"fmt"
	"slices"
)

// SigningAlgorithm is the closed set of JWS signing algorithms this server can
// produce. Algorithm is a config-time value (configured per issuer/callback,
// never read off a token request); ES256K, ES512, EdDSA, all HMAC, and none are
// known-and-refused, not merely unknown.
type SigningAlgorithm string

// The supported signing algorithms plus the default. RSA and PS families are
// RSA-keyed; ES families are EC-keyed. DefaultSigningAlgorithm is used when no
// algorithm is configured.
const (
	RS256 SigningAlgorithm = "RS256"
	RS384 SigningAlgorithm = "RS384"
	RS512 SigningAlgorithm = "RS512"
	PS256 SigningAlgorithm = "PS256"
	PS384 SigningAlgorithm = "PS384"
	PS512 SigningAlgorithm = "PS512"
	ES256 SigningAlgorithm = "ES256"
	ES384 SigningAlgorithm = "ES384"

	// DefaultSigningAlgorithm is used when no algorithm is configured.
	DefaultSigningAlgorithm = RS256
)

// supportedSigningAlgorithms is the single source of truth for what the signer
// can produce AND what discovery may advertise (the §6 constant-sync test pins
// these equal). The EC family is listed first to match upstream discovery
// ordering.
//
//nolint:gochecknoglobals // single source of truth for the closed algorithm set (TDD §4).
var supportedSigningAlgorithms = []SigningAlgorithm{
	ES256, ES384,
	RS256, RS384, RS512, PS256, PS384, PS512,
}

// SupportedSigningAlgorithms returns the advertised/producible algorithm set in
// discovery order. It returns a clone so callers cannot mutate the source of
// truth; it is the single accessor discovery feeds into
// id_token_signing_alg_values_supported (no SupportedAlgorithms alias exists).
func SupportedSigningAlgorithms() []SigningAlgorithm {
	return slices.Clone(supportedSigningAlgorithms)
}

// ParseSigningAlgorithm accepts a supported algorithm and rejects every other
// value with ErrUnsupportedAlgorithm. Known-but-refused algorithms (EdDSA,
// ES256K, ES512, HMAC, none) are reported with upstream's asserted wording
// ("Unsupported algorithm: <x>"). As a config-time parser it returns a wrapped
// sentinel, not a *ProtocolError.
func ParseSigningAlgorithm(s string) (SigningAlgorithm, error) {
	a := SigningAlgorithm(s)
	if slices.Contains(supportedSigningAlgorithms, a) {
		return a, nil
	}
	return "", fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, s)
}
