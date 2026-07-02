package http

import (
	"net/http"

	"github.com/meigma/mock-oidc/internal/observability"
)

// NewMetricsHandler returns a minimal handler that serves the Prometheus metrics
// endpoint at /metrics and nothing else. It is meant for a dedicated metrics
// listener, kept off the public API surface and outside its middleware chain.
func NewMetricsHandler(metrics *observability.Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())

	return mux
}
