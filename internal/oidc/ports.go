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

// Signer is the JOSE-serialization seam: it mints signed JWTs and performs the
// inverse, deliberately-unverified decode the delegation grants need. The
// unsigned Token (header + claims + KeyID) is a pure domain value; the adapter
// holds the private key and performs the JWS, stamping alg/kid/typ from the
// Token's header. JOSE compact-serialization lives entirely in the signing
// adapter, never in the domain.
//
// ParseUnverified lives here (on Signer) rather than on TokenVerifier by design.
// TokenVerifier's contract is genuine verification (signature + iss + typ + times)
// and its sole consumer is the SessionService (/userinfo, /introspect); grafting a
// no-verification parse onto it would blur that contract. The delegation grants
// need only the payload of an inbound compact JWS, and they run inside the
// TokenService, which already holds this Signer — so the unverified parse belongs
// on the same seam TokenService already depends on (TDD §8.8 "reuses Signer"). The
// signing adapter satisfies both ports, so no wiring changes.
//
// Signer deliberately exposes no Algorithms() method: the advertised set is the
// domain constant SupportedSigningAlgorithms(), and the constant-sync test
// cross-checks the adapter against it (TDD §3, single-source correction).
type Signer interface {
	// Sign serializes and signs tok for issuer id, returning the compact JWS.
	Sign(ctx context.Context, id IssuerID, tok Token) (SignedToken, error)
	// ParseUnverified decodes the claims of a compact JWS WITHOUT checking its
	// signature, issuer, or temporal claims — the on-behalf-of assertion and
	// token-exchange subject_token are parsed, not verified (catalog lines 97-98).
	// It must reject structurally-malformed input (non-3-segment, over-length, or
	// undecodable header/payload) with a typed invalid_request *ProtocolError and
	// never panic, never trust the header alg, and never perform any key op.
	ParseUnverified(ctx context.Context, token SignedToken) (ClaimSet, error)
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

// CodeStore is the single-use cache for authorization codes. Save stores the
// CodeRecord snapshot under a freshly minted code; Take atomically returns AND
// removes it, so a code redeems exactly once — the removal happens before the
// PKCE check, so a failed exchange still burns the code (catalog). A miss is a
// sentinel the token service maps to invalid_grant "unknown or already-used
// authorization code". Implementations must be concurrency-safe.
type CodeStore interface {
	// Save stores a single-use record under code.
	Save(ctx context.Context, code AuthorizationCode, rec CodeRecord) error
	// Take atomically returns and removes the record for code; a miss returns a
	// non-nil error.
	Take(ctx context.Context, code AuthorizationCode) (CodeRecord, error)
}

// RefreshTokenStore persists issued refresh tokens for later redemption. It is
// PERSIST-ONLY: the domain service mints the token value and decides its form
// (bare UUID, or the unsigned alg=none PlainJWT form when a nonce is present),
// and the signing adapter produces any compact bytes — the store never mints.
// The service enforces issuer binding: a token minted by issuer A and presented
// to B is rejected with the corrected cross-issuer text after Lookup, so Lookup
// returns the record bound to a token regardless of the presented issuer.
// Implementations must be concurrency-safe.
type RefreshTokenStore interface {
	// Save persists rec (bound callback + nonce) under tok for issuer,
	// overwriting any prior record for the same token.
	Save(ctx context.Context, issuer IssuerID, tok RefreshToken, rec RefreshRecord) error
	// Lookup returns the record bound to tok. A miss returns a non-nil sentinel
	// the service maps to invalid_grant.
	Lookup(ctx context.Context, issuer IssuerID, tok RefreshToken) (RefreshRecord, error)
	// Remove invalidates tok (revoke / rotation). Removing an absent token is a
	// no-op so revoke is idempotent.
	Remove(ctx context.Context, tok RefreshToken) error
}

// TokenVerifier verifies a signed JWT against an issuer and returns its claims.
// It backs /userinfo and /introspect. now threads the SAME freezable Clock as
// issuance so iat/exp are checked against one time base — a control-plane clock
// advance moves both issuance and verification. Verification pins the alg to the
// resolved key's algorithm (alg-confusion guard, alg=none rejected), matches iss
// to the resolved issuer, and accepts typ=JWT and typ=at+jwt (RFC 9068, Decision
// D-4), rejecting any other typ. A failure is a typed error the service maps to
// invalid_token (/userinfo) or {active:false} (/introspect). Only the signing
// adapter satisfies this port (it is the sole JOSE importer).
type TokenVerifier interface {
	// Verify checks signature, issuer, typ, iat and exp, returning the parsed
	// ClaimSet on success or a typed error on failure.
	Verify(ctx context.Context, issuer IssuerID, token SignedToken, now Instant) (ClaimSet, error)
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
