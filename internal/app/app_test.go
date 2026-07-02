package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/app"
	"github.com/meigma/mock-oidc/internal/config"
	"github.com/meigma/mock-oidc/internal/observability"
)

// TestAppWiringServesInfraRoutes proves the skeleton composition root wires a
// working server. With no OIDC services registered, the server serves only the
// infrastructure routes; /metrics is co-located on the API listener here by
// clearing metrics-addr.
func TestAppWiringServesInfraRoutes(t *testing.T) {
	t.Parallel()

	vp := viper.New()
	vp.Set("metrics-addr", "")
	cfg := config.Load(vp)
	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")

	application, err := app.New(context.Background(), cfg, logger, "test")
	require.NoError(t, err)

	handler := application.Handler()
	require.NotNil(t, handler)

	for _, path := range []string{"/isalive", "/healthz", "/readyz", "/metrics"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equalf(t, http.StatusOK, rec.Code, "GET %s", path)
	}
}

// TestAppServesDefaultDiscovery proves the composition root wires the OIDC
// hexagon: with zero config the default issuer's discovery document is served,
// issuer-scoped to the request-derived base URL.
func TestAppServesDefaultDiscovery(t *testing.T) {
	t.Parallel()

	cfg := config.Load(viper.New())
	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")

	application, err := app.New(context.Background(), cfg, logger, "test")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/default/.well-known/openid-configuration", nil)
	application.Handler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	issuer, _ := doc["issuer"].(string)
	assert.Contains(t, issuer, "/default", "issuer scoped to the default provider")
}

// TestAppControlPlaneCoLocated proves the /_mock control plane is registered on
// the main API listener by default (ControlEnabled=true, ControlAddr=""), stamps
// the testing-only marker header, and drives the shared clock: freezing time via
// PUT /_mock/clock is reflected in GET /_mock/clock.
func TestAppControlPlaneCoLocated(t *testing.T) {
	t.Parallel()

	cfg := config.Load(viper.New())
	cfg.MetricsAddr = "" // co-locate everything on the API handler for the test
	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")

	application, err := app.New(context.Background(), cfg, logger, "test")
	require.NoError(t, err)
	handler := application.Handler()

	// Freeze the clock through the control plane.
	freeze := `{"frozen":true,"instant":"2020-01-01T00:00:00Z"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/_mock/clock", bytes.NewBufferString(freeze))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	require.Equalf(t, http.StatusOK, rec.Code, "PUT /_mock/clock: %s", rec.Body)
	assert.Equal(t, "testing-only", rec.Header().Get("X-Mock-Oidc"), "control responses carry the marker header")

	// The freeze is observable through GET /_mock/clock (one shared clock).
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_mock/clock", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var clock struct {
		Frozen bool   `json:"frozen"`
		Now    string `json:"now"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &clock))
	assert.True(t, clock.Frozen)
	assert.Equal(t, "2020-01-01T00:00:00Z", clock.Now)
}

// TestAppControlPlaneDisabled proves --control-enabled=false leaves every /_mock
// route returning the ordinary OIDC 404 (not a control response).
func TestAppControlPlaneDisabled(t *testing.T) {
	t.Parallel()

	cfg := config.Load(viper.New())
	cfg.ControlEnabled = false
	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")

	application, err := app.New(context.Background(), cfg, logger, "test")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/_mock/reset", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")
	application.Handler().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "/_mock is a plain 404 when the control plane is disabled")
	assert.Empty(t, rec.Header().Get("X-Mock-Oidc"), "a disabled control plane stamps no marker header")
}

// TestAppControlTokenGate proves --control-token requires the X-Mock-Control-Token
// header: an un-tokened /_mock call is a 401 problem+json, and the correct token
// passes.
func TestAppControlTokenGate(t *testing.T) {
	t.Parallel()

	cfg := config.Load(viper.New())
	cfg.ControlToken = "s3cret"
	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")

	application, err := app.New(context.Background(), cfg, logger, "test")
	require.NoError(t, err)
	handler := application.Handler()

	// Missing token → 401 problem+json.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_mock/clock", nil)
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/problem+json")

	// Correct token → accepted.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/_mock/clock", nil)
	req.Header.Set("X-Mock-Control-Token", "s3cret")
	handler.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "the correct control token passes the gate")
}

// TestAppControlDedicatedListener proves a non-empty ControlAddr moves the control
// plane to its own server: the API handler no longer carries /_mock (plain 404),
// while the dedicated control handler serves it.
func TestAppControlDedicatedListener(t *testing.T) {
	t.Parallel()

	cfg := config.Load(viper.New())
	cfg.ControlAddr = ":0"
	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")

	application, err := app.New(context.Background(), cfg, logger, "test")
	require.NoError(t, err)

	// The API listener carries no /_mock routes.
	rec := httptest.NewRecorder()
	application.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_mock/clock", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code, "co-located /_mock is absent in dedicated-listener mode")

	// The dedicated control handler serves it.
	ctrl := application.ControlHandler()
	require.NotNil(t, ctrl)
	rec = httptest.NewRecorder()
	ctrl.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/_mock/clock", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "the dedicated control listener serves /_mock")
	assert.Equal(t, "testing-only", rec.Header().Get("X-Mock-Oidc"))
}

// TestAppLogsForTestingBanner proves the for-testing-only positioning banner is
// emitted at startup (C10).
func TestAppLogsForTestingBanner(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, slog.LevelInfo, "json")
	cfg := config.Load(viper.New())

	_, err := app.New(context.Background(), cfg, logger, "test")
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "FOR TESTING ONLY")
}
