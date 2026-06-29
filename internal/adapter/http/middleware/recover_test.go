package middleware_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meigma/template-go-api/internal/adapter/http/middleware"
	"github.com/meigma/template-go-api/internal/adapter/http/problem"
)

// TestRecovererReturnsProblemJSON verifies a panic becomes an RFC 9457 500.
func TestRecovererReturnsProblemJSON(t *testing.T) {
	t.Parallel()

	discard := slog.New(slog.DiscardHandler)
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	handler := middleware.Recoverer(discard)(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, problem.ContentType, rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), `"status":500`)
}
