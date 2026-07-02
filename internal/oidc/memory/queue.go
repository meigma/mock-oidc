package memory

import (
	"context"
	"strconv"
	"sync"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// CallbackQueue is the in-memory, mutex-guarded FIFO of one-shot Scenarios. One
// instance satisfies BOTH the domain read port oidc.CallbackQueue (DequeueFor —
// the TokenService's sole consumer, shared by every grant including refresh) and
// the control-plane write/inspect facet controlapi.ScenarioStore
// (Enqueue/List/Clear). The two non-cooperating adapters share this single
// backing store through those narrow ports without importing each other.
//
// Consumption is HEAD-only and issuer-conditional: DequeueFor peeks the head and
// pops it ONLY when its issuer matches the request's issuer, so a queued scenario
// for issuer A blocks issuer B even if B's request arrives first (upstream
// parity). FIFO, single-use.
type CallbackQueue struct {
	mu    sync.Mutex
	items []queuedScenario
	seq   uint64
}

// queuedScenario pairs an enqueued Scenario with the id minted at Enqueue time so
// the control plane can report it; the id is opaque and monotonically assigned.
type queuedScenario struct {
	id       oidc.ScenarioID
	scenario oidc.Scenario
}

// NewCallbackQueue builds an empty scenario queue.
func NewCallbackQueue() *CallbackQueue { return &CallbackQueue{} }

// DequeueFor removes and returns the head scenario iff its issuer == id. When the
// queue is empty, or the head belongs to a different issuer, ok is false and the
// queue is left unchanged — the head keeps blocking consumption for every other
// issuer until a matching request drains it (the trickiest upstream parity, made
// an invariant of the port).
func (q *CallbackQueue) DequeueFor(_ context.Context, id oidc.IssuerID) (oidc.Scenario, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return oidc.Scenario{}, false, nil
	}
	head := q.items[0]
	if head.scenario.IssuerID() != id {
		return oidc.Scenario{}, false, nil // head belongs to another issuer: blocks
	}
	q.items = q.items[1:]
	return head.scenario, true, nil
}

// Enqueue appends s to the tail and returns a freshly minted, opaque ScenarioID.
// It is the control plane's only enqueuer (POST /_mock/scenarios).
func (q *CallbackQueue) Enqueue(s oidc.Scenario) (oidc.ScenarioID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	id := oidc.ScenarioID("scenario-" + strconv.FormatUint(q.seq, 10))
	q.items = append(q.items, queuedScenario{id: id, scenario: s})
	return id, nil
}

// List returns the pending scenarios in FIFO (head-first) order for control-plane
// inspection (GET /_mock/scenarios). The slice is a fresh snapshot; mutating it
// does not affect the queue.
func (q *CallbackQueue) List() []oidc.Scenario {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]oidc.Scenario, len(q.items))
	for i, it := range q.items {
		out[i] = it.scenario
	}
	return out
}

// Clear flushes every pending scenario (DELETE /_mock/scenarios and the reset
// path). Draining the queue is part of a control-plane reset.
func (q *CallbackQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = nil
}
