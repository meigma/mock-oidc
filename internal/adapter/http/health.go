package http

import (
	"context"
	"encoding/json"
	"net/http"
)

// The infrastructure endpoints (/healthz, /readyz, /metrics) are intentionally
// excluded from the RFC 9457 problem+json error contract that the API routes
// follow: liveness/readiness return plain application/json status objects and
// /metrics returns the Prometheus exposition format, since these are consumed by
// probes and scrapers rather than API clients.

// statusKey is the JSON field name used by the liveness response.
const statusKey = "status"

const (
	statusOK          = "ok"
	statusReady       = "ready"
	statusUnavailable = "unavailable"
)

// ReadinessCheck is a named probe of a dependency required to serve traffic.
// Check returns nil when the dependency is ready and a non-nil error otherwise;
// it receives the request context so probes (for example, a database ping) can
// honor cancellation and deadlines. Name labels the check in the /readyz body.
type ReadinessCheck struct {
	// Name labels the check in the readiness response.
	Name string
	// Check probes the dependency, returning nil when it is ready.
	Check func(ctx context.Context) error
}

// readyzResponse is the /readyz body: an overall status plus a per-check map.
type readyzResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// handleHealthz reports liveness: the process is up and serving.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{statusKey: statusOK})
}

// handleReadyz reports readiness by evaluating every check (without
// short-circuiting) and reporting each result by name. The endpoint is ready
// only when all checks pass; with no checks the server is always ready.
func handleReadyz(checks []ReadinessCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := make(map[string]string, len(checks))
		ready := true
		for _, check := range checks {
			if err := check.Check(r.Context()); err != nil {
				results[check.Name] = statusUnavailable
				ready = false

				continue
			}
			results[check.Name] = statusOK
		}

		status, overall := http.StatusOK, statusReady
		if !ready {
			status, overall = http.StatusServiceUnavailable, statusUnavailable
		}

		writeJSON(w, status, readyzResponse{Status: overall, Checks: results})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
