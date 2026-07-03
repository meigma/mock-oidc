package controlapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// pathScenarios is the group-relative path shared by the scenario operations.
const pathScenarios = "/scenarios"

// registerScenarios mounts the POST/GET/DELETE /_mock/scenarios operations.
func (h *handlers) registerScenarios(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "mock-enqueue-scenario",
		Method:      http.MethodPost,
		Path:        pathScenarios,
		Summary:     "Enqueue a one-shot scenario (enqueueCallback)",
		Description: "Enqueues a single-use, issuer-matched callback consumed by the NEXT matching /token " +
			"(or refresh) request for that issuer. It does not return a token; it changes how the next real request responds.",
		Tags: []string{tagMockControl},
	}, h.enqueueScenario)

	huma.Register(api, huma.Operation{
		OperationID: "mock-list-scenarios",
		Method:      http.MethodGet,
		Path:        pathScenarios,
		Summary:     "List the pending scenario queue",
		Tags:        []string{tagMockControl},
	}, h.listScenarios)

	huma.Register(api, huma.Operation{
		OperationID: "mock-clear-scenarios",
		Method:      http.MethodDelete,
		Path:        pathScenarios,
		Summary:     "Flush the scenario queue",
		Tags:        []string{tagMockControl},
	}, h.clearScenarios)
}

// enqueueScenario maps the DTO onto a domain callback (shared with the config
// parser), wraps it as a one-shot Scenario, and enqueues it on the SAME queue the
// TokenService consults during grant resolution.
func (h *handlers) enqueueScenario(_ context.Context, in *EnqueueScenarioInput) (*EnqueueScenarioOutput, error) {
	cb, err := toTokenCallback(in.Body)
	if err != nil {
		return nil, toControlError(err)
	}
	scenario, err := oidc.NewScenario(cb)
	if err != nil {
		return nil, toControlError(err)
	}
	id, err := h.deps.Scenarios.Enqueue(scenario)
	if err != nil {
		return nil, toControlError(err)
	}
	out := &EnqueueScenarioOutput{}
	out.Body.ScenarioID = string(id)
	out.Body.QueueDepth = len(h.deps.Scenarios.List())
	return out, nil
}

// listScenarios returns the pending queue (depth + head-first decoded summaries).
func (h *handlers) listScenarios(_ context.Context, _ *struct{}) (*ListScenariosOutput, error) {
	pending := h.deps.Scenarios.List()
	out := &ListScenariosOutput{}
	out.Body.QueueDepth = len(pending)
	out.Body.Scenarios = make([]ScenarioSummaryDTO, 0, len(pending))
	for _, s := range pending {
		out.Body.Scenarios = append(out.Body.Scenarios, ScenarioSummaryDTO{
			Issuer: string(s.IssuerID()),
			Kind:   scenarioKind(s),
		})
	}
	return out, nil
}

// clearScenarios flushes the queue.
func (h *handlers) clearScenarios(_ context.Context, _ *struct{}) (*ClearScenariosOutput, error) {
	h.deps.Scenarios.Clear()
	out := &ClearScenariosOutput{}
	out.Body.QueueDepth = len(h.deps.Scenarios.List())
	return out, nil
}
