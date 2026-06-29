// Package logctx carries a request-scoped [slog.Logger] on a [context.Context].
//
// It is a dependency-free leaf (standard library only) so the domain can resolve
// the per-request logger without importing transport or observability packages,
// keeping the hexagonal dependency arrows pointing inward.
package logctx

import (
	"context"
	"log/slog"
)

// contextKey is a private type for the context key defined in this package.
type contextKey int

const loggerKey contextKey = iota

// WithLogger returns a copy of ctx carrying logger, retrievable with [From].
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// From returns the logger stored in ctx by [WithLogger]. The boolean reports
// whether a logger was present.
func From(ctx context.Context) (*slog.Logger, bool) {
	logger, ok := ctx.Value(loggerKey).(*slog.Logger)

	return logger, ok
}
