package logctx_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meigma/mock-oidc/internal/logctx"
)

func TestWithLoggerRoundTrip(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	ctx := logctx.WithLogger(context.Background(), logger)

	got, ok := logctx.From(ctx)
	assert.True(t, ok)
	assert.Same(t, logger, got)
}

func TestFromAbsentReportsMiss(t *testing.T) {
	t.Parallel()

	got, ok := logctx.From(context.Background())
	assert.False(t, ok)
	assert.Nil(t, got)
}
