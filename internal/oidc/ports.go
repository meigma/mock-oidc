package oidc

import (
	"context"
	"fmt"
)

// KeyStore provides per-issuer PUBLIC signing-key metadata and the published JWK
// set. Keys are generated lazily on first reference (computeIfAbsent) with
// kid == issuerId, drawn from an embedded RSA JWKS seed before any key is
// generated (stable keys across restarts). The same id always yields the same
// key for the process lifetime; private material never crosses this port.
// Implementations must be concurrency-safe.
type KeyStore interface {
	// SigningKey returns the public metadata for id's signing key, materializing
	// it on first reference.
	SigningKey(ctx context.Context, id IssuerID) (SigningKey, error)
	// PublicKeys returns the JWK set served at the issuer's jwks_uri. It forces
	// materialization of id's key so the set is never empty.
	PublicKeys(ctx context.Context, id IssuerID) (JWKS, error)
}

// Signer mints signed JWTs. The unsigned Token (header + claims + KeyID) is a
// pure domain value; the adapter holds the private key and performs the JWS,
// stamping alg/kid/typ from the Token's header. JOSE compact-serialization lives
// entirely in the signing adapter, never in the domain.
//
// Signer deliberately exposes no Algorithms() method: the advertised set is the
// domain constant SupportedSigningAlgorithms(), and the constant-sync test
// cross-checks the adapter against it (TDD §3, single-source correction).
type Signer interface {
	// Sign serializes and signs tok for issuer id, returning the compact JWS.
	Sign(ctx context.Context, id IssuerID, tok Token) (SignedToken, error)
}

// IssuerRecord is the static, per-issuer state the registry holds: the issuer's
// id and any callbacks seeded from JSON config (empty for zero-config, on-demand
// issuers). It carries no key material and no per-request base URL.
type IssuerRecord struct {
	ID        IssuerID
	Callbacks []TokenCallback // configured RequestMapping callbacks, first-match order
}

// IssuerRegistry records issuers on demand and exposes their static config. Any
// non-reserved IssuerID becomes live on first reference (computeIfAbsent); the
// config seed pre-populates records that carry configured callbacks.
// Implementations must be concurrency-safe.
type IssuerRegistry interface {
	// Materialize records id as a live issuer on first reference and returns its
	// record. Idempotent for the process lifetime.
	Materialize(ctx context.Context, id IssuerID) (IssuerRecord, error)
	// Known returns every materialized issuer id, for control-plane enumeration.
	Known(ctx context.Context) ([]IssuerID, error)
}

// issuerResolver assembles the per-request Issuer aggregate from the registry
// (identity + configured callbacks), the key store (public key), and the
// proxy-aware base URL. It is a domain-internal collaborator — not a port, not a
// service — shared by the services that need an Issuer, so none of them depend
// on another service.
type issuerResolver struct {
	registry IssuerRegistry
	keys     KeyStore
}

// resolve materializes the issuer, fetches its public signing key, and resolves
// the proxy-aware base URL, returning the assembled Issuer aggregate. BaseURL is
// intentionally not cached: it is a per-request function of the RequestOrigin.
func (r issuerResolver) resolve(ctx context.Context, id IssuerID, origin RequestOrigin) (Issuer, error) {
	rec, err := r.registry.Materialize(ctx, id)
	if err != nil {
		return Issuer{}, fmt.Errorf("materialize issuer: %w", err)
	}
	key, err := r.keys.SigningKey(ctx, id)
	if err != nil {
		return Issuer{}, fmt.Errorf("issuer signing key: %w", err)
	}
	base, err := ResolveBaseURL(origin)
	if err != nil {
		return Issuer{}, fmt.Errorf("resolve base url: %w", err)
	}
	return NewIssuer(id, base, key, rec.Callbacks), nil
}
