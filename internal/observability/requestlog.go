package observability

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/meigma/mock-oidc/internal/logctx"
)

// LoggerFrom returns the request-scoped logger stored in ctx by RequestLogger.
// The boolean reports whether a logger was present. It delegates to
// [logctx.From], the dependency-free leaf that owns the context key.
func LoggerFrom(ctx context.Context) (*slog.Logger, bool) {
	return logctx.From(ctx)
}

// RequestLogger returns middleware that derives a request-scoped child logger
// carrying the chi request id, stores it in the request context, and logs one
// structured line per request after the handler returns.
func RequestLogger(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			logger := base.With(slog.String("request_id", middleware.GetReqID(r.Context())))
			ctx := logctx.WithLogger(r.Context(), logger)
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(wrapped, r.WithContext(ctx))

			// The access log records request metadata only — never request
			// headers or bodies. Credential-bearing headers (Authorization,
			// X-API-Key) are therefore redacted by construction: they are never
			// read here. Keep it that way when extending this line, so API keys
			// and bearer tokens cannot leak into logs.
			logger.LogAttrs(ctx, slog.LevelInfo, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("client_ip", middleware.GetClientIP(r.Context())),
				slog.Int("status", wrapped.Status()),
				slog.Int("bytes", wrapped.BytesWritten()),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}
