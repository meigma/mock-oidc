package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/adapter/http/problem"
	"github.com/meigma/mock-oidc/internal/observability"
)

const testRequestTimeout = 5 * time.Second

// TestInfraRoutesWithoutResources exercises the generic transport with no resource
// registrations (Register is nil): the infrastructure routes must still serve.
func TestInfraRoutesWithoutResources(t *testing.T) {
	t.Parallel()

	discard := observability.NewLogger(io.Discard, slog.LevelError, "json")
	handler := NewRouter(RouterDeps{
		Logger:               discard,
		Metrics:              observability.NewMetrics(),
		ServeMetricsEndpoint: true,
		Version:              "test",
		RequestTimeout:       testRequestTimeout,
		Readiness:            nil,
		Register:             nil,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	for _, path := range []string{"/isalive", "/healthz", "/readyz"} {
		resp := get(t, srv, path)
		assert.Equal(t, http.StatusOK, resp.status, path)
	}

	get(t, srv, "/healthz")

	metrics := get(t, srv, "/metrics")
	require.Equal(t, http.StatusOK, metrics.status)
	assert.Contains(t, metrics.body, "http_requests_total")
	assert.Contains(t, metrics.body, "go_goroutines")
}

// TestMetricsEndpointOmittedWhenServedSeparately verifies /metrics is absent from
// the main router when a dedicated metrics listener serves it.
func TestMetricsEndpointOmittedWhenServedSeparately(t *testing.T) {
	t.Parallel()

	discard := observability.NewLogger(io.Discard, slog.LevelError, "json")
	handler := NewRouter(RouterDeps{
		Logger:               discard,
		Metrics:              observability.NewMetrics(),
		ServeMetricsEndpoint: false,
		Version:              "test",
		RequestTimeout:       testRequestTimeout,
		Readiness:            nil,
		Register:             nil,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	assert.Equal(t, http.StatusNotFound, get(t, srv, "/metrics").status)
	assert.Equal(t, http.StatusOK, get(t, srv, "/healthz").status)
}

// TestRouterWithTracingServesRequests verifies that enabling tracing wraps the
// handler with the otelhttp server-span instrumentation without breaking
// routing: infra routes still serve normally.
func TestRouterWithTracingServesRequests(t *testing.T) {
	t.Parallel()

	discard := observability.NewLogger(io.Discard, slog.LevelError, "json")
	handler := NewRouter(RouterDeps{
		Logger:               discard,
		Metrics:              observability.NewMetrics(),
		ServeMetricsEndpoint: true,
		Version:              "test",
		RequestTimeout:       testRequestTimeout,
		Tracing:              true,
		Register:             nil,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	assert.Equal(t, http.StatusOK, get(t, srv, "/healthz").status)
	assert.Equal(t, http.StatusOK, get(t, srv, "/metrics").status)
}

// TestTraceableRequest checks that the infrastructure routes are excluded from
// tracing while resource routes are traced.
func TestTraceableRequest(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"/isalive": false,
		"/healthz": false,
		"/readyz":  false,
		"/metrics": false,
		"/default/.well-known/openid-configuration": true,
		"/default/token": true,
	}
	for path, want := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		assert.Equalf(t, want, traceableRequest(req), "traceableRequest(%q)", path)
	}
}

// TestNewMetricsHandler verifies the dedicated metrics handler serves /metrics
// and nothing else.
func TestNewMetricsHandler(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(NewMetricsHandler(observability.NewMetrics()))
	t.Cleanup(srv.Close)

	metrics := get(t, srv, "/metrics")
	require.Equal(t, http.StatusOK, metrics.status)
	assert.Contains(t, metrics.body, "go_goroutines")

	assert.Equal(t, http.StatusNotFound, get(t, srv, "/healthz").status)
}

// TestReadyzReflectsChecks verifies the named readiness seam: every check runs
// (no short-circuit) and each result is reported by name, with the overall
// status reflecting whether any check failed.
func TestReadyzReflectsChecks(t *testing.T) {
	t.Parallel()

	discard := observability.NewLogger(io.Discard, slog.LevelError, "json")

	ok := func(context.Context) error { return nil }
	down := func(context.Context) error { return errors.New("down") }

	tests := []struct {
		name       string
		checks     []ReadinessCheck
		wantStatus int
		wantBody   map[string]string
	}{
		{
			name:       "no checks is ready",
			checks:     nil,
			wantStatus: http.StatusOK,
			wantBody:   map[string]string{},
		},
		{
			name:       "all pass",
			checks:     []ReadinessCheck{{Name: "store", Check: ok}},
			wantStatus: http.StatusOK,
			wantBody:   map[string]string{"store": "ok"},
		},
		{
			name:       "any failure is unavailable",
			checks:     []ReadinessCheck{{Name: "store", Check: ok}, {Name: "cache", Check: down}},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   map[string]string{"store": "ok", "cache": "unavailable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := NewRouter(RouterDeps{
				Logger:         discard,
				Metrics:        observability.NewMetrics(),
				Version:        "test",
				RequestTimeout: testRequestTimeout,
				Readiness:      tt.checks,
				Register:       nil,
			})

			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)

			resp := get(t, srv, "/readyz")
			assert.Equal(t, tt.wantStatus, resp.status)

			var body readyzResponse
			require.NoError(t, json.Unmarshal([]byte(resp.body), &body))
			assert.Equal(t, tt.wantBody, body.Checks)
		})
	}
}

// TestNotFoundReturnsProblemJSON verifies the chi NotFound fallback emits RFC 9457
// problem+json instead of chi's text/plain default.
func TestNotFoundReturnsProblemJSON(t *testing.T) {
	t.Parallel()

	discard := observability.NewLogger(io.Discard, slog.LevelError, "json")
	handler := NewRouter(RouterDeps{
		Logger:         discard,
		Metrics:        observability.NewMetrics(),
		Version:        "test",
		RequestTimeout: testRequestTimeout,
		Readiness:      nil,
		Register:       nil,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	resp := get(t, srv, "/does-not-exist")
	assert.Equal(t, http.StatusNotFound, resp.status)
	assert.Equal(t, problem.ContentType, resp.contentType)
	assert.Contains(t, resp.body, `"status":404`)
}

// TestFallbackWriterHandles405 verifies the transport-neutral FallbackWriter seam
// is consulted on a MethodNotAllowed before the default problem+json fallback: a
// strategy returning true fully owns the response.
func TestFallbackWriterHandles405(t *testing.T) {
	t.Parallel()

	discard := observability.NewLogger(io.Discard, slog.LevelError, "json")
	var called bool
	handler := NewRouter(RouterDeps{
		Logger:         discard,
		Metrics:        observability.NewMetrics(),
		Version:        "test",
		RequestTimeout: testRequestTimeout,
		Register:       nil,
		FallbackWriter: func(w http.ResponseWriter, _ *http.Request) bool {
			called = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"error":"custom"}`))
			return true
		},
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// POST /healthz matches the GET-only infra route, so the router's
	// MethodNotAllowed handler runs and consults the FallbackWriter.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/healthz", nil)
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.True(t, called, "FallbackWriter should be consulted on a 405")
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.JSONEq(t, `{"error":"custom"}`, string(data))
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

type testResponse struct {
	status      int
	body        string
	contentType string
}

func get(t *testing.T, srv *httptest.Server, path string) testResponse {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return testResponse{
		status:      resp.StatusCode,
		body:        string(data),
		contentType: resp.Header.Get("Content-Type"),
	}
}

// TestCORSPreflightAllowsConfiguredOrigin verifies a preflight against a
// configured origin reflects that origin, sets credentials true, advertises the
// fixed method set, echoes the requested headers, and answers 204 (Decision D-3).
func TestCORSPreflightAllowsConfiguredOrigin(t *testing.T) {
	t.Parallel()

	srv := corsTestServer(t, []string{"https://app.example"})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodOptions, srv.URL+"/default/token", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, "https://app.example", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "POST, GET, OPTIONS", resp.Header.Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Authorization, Content-Type", resp.Header.Get("Access-Control-Allow-Headers"))
	assert.Contains(t, resp.Header.Get("Vary"), "Origin")
}

// TestCORSDefaultOnReflectsOrigin verifies the default-ON reflect-origin policy
// (Decision D-3): with no configured allowlist, any request Origin is reflected
// back with credentials enabled — never the "*" wildcard — so a browser client
// works with zero configuration.
func TestCORSDefaultOnReflectsOrigin(t *testing.T) {
	t.Parallel()

	srv := corsTestServer(t, nil)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/healthz", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://spa.example")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "https://spa.example", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
	assert.Contains(t, resp.Header.Get("Vary"), "Origin")
}

// TestCORSAllowlistTightens verifies a configured allowlist rejects origins
// outside it: no CORS headers are emitted for a disallowed origin.
func TestCORSAllowlistTightens(t *testing.T) {
	t.Parallel()

	srv := corsTestServer(t, []string{"https://app.example"})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/healthz", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://evil.example")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
}

func corsTestServer(t *testing.T, origins []string) *httptest.Server {
	t.Helper()

	discard := observability.NewLogger(io.Discard, slog.LevelError, "json")
	handler := NewRouter(RouterDeps{
		Logger:             discard,
		Metrics:            observability.NewMetrics(),
		Version:            "test",
		RequestTimeout:     testRequestTimeout,
		CORSAllowedOrigins: origins,
		Readiness:          nil,
		Register:           nil,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

// TestClientIPResolution verifies the trust model: by default a forged proxy
// header is ignored and the access log records the TCP peer, but a configured
// trusted header is honored. The handler is driven synchronously so the shared
// log buffer is written and read on one goroutine.
func TestClientIPResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		trustedHeader string
		wantClientIP  string
	}{
		{name: "ignores forged header by default", trustedHeader: "", wantClientIP: "192.0.2.1"},
		{name: "honors trusted header", trustedHeader: "X-Real-IP", wantClientIP: "203.0.113.7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := observability.NewLogger(&buf, slog.LevelInfo, "json")
			handler := NewRouter(RouterDeps{
				Logger:             logger,
				Metrics:            observability.NewMetrics(),
				Version:            "test",
				RequestTimeout:     testRequestTimeout,
				TrustedProxyHeader: tt.trustedHeader,
				Readiness:          nil,
				Register:           nil,
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil) // RemoteAddr 192.0.2.1:1234
			req.Header.Set("X-Real-IP", "203.0.113.7")
			handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.wantClientIP, findAccessLog(t, buf.String())["client_ip"])
		})
	}
}

// findAccessLog returns the parsed "http request" access-log line from logs.
func findAccessLog(t *testing.T, logs string) map[string]any {
	t.Helper()

	for line := range strings.SplitSeq(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}

		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["msg"] == "http request" {
			return entry
		}
	}

	t.Fatalf("no access-log line in:\n%s", logs)

	return nil
}
