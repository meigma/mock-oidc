package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/cedar-policy/cedar-go/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingResolver owns one entity type and counts how many times Resolve is
// called, so the getter's caching can be observed.
type recordingResolver struct {
	typ    string
	uid    types.EntityUID
	calls  int
	err    error
	sinkOf context.Context
}

func (r *recordingResolver) Types() []string { return []string{r.typ} }

func (r *recordingResolver) Resolve(uid types.EntityUID) (types.Entity, bool) {
	r.calls++
	if r.err != nil {
		RecordLoadError(r.sinkOf, r.err)

		return types.Entity{}, false
	}
	if uid != r.uid {
		return types.Entity{}, false
	}

	return types.Entity{UID: uid}, true
}

func TestGetterRoutesAndCaches(t *testing.T) {
	t.Parallel()

	todoUID := types.NewEntityUID("Todo", "1")
	resolver := &recordingResolver{typ: "Todo", uid: todoUID}
	g := newGetter(context.Background(), Anonymous(), []Contribution{{
		Types: []string{"Todo"},
		Resolver: func(ctx context.Context, _ Principal) EntityResolver {
			resolver.sinkOf = ctx

			return resolver
		},
	}})

	entity, ok := g.Get(todoUID)
	assert.True(t, ok)
	assert.Equal(t, todoUID, entity.UID)

	// A second lookup of the same uid is served from the cache.
	_, _ = g.Get(todoUID)
	assert.Equal(t, 1, resolver.calls, "the entity should be loaded once and cached")
	assert.NoError(t, g.Err())
}

func TestGetterCachesMisses(t *testing.T) {
	t.Parallel()

	resolver := &recordingResolver{typ: "Todo", uid: types.NewEntityUID("Todo", "1")}
	g := newGetter(context.Background(), Anonymous(), []Contribution{{
		Types: []string{"Todo"},
		Resolver: func(ctx context.Context, _ Principal) EntityResolver {
			resolver.sinkOf = ctx

			return resolver
		},
	}})

	missing := types.NewEntityUID("Todo", "404")
	_, ok := g.Get(missing)
	assert.False(t, ok)
	_, _ = g.Get(missing)
	assert.Equal(t, 1, resolver.calls, "a miss is cached so it is not retried")
}

func TestGetterUnownedTypeIsMiss(t *testing.T) {
	t.Parallel()

	g := newGetter(context.Background(), Anonymous(), nil)
	_, ok := g.Get(types.NewEntityUID("Unknown", "x"))
	assert.False(t, ok)
	assert.NoError(t, g.Err(), "an unowned type is a miss, not an error")
}

// TestGetterResolvesCustomPrincipalType proves a custom Authenticator that mints
// a non-User principal (the documented WithAuthenticator extension point) still
// resolves through the composite getter, with its role parents projected — so
// `principal in Role::"…"` policies hold. Routing the principal resolver only by
// its static reserved types ["User","Anonymous"] regressed this: a Service::"x"
// principal had no resolver and silently failed every role check.
func TestGetterResolvesCustomPrincipalType(t *testing.T) {
	t.Parallel()

	principal := Principal{
		UID: types.NewEntityUID("Service", "svc-1"),
		Claims: types.NewRecord(types.RecordMap{
			RolesClaim: types.NewSet(types.String("admin")),
		}),
	}

	authorizer, err := New(nil)
	require.NoError(t, err)

	g := newGetter(context.Background(), principal, authorizer.Contributions())
	entity, ok := g.Get(principal.UID)

	require.True(t, ok, "a custom principal type must still resolve via the principal resolver")
	assert.True(t, entity.Parents.Contains(types.NewEntityUID(RoleType, "admin")),
		"the principal's role claims must project onto its Role parents")
}

// TestGetterCustomPrincipalTypeCoexistsWithSlice proves that when a slice owns
// the same Cedar type as a custom principal, both resolve: the principal resolver
// answers the principal's own UID and the slice answers its other instances. The
// principal resolver is chained ahead of the slice for the exact principal UID
// only, so neither shadows the other.
func TestGetterCustomPrincipalTypeCoexistsWithSlice(t *testing.T) {
	t.Parallel()

	principal := Principal{UID: types.NewEntityUID("Service", "svc-1")}

	dataUID := types.NewEntityUID("Service", "svc-2")
	sliceResolver := &staticResolver{
		typ:    "Service",
		entity: types.Entity{UID: dataUID, Attributes: types.NewRecord(types.RecordMap{"slice": types.Boolean(true)})},
	}
	authorizer, err := New([]Contribution{{
		Types:    []string{"Service"},
		Resolver: func(_ context.Context, _ Principal) EntityResolver { return sliceResolver },
	}})
	require.NoError(t, err)

	g := newGetter(context.Background(), principal, authorizer.Contributions())

	// The principal's own UID routes to the principal resolver.
	principalEntity, ok := g.Get(principal.UID)
	require.True(t, ok, "the principal entity must resolve")
	_, hasSliceAttr := principalEntity.Attributes.Get("slice")
	assert.False(t, hasSliceAttr, "the principal lookup must not route to the slice resolver")

	// A different instance of the same type falls through to the slice resolver.
	dataEntity, ok := g.Get(dataUID)
	require.True(t, ok, "the slice's own instances of the shared type must still resolve")
	_, hasSliceAttr = dataEntity.Attributes.Get("slice")
	assert.True(t, hasSliceAttr, "a non-principal instance must route to the slice resolver")
}

func TestGetterCapturesFirstLoadError(t *testing.T) {
	t.Parallel()

	first := errors.New("first failure")
	resolver := &recordingResolver{typ: "Todo", err: first}
	g := newGetter(context.Background(), Anonymous(), []Contribution{{
		Types: []string{"Todo"},
		Resolver: func(ctx context.Context, _ Principal) EntityResolver {
			resolver.sinkOf = ctx

			return resolver
		},
	}})

	_, ok := g.Get(types.NewEntityUID("Todo", "1"))
	assert.False(t, ok)
	assert.ErrorIs(t, g.Err(), first, "the resolver's load error must be captured for fail-closed handling")
}
