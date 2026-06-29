package observability

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestLoggerLogsRequestID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := NewLogger(&buf, slog.LevelInfo, "json")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scoped, ok := LoggerFrom(r.Context())
		assert.True(t, ok)
		assert.NotNil(t, scoped)
		w.WriteHeader(http.StatusTeapot)
	})
	handler := middleware.RequestID(RequestLogger(logger)(inner))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/things", nil)
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTeapot, rec.Code)

	logged := buf.String()
	assert.Contains(t, logged, "request_id")
	assert.Contains(t, logged, `"status":418`)
	assert.Contains(t, logged, "http request")
}
