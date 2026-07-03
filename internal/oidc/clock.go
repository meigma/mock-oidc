package oidc

import (
	"fmt"
	"time"
)

// Instant is a point in time used for iat/nbf/exp. It wraps a UTC-normalized
// [time.Time] so the domain has exactly one time type and the frozen-clock seam
// is explicit. A built Instant is immutable.
type Instant struct{ t time.Time }

// NewInstant wraps a [time.Time], normalizing it to UTC.
func NewInstant(t time.Time) Instant { return Instant{t: t.UTC()} }

// ParseInstant parses an RFC 3339 timestamp (the config `systemTime` form). It
// is a config-time parser, so it returns a wrapped error on failure.
func ParseInstant(s string) (Instant, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return Instant{}, fmt.Errorf("invalid systemTime %q: %w", s, err)
	}
	return NewInstant(t), nil
}

// Time returns the underlying UTC [time.Time].
func (i Instant) Time() time.Time { return i.t }

// Unix returns the instant as Unix epoch seconds (the JWT numeric-date form).
func (i Instant) Unix() int64 { return i.t.Unix() }

// Add returns a new Instant advanced by d; the receiver is unchanged.
func (i Instant) Add(d time.Duration) Instant { return Instant{t: i.t.Add(d)} }

// Clock is the outbound time port. Now returns the configured instant — real
// wall-clock by default, or a frozen/advanced systemTime when one is set.
// iat/nbf/exp AND the reported expires_in are all derived from the same Clock,
// so they never diverge under a frozen clock (a deliberate correction of
// upstream's expires_in-from-real-now quirk; P3 determinism). Implementations
// must be safe for concurrent use.
type Clock interface {
	Now() Instant
}

// ClockState is a snapshot of the mutable clock's control-plane state: whether it
// is currently frozen and the instant it reports now. It is the return value of
// the controlapi ClockController.State facet (GET /_mock/clock) and carries no
// behavior — a plain read model over the running clock.
type ClockState struct {
	Frozen bool
	Now    Instant
}

// SystemClock reads the wall clock. It is a unit-test/default seam; the running
// server wires the mutable memory.Clock instead.
type SystemClock struct{}

// Now returns the current instant.
func (SystemClock) Now() Instant { return NewInstant(time.Now()) }

// FixedClock returns a pinned instant, freezing iat/nbf/exp for deterministic
// tests. It is an immutable value seam and cannot be advanced.
type FixedClock struct{ at Instant }

// NewFixedClock pins the clock at the given instant.
func NewFixedClock(at Instant) FixedClock { return FixedClock{at: at} }

// Now returns the pinned instant.
func (c FixedClock) Now() Instant { return c.at }
