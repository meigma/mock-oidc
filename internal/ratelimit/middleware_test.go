package ratelimit_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"

	"github.com/meigma/template-go-api/internal/ratelimit"
)

// testClientHeader names the header the test key function reads to identify the
// client, so a test can simulate distinct clients without distinct IPs.
const testClientHeader = "X-Test-Client"

// keyByHeader keys requests by the testClientHeader value, falling back to a
// shared key when the header is absent.
func keyByHeader(ctx huma.Context) (string, error) {
	if c := ctx.Header(testClientHeader); c != "" {
		return c, nil
	}

	return "default", nil
}

// newTestAPI installs the rate-limit middleware over limiter on a humatest API
// and registers a single GET /ping operation behind it.
func newTestAPI(t *testing.T, limiter ratelimit.Limiter, enabled bool) humatest.TestAPI {
	t.Helper()

	_, api := humatest.New(t)
	logger := slog.New(slog.DiscardHandler)
	ratelimit.NewMiddleware(api, limiter, keyByHeader, logger, enabled).Install()

	huma.Register(api, huma.Operation{
		OperationID: "ping",
		Method:      http.MethodGet,
		Path:        "/ping",
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api
}

func TestMiddlewareAllowsBurstThenReturns429(t *testing.T) {
	t.Parallel()

	limiter := ratelimit.NewInMemory(rate.Limit(1), 2, time.Minute)
	t.Cleanup(limiter.Stop)
	api := newTestAPI(t, limiter, true)

	// The burst of 2 passes.
	assert.Equal(t, http.StatusNoContent, api.Get("/ping").Code)
	assert.Equal(t, http.StatusNoContent, api.Get("/ping").Code)

	// The third request within the same second is throttled.
	resp := api.Get("/ping")
	assert.Equal(t, http.StatusTooManyRequests, resp.Code)
	assert.NotEmpty(t, resp.Header().Get("Retry-After"), "a 429 carries a Retry-After hint")
	assert.Contains(t, resp.Body.String(), "rate limit exceeded", "the body is the RFC 9457 problem detail")
}

func TestMiddlewareLimitsPerClientKey(t *testing.T) {
	t.Parallel()

	limiter := ratelimit.NewInMemory(rate.Limit(1), 1, time.Minute)
	t.Cleanup(limiter.Stop)
	api := newTestAPI(t, limiter, true)

	// alice exhausts her own bucket.
	assert.Equal(t, http.StatusNoContent, api.Get("/ping", testClientHeader+": alice").Code)
	assert.Equal(t, http.StatusTooManyRequests, api.Get("/ping", testClientHeader+": alice").Code)

	// bob is unaffected: he has an independent bucket.
	assert.Equal(t, http.StatusNoContent, api.Get("/ping", testClientHeader+": bob").Code)
}

func TestMiddlewareDisabledIsPassthrough(t *testing.T) {
	t.Parallel()

	// A burst of 1 would throttle the second request if the middleware ran, but
	// disabled it must never install, so every request reaches the handler.
	limiter := ratelimit.NewInMemory(rate.Limit(1), 1, time.Minute)
	t.Cleanup(limiter.Stop)
	api := newTestAPI(t, limiter, false)

	for range 3 {
		assert.Equal(t, http.StatusNoContent, api.Get("/ping").Code)
	}
}
