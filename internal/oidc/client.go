package oidc

import "time"

// maxPrivateKeyJWTLifetime caps the accepted lifetime (exp - iat) of a
// private_key_jwt client assertion. Upstream's requirePrivateKeyJwt sets
// maxLifetimeSeconds=120 (catalog line 109).
const maxPrivateKeyJWTLifetime = 120 * time.Second

// ValidatePrivateKeyJWT applies the STRUCTURAL-ONLY validation of a token-exchange
// private_key_jwt client assertion (RFC 7523 §3, catalog line 109). The assertion
// signature is DELIBERATELY NOT cryptographically verified — its claims are decoded
// (unverified) at the adapter edge and handed inward as a typed ClaimSet; this
// method checks only the structural rules, each of which fails with invalid_request:
//
//  1. lifetime (exp - iat) exceeds 120s;
//  2. iss != client_id;
//  3. sub != client_id;
//  4. audience is empty;
//  5. audience carries more than one value;
//  6. audience[0] is neither the issuer URL nor the token-endpoint URL.
//
// The receiver is unnamed because the check is keyed on the assertion, not on how
// the client presented itself; associating it with ClientAuth keeps the call site
// (client.Auth.ValidatePrivateKeyJWT(...)) intent-revealing. now is reserved for
// symmetry with the other temporal validators; the lifetime rule is measured as
// exp - iat, independent of the current instant.
func (ClientAuth) ValidatePrivateKeyJWT(
	assertion ClaimSet,
	clientID ClientID,
	issuerURL, tokenEndpointURL string,
	_ Instant,
) error {
	lifetime := assertion.Expiry.Time().Sub(assertion.IssuedAt.Time())
	if lifetime > maxPrivateKeyJWTLifetime {
		return MalformedRequest("client assertion lifetime exceeds the 120s maximum")
	}
	if assertion.Issuer != string(clientID) {
		return MalformedRequest("client assertion iss must equal the client_id")
	}
	if string(assertion.Subject) != string(clientID) {
		return MalformedRequest("client assertion sub must equal the client_id")
	}
	switch len(assertion.Audience) {
	case 0:
		return MalformedRequest("client assertion audience must not be empty")
	case 1:
		// exactly one audience — checked below.
	default:
		return MalformedRequest("client assertion audience must carry exactly one value")
	}
	if aud := assertion.Audience[0]; aud != issuerURL && aud != tokenEndpointURL {
		return MalformedRequest("client assertion audience is not an accepted value")
	}
	return nil
}
