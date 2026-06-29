package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// minIdleTTL floors the eviction interval so a misconfigured zero or negative
// TTL cannot panic [time.NewTicker] or spin the janitor.
const minIdleTTL = time.Minute

// InMemory is an in-process Limiter backed by a per-key token bucket
// (golang.org/x/time/rate). Each distinct key gets its own bucket that refills
// at rps tokens per second up to a depth of burst, so a client may burst up to
// burst requests and then sustains rps requests per second.
//
// Buckets are created on first use and evicted after they go idle for idleTTL,
// bounding memory under churning keys (for example, per-IP keys behind NAT or
// during a scan). A background janitor performs the eviction; call Stop when the
// limiter is no longer needed so the goroutine exits. The composition root owns
// that lifecycle and stops the limiter on shutdown.
type InMemory struct {
	rps   rate.Limit
	burst int
	ttl   time.Duration

	mu      sync.Mutex
	clients map[string]*clientBucket
	stop    chan struct{}
	stopped bool
}

// clientBucket is one key's token bucket plus the last time it was seen, which
// the janitor uses to evict idle keys.
type clientBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewInMemory builds an in-process limiter that allows up to burst requests
// immediately and rps requests per second sustained, independently per key.
// Buckets unused for idleTTL are evicted. The returned limiter starts a janitor
// goroutine; call Stop to end it.
func NewInMemory(rps rate.Limit, burst int, idleTTL time.Duration) *InMemory {
	if idleTTL < minIdleTTL {
		idleTTL = minIdleTTL
	}

	l := &InMemory{
		rps:     rps,
		burst:   burst,
		ttl:     idleTTL,
		clients: make(map[string]*clientBucket),
		stop:    make(chan struct{}),
	}
	go l.janitor()

	return l
}

// Allow reports whether the request for key may proceed, consuming a token when
// it does. A denied request reports how long until the next token frees up so
// the middleware can set Retry-After. It never returns an error: an in-process
// bucket has no backend that can fail.
func (l *InMemory) Allow(_ context.Context, key string) (Decision, error) {
	limiter := l.bucket(key)

	reservation := limiter.Reserve()
	if !reservation.OK() {
		// burst is non-positive, so a token can never be granted: deny with no
		// retry hint rather than reporting an infinite wait.
		return Decision{Allowed: false}, nil
	}

	if delay := reservation.Delay(); delay > 0 {
		// No token is available yet. Cancel so this check does not consume a
		// future token, and report the wait.
		reservation.Cancel()

		return Decision{Allowed: false, RetryAfter: delay}, nil
	}

	return Decision{Allowed: true}, nil
}

// bucket returns the token bucket for key, creating it on first use, and
// records that the key was just seen so the janitor does not evict it.
func (l *InMemory) bucket(key string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	c, ok := l.clients[key]
	if !ok {
		c = &clientBucket{limiter: rate.NewLimiter(l.rps, l.burst)}
		l.clients[key] = c
	}
	c.lastSeen = time.Now()

	return c.limiter
}

// Stop ends the janitor goroutine. It is safe to call more than once; calls
// after the first are no-ops. Allow still works after Stop, but idle buckets are
// no longer evicted.
func (l *InMemory) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.stopped {
		return
	}
	l.stopped = true
	close(l.stop)
}

// janitor evicts idle buckets on each tick until Stop closes the stop channel.
func (l *InMemory) janitor() {
	ticker := time.NewTicker(l.ttl)
	defer ticker.Stop()

	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.evictIdle()
		}
	}
}

// evictIdle removes every bucket not seen within the idle TTL.
func (l *InMemory) evictIdle() {
	cutoff := time.Now().Add(-l.ttl)

	l.mu.Lock()
	defer l.mu.Unlock()

	for key, c := range l.clients {
		if c.lastSeen.Before(cutoff) {
			delete(l.clients, key)
		}
	}
}
