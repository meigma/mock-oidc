package authz

import (
	"context"

	"github.com/cedar-policy/cedar-go/types"
)

// RoleType is the Cedar entity type for a role group. Roles carried on a
// principal's claims are projected onto the principal entity's parents as
// Role::"<name>", so policies can test `principal in Role::"…"` with no load.
const RoleType types.EntityType = "Role"

// PrincipalType is the Cedar entity type of an authenticated caller. The API-key
// authenticator mints principals of this type; it is reserved so no slice
// resolver can shadow the always-present principal resolver.
const PrincipalType types.EntityType = "User"

// RolesClaim is the claim key the principal resolver reads the caller's roles
// from, and the key an Authenticator writes them under, so role membership is
// projected onto the principal entity's parents for `principal in Role::"…"`.
const RolesClaim types.String = "roles"

// reservedTypes returns the Cedar entity types owned by the base principal
// resolver: the authenticated and anonymous principal types. They can never be
// claimed by a slice contribution, so a slice resolver cannot shadow the
// principal resolver in the composite getter (see [New] and [newGetter]). Role
// entities carry no attributes and need no resolver, so Role is not listed here.
func reservedTypes() []string {
	return []string{string(PrincipalType), string(AnonymousType)}
}

// principalContribution is the always-present base contribution that resolves
// the authenticated principal entity from its claims. It owns the reserved
// principal entity types so the composite getter routes principal lookups here,
// letting cross-cutting policies (base.cedar's admin override) and slice policies
// test principal group membership without any database load.
func principalContribution() Contribution {
	return Contribution{Types: reservedTypes(), Resolver: newPrincipalResolver}
}

// principalResolver materializes the request's principal entity (and its role
// parents) from the bound Principal. It owns the principal entity's own type so
// `principal in Role::"…"` checks resolve from the claims projected at
// authentication time — no load, fail-closed-safe.
type principalResolver struct {
	principal Principal
}

// newPrincipalResolver builds the per-request principal resolver. It satisfies
// ResolverFactory; the context is unused because the principal's identity and
// roles are already in hand from authentication.
func newPrincipalResolver(_ context.Context, p Principal) EntityResolver {
	return &principalResolver{principal: p}
}

// Types reports the principal entity's own type, so the composite getter routes
// a lookup of the principal entity to this resolver. Role entities themselves
// carry no attributes and need no resolver — Cedar reads ancestry from the
// principal entity's parents.
func (r *principalResolver) Types() []string {
	return []string{string(r.principal.UID.Type)}
}

// Resolve returns the principal entity with its role parents when uid is the
// bound principal, otherwise a miss. Roles recorded on the principal's claims
// become Role::"<name>" parents so membership tests evaluate with no load.
func (r *principalResolver) Resolve(uid types.EntityUID) (types.Entity, bool) {
	if uid != r.principal.UID {
		return types.Entity{}, false
	}

	return types.Entity{
		UID:        r.principal.UID,
		Parents:    roleParents(r.principal.Claims),
		Attributes: r.principal.Claims,
	}, true
}

// roleParents reads the roles claim and returns the principal's Role parents.
func roleParents(claims types.Record) types.EntityUIDSet {
	value, ok := claims.Get(RolesClaim)
	if !ok {
		return types.NewEntityUIDSet()
	}
	roles, ok := value.(types.Set)
	if !ok {
		return types.NewEntityUIDSet()
	}

	uids := make([]types.EntityUID, 0, roles.Len())
	for role := range roles.All() {
		name, ok := role.(types.String)
		if !ok {
			continue
		}
		uids = append(uids, types.NewEntityUID(RoleType, name))
	}

	return types.NewEntityUIDSet(uids...)
}
