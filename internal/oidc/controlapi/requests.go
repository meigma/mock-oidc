package controlapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// registerRequests mounts the take/list/clear request-log operations.
func (h *handlers) registerRequests(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "mock-take-request",
		Method:      http.MethodPost,
		Path:        "/requests/take",
		Summary:     "Destructively take the oldest captured request (takeRequest)",
		Description: "Long-polls up to timeoutMs for the oldest matching captured request, removes it, and " +
			"returns it. A miss within the timeout is a clean 404 (not an exception). Raw body bytes are preserved.",
		Tags: []string{tagMockControl},
	}, h.takeRequest)

	huma.Register(api, huma.Operation{
		OperationID: "mock-list-requests",
		Method:      http.MethodGet,
		Path:        "/requests",
		Summary:     "List the captured-request log (non-destructive)",
		Tags:        []string{tagMockControl},
	}, h.listRequests)

	huma.Register(api, huma.Operation{
		OperationID: "mock-clear-requests",
		Method:      http.MethodDelete,
		Path:        "/requests",
		Summary:     "Clear the captured-request log",
		Tags:        []string{tagMockControl},
	}, h.clearRequests)
}

// takeRequest destructively dequeues the oldest matching capture, long-polling up
// to timeoutMs. Upstream throws on timeout; we return a clean RFC 9457 404.
func (h *handlers) takeRequest(ctx context.Context, in *TakeRequestInput) (*TakeRequestOutput, error) {
	filter := toCaptureFilter(in.Body.Issuer, in.Body.Endpoint)
	timeout := time.Duration(in.Body.TimeoutMs) * time.Millisecond
	rec, ok := h.deps.Requests.Take(ctx, filter, timeout)
	if !ok {
		return nil, huma.Error404NotFound("no captured request matched within the timeout")
	}
	return &TakeRequestOutput{Body: toCapturedRequestDTO(rec)}, nil
}

// listRequests returns the non-destructive, arrival-ordered log.
func (h *handlers) listRequests(_ context.Context, in *ListRequestsInput) (*ListRequestsOutput, error) {
	filter := toCaptureFilter(in.Issuer, in.Endpoint)
	recs := h.deps.Requests.List(filter)
	out := &ListRequestsOutput{}
	out.Body.Count = len(recs)
	out.Body.Requests = make([]CapturedRequestDTO, 0, len(recs))
	for _, r := range recs {
		out.Body.Requests = append(out.Body.Requests, toCapturedRequestDTO(r))
	}
	return out, nil
}

// clearRequests drops every retained capture.
func (h *handlers) clearRequests(_ context.Context, _ *struct{}) (*ClearRequestsOutput, error) {
	h.deps.Requests.Clear()
	out := &ClearRequestsOutput{}
	out.Body.Cleared = true
	return out, nil
}
