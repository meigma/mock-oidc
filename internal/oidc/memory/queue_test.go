package memory_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/memory"
)

// scenarioFor wraps the default callback for issuer as a one-shot Scenario.
func scenarioFor(t *testing.T, issuer oidc.IssuerID) oidc.Scenario {
	t.Helper()
	sc, err := oidc.NewScenario(oidc.NewDefaultTokenCallback(issuer))
	require.NoError(t, err)
	return sc
}

// TestCallbackQueueIssuerMatchedHeadBlocks proves the trickiest parity behavior:
// a scenario queued for issuer A at the head blocks issuer B even though B's
// request arrives first — DequeueFor(B) leaves the queue untouched until A drains
// the head.
func TestCallbackQueueIssuerMatchedHeadBlocks(t *testing.T) {
	t.Parallel()

	q := memory.NewCallbackQueue()
	ctx := context.Background()

	_, err := q.Enqueue(scenarioFor(t, "issuer-a"))
	require.NoError(t, err)
	_, err = q.Enqueue(scenarioFor(t, "issuer-b"))
	require.NoError(t, err)

	// B arrives first but the head belongs to A: B is blocked, queue unchanged.
	_, ok, err := q.DequeueFor(ctx, "issuer-b")
	require.NoError(t, err)
	assert.False(t, ok, "issuer-b must not consume issuer-a's head")
	assert.Len(t, q.List(), 2, "a blocked dequeue leaves the queue intact")

	// A drains its head; now B's scenario is at the head and consumable.
	sc, ok, err := q.DequeueFor(ctx, "issuer-a")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, oidc.IssuerID("issuer-a"), sc.IssuerID())

	sc, ok, err = q.DequeueFor(ctx, "issuer-b")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, oidc.IssuerID("issuer-b"), sc.IssuerID())
}

// TestCallbackQueueFIFOSingleUse covers FIFO order and single-use: two scenarios
// for one issuer come back in enqueue order, and once drained the queue reports
// empty (a consumed scenario is gone).
func TestCallbackQueueFIFOSingleUse(t *testing.T) {
	t.Parallel()

	q := memory.NewCallbackQueue()
	ctx := context.Background()

	first, err := q.Enqueue(scenarioFor(t, "default"))
	require.NoError(t, err)
	second, err := q.Enqueue(scenarioFor(t, "default"))
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "each Enqueue mints a distinct id")

	assert.Len(t, q.List(), 2)

	_, ok, err := q.DequeueFor(ctx, "default")
	require.NoError(t, err)
	require.True(t, ok)
	_, ok, err = q.DequeueFor(ctx, "default")
	require.NoError(t, err)
	require.True(t, ok)

	// Drained: single-use means a third dequeue is a miss.
	_, ok, err = q.DequeueFor(ctx, "default")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, q.List())
}

// TestCallbackQueueEmptyDequeue confirms DequeueFor on an empty queue is a clean
// miss, not an error.
func TestCallbackQueueEmptyDequeue(t *testing.T) {
	t.Parallel()

	q := memory.NewCallbackQueue()
	_, ok, err := q.DequeueFor(context.Background(), "default")
	require.NoError(t, err)
	assert.False(t, ok)
}

// TestCallbackQueueClear flushes the queue (the DELETE / reset path).
func TestCallbackQueueClear(t *testing.T) {
	t.Parallel()

	q := memory.NewCallbackQueue()
	_, err := q.Enqueue(scenarioFor(t, "default"))
	require.NoError(t, err)
	q.Clear()
	assert.Empty(t, q.List())
}

// TestCallbackQueueConcurrent hammers Enqueue and DequeueFor from many goroutines
// under -race: every enqueued scenario is either consumed or still listed, and no
// scenario is consumed twice (the drained count never exceeds the enqueued count).
func TestCallbackQueueConcurrent(t *testing.T) {
	t.Parallel()

	q := memory.NewCallbackQueue()
	ctx := context.Background()

	const workers = 8
	const perWorker = 50

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range perWorker {
				_, err := q.Enqueue(scenarioFor(t, "default"))
				assert.NoError(t, err)
			}
		})
	}

	var drained sync.WaitGroup
	var mu sync.Mutex
	consumed := 0
	for range workers {
		drained.Go(func() {
			for range perWorker {
				if _, ok, err := q.DequeueFor(ctx, "default"); err == nil && ok {
					mu.Lock()
					consumed++
					mu.Unlock()
				}
			}
		})
	}

	wg.Wait()
	drained.Wait()

	// Whatever was not consumed remains queued; total is conserved (single-use).
	assert.Equal(t, workers*perWorker, consumed+len(q.List()))
}
