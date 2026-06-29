package authz

import (
	"context"

	"github.com/cedar-policy/cedar-go/types"
)

// Contribution is one domain slice's input to the unified authorization engine.
// Each slice ships its embedded Cedar policies, the action identifiers it
// declares (for validation and discovery), and a factory that builds its
// request-scoped entity resolver. The composition root collects all
// contributions and merges them in [New].
type Contribution struct {
	// Policies is the embedded .cedar source for this slice. Policy IDs are
	// re-assigned slice-prefixed during the merge so they stay unique across
	// slices.
	Policies []byte
	// Actions lists the action entities this slice declares (for example
	// Action::"todo:read"). They are recorded for discovery and validation; the
	// engine does not require them to evaluate.
	Actions []types.EntityUID
	// Types lists the Cedar entity type names this slice's resolver owns (for
	// example "Todo"). It is the authoritative routing key: [New] validates that
	// no two contributions claim the same type and that a slice never claims a
	// reserved principal type, and the composite getter routes by it so a slice
	// resolver can never shadow the always-present principal resolver. A slice
	// with a Resolver must declare the types it owns; a slice contributing only
	// coarse policies leaves it empty.
	Types []string
	// Resolver builds this slice's entity resolver, bound to a request. It is nil
	// for slices that contribute only coarse policies needing no entity loads.
	Resolver ResolverFactory
}

// ResolverFactory builds a slice's EntityResolver for a single request, bound to
// the request context and the authenticated principal. Binding here is required
// because Cedar's pull interface (Get(uid) (Entity, bool)) carries neither a
// context nor an error — see [getter].
type ResolverFactory func(ctx context.Context, p Principal) EntityResolver

// EntityResolver sources the Cedar entities ("facts") a slice owns. It is
// narrower than Cedar's EntityGetter: the composite getter routes a lookup to
// the resolver that owns the entity's type and composes the per-request results.
type EntityResolver interface {
	// Resolve returns the entity for uid and whether it was found. A miss
	// (false) is distinct from a load error, which the resolver records on the
	// bound context so the middleware can fail closed; see [getter].
	Resolve(uid types.EntityUID) (types.Entity, bool)
	// Types lists the entity type names this resolver owns, so the composite
	// getter can route lookups without trying every resolver.
	Types() []string
}
