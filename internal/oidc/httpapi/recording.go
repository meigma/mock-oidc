package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// maxCaptureBytes caps how many request-body bytes the recorder buffers. Protocol
// bodies are small url-encoded forms / JSON, so 1 MiB is generous; a larger body
// is truncated in the capture but streamed intact to the handler.
const maxCaptureBytes = 1 << 20

// RecordRequests is the mux-level chi middleware that mirrors every inbound OIDC
// protocol request into the write-only [oidc.RequestRecorder] for control-plane
// inspection (takeRequest). It converts transport values to stdlib primitives at
// the EDGE — r.URL.String(), explicit map[string][]string copies of the headers
// and query, and the raw body bytes — and hands them to oidc.NewCapturedRequest,
// so the domain constructor never names [net/url.URL] / [http.Header] (contract §3).
//
// It path-guards with isOIDCPath: the control plane (/_mock/*) and the infra
// routes (/healthz, /readyz, /metrics, /openapi*, /docs, /favicon.ico) are never
// recorded, so the control plane can never observe itself. The body is buffered
// under maxCaptureBytes and restored via [io.NopCloser] so the handler still reads
// an intact body.
func RecordRequests(rec oidc.RequestRecorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isOIDCPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			var body []byte
			if r.Body != nil {
				body, _ = io.ReadAll(io.LimitReader(r.Body, maxCaptureBytes))
				_ = r.Body.Close()
				r.Body = io.NopCloser(bytes.NewReader(body)) // hand the handler an intact body
			}

			_ = rec.Record(r.Context(), oidc.NewCapturedRequest(
				r.Method,
				r.URL.String(),
				map[string][]string(r.Header),
				map[string][]string(r.URL.Query()),
				body,
			))
			next.ServeHTTP(w, r)
		})
	}
}

// isOIDCPath reports whether p is an inbound protocol path the recorder should
// capture. It is a BLACKLIST (unlike IsProtocolPath's whitelist): everything is
// recorded except the reserved control prefix and the infra/utility routes, so a
// new protocol endpoint (for example the debugger) is captured automatically
// while the control plane never records itself.
func isOIDCPath(p string) bool {
	switch {
	case p == "" || p == "/":
		return false
	case p == "/_mock" || strings.HasPrefix(p, "/_mock/"):
		return false
	case p == "/healthz", p == "/readyz", p == "/metrics", p == "/isalive":
		return false
	case p == "/favicon.ico":
		return false
	case strings.HasPrefix(p, "/openapi"):
		return false
	case p == "/docs" || strings.HasPrefix(p, "/docs"):
		return false
	}
	return true
}
