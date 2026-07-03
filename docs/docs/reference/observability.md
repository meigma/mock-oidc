---
title: Observability
description: Reference for the health, readiness, metrics, tracing, and API-documentation endpoints.
---

# Observability

The health, metrics, tracing, and API-documentation surfaces sit outside every
issuer namespace. Health, readiness, and the API-documentation routes are served
on the API listener (`--addr`, default `:8080`); metrics are served on a
dedicated listener by default. The flags named on this page are documented in
full in [Configuration](configuration.md).

## Health and status

| Route | Response body | Kind |
| --- | --- | --- |
| `GET /isalive` | `{"status":"ok"}` | Liveness. Upstream-parity alias of `/healthz`. |
| `GET /healthz` | `{"status":"ok"}` | Liveness. |
| `GET /readyz` | `{"status":"ready","checks":{}}` | Readiness. |

The server is DB-less and holds no external dependencies, so `/readyz` is always
ready and its `checks` object is always empty.

```bash
curl -s http://localhost:8080/healthz
#   => {"status":"ok"}

curl -s http://localhost:8080/readyz
#   => {"status":"ready","checks":{}}
```

## Metrics

`GET /metrics` serves Prometheus exposition format.

| Property | Value |
| --- | --- |
| Route | `GET /metrics` |
| Format | Prometheus text exposition |
| Listener | Dedicated listener, default `:9090` (`--metrics-addr`) |

When `--metrics-addr` is empty, `/metrics` is served on the API listener
(`--addr`) instead of on a separate listener.

```bash
curl -s http://localhost:9090/metrics
```

!!! note
    The container publishes only `8080` by default. Add `-p 9090:9090` to reach
    the metrics listener:
    `docker run --rm -p 8080:8080 -p 9090:9090 ghcr.io/meigma/mock-oidc`.

!!! note
    The metrics listener always serves plain HTTP. TLS terminates on the API
    listener only and does not apply to `/metrics`. See
    [Serve over TLS](../how-to/serve-over-tls.md).

## Tracing

Tracing is opt-in and off by default. It requires an external OTLP collector.

| Property | Value |
| --- | --- |
| Enable flag | `--tracing-enabled` (default `false`) |
| Export protocol | OTLP/HTTP |
| Configuration | Standard `OTEL_*` environment variables |
| `service.name` default | `mock-oidc` |
| `service.version` default | Build version |
| Shutdown | Pending spans are flushed on graceful shutdown |

`--tracing-enabled` is the only mock-oidc-specific tracing setting; everything
else is configured through the standard OpenTelemetry environment variables.

| Environment variable | Effect |
| --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Base endpoint of the OTLP/HTTP collector spans are exported to. |
| `OTEL_SERVICE_NAME` | Overrides the `service.name` resource attribute (default `mock-oidc`). |
| `OTEL_TRACES_SAMPLER` | Selects the trace sampler. |
| `OTEL_TRACES_SAMPLER_ARG` | Argument passed to the selected sampler. |
| `OTEL_RESOURCE_ATTRIBUTES` | Additional resource attributes as comma-separated `key=value` pairs. |

Inbound HTTP requests are recorded as `otelhttp` server spans. W3C trace context
is extracted from the incoming request headers, so spans join an upstream trace
when the caller propagates one. The infrastructure routes `/isalive`,
`/healthz`, `/readyz`, and `/metrics` are excluded from span creation.

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
OTEL_SERVICE_NAME=mock-oidc \
OTEL_TRACES_SAMPLER=parentbased_traceidratio \
OTEL_TRACES_SAMPLER_ARG=1.0 \
./bin/mock-oidc serve --tracing-enabled
```

## API documentation

Huma serves the interactive and machine-readable API documentation on the API
listener.

| Route | Content |
| --- | --- |
| `GET /docs` | Interactive API documentation (Stoplight UI). |
| `GET /openapi.json` | OpenAPI 3.0.3 document, JSON. |
| `GET /openapi.yaml` | OpenAPI 3.0.3 document, YAML. |

The rendered [API Reference](../api.md) is generated from the same OpenAPI
document, which the [`openapi` subcommand](cli.md) also writes to a file or to
standard output.

!!! note
    The health, metrics, and API-documentation routes are never captured by the
    request recorder. See [Control plane](control-plane.md) for the full list of
    excluded paths.
