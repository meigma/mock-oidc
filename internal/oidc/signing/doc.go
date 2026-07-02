// Package signing is the driven adapter implementing the core's Signer and
// KeyStore ports with real key-bearing crypto (RSA-2048). It materializes one
// deterministic key per issuer (kid == issuer id), drawn FIFO from an embedded
// 5-key RSA JWKS seed before any fresh key is generated, and performs the JWS
// compact serialization. It is the only OIDC package that touches key-bearing
// crypto; the domain never serializes or holds private material.
package signing
