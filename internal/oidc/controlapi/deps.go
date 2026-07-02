package controlapi

import (
	"context"
	"time"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// Prefix is the reserved path prefix the control plane mounts under. Register
// applies it via huma.NewGroup and registers RELATIVE paths (/mint, /scenarios,
// …), so the group yields /_mock/mint, /_mock/scenarios, … with no double-prefix.
// The composition root passes the base huma.API to Register — it must NOT pre-wrap
// the API in a group itself, or the operations would resolve to /_mock/_mock.
const Prefix = "/_mock"

// ScenarioStore is the control-plane write/inspect view of the one-shot callback
// queue. The OIDC core consumes the same backing store through the read-side
// oidc.CallbackQueue port (DequeueFor only); this facet is the only enqueuer. It
// is satisfied by *memory.CallbackQueue.
type ScenarioStore interface {
	Enqueue(s oidc.Scenario) (oidc.ScenarioID, error)
	List() []oidc.Scenario
	Clear()
}

// RequestLog is the control-plane read view of the recorder. The OIDC edge writes
// through the narrower oidc.RequestRecorder (Record only); this facet drains it.
// It is satisfied by *memory.RequestRecorder.
type RequestLog interface {
	List(filter oidc.CaptureFilter) []oidc.CapturedRequest
	// Take dequeues the oldest matching request (FIFO), blocking up to timeout for
	// one to arrive; ok is false on timeout or context cancellation. This is the
	// takeRequest equivalent.
	Take(ctx context.Context, filter oidc.CaptureFilter, timeout time.Duration) (oidc.CapturedRequest, bool)
	Clear()
}

// ClockController is the control-plane write view of the mutable clock. The OIDC
// core reads the same clock through the oidc.Clock port (Now only), and so does
// the /userinfo + /introspect verifier — one clock drives both issuance and
// verification, so a control freeze/advance moves both. It is satisfied by
// *memory.Clock.
type ClockController interface {
	Freeze(at oidc.Instant)
	Unfreeze()
	Advance(d time.Duration)
	State() oidc.ClockState
}

// Deps are the collaborators the control operations drive. Tokens are minted
// through the very same application service the /token endpoint uses, so a minted
// token is indistinguishable from a granted one and verifies against /jwks.
type Deps struct {
	Tokens    *oidc.TokenService
	Scenarios ScenarioStore
	Requests  RequestLog
	Clock     ClockController
}

// handlers binds the dependencies for the operation handler methods.
type handlers struct {
	deps Deps
}
