// Package authz is the cross-cutting authorization engine: a thin app-owned
// wrapper over AWS Cedar (github.com/cedar-policy/cedar-go), committed as the
// authorization engine with no portability layer. It owns the merged runtime
// PolicySet, the per-request authentication seam and opaque Principal, the
// global Huma middleware that enforces a deny-by-default policy, and the
// per-operation Require/Public declarations recorded as Huma operation metadata.
//
// Authoring is modular: each domain slice contributes its policies, action
// identifiers, and lazy entity resolvers via a Contribution; the composition
// root merges them into one PolicySet and one request-scoped composite
// EntityGetter. Evaluation is unified over that single shared namespace.
package authz

import (
	"context"

	"github.com/cedar-policy/cedar-go/types"
)

// AnonymousType is the Cedar entity type assigned to an unauthenticated caller.
// The authn middleware stores an anonymous Principal when no credentials are
// present, letting public operations proceed and authz reject protected ones
// with 401 rather than 403.
const AnonymousType types.EntityType = "Anonymous"

// AnonymousID is the identifier of the singleton anonymous principal.
const AnonymousID types.String = "anonymous"

// Principal is the opaque caller identity handed from authentication to
// authorization. UID is the Cedar entity (for example User::"alice"); Claims
// carries roles, groups, scopes, and arbitrary verified attributes, opaque to
// the template. Group memberships are projected onto the principal entity's
// Parents at resolve time so Cedar's `principal in Group::"…"` works without a
// load.
type Principal struct {
	// UID is the Cedar entity identifying the caller.
	UID types.EntityUID
	// Claims carries the caller's verified attributes (roles, groups, scopes).
	Claims types.Record
}

// Anonymous returns the principal used when a request carries no credentials.
func Anonymous() Principal {
	return Principal{UID: types.NewEntityUID(AnonymousType, AnonymousID)}
}

// IsAnonymous reports whether p is the unauthenticated principal.
func (p Principal) IsAnonymous() bool {
	return p.UID.Type == AnonymousType
}

// principalKey is the unexported context key under which the authn middleware
// stores the Principal, keeping the key private to this package.
type principalKey struct{}

// WithPrincipal returns a copy of ctx carrying p. The authn middleware uses it
// to hand the verified principal to the downstream authz middleware.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the Principal stored in ctx. The boolean reports whether
// one was present; when absent, the caller should treat the request as
// anonymous (fail-closed).
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)

	return p, ok
}
