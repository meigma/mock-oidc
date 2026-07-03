package controlapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// registerReset mounts POST /_mock/reset.
func (h *handlers) registerReset(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "mock-reset",
		Method:      http.MethodPost,
		Path:        "/reset",
		Summary:     "Reset control-plane state (the @AfterEach)",
		Description: "Atomically flushes the scenario queue and the request log and unfreezes the clock. It " +
			"deliberately does NOT drop materialized signing keys, so JWKS a client already fetched stays valid.",
		Tags: []string{tagMockControl},
	}, h.reset)
}

// reset clears the scenario queue and request log and unfreezes the clock. Signing
// keys are intentionally preserved (kid == issuer id, stable for the process
// lifetime) so previously-fetched JWKS still verifies.
func (h *handlers) reset(_ context.Context, _ *struct{}) (*ResetOutput, error) {
	h.deps.Scenarios.Clear()
	h.deps.Requests.Clear()
	h.deps.Clock.Unfreeze()
	out := &ResetOutput{}
	out.Body.Reset = true
	return out, nil
}
