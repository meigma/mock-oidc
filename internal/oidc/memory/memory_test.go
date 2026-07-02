package memory_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/memory"
)

func TestIssuerRegistryMaterializeIsIdempotent(t *testing.T) {
	t.Parallel()

	reg := memory.NewIssuerRegistry()
	ctx := context.Background()

	first, err := reg.Materialize(ctx, "default")
	require.NoError(t, err)
	assert.Equal(t, oidc.IssuerID("default"), first.ID)

	again, err := reg.Materialize(ctx, "default")
	require.NoError(t, err)
	assert.Equal(t, first, again)

	_, err = reg.Materialize(ctx, "tenant-b")
	require.NoError(t, err)

	known, err := reg.Known(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []oidc.IssuerID{"default", "tenant-b"}, known)
}

func TestIssuerRegistrySeedsConfiguredRecords(t *testing.T) {
	t.Parallel()

	seeded := oidc.IssuerRecord{
		ID:        "configured",
		Callbacks: []oidc.TokenCallback{oidc.NewDefaultTokenCallback("configured")},
	}
	reg := memory.NewIssuerRegistry(seeded)

	rec, err := reg.Materialize(context.Background(), "configured")
	require.NoError(t, err)
	assert.Len(t, rec.Callbacks, 1)
}

func TestCodeStoreSingleUse(t *testing.T) {
	t.Parallel()

	store := memory.NewCodeStore()
	ctx := context.Background()
	rec := oidc.CodeRecord{Issuer: "default", RedirectURI: "https://app/cb"}

	require.NoError(t, store.Save(ctx, "code-1", rec))

	got, err := store.Take(ctx, "code-1")
	require.NoError(t, err)
	assert.Equal(t, rec, got)

	// Second Take of the same code is a miss — the code was burned.
	_, err = store.Take(ctx, "code-1")
	require.ErrorIs(t, err, memory.ErrCodeNotFound)

	// An unknown code is also a miss.
	_, err = store.Take(ctx, "never-issued")
	assert.ErrorIs(t, err, memory.ErrCodeNotFound)
}

// TestRefreshTokenStoreSaveLookup covers the persist-and-redeem adapter: a saved
// record is retrieved by token and the latest write for a token wins.
func TestRefreshTokenStoreSaveLookup(t *testing.T) {
	t.Parallel()

	store := memory.NewRefreshTokenStore()
	ctx := context.Background()
	rec := oidc.RefreshRecord{Issuer: "default", Subject: "alice", Format: oidc.RefreshBareUUID}

	require.NoError(t, store.Save(ctx, "default", "rt-1", rec))
	got, err := store.Lookup(ctx, "default", "rt-1")
	require.NoError(t, err)
	assert.Equal(t, rec, got)

	// The latest write for a token wins.
	updated := oidc.RefreshRecord{Issuer: "default", Subject: "bob", Format: oidc.RefreshBareUUID}
	require.NoError(t, store.Save(ctx, "default", "rt-1", updated))
	got, err = store.Lookup(ctx, "default", "rt-1")
	require.NoError(t, err)
	assert.Equal(t, updated, got)
}

// TestRefreshTokenStoreLookupResolvesByTokenAcrossIssuers proves Lookup resolves
// by token regardless of the presented issuer, so the service (not the store)
// raises the corrected cross-issuer text — a miss would lose that distinction.
func TestRefreshTokenStoreLookupResolvesByTokenAcrossIssuers(t *testing.T) {
	t.Parallel()

	store := memory.NewRefreshTokenStore()
	ctx := context.Background()
	rec := oidc.RefreshRecord{Issuer: "issuer-a", Subject: "alice", Format: oidc.RefreshBareUUID}
	require.NoError(t, store.Save(ctx, "issuer-a", "rt-1", rec))

	// Presented under a DIFFERENT issuer, the record still resolves by token.
	got, err := store.Lookup(ctx, "issuer-b", "rt-1")
	require.NoError(t, err)
	assert.Equal(t, oidc.IssuerID("issuer-a"), got.Issuer)
}

