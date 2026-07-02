package memory_test

import (
	"context"
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
