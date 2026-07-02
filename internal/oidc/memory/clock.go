package memory

import (
	"sync"
	"time"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// Clock is the mutable, concurrency-safe [oidc.Clock] the running server wires
// (in contrast to the immutable oidc.FixedClock/SystemClock used by unit tests).
// Unfrozen it reads the wall clock; frozen it returns a pinned instant that
// Advance moves. The same instance backs the control plane's freeze/advance so a
// runtime freeze transparently reflects into every issuer's iat/nbf/exp.
//
// It satisfies both the domain read port oidc.Clock (Now only) and the
// control-plane write facet controlapi.ClockController (Freeze/Unfreeze/Advance/
// State) — one clock drives issuance AND verification, so a control freeze moves
// both.
type Clock struct {
	mu     sync.Mutex
	frozen bool
	at     oidc.Instant
}

// NewClock builds an unfrozen clock reading the wall clock.
func NewClock() *Clock { return &Clock{} }

// NewFrozenClock builds a clock pinned at the given instant (the config
// systemTime seed, which freezes the clock at startup).
func NewFrozenClock(at oidc.Instant) *Clock {
	return &Clock{frozen: true, at: at}
}

// Now returns the pinned instant when frozen, otherwise the current wall time.
func (c *Clock) Now() oidc.Instant {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return c.at
	}
	return oidc.NewInstant(time.Now())
}

// Freeze pins the clock at the given instant (the control plane's
// PUT /_mock/clock {frozen:true, instant} path). Re-freezing repins to the new
// instant, so a test can jump the clock to any fixed time.
func (c *Clock) Freeze(at oidc.Instant) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frozen = true
	c.at = at
}

// Unfreeze returns the clock to reading the wall clock.
func (c *Clock) Unfreeze() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.frozen = false
}

// Advance moves the clock forward by d, freezing it at the current instant first
// if it was not already frozen.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.frozen {
		c.frozen = true
		c.at = oidc.NewInstant(time.Now())
	}
	c.at = c.at.Add(d)
}

// State returns a snapshot of the clock for the control plane's GET /_mock/clock:
// whether it is frozen and the instant it currently reports (the pinned instant
// when frozen, otherwise the wall clock).
func (c *Clock) State() oidc.ClockState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return oidc.ClockState{Frozen: true, Now: c.at}
	}
	return oidc.ClockState{Frozen: false, Now: oidc.NewInstant(time.Now())}
}
