package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/app"
	"github.com/meigma/mock-oidc/internal/config"
	"github.com/meigma/mock-oidc/internal/observability"
)

// newSlice6App builds a zero-config app (metrics co-located on the API listener)
// with an optional static-assets directory, returning its wired handler.
func newSlice6App(t *testing.T, staticDir string) http.Handler {
	t.Helper()

	vp := viper.New()
	vp.Set("metrics-addr", "") // co-locate /metrics on the API handler so we can scrape it
	cfg := config.Load(vp)
	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")

	seed := config.DefaultSeed()
	seed.StaticAssetsPath = staticDir

	application, err := app.New(context.Background(), cfg, logger, "test", app.WithSeed(seed))
	require.NoError(t, err)
	return application.Handler()
}

// TestSlice6ForwardedIssuerReflected proves proxy-aware base-URL resolution end to
// end: with X-Forwarded-* headers, the discovery document's issuer and every
// URL-bearing endpoint reflect the external https address at the host root (no
// port), not the internal listen address.
func TestSlice6ForwardedIssuerReflected(t *testing.T) {
	t.Parallel()

	handler := newSlice6App(t, "")

	req := httptest.NewRequest(http.MethodGet, "/default/.well-known/openid-configuration", nil)
	req.Host = "internal:8080"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "idp.example.com")
	req.Header.Set("X-Forwarded-Port", "443")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))

	assert.Equal(t, "https://idp.example.com/default", doc["issuer"])

	// Every URL-bearing field is host-rooted at the external address (https, no port).
	for k, v := range doc {
		s, ok := v.(string)
		if !ok || !strings.HasPrefix(s, "http") {
			continue
		}
		assert.Truef(t, strings.HasPrefix(s, "https://idp.example.com/default"),
			"%s = %q must reflect the forwarded external address", k, s)
	}
}

// TestSlice6CORSDefaultOnPreflight proves the zero-config CORS default: a bare
// OPTIONS preflight (no Access-Control-Request-Method) to a protocol route
// reflects the Origin with credentials and answers 204, echoing the requested
// headers — the exact DoD acceptance shape.
func TestSlice6CORSDefaultOnPreflight(t *testing.T) {
	t.Parallel()

	handler := newSlice6App(t, "")

	req := httptest.NewRequest(http.MethodOptions, "/default/token", nil)
	req.Header.Set("Origin", "http://app.test")
	req.Header.Set("Access-Control-Request-Headers", "authorization")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.Bytes())
	assert.Equal(t, "http://app.test", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "authorization", rec.Header().Get("Access-Control-Allow-Headers"))
}

// TestSlice6StaticTraversalGuarded proves the wired /static tree serves a real
// asset but refuses a traversal escape with 404, over the full router stack.
func TestSlice6StaticTraversalGuarded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.css"), []byte("body{}"), 0o644))
	handler := newSlice6App(t, dir)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.css", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "a real asset serves")

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/../../etc/passwd", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code, "a traversal escape is refused")
}

// TestSlice6MetricLabelIsRouteTemplate proves request metrics label the chi route
// TEMPLATE (/{issuer}/token), never the client-controlled {issuer} value, so a
// hostile client cannot explode time-series cardinality.
func TestSlice6MetricLabelIsRouteTemplate(t *testing.T) {
	t.Parallel()

	handler := newSlice6App(t, "")

	// Drive a request through a templated protocol route; the body is irrelevant —
	// the metric records the matched route regardless of status.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/somerandomissuer/token", nil))

	// Scrape the co-located /metrics endpoint and inspect the label set.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	metrics := rec.Body.String()

	assert.Contains(t, metrics, `route="/{issuer}/token"`,
		"the metric label is the route template")
	assert.NotContains(t, metrics, `route="/somerandomissuer/token"`,
		"the raw client-controlled issuer must never become a label value")
}
