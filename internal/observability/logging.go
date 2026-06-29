// Package observability provides the cross-cutting concerns shared across the
// server: structured logging (slog), request-scoped logging middleware, and
// Prometheus metrics. None of it uses global state, so dependencies are injected.
package observability

import (
	"io"
	"log/slog"
	"strings"
)

// NewLogger constructs a [slog.Logger] writing to w at the given level. A format
// of "text" selects the text handler; any other value selects JSON.
func NewLogger(w io.Writer, level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if strings.EqualFold(format, "text") {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	return slog.New(handler)
}

// ParseLevel maps a textual level (debug, info, warn, error) to a [slog.Level],
// defaulting to info for empty or unrecognized values.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
