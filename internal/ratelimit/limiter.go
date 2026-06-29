// Package ratelimit provides per-client request rate limiting for the API: a
// Limiter port, an in-process token-bucket adapter, and a Huma middleware that
// enforces the limit and emits an RFC 9457 429 response when it is exceeded.
//
// The Limiter interface is the extension seam. The shipped in-process adapter
// (NewInMemory) limits per process, which is the right default for a single
// instance; behind multiple replicas a shared backend (for example, Redis)
// keeps the limit global. Implement Limiter with that backend and wire it in
// the composition root — the middleware and key function are unchanged.
package ratelimit

import (
	"context"
	"time"
)

// Decision is the outcome of a limiter check for a single request. It carries
// what the middleware needs to answer the client: whether the request is
// allowed and, when it is not, how long the client should wait before retrying.
type Decision struct {
	// Allowed reports whether the request may proceed.
	Allowed bool
	// RetryAfter is how long until the client may retry. It is meaningful only
	// when Allowed is false; the middleware rounds it up to whole seconds for the
	// Retry-After response header. Zero means no hint is available.
	RetryAfter time.Duration
}

// Limiter decides whether a request identified by key may proceed now. key is
// the client identity the middleware extracts — by default the client IP.
// Implementations must be safe for concurrent use.
//
// Allow returns a non-nil error only for an infrastructure failure (for
// example, a distributed backend being unreachable). The middleware fails open
// on such an error — it lets the request through — so that a limiter outage
// cannot take the whole API down with it. A plain allow/deny decision returns a
// nil error.
type Limiter interface {
	Allow(ctx context.Context, key string) (Decision, error)
}
