package authz

import "github.com/danielgtaylor/huma/v2"

// Authenticator inspects an incoming request and resolves the caller's identity.
// It is the deferred-authentication seam: the template ships an API-key
// authenticator (internal/authz/apikey), and integrators swap in a real
// verifier (JWT/OIDC/session) without touching the authz engine.
type Authenticator interface {
	// Authenticate inspects ctx and returns a verified Principal, or
	// (Anonymous, nil) when no credentials are present — public operations must
	// still work, so absence of credentials is not an error here. It returns an
	// error only when a credential is present but malformed or invalid, which
	// the authn middleware maps to 401.
	Authenticate(ctx huma.Context) (Principal, error)
}
