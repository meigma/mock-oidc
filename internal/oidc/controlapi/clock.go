package controlapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// registerClock mounts the GET/PUT /_mock/clock and POST /_mock/clock/advance ops.
func (h *handlers) registerClock(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "mock-get-clock",
		Method:      http.MethodGet,
		Path:        "/clock",
		Summary:     "Get the clock state",
		Tags:        []string{tagMockControl},
	}, h.getClock)

	huma.Register(api, huma.Operation{
		OperationID: "mock-set-clock",
		Method:      http.MethodPut,
		Path:        "/clock",
		Summary:     "Freeze or unfreeze the clock",
		Description: "frozen=true pins the clock at instant (required); frozen=false returns it to the wall " +
			"clock. One global clock drives issuance AND verification, so this moves iat/nbf/exp and the verifier alike.",
		Tags: []string{tagMockControl},
	}, h.setClock)

	huma.Register(api, huma.Operation{
		OperationID: "mock-advance-clock",
		Method:      http.MethodPost,
		Path:        "/clock/advance",
		Summary:     "Advance the (frozen) clock by a Go duration",
		Tags:        []string{tagMockControl},
	}, h.advanceClock)
}

// getClock returns the current clock state.
func (h *handlers) getClock(_ context.Context, _ *struct{}) (*ClockStateOutput, error) {
	return clockStateOutput(h.deps.Clock.State()), nil
}

// setClock freezes (at a required instant) or unfreezes the clock and returns the
// resulting state.
func (h *handlers) setClock(_ context.Context, in *SetClockInput) (*ClockStateOutput, error) {
	if in.Body.Frozen {
		if in.Body.Instant == nil {
			return nil, toControlError(fmt.Errorf("%w: instant is required when frozen=true", oidc.ErrInvalidInstant))
		}
		h.deps.Clock.Freeze(oidc.NewInstant(*in.Body.Instant))
	} else {
		h.deps.Clock.Unfreeze()
	}
	return clockStateOutput(h.deps.Clock.State()), nil
}

// advanceClock parses a Go duration and advances (freezing first if needed) the
// clock, returning the resulting state.
func (h *handlers) advanceClock(_ context.Context, in *AdvanceClockInput) (*ClockStateOutput, error) {
	d, err := time.ParseDuration(in.Body.Duration)
	if err != nil {
		return nil, toControlError(fmt.Errorf("%w: %q: %w", oidc.ErrInvalidDuration, in.Body.Duration, err))
	}
	h.deps.Clock.Advance(d)
	return clockStateOutput(h.deps.Clock.State()), nil
}

// clockStateOutput renders a domain ClockState onto the wire DTO.
func clockStateOutput(st oidc.ClockState) *ClockStateOutput {
	out := &ClockStateOutput{}
	out.Body.Frozen = st.Frozen
	out.Body.Now = st.Now.Time()
	return out
}
