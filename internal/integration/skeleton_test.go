//go:build integration

package integration

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// imageTag is the image the R3 suite drives. It defaults to the local
// `mise run image-local` output (mock-oidc:dev) and can be overridden via
// MOCK_OIDC_IMAGE so the same suite runs against the published
// ghcr.io/meigma/mock-oidc artifact in Slice 6's R4 gate. If the image is
// absent on the Docker host each test skips loudly so a CI job without the
// pre-built image still passes.
//
//nolint:gochecknoglobals // resolved once from MOCK_OIDC_IMAGE; shared by every R3 test
var imageTag = resolveImageTag()

// resolveImageTag reads MOCK_OIDC_IMAGE, falling back to the local mock-oidc:dev
// tag produced by `mise run image-local`.
func resolveImageTag() string {
	if v := strings.TrimSpace(os.Getenv("MOCK_OIDC_IMAGE")); v != "" {
		return v
	}
	return "mock-oidc:dev"
}

// bannerMarker is the load-bearing fragment of the for-testing-only positioning
// banner (app.bootBanner). Container logs must carry it (C10).
const bannerMarker = "FOR TESTING ONLY"

// apiPort is the port the API (and its infra liveness/readiness routes) listens
// on with zero config.
const apiPort = "8080/tcp"

// metricsPort is the dedicated /metrics listener the skeleton exposes with zero
// config (config.defaultMetricsAddr = ":9090"); /metrics is kept off the API
// surface and its middleware chain.
const metricsPort = "9090/tcp"

// TestSkeleton is the trivial build/anchor test: it proves the integration
// package compiles and runs under the `integration` build tag even on a host
// without Docker, so `go test -tags integration ./internal/integration/...` never
// errors on an empty package.
func TestSkeleton(t *testing.T) {
	t.Parallel()
}

// TestContainerInfraRoutes is the Slice 0 R3 smoke test: it boots the shipped
// mock-oidc:dev image with zero config/env, then asserts the four infrastructure
// routes all return 200 and that the for-testing-only banner appears in the
// container logs. It skips loudly when the image is not present locally.
func TestContainerInfraRoutes(t *testing.T) {
	ctx := context.Background()

	skipIfImageMissing(ctx, t)

	req := testcontainers.ContainerRequest{
		Image:        imageTag,
		ExposedPorts: []string{apiPort, metricsPort},
		// The image is local-only (built by `mise run image-local`); never try to
		// pull it from a registry.
		AlwaysPullImage: false,
		WaitingFor: wait.ForHTTP("/isalive").
			WithPort(apiPort).
			WithStartupTimeout(60 * time.Second),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "boot %s with zero config", imageTag)
	t.Cleanup(func() {
		if terr := ctr.Terminate(context.Background()); terr != nil {
			t.Logf("terminate container: %v", terr)
		}
	})

	host, err := ctr.Host(ctx)
	require.NoError(t, err)

	// Infra liveness/readiness routes live on the API port.
	apiMapped, err := ctr.MappedPort(ctx, "8080")
	require.NoError(t, err)
	apiBase := "http://" + host + ":" + apiMapped.Port()
	for _, path := range []string{"/isalive", "/healthz", "/readyz"} {
		assert.Equalf(t, http.StatusOK, getStatus(ctx, t, apiBase+path),
			"GET %s should return 200 with zero config", path)
	}

	// /metrics is served on the dedicated metrics listener (:9090), off the API
	// surface and its middleware chain.
	metricsMapped, err := ctr.MappedPort(ctx, "9090")
	require.NoError(t, err)
	metricsBase := "http://" + host + ":" + metricsMapped.Port()
	assert.Equal(t, http.StatusOK, getStatus(ctx, t, metricsBase+"/metrics"),
		"GET /metrics should return 200 with zero config")

	logs, err := ctr.Logs(ctx)
	require.NoError(t, err)
	defer logs.Close()
	out, err := io.ReadAll(logs)
	require.NoError(t, err)
	assert.Contains(t, string(out), bannerMarker,
		"container logs must carry the for-testing-only positioning banner")
}

// skipIfImageMissing skips the test loudly when either the Docker daemon is
// unreachable or the mock-oidc:dev image is absent, so CI without the pre-built
// image (or without Docker) still passes.
func skipIfImageMissing(ctx context.Context, t *testing.T) {
	t.Helper()

	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		t.Skipf("SKIP: Docker unavailable (%v); run `mise run image-local` with Docker running", err)
	}
	defer provider.Close()

	if _, err := provider.Client().ImageInspect(ctx, imageTag); err != nil {
		t.Skipf("SKIP: image %q not on the Docker host (%v); build it with `mise run image-local`", imageTag, err)
	}
}

// getStatus issues a context-bound GET and returns the response status code.
func getStatus(ctx context.Context, t *testing.T, url string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode
}