// TestRefreshTokenStoreLookupMissIsSentinel covers the miss path: an unknown
// token returns the ErrRefreshTokenNotFound sentinel the service maps to
// invalid_grant.
func TestRefreshTokenStoreLookupMissIsSentinel(t *testing.T) {
	t.Parallel()

	store := memory.NewRefreshTokenStore()
	_, err := store.Lookup(context.Background(), "default", "absent")
	require.ErrorIs(t, err, memory.ErrRefreshTokenNotFound)
}

// TestRefreshTokenStoreRemoveIsIdempotent covers revocation: Remove drops a token
// and removing an absent token is a no-op (idempotent revoke).
func TestRefreshTokenStoreRemoveIsIdempotent(t *testing.T) {
	t.Parallel()

	store := memory.NewRefreshTokenStore()
	ctx := context.Background()
	rec := oidc.RefreshRecord{Issuer: "default", Subject: "alice", Format: oidc.RefreshBareUUID}
	require.NoError(t, store.Save(ctx, "default", "rt-1", rec))

	require.NoError(t, store.Remove(ctx, "rt-1"))
	_, err := store.Lookup(ctx, "default", "rt-1")
	require.ErrorIs(t, err, memory.ErrRefreshTokenNotFound)

	// Removing an already-absent token is a no-op, so revoke is idempotent.
	require.NoError(t, store.Remove(ctx, "rt-1"))
	require.NoError(t, store.Remove(ctx, "never-existed"))
}

func TestClockFreezeAdvanceUnfreeze(t *testing.T) {
	t.Parallel()

	at := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	clock := memory.NewFrozenClock(at)
	assert.Equal(t, at, clock.Now())

	clock.Advance(time.Hour)
	assert.Equal(t, at.Add(time.Hour), clock.Now())

	clock.Unfreeze()
	assert.WithinDuration(t, time.Now(), clock.Now().Time(), time.Minute)

	// An unfrozen clock advances by freezing at the current instant first.
	wall := memory.NewClock()
	before := wall.Now().Time()
	wall.Advance(time.Hour)
	assert.False(t, wall.Now().Time().Before(before.Add(time.Hour)))
}

// TestClockFreezeAtAndState covers the control-plane facet: Freeze pins the clock
// at an explicit instant (re-freezing repins), State reports frozen + instant,
// Advance moves the frozen instant, and Unfreeze returns to the wall clock with a
// State that reports not-frozen.
func TestClockFreezeAtAndState(t *testing.T) {
	t.Parallel()

	clock := memory.NewClock()

	// Unfrozen: State reports not frozen and a near-now instant.
	st := clock.State()
	assert.False(t, st.Frozen)
	assert.WithinDuration(t, time.Now(), st.Now.Time(), time.Minute)

	at := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	clock.Freeze(at)
	assert.Equal(t, at, clock.Now())
	st = clock.State()
	assert.True(t, st.Frozen)
	assert.Equal(t, at, st.Now)

	// Advance moves the pinned instant.
	clock.Advance(90 * time.Second)
	assert.Equal(t, at.Add(90*time.Second), clock.State().Now)

	// Re-freezing repins to a new instant (jump the clock anywhere).
	other := oidc.NewInstant(time.Unix(1_800_000_000, 0))
	clock.Freeze(other)
	assert.Equal(t, other, clock.Now())

	// Unfreeze returns to the wall clock.
	clock.Unfreeze()
	st = clock.State()
	assert.False(t, st.Frozen)
	assert.WithinDuration(t, time.Now(), st.Now.Time(), time.Minute)
}

// TestIssuerRegistryMaterializeConcurrent hammers Materialize from many
// goroutines — both racing on a single issuer (idempotency under contention) and
// materializing distinct issuers in parallel — and confirms the registry stays
// consistent. Run under `go test -race`, it guards the computeIfAbsent
// double-checked locking against data races and lost updates.
func TestIssuerRegistryMaterializeConcurrent(t *testing.T) {
	t.Parallel()

	reg := memory.NewIssuerRegistry()
	ctx := context.Background()

	const (
		issuers = 16
		workers = 8
	)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range issuers {
				id := oidc.IssuerID("issuer-" + strconv.Itoa(i))
				rec, _ := reg.Materialize(ctx, id)
				assert.Equal(t, id, rec.ID)
			}
		})
	}
	wg.Wait()

	known, err := reg.Known(ctx)
	require.NoError(t, err)
	assert.Len(t, known, issuers, "each distinct issuer is materialized exactly once")
}
