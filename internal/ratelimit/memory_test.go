package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/meigma/template-go-api/internal/ratelimit"
)

func TestInMemoryAllowsBurstThenDenies(t *testing.T) {
	t.Parallel()

	// Refill of 1 token/sec with a burst of 3: the first three requests pass
	// immediately, the fourth (within the same second) is denied with a hint.
	limiter := ratelimit.NewInMemory(rate.Limit(1), 3, time.Minute)
	t.Cleanup(limiter.Stop)

	ctx := context.Background()
	for i := range 3 {
		d, err := limiter.Allow(ctx, "client-a")
		require.NoError(t, err)
		assert.Truef(t, d.Allowed, "burst request %d should be allowed", i+1)
	}

	d, err := limiter.Allow(ctx, "client-a")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "the request past the burst should be denied")
	assert.Positive(t, d.RetryAfter, "a denied request reports when to retry")
}

func TestInMemoryKeysAreIndependent(t *testing.T) {
	t.Parallel()

	limiter := ratelimit.NewInMemory(rate.Limit(1), 1, time.Minute)
	t.Cleanup(limiter.Stop)
	ctx := context.Background()

	first, err := limiter.Allow(ctx, "client-a")
	require.NoError(t, err)
	require.True(t, first.Allowed)

	// client-a is now exhausted, but client-b has its own independent bucket.
	exhausted, err := limiter.Allow(ctx, "client-a")
	require.NoError(t, err)
	assert.False(t, exhausted.Allowed)

	other, err := limiter.Allow(ctx, "client-b")
	require.NoError(t, err)
	assert.True(t, other.Allowed, "a different key has an independent bucket")
}

func TestInMemoryRefillsOverTime(t *testing.T) {
	t.Parallel()

	// 50 tokens/sec => a token refills in ~20ms. Burst 1, so the bucket empties
	// after one request and then refills shortly after.
	limiter := ratelimit.NewInMemory(rate.Limit(50), 1, time.Minute)
	t.Cleanup(limiter.Stop)
	ctx := context.Background()

	first, err := limiter.Allow(ctx, "c")
	require.NoError(t, err)
	require.True(t, first.Allowed)

	denied, err := limiter.Allow(ctx, "c")
	require.NoError(t, err)
	require.False(t, denied.Allowed)

	// After the refill interval the bucket allows again. Reserve/Cancel on the
	// denied polls does not consume a future token, so this converges.
	require.Eventually(t, func() bool {
		d, err := limiter.Allow(ctx, "c")

		return err == nil && d.Allowed
	}, time.Second, 5*time.Millisecond, "the bucket should refill and allow again")
}

func TestInMemoryStopIsIdempotent(t *testing.T) {
	t.Parallel()

	limiter := ratelimit.NewInMemory(rate.Limit(1), 1, time.Minute)
	limiter.Stop()
	assert.NotPanics(t, limiter.Stop, "Stop is safe to call more than once")
}
