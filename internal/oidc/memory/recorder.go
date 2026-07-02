package memory

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// defaultRecorderCapacity caps how many recent requests each issuer retains. The
// ring is per-issuer so a chatty issuer cannot evict another's captures; the
// bound exists only to keep the for-testing-only server from growing unbounded.
const defaultRecorderCapacity = 1024

// RequestRecorder is the in-memory per-issuer capture log. One instance satisfies
// BOTH the OIDC edge's write-only port oidc.RequestRecorder (Record — the httpapi
// recording middleware is its sole writer; no core service consumes it) and the
// control-plane read facet controlapi.RequestLog (List/Take/Clear). The two
// non-cooperating adapters share this single store through those narrow ports.
//
// Storage is a bounded ring PER ISSUER (oldest dropped on overflow) to cap
// memory; Take and List order across issuers by a global insertion sequence so
// the destructive FIFO pull and the non-destructive log both reflect true arrival
// order. Raw body bytes are stored verbatim and never reparsed (param order
// matters to the takeRequest contract).
type RequestRecorder struct {
	mu       sync.Mutex
	byIssuer map[oidc.IssuerID][]recordedRequest
	seq      uint64
	capacity int
	// signal is closed on every Record and replaced under the lock, broadcasting
	// arrivals to any goroutine blocked in Take without pinning it.
	signal chan struct{}
}

// recordedRequest tags a captured request with its global insertion sequence so
// Take/List can order across the per-issuer rings.
type recordedRequest struct {
	seq uint64
	req oidc.CapturedRequest
}

// RecorderOption configures a RequestRecorder at construction.
type RecorderOption func(*RequestRecorder)

// WithRecorderCapacity overrides the per-issuer ring capacity. A non-positive
// value is ignored (the default is kept).
func WithRecorderCapacity(n int) RecorderOption {
	return func(r *RequestRecorder) {
		if n > 0 {
			r.capacity = n
		}
	}
}

// NewRequestRecorder builds an empty recorder with the default per-issuer ring
// capacity unless overridden.
func NewRequestRecorder(opts ...RecorderOption) *RequestRecorder {
	r := &RequestRecorder{
		byIssuer: make(map[oidc.IssuerID][]recordedRequest),
		capacity: defaultRecorderCapacity,
		signal:   make(chan struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Record stores req in its issuer's ring, stamping the adapter-owned ID and
// ReceivedAt metadata the edge constructor leaves zero. When the ring is full the
// oldest capture for that issuer is dropped. It then broadcasts to any blocked
// Take. The body bytes are stored exactly as received.
func (r *RequestRecorder) Record(_ context.Context, req oidc.CapturedRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	req.ID = "req-" + strconv.FormatUint(r.seq, 10)
	req.ReceivedAt = oidc.NewInstant(time.Now())

	ring := r.byIssuer[req.Issuer]
	ring = append(ring, recordedRequest{seq: r.seq, req: req})
	if len(ring) > r.capacity {
		ring = ring[len(ring)-r.capacity:] // drop oldest, keep the newest capacity
	}
	r.byIssuer[req.Issuer] = ring

	close(r.signal)                // wake every blocked Take
	r.signal = make(chan struct{}) // arm the next broadcast
	return nil
}

// List returns every retained capture matching filter, ordered oldest-first by
// arrival, as a non-destructive snapshot (GET /_mock/requests).
func (r *RequestRecorder) List(filter oidc.CaptureFilter) []oidc.CapturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	var matches []recordedRequest
	for _, ring := range r.byIssuer {
		for _, rr := range ring {
			if filter.Matches(rr.req) {
				matches = append(matches, rr)
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].seq < matches[j].seq })
	out := make([]oidc.CapturedRequest, len(matches))
	for i, rr := range matches {
		out[i] = rr.req
	}
	return out
}

// Take dequeues the oldest capture matching filter (destructive FIFO), blocking
// up to timeout for one to arrive when none matches yet. It is the takeRequest
// equivalent: on timeout or context cancellation it returns ok=false rather than
// hanging. A non-positive timeout degrades to a non-blocking poll of whatever is
// already recorded.
func (r *RequestRecorder) Take(
	ctx context.Context,
	filter oidc.CaptureFilter,
	timeout time.Duration,
) (oidc.CapturedRequest, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		r.mu.Lock()
		if req, ok := r.takeMatchLocked(filter); ok {
			r.mu.Unlock()
			return req, true
		}
		wait := r.signal // the channel closed by the next Record
		r.mu.Unlock()

		select {
		case <-wait:
			// A request arrived; loop and re-check for a match.
		case <-timer.C:
			return oidc.CapturedRequest{}, false
		case <-ctx.Done():
			return oidc.CapturedRequest{}, false
		}
	}
}

// takeMatchLocked finds and removes the oldest capture (by global sequence)
// matching filter across all issuer rings. The caller holds the lock.
func (r *RequestRecorder) takeMatchLocked(filter oidc.CaptureFilter) (oidc.CapturedRequest, bool) {
	var (
		bestIssuer oidc.IssuerID
		bestIdx    int
		bestSeq    uint64
		found      bool
	)
	for issuer, ring := range r.byIssuer {
		for i, rr := range ring {
			if !filter.Matches(rr.req) {
				continue
			}
			if !found || rr.seq < bestSeq {
				found, bestSeq, bestIssuer, bestIdx = true, rr.seq, issuer, i
			}
		}
	}
	if !found {
		return oidc.CapturedRequest{}, false
	}
	ring := r.byIssuer[bestIssuer]
	req := ring[bestIdx].req
	r.byIssuer[bestIssuer] = append(ring[:bestIdx], ring[bestIdx+1:]...)
	return req, true
}

// Clear drops every retained capture (DELETE /_mock/requests and the reset path).
func (r *RequestRecorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byIssuer = make(map[oidc.IssuerID][]recordedRequest)
}
