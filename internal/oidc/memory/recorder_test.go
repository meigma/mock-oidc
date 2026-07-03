package memory_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/memory"
)

// capture builds a CapturedRequest the way the httpapi edge would, deriving the
// issuer from the URL's first path segment.
func capture(issuer, endpoint string, body []byte) oidc.CapturedRequest {
	return oidc.NewCapturedRequest(
		"POST",
		"http://mock/"+issuer+"/"+endpoint,
		map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		nil,
		body,
	)
}

// TestRequestRecorderRingBounds proves the per-issuer ring drops the oldest
// capture once the capacity is exceeded, keeping only the newest entries.
func TestRequestRecorderRingBounds(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder(memory.WithRecorderCapacity(2))
	ctx := context.Background()

	require.NoError(t, rec.Record(ctx, capture("default", "token", []byte("n=1"))))
	require.NoError(t, rec.Record(ctx, capture("default", "token", []byte("n=2"))))
	require.NoError(t, rec.Record(ctx, capture("default", "token", []byte("n=3"))))

	got := rec.List(oidc.CaptureFilter{})
	require.Len(t, got, 2, "capacity 2 keeps only the two newest")
	assert.Equal(t, []byte("n=2"), got[0].Body)
	assert.Equal(t, []byte("n=3"), got[1].Body, "newest survives; oldest dropped")
}

// TestRequestRecorderRingIsPerIssuer proves one issuer's overflow does not evict
// another issuer's captures — each issuer has its own ring.
func TestRequestRecorderRingIsPerIssuer(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder(memory.WithRecorderCapacity(1))
	ctx := context.Background()

	require.NoError(t, rec.Record(ctx, capture("issuer-a", "token", []byte("a"))))
	require.NoError(t, rec.Record(ctx, capture("issuer-b", "token", []byte("b1"))))
	require.NoError(t, rec.Record(ctx, capture("issuer-b", "token", []byte("b2"))))

	assert.Len(t, rec.List(oidc.CaptureFilter{Issuer: "issuer-a"}), 1)
	b := rec.List(oidc.CaptureFilter{Issuer: "issuer-b"})
	require.Len(t, b, 1)
	assert.Equal(t, []byte("b2"), b[0].Body)
}

// TestRequestRecorderRawByteFidelity proves the body bytes are stored verbatim —
// duplicate keys and param order are preserved (never reparsed).
func TestRequestRecorderRawByteFidelity(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder()
	ctx := context.Background()
	raw := []byte("b=2&a=1&a=3&grant_type=client_credentials")

	require.NoError(t, rec.Record(ctx, capture("default", "token", raw)))

	got := rec.List(oidc.CaptureFilter{})
	require.Len(t, got, 1)
	assert.Equal(t, raw, got[0].Body, "raw bytes preserved verbatim")
	assert.NotEmpty(t, got[0].ID, "the adapter stamps an id on storage")
	assert.False(t, got[0].ReceivedAt.Time().IsZero(), "the adapter stamps ReceivedAt")
}

// TestRequestRecorderFilterMatching covers issuer and endpoint narrowing on both
// List and the destructive Take.
func TestRequestRecorderFilterMatching(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder()
	ctx := context.Background()

	require.NoError(t, rec.Record(ctx, capture("issuer-a", "token", []byte("t"))))
	require.NoError(t, rec.Record(ctx, capture("issuer-a", "authorize", []byte("z"))))
	require.NoError(t, rec.Record(ctx, capture("issuer-b", "token", []byte("t"))))

	assert.Len(t, rec.List(oidc.CaptureFilter{Issuer: "issuer-a"}), 2)
	assert.Len(t, rec.List(oidc.CaptureFilter{Endpoint: "token"}), 2)
	assert.Len(t, rec.List(oidc.CaptureFilter{Issuer: "issuer-a", Endpoint: "token"}), 1)

	req, ok := rec.Take(ctx, oidc.CaptureFilter{Issuer: "issuer-a", Endpoint: "authorize"}, 0)
	require.True(t, ok)
	assert.Equal(t, []byte("z"), req.Body)
	// Take is destructive: the authorize capture is gone, the two token ones stay.
	assert.Empty(t, rec.List(oidc.CaptureFilter{Endpoint: "authorize"}))
	assert.Len(t, rec.List(oidc.CaptureFilter{}), 2)
}

