package observability_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/meigma/mock-oidc/internal/observability"
)

func TestNewTracerProviderDisabled(t *testing.T) {
	t.Parallel()

	shutdown, err := observability.NewTracerProvider(
		context.Background(),
		observability.TracingConfig{Enabled: false},
	)
	require.NoError(t, err)
	assert.Nil(t, shutdown, "a disabled provider installs nothing and has no shutdown")
}

func TestNewTracerProviderEnabled(t *testing.T) {
	// Not parallel: this registers a global tracer provider and propagator, which
	// are restored on cleanup so other tests see the original globals.
	prevProvider := otel.GetTracerProvider()
	prevPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevProvider)
		otel.SetTextMapPropagator(prevPropagator)
	})

	shutdown, err := observability.NewTracerProvider(context.Background(), observability.TracingConfig{
		Enabled:        true,
		ServiceName:    "test-service",
		ServiceVersion: "1.2.3",
	})
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	// The OTLP/HTTP exporter connects lazily, so no collector is needed; shutdown
	// flushes the (empty) batch without error.
	assert.NoError(t, shutdown(context.Background()))
}

func TestTraceSpanNamerRenamesSpanToOperationID(t *testing.T) {
	t.Parallel()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	_, api := humatest.New(t)
	// Simulate the otelhttp server span: start a recording span and put it on the
	// request context before the namer runs.
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		spanCtx, span := provider.Tracer("test").Start(ctx.Context(), "HTTP")
		defer span.End()
		next(huma.WithContext(ctx, spanCtx))
	})
	api.UseMiddleware(observability.TraceSpanNamer)

	huma.Register(api, huma.Operation{
		OperationID: "get-thing",
		Method:      http.MethodGet,
		Path:        "/things/{id}",
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	api.Get("/things/42")

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "get-thing", spans[0].Name, "the active span is renamed to the operation id")
}
