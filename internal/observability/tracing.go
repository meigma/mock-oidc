package observability

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/trace"
)

// TracingConfig configures the OpenTelemetry tracer provider.
type TracingConfig struct {
	// Enabled is the master switch. When false, NewTracerProvider installs
	// nothing and returns a no-op shutdown.
	Enabled bool
	// ServiceName and ServiceVersion seed the resource's service.name and
	// service.version. They are defaults: the standard OTEL_SERVICE_NAME and
	// OTEL_RESOURCE_ATTRIBUTES environment variables override them.
	ServiceName    string
	ServiceVersion string
}

// NewTracerProvider configures OpenTelemetry tracing and registers it globally,
// returning a shutdown function that flushes buffered spans. When cfg.Enabled is
// false it installs nothing — the global no-op tracer provider stays in place —
// and returns a nil shutdown, so instrumentation such as otelhttp and otelpgx
// adds no overhead. Callers must nil-check the returned shutdown.
//
// The exporter is OTLP/HTTP, configured entirely from the standard OTEL_*
// environment variables (OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS,
// OTEL_TRACES_SAMPLER, and so on), so deployments tune tracing the OpenTelemetry
// way rather than through bespoke flags. The global propagator is set to W3C
// Trace Context + Baggage so trace context flows across services.
func NewTracerProvider(ctx context.Context, cfg TracingConfig) (func(context.Context) error, error) {
	if !cfg.Enabled {
		return nil, nil //nolint:nilnil // tracing disabled: there is no shutdown to run and no error.
	}

	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}

	// Only WithTelemetrySDK carries a schema URL; WithAttributes and WithFromEnv
	// are schemaless, so the merge cannot conflict. WithFromEnv is last so
	// OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES override the seeded defaults.
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("build trace resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return provider.Shutdown, nil
}

// TraceSpanNamer is router-agnostic Huma middleware that renames the active
// server span (created by otelhttp at the edge) to the matched operation's ID
// and tags it with the route template, giving low-cardinality, meaningful span
// names like "get-todo" instead of bare HTTP methods or high-cardinality paths.
// It is a no-op when no span is recording (tracing disabled), so it is safe to
// install unconditionally — though the composition root installs it only when
// tracing is enabled.
func TraceSpanNamer(ctx huma.Context, next func(huma.Context)) {
	if span := trace.SpanFromContext(ctx.Context()); span.IsRecording() {
		op := ctx.Operation()
		span.SetName(op.OperationID)
		span.SetAttributes(semconv.HTTPRoute(op.Path))
	}

	next(ctx)
}