// TestRequestRecorderTakeFIFO proves Take drains matching captures oldest-first
// across issuers by arrival order.
func TestRequestRecorderTakeFIFO(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder()
	ctx := context.Background()

	require.NoError(t, rec.Record(ctx, capture("issuer-a", "token", []byte("first"))))
	require.NoError(t, rec.Record(ctx, capture("issuer-b", "token", []byte("second"))))

	req, ok := rec.Take(ctx, oidc.CaptureFilter{}, 0)
	require.True(t, ok)
	assert.Equal(t, []byte("first"), req.Body, "oldest arrival first")

	req, ok = rec.Take(ctx, oidc.CaptureFilter{}, 0)
	require.True(t, ok)
	assert.Equal(t, []byte("second"), req.Body)
}

// TestRequestRecorderTakeTimesOut proves a blocking Take with no matching request
// returns ok=false after the timeout rather than hanging.
func TestRequestRecorderTakeTimesOut(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder()
	start := time.Now()
	_, ok := rec.Take(context.Background(), oidc.CaptureFilter{}, 40*time.Millisecond)
	elapsed := time.Since(start)

	assert.False(t, ok, "no request matched within the timeout")
	assert.GreaterOrEqual(t, elapsed, 40*time.Millisecond, "it waited for the timeout")
	assert.Less(t, elapsed, 5*time.Second, "it did not hang")
}

// TestRequestRecorderTakeUnblocksOnRecord proves a blocked Take wakes as soon as a
// matching request is recorded and returns it.
func TestRequestRecorderTakeUnblocksOnRecord(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder()
	ctx := context.Background()

	type result struct {
		req oidc.CapturedRequest
		ok  bool
	}
	ch := make(chan result, 1)
	go func() {
		req, ok := rec.Take(ctx, oidc.CaptureFilter{Issuer: "default"}, 2*time.Second)
		ch <- result{req, ok}
	}()

	// Let Take enter its blocking wait, then deliver a matching capture.
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, rec.Record(ctx, capture("default", "token", []byte("arrived"))))

	got := <-ch
	require.True(t, got.ok, "Take must return the request that arrived")
	assert.Equal(t, []byte("arrived"), got.req.Body)
}

// TestRequestRecorderTakeHonorsContextCancel proves a blocked Take returns
// ok=false promptly when its context is cancelled.
func TestRequestRecorderTakeHonorsContextCancel(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, ok := rec.Take(ctx, oidc.CaptureFilter{}, 5*time.Second)
	assert.False(t, ok, "a cancelled context ends the wait")
}

// TestRequestRecorderClear empties the log (the DELETE / reset path).
func TestRequestRecorderClear(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder()
	ctx := context.Background()
	require.NoError(t, rec.Record(ctx, capture("default", "token", []byte("x"))))

	rec.Clear()
	assert.Empty(t, rec.List(oidc.CaptureFilter{}))
}

// TestRequestRecorderConcurrent hammers Record alongside List and Take under
// -race to guard the mutex and the broadcast channel.
func TestRequestRecorderConcurrent(t *testing.T) {
	t.Parallel()

	rec := memory.NewRequestRecorder()
	ctx := context.Background()

	const writers = 8
	const perWriter = 50

	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			for range perWriter {
				assert.NoError(t, rec.Record(ctx, capture("default", "token", []byte("x"))))
			}
		})
	}

	var readers sync.WaitGroup
	for range writers {
		readers.Go(func() {
			for range perWriter {
				_ = rec.List(oidc.CaptureFilter{Issuer: "default"})
				rec.Take(ctx, oidc.CaptureFilter{Issuer: "default"}, 0)
			}
		})
	}

	wg.Wait()
	readers.Wait()
}
