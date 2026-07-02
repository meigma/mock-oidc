// Package oidc is the mock-oidc application core: the pure OIDC/OAuth2 domain
// (typed values, invariants, closed enums, typed errors) and the application
// services that orchestrate use-cases over outbound ports.
//
// Dependency rule (enforced by the depguard "oidc-core" rule in .golangci.yml
// and by TestCoreImportsAreClean): this package MAY import only the standard
// library, log/slog, and internal/logctx. It MUST NOT import huma, chi, humachi,
// net/http, net/url, the crypto signing/JOSE packages (crypto/rsa, crypto/ecdsa,
// crypto/ed25519, crypto/tls, crypto/x509, go-jose/jwx), viper, cobra, otel,
// prometheus, pgx, or any internal/adapter/*. Crypto signing, HTTP, IO, and time
// are reached solely through the ports declared in ports.go and clock.go.
//
// One carve-out: the keyless PKCE S256 transform may use crypto/sha256,
// crypto/subtle, and encoding/base64 — pure, keyless computation, not key-bearing
// signing — and these are deliberately NOT denied by the depguard rule.
//
// This slice (Slice 0) ships the package empty of behavior; the domain types,
// ports, and services land in Slice 1+.
package oidc
