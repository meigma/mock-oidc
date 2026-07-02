package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// faviconInput is the empty input for the root favicon operation: no path
// params, no body — /favicon.ico is issuer-independent.
type faviconInput struct{}

// registerFavicon mounts the flat GET /favicon.ico root operation. It is
// issuer-independent (no {issuer} segment), returns an empty 200, and — crucially
// — keeps the committed OpenAPI document carrying the endpoint the design's
// inventory mandates, so the first browser surface stops emitting spurious 404s.
func (h *handlers) registerFavicon(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "favicon",
		Method:      http.MethodGet,
		Path:        "/favicon.ico",
		Summary:     "Favicon (empty 200)",
		Tags:        []string{tagOIDC},
	}, h.favicon)
}

// favicon returns an empty 200. The shared BrowserOutput carries no body and no
// headers; Huma writes the empty body and skips the empty Location/Content-Type.
func (h *handlers) favicon(_ context.Context, _ *faviconInput) (*BrowserOutput, error) {
	return &BrowserOutput{Status: http.StatusOK}, nil
}
