package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/meigma/mock-oidc/internal/adapter/http/middleware"
	"github.com/meigma/mock-oidc/internal/adapter/http/problem"
)

// TestTimeoutReturnsProblemJSON verifies an elapsed request deadline becomes an
// RFC 9457 504 when the handler returns without writing.
func TestTimeoutReturnsProblemJSON(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	handler := middleware.Timeout(time.Millisecond)(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/slow", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusGatewayTimeout, rec.Code)
	assert.Equal(t, problem.ContentType, rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), `"status":504`)
}
