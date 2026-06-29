package authz

import (
	"context"
	"testing"

	"github.com/cedar-policy/cedar-go/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staticResolver owns one type and resolves a single fixed entity, so the
// precedence test can prove which resolver a lookup routes to.
type staticResolver struct {
	typ    string
	entity types.Entity
}

func (r *staticResolver) Types() []string { return []string{r.typ} }

func (r *staticResolver) Resolve(uid types.EntityUID) (types.Entity, bool) {
	if uid == r.entity.UID {
		return r.entity, true
	}

	return types.Entity{}, false
}

func TestNewRejectsDuplicateTypeOwnership(t *testing.T) {
	t.Parallel()

	contribs := []Contribution{
		{Types: []string{"Todo"}, Resolver: nopResolver},
		{Types: []string{"Todo"}, Resolver: nopResolver},
	}

	_, err := New(contribs)
	require.Error(t, err, "two slices claiming the same entity type must fail construction")
	assert.Contains(t, err.Error(), "Todo")
}

func TestNewRejectsSliceClaimingReservedPrincipalType(t *testing.T) {
	t.Parallel()

	// A slice may not claim the principal's reserved type; doing so would let it
	// shadow the always-present principal resolver in the composite getter.
	_, err := New([]Contribution{{Types: []string{string(PrincipalType)}, Resolver: nopResolver}})
	require.Error(t, err, "a slice claiming the reserved principal type must fail construction")
	assert.Contains(t, err.Error(), string(PrincipalType))
}

func TestNewRejectsResolverWithoutTypes(t *testing.T) {
	t.Parallel()

	// A slice with a Resolver but no Types would be registered under zero type
	// keys and never invoked, so its policies' entity dereferences would silently
	// fail closed. The misconfiguration must fail startup, not at request time.
	_, err := New([]Contribution{{Resolver: nopResolver}})
	require.Error(t, err, "a Resolver with no declared Types must fail construction")
	assert.Contains(t, err.Error(), "Types")
}

func TestNewAllowsCoarseSliceWithoutResolver(t *testing.T) {
	t.Parallel()

	// A slice contributing only coarse policies (no Resolver) may leave Types
	// empty — there is nothing to route.
	_, err := New([]Contribution{{Policies: []byte(`permit (principal, action, resource);`)}})
	require.NoError(t, err, "a coarse slice with no Resolver may omit Types")
}

func TestNewAllowsDistinctTypes(t *testing.T) {
	t.Parallel()

	_, err := New([]Contribution{
		{Types: []string{"Todo"}, Resolver: nopResolver},
		{Types: []string{"Project"}, Resolver: nopResolver},
	})
	require.NoError(t, err, "slices owning distinct types must construct cleanly")
}

// TestPrincipalResolverWinsOverSliceForPrincipalType proves the precedence fix
// holds even when a slice resolver claims, at runtime, to own the principal type
// (a Types() that disagrees with its declared Contribution.Types). Routing keys
// off the contribution's declared Types, which New validated, so a User lookup
// still resolves to the principal resolver — never the rogue slice resolver.
func TestPrincipalResolverWinsOverSliceForPrincipalType(t *testing.T) {
	t.Parallel()

	principal := Principal{UID: types.NewEntityUID(PrincipalType, "alice")}

	// A misbehaving slice resolver: it declares "Todo" on its Contribution but
	// its Types() lies and claims the principal type. The static routing must
	// ignore Types() and route only by the declared "Todo".
	rogue := &staticResolver{
		typ: string(PrincipalType),
		entity: types.Entity{
			UID:        types.NewEntityUID(PrincipalType, "alice"),
			Attributes: types.NewRecord(types.RecordMap{"rogue": types.Boolean(true)}),
		},
	}
	contrib := Contribution{
		Types:    []string{"Todo"},
		Resolver: func(_ context.Context, _ Principal) EntityResolver { return rogue },
	}

	authorizer, err := New([]Contribution{contrib})
	require.NoError(t, err)

	getter := newGetter(context.Background(), principal, authorizer.Contributions())
	entity, ok := getter.Get(principal.UID)

	require.True(t, ok, "the principal entity must resolve")
	_, hasRogue := entity.Attributes.Get("rogue")
	assert.False(t, hasRogue, "the principal lookup must route to the principal resolver, not the slice resolver")
}

// nopResolver is a ResolverFactory that returns a resolver owning nothing useful;
// the ownership tests only exercise New's validation, never Resolve.
func nopResolver(_ context.Context, _ Principal) EntityResolver {
	return &staticResolver{}
}
