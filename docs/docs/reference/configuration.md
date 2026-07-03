---
title: Configuration
description: The complete configuration reference — flags, environment variables, upstream aliases, JSON config, and CORS defaults.
---

# Configuration

This page enumerates every configuration input `mock-oidc` reads: command-line
flags, their `MOCK_OIDC_*` environment equivalents, the unprefixed upstream
environment aliases, the JSON configuration file, and the CORS defaults. It
describes what each input is and its default value. For the rationale behind the
defaults, see [The security model](../explanation/security-model.md); for the
`serve` invocation itself, see the [CLI reference](cli.md).

## Precedence

Each flag has three possible sources. The first that is set wins:

1. **Flag** — the command-line flag (for example `--addr`).
2. **Environment variable** — the flag name uppercased, `MOCK_OIDC_`-prefixed,
   with dashes converted to underscores (for example `MOCK_OIDC_ADDR`).
3. **Default** — the built-in default listed in the table below.

Every flag has a corresponding `MOCK_OIDC_*` environment variable formed by this
rule. There are no exceptions. Boolean flags accept the standard string values
(`true`/`false`); duration flags accept Go duration strings (for example `5s`,
`120s`, `1m`).

## Flags

| Flag | Env var | Default | Description |
| --- | --- | --- | --- |
| `--addr` | `MOCK_OIDC_ADDR` | *(empty)* | API listener `host:port`, serving the OIDC/OAuth2 endpoints, `/_mock`, and static assets. When set it overrides `--server-hostname`/`--server-port`; when empty the listen address is composed from them (see [Listen address](#listen-address)). |
| `--server-hostname` | `MOCK_OIDC_SERVER_HOSTNAME` | *(empty)* | Host portion of the listen address, composed with `--server-port` when `--addr` is empty. |
| `--server-port` | `MOCK_OIDC_SERVER_PORT` | `8080` | Port portion of the listen address, composed with `--server-hostname` when `--addr` is empty. |
| `--metrics-addr` | `MOCK_OIDC_METRICS_ADDR` | `:9090` | Dedicated Prometheus `/metrics` listener. When empty, `/metrics` is served on the `--addr` listener instead. |
| `--log-level` | `MOCK_OIDC_LOG_LEVEL` | `info` | Log level: one of `debug`, `info`, `warn`, `error`. |
| `--log-format` | `MOCK_OIDC_LOG_FORMAT` | `json` | Log output format: `json` or `text`. |
| `--read-timeout` | `MOCK_OIDC_READ_TIMEOUT` | `5s` | Maximum duration for reading an entire request, including the body. |
| `--read-header-timeout` | `MOCK_OIDC_READ_HEADER_TIMEOUT` | `5s` | Maximum duration for reading request headers. |
| `--write-timeout` | `MOCK_OIDC_WRITE_TIMEOUT` | `10s` | Maximum duration before timing out writes of the response. |
| `--idle-timeout` | `MOCK_OIDC_IDLE_TIMEOUT` | `120s` | Maximum time to wait for the next request on a keep-alive connection. |
| `--request-timeout` | `MOCK_OIDC_REQUEST_TIMEOUT` | `15s` | Per-request handler timeout. |
| `--shutdown-grace` | `MOCK_OIDC_SHUTDOWN_GRACE` | `15s` | Grace period for in-flight requests to complete during graceful shutdown. |
| `--cors-allowed-origins` | `MOCK_OIDC_CORS_ALLOWED_ORIGINS` | *(empty)* | Comma-separated origin allowlist. When empty, any request `Origin` is reflected. See [CORS](#cors). |
| `--trusted-proxy-header` | `MOCK_OIDC_TRUSTED_PROXY_HEADER` | *(empty)* | Header to read the client IP from (for example `X-Real-IP`). When empty, the TCP peer address is trusted. |
| `--tls-cert-file` | `MOCK_OIDC_TLS_CERT_FILE` | *(empty)* | Path to a TLS certificate file. Must be paired with `--tls-key-file`. |
| `--tls-key-file` | `MOCK_OIDC_TLS_KEY_FILE` | *(empty)* | Path to a TLS private key file. Must be paired with `--tls-cert-file`. |
| `--control-enabled` | `MOCK_OIDC_CONTROL_ENABLED` | `true` | Whether the `/_mock` control plane is mounted. When `false`, `/_mock` returns 404. |
| `--control-addr` | `MOCK_OIDC_CONTROL_ADDR` | *(empty)* | Dedicated `host:port` listener for the `/_mock` control plane. When empty, `/_mock` is co-located on the `--addr` listener; a dedicated listener carries no request-recording middleware and must differ from `--addr` and `--metrics-addr`. See [Lock down the control plane](../how-to/lock-down-the-control-plane.md). |
| `--control-token` | `MOCK_OIDC_CONTROL_TOKEN` | *(empty)* | Bearer token required in the `X-Mock-Control-Token` header on `/_mock`. When empty, the control plane is unauthenticated. |
| `--rate-limit-enabled` | `MOCK_OIDC_RATE_LIMIT_ENABLED` | `false` | Whether request rate limiting is applied. Off by default so test traffic is never throttled. |
| `--rate-limit-rps` | `MOCK_OIDC_RATE_LIMIT_RPS` | `10` | Sustained requests per second when rate limiting is enabled. |
| `--rate-limit-burst` | `MOCK_OIDC_RATE_LIMIT_BURST` | `20` | Burst allowance when rate limiting is enabled. |
| `--tracing-enabled` | `MOCK_OIDC_TRACING_ENABLED` | `false` | Whether OTLP trace export is enabled. Exporter and sampler are configured via the standard `OTEL_*` variables. |

!!! note
    TLS applies to the `--addr` listener only. The `--tls-*` flags and the JSON
    `httpServer.ssl` config are two independent ways to enable it; see
    [Serve over TLS](../how-to/serve-over-tls.md). The `--metrics-addr` listener
    and `/_mock` stay plain HTTP.

Behavior driven by these flags is documented in the how-to guides:
[running behind a proxy or in Docker](../how-to/run-behind-a-proxy-or-in-docker.md)
covers `--trusted-proxy-header`;
[locking down the control plane](../how-to/lock-down-the-control-plane.md) covers
`--control-enabled` and `--control-token`; and the
[observability reference](observability.md) covers the `OTEL_*` variables read
when `--tracing-enabled` is set.

## Upstream environment aliases

For drop-in compatibility with `mock-oauth2-server`, the following unprefixed
environment variables are also read. They exist alongside the `MOCK_OIDC_*`
variables above.

| Variable | Purpose |
| --- | --- |
| `SERVER_HOSTNAME` | Host portion of the listen address (composed with `SERVER_PORT`). |
| `SERVER_PORT` | Port portion of the listen address (composed with `SERVER_HOSTNAME`). |
| `PORT` | Listen port, used when `SERVER_HOSTNAME`/`SERVER_PORT` are not set. |
| `JSON_CONFIG` | Inline JSON configuration string. See [JSON configuration](#json-configuration). |
| `JSON_CONFIG_PATH` | Path to a JSON configuration file. |
| `LOG_LEVEL` | Log level, accepted as an alias for `--log-level`. |
| `LOGBACK_CONFIG` | Accepted for compatibility and ignored (no-op). |

### Listen address

The listen address is resolved from the first source that is set, in this order:

1. `--addr` (or `MOCK_OIDC_ADDR`)
2. `--server-hostname` + `--server-port` (fed by `SERVER_HOSTNAME` and `SERVER_PORT` > `PORT`)
3. `:8080`

## JSON configuration

A JSON configuration document pre-seeds issuers, keys, the clock, and TLS. The
configuration source is resolved from the first that is present:

1. `JSON_CONFIG` — an inline JSON string.
2. `JSON_CONFIG_PATH` — a path to a JSON file.
3. `./config.json` — a file in the working directory.
4. Built-in defaults — used when none of the above is present.

The document is compatible with the upstream `mock-oauth2-server` config format.
**Unknown keys are ignored.**

### Shape

```json
{
  "interactiveLogin": false,
  "rotateRefreshToken": false,
  "staticAssetsPath": "/srv/static",
  "tokenProvider": {
    "systemTime": "2026-07-03T00:00:00Z",
    "keyProvider": {
      "algorithm": "RS256",
      "initialKeys": []
    }
  },
  "tokenCallbacks": [
    {
      "issuer": "default",
      "subject": "alice",
      "audience": ["my-api"],
      "claims": { "roles": ["admin"] },
      "typ": "JWT",
      "expirySeconds": 3600,
      "requestMappings": [
        {
          "param": "client_id",
          "match": "*",
          "typeHeader": "JWT",
          "claims": { "tenant": "acme" }
        }
      ]
    }
  ],
  "httpServer": { "ssl": {} }
}
```

### Fields

`interactiveLogin` (boolean, default `false`)
:   When `true`, `GET /{issuer}/authorize` renders the login page instead of
    auto-issuing a code. See
    [Drive the authorization-code flow](../how-to/drive-the-authorization-code-flow.md).

`rotateRefreshToken` (boolean, default `false`)
:   When `true`, the `refresh_token` grant issues a new refresh token on each
    redemption. When `false`, the same refresh token keeps redeeming.

`staticAssetsPath` (string)
:   Filesystem directory mounted at `/static/*` on the API listener. Path
    traversal and symlink escapes are rejected with 404; there is no directory
    index.

`tokenProvider.systemTime` (string, RFC 3339)
:   Freezes the server clock at this instant. The single clock drives both token
    issuance and verification. See
    [Simulate expiry and time](../how-to/simulate-expiry-and-time.md).

`tokenProvider.keyProvider.algorithm` (string)
:   Default signing algorithm for lazily generated issuer keys. One of `RS256`,
    `RS384`, `RS512`, `PS256`, `PS384`, `PS512`, `ES256`, `ES384`. Defaults to
    `RS256`.

`tokenProvider.keyProvider.initialKeys` (array of JWK)
:   Signing keys to preload rather than generating lazily.

`tokenCallbacks` (array)
:   Per-issuer callbacks that pre-seed those issuers and shape their minted
    tokens. Each entry has the fields `issuer`, `subject`, `audience`,
    `claims`, `typ`, `expirySeconds`, and `requestMappings`. Each `requestMappings`
    entry has `param`, `match` (`"*"`, an exact string, or a regular
    expression), `typeHeader`, and `claims`. A `tokenCallbacks` entry is the
    **same shape as a `POST /_mock/scenarios` body**; see the
    [control-plane reference](control-plane.md) and
    [Shape token claims](../how-to/shape-token-claims.md).

`httpServer` (string or object)
:   A string form is accepted for upstream compatibility. The object form
    `{ "ssl": {} }` enables an in-process self-signed `localhost` certificate on
    the API listener. See [Serve over TLS](../how-to/serve-over-tls.md).

### Callback resolution order

When a token is minted, the callback applied is the first that matches, in this
order:

1. An enqueued one-shot scenario from `/_mock/scenarios` (issuer-matched, single
   use).
2. A `tokenCallbacks` entry (first match by issuer).
3. The built-in default callback.

The full set of default and derived claims is described in the
[tokens and claims reference](tokens-and-claims.md).

## CORS

CORS is **on by default**. With no allowlist configured
(`--cors-allowed-origins` empty), the server:

- Reflects any request `Origin` back in `Access-Control-Allow-Origin` verbatim.
- Sets `Access-Control-Allow-Credentials: true`.
- Answers preflight `OPTIONS` with 204, `Access-Control-Allow-Methods: POST, GET, OPTIONS`, and echoes the requested `Access-Control-Request-Headers`.

The `*` wildcard is **never** emitted; the specific origin is echoed instead.
Setting `--cors-allowed-origins` to a comma-separated list tightens reflection to
exactly those origins. The rationale for these defaults is in
[The security model](../explanation/security-model.md).
