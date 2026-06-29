package authz

import (
	"context"

	"github.com/cedar-policy/cedar-go/types"
)

// getter is the request-scoped composite EntityGetter handed to Cedar. It routes
// each lookup to the slice resolver that owns the entity's type, caches results
// for the life of the request (so an entity chain is read once), and captures
// the first load error.
//
// Cedar's pull interface is Get(uid) (Entity, bool): no context and no error.
// Two consequences are handled here:
//   - Context is bound at construction (newGetter), because Cedar will not pass
//     one to Get; slice resolvers close over the request context.
//   - A resolver cannot signal failure through Get, so it records the first
//     error via setErr; the middleware checks Err after Authorize and fails
//     closed (500) rather than trusting a decision made on missing data.
//
// A getter is single-request scoped and is not safe for concurrent use, which
// matches Cedar's sequential evaluation of one request.
type getter struct {
	// ctx is the request context, bound at construction and shared with the
	// slice resolvers (which close over it via their ResolverFactory).
	ctx context.Context
	// byType routes a lookup to the resolver that owns the entity's type.
	byType map[string]EntityResolver
	// cache memoizes resolved entities (and misses) for the life of the request.
	cache map[types.EntityUID]cacheEntry
	// firstErr is the first load failure recorded by a resolver, if any.
	firstErr error
}

// cacheEntry memoizes one lookup, including a miss (found == false), so a
// repeated dereference of the same entity costs no second load.
type cacheEntry struct {
	entity types.Entity
	found  bool
}

// errorSinkKey is the context key under which the getter installs its
// error-recording sink. Slice resolvers retrieve it via RecordLoadError to
// report a load failure, since Cedar's Get signature carries no error.
type errorSinkKey struct{}

// newGetter assembles the composite getter for one request. It first installs an
// error sink on ctx (so resolvers can report load failures via RecordLoadError),
// then builds each contribution's resolver bound to that context and the
// principal, indexing them by the entity types they own. Slices with no Resolver
// contribute nothing.
//
// Routing has two layers, keeping the precedence-fix guarantee while not breaking
// custom principal types:
//   - Slice resolvers route by their statically declared Types — never the
//     resolver's runtime Types() — so a slice resolver cannot claim a type it did
//     not declare (and [New] rejected) and thereby shadow the principal resolver.
//   - The always-present principal resolver additionally routes under the
//     principal's actual UID type (p.UID.Type), so a custom Authenticator minting
//     a non-User principal (for example Service::"x") still resolves its entity
//     and projects its role parents. When a slice already owns that type (a custom
//     principal type that collides with a slice's data type), the principal
//     resolver is chained ahead of the slice resolver for the exact principal UID
//     only; the slice still serves its own instances of that type.
func newGetter(ctx context.Context, p Principal, contributions []Contribution) *getter {
	g := &getter{
		byType: make(map[string]EntityResolver),
		cache:  make(map[types.EntityUID]cacheEntry),
	}

	// Bind the error sink onto the context the resolvers receive, so a resolver
	// can report a load failure even though Get cannot return an error.
	g.ctx = context.WithValue(ctx, errorSinkKey{}, g.setErr)

	var principalResolver EntityResolver
	for _, c := range contributions {
		if c.Resolver == nil {
			continue
		}
		resolver := c.Resolver(g.ctx, p)
		if isPrincipalResolver(resolver) {
			// Routed last, under the principal's actual UID type, so it survives a
			// custom principal type and never depends on slice ordering.
			principalResolver = resolver

			continue
		}
		// Route by the contribution's statically declared Types, not the
		// resolver's runtime Types(), so a slice resolver cannot claim a type it
		// did not declare (and [New] rejected) and thereby shadow the principal
		// resolver. Contributions are applied in order, but [New] guarantees the
		// keys are disjoint, so order does not affect routing.
		for _, t := range c.Types {
			g.byType[t] = resolver
		}
	}

	if principalResolver != nil {
		g.routePrincipal(principalResolver, p.UID.Type)
	}

	return g
}

// routePrincipal indexes the always-present principal resolver under the
// principal's actual UID type. If a slice already owns that type (a custom
// principal type colliding with a slice's data type), the two are chained so the
// principal resolver answers the exact principal UID and the slice answers its
// own instances; otherwise the principal resolver owns the type outright.
func (g *getter) routePrincipal(principalResolver EntityResolver, principalType types.EntityType) {
	key := string(principalType)
	if existing, ok := g.byType[key]; ok {
		g.byType[key] = principalFirst{principal: principalResolver, fallback: existing}

		return
	}

	g.byType[key] = principalResolver
}

// isPrincipalResolver reports whether r is the base principal resolver, so
// newGetter can route it under the principal's actual UID type rather than its
// statically declared reserved types alone.
func isPrincipalResolver(r EntityResolver) bool {
	_, ok := r.(*principalResolver)

	return ok
}

// principalFirst chains the principal resolver ahead of a slice resolver that
// owns the same Cedar type as a custom principal. The principal resolver only
// matches its own bound UID, so a miss falls through to the slice resolver, which
// owns every other instance of that type.
type principalFirst struct {
	principal EntityResolver
	fallback  EntityResolver
}

// Types reports the union of both chained resolvers' owned types.
func (p principalFirst) Types() []string {
	return append(p.principal.Types(), p.fallback.Types()...)
}

// Resolve tries the principal resolver first (it matches only the principal's own
// UID) and falls back to the slice resolver for every other instance of the type.
func (p principalFirst) Resolve(uid types.EntityUID) (types.Entity, bool) {
	if entity, ok := p.principal.Resolve(uid); ok {
		return entity, true
	}

	return p.fallback.Resolve(uid)
}

// RecordLoadError reports a fact-load failure to the request's getter so the
// middleware can fail closed. A slice resolver calls it from its Resolve method
// (Cedar's Get(uid) (Entity, bool) has no error return) using the context it was
// constructed with. It is a no-op when no sink is bound (for example, outside a
// request), so resolvers can call it unconditionally.
func RecordLoadError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	if sink, ok := ctx.Value(errorSinkKey{}).(func(error)); ok {
		sink(err)
	}
}

// setErr records err as the first load failure if none has been captured yet.
// Slice resolvers call it (via the bound context) when a load fails, since Get
// cannot return an error.
func (g *getter) setErr(err error) {
	if g.firstErr == nil {
		g.firstErr = err
	}
}

// Err returns the first load failure recorded during evaluation, or nil. The
// middleware checks it after Authorize to fail closed.
func (g *getter) Err() error {
	return g.firstErr
}

// Get resolves uid for Cedar, satisfying types.EntityGetter. It serves cached
// results (including misses), otherwise routes to the owning resolver and caches
// the outcome. An unowned type is a miss, not an error.
func (g *getter) Get(uid types.EntityUID) (types.Entity, bool) {
	if hit, ok := g.cache[uid]; ok {
		return hit.entity, hit.found
	}

	resolver, ok := g.byType[string(uid.Type)]
	if !ok {
		g.cache[uid] = cacheEntry{}

		return types.Entity{}, false
	}

	entity, found := resolver.Resolve(uid)
	g.cache[uid] = cacheEntry{entity: entity, found: found}

	return entity, found
}
