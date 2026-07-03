package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/httpapi"
)

// captureRecorder is a minimal oidc.RequestRecorder that accumulates captures.
type captureRecorder struct {
	mu   sync.Mutex
	recs []oidc.CapturedRequest
}

func (r *captureRecorder) Record(_ context.Context, c oidc.CapturedRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, c)
	return nil
}

func (r *captureRecorder) all() []oidc.CapturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]oidc.CapturedRequest(nil), r.recs...)
}

// TestRecordRequestsCapturesProtocolAndRestoresBody proves the middleware records
// an OIDC protocol request with its raw body intact AND leaves the body readable
// for the wrapped handler.
func TestRecordRequestsCapturesProtocolAndRestoresBody(t *testing.T) {
	t.Parallel()

	rec := &captureRecorder{}
	var handlerBody string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		handlerBody = string(b)
	})
	mw := httpapi.RecordRequests(rec)(inner)

	const body = "grant_type=client_credentials&scope=a&scope=b"
	req := httptest.NewRequest(http.MethodPost, "/default/token", strings.NewReader(body))
	mw.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, rec.all(), 1)
	got := rec.all()[0]
	assert.Equal(t, "POST", got.Method)
	assert.Equal(t, "/default/token", got.Path)
	assert.Equal(t, oidc.IssuerID("default"), got.Issuer)
	assert.Equal(t, body, string(got.Body), "raw body preserved verbatim (order intact)")
	assert.Equal(t, body, handlerBody, "handler still reads the intact body")
}

// TestRecordRequestsSkipsControlAndInfra proves the blacklist: the control plane
// and infra/utility routes are never recorded.
func TestRecordRequestsSkipsControlAndInfra(t *testing.T) {
	t.Parallel()

	skipped := []string{
		"/_mock/mint", "/_mock", "/healthz", "/readyz", "/metrics",
		"/openapi.yaml", "/docs", "/favicon.ico", "/",
	}
	for _, path := range skipped {
		rec := &captureRecorder{}
		mw := httpapi.RecordRequests(rec)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		mw.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		assert.Emptyf(t, rec.all(), "path %q must not be recorded", path)
	}
}
