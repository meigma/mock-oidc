package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/meigma/template-go-api/internal/adapter/http/problem"
)

// Timeout returns middleware that bounds request processing to d. If the deadline
// elapses before the handler responds, it writes an RFC 9457 504 response. Like
// chi's middleware.Timeout, it relies on handlers honoring the request context: a
// handler that ignores cancellation and writes its own response makes the 504 a
// no-op.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer func() {
				cancel()
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					problem.Write(w, http.StatusGatewayTimeout, "the request timed out")
				}
			}()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
