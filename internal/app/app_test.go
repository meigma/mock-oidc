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
