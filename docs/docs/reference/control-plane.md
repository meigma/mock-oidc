---
title: Control plane (/_mock)
description: Endpoint and DTO reference for the /_mock control plane — mint, scenarios, request capture, clock, and reset.
---

# Control plane (/_mock)

The `/_mock` control plane is the out-of-band API for driving `mock-oidc` from a
test: minting tokens, enqueueing one-shot scenarios, reading captured requests,
and controlling the clock. All request and response bodies are JSON.

- **Location** — co-located on the API listener (`:8080` by default). A
  dedicated-listener mode moves it to a separate control address.
- **Enabled** — on by default. When disabled, every `/_mock/*` route returns
  `404`.
- **Authentication** — an optional `X-Mock-Control-Token` header gate, active
  only when a control token is configured.
- **Response header** — every control-plane response carries
  `X-Mock-Oidc: testing-only`.

!!! warning "For testing only"
    The control plane mints and rewrites tokens for arbitrary identities with no
    authentication beyond the optional token gate. It must never be exposed on a
    production surface. See [The security model](../explanation/security-model.md).

## Common behavior

| Aspect | Value |
| --- | --- |
| Base path | `/_mock` |
| Body format | `application/json` |
| Listener | API listener (`:8080`) by default; a configured control address moves it to a dedicated listener |
| Enabled by default | Yes (`--control-enabled`, default `true`) |
| Disabled behavior | All `/_mock/*` routes return `404` |
| Auth gate | `X-Mock-Control-Token` header, required only when `--control-token` is set; compared in constant time |
| Response header | `X-Mock-Oidc: testing-only` on every response |
| Error format | RFC 9457 `application/problem+json` |

!!! note "Dedicated-listener mode disables request capture"
    When the control plane runs on its own address, the API listener carries no
    request-recording middleware and the [request-capture](#request-capture)
    endpoints record nothing. Request capture is available only in the default
    co-located mode. See
    [Lock down the control plane](../how-to/lock-down-the-control-plane.md).

The `--control-enabled` and `--control-token` settings are documented in the
[configuration reference](configuration.md).

## Mint

Mints a signed token directly, bypassing any grant. The result is byte-identical
to a granted token: it verifies against `/{issuer}/jwks` and is accepted at
`/{issuer}/userinfo`. Issuers with the reserved `_mock` prefix are rejected.

### `POST /_mock/mint`

**Request**

| Field | Type | Description |
| --- | --- | --- |
| `issuer` | string | Issuer id the token is minted under. Its signing key `kid` equals this id. Reserved-prefix issuers are rejected. |
| `issuerUrl` | string (optional) | Explicit issuer URL to stamp as the `iss` claim, overriding the per-request derived value. |
| `subject` | string | Value of the `sub` claim. |
| `audience` | string[] | Value of the `aud` claim. |
| `scope` | string[] | Scopes carried by the token. |
| `clientId` | string | Client id associated with the token. |
| `kind` | string | `access_token` or `id_token`. |
| `typ` | string | JWS `typ` header (for example `JWT` or `at+jwt`). |
| `claims` | object | Additional claims merged into the token. |
| `expirySeconds` | number | Token lifetime in seconds. |

**Response**

| Field | Type | Description |
| --- | --- | --- |
| `token` | string | The signed compact JWS. |
| `kid` | string | Signing key id (equals the issuer id). |
| `algorithm` | string | Signing algorithm (for example `RS256`). |
| `issuer` | string | Issuer the token was minted under. |
| `expiresAt` | string | Expiry timestamp. |
| `claims` | object | The full claim set of the minted token. |

```json
{
  "issuer": "default",
  "subject": "alice",
  "audience": ["my-api"],
  "scope": ["read"],
  "clientId": "my-app",
  "kind": "access_token",
  "typ": "at+jwt",
  "claims": { "role": "admin" },
  "expirySeconds": 3600
}
```

Claim shaping is covered in [Shape token claims](../how-to/shape-token-claims.md);
claim semantics are in [Tokens and claims](tokens-and-claims.md).

## Scenarios

Scenarios enqueue one-shot, issuer-matched token callbacks. A scenario alters
the next matching token for its issuer only, then reverts (single-use). The
`refresh_token` grant consults the same queue. A scenario body has the same
shape as a config `tokenCallbacks` entry (see the
[configuration reference](configuration.md)).

### `POST /_mock/scenarios`

**Request**

| Field | Type | Description |
| --- | --- | --- |
| `issuer` | string | Issuer the scenario matches. |
| `subject` | string | Overrides the `sub` claim. |
| `audience` | string[] | Overrides the `aud` claim (an explicit empty list is honored). |
| `claims` | object | Claims merged into the matched token. |
| `typ` | string | JWS `typ` header for the matched token. |
| `expirySeconds` | number | Token lifetime in seconds. |
| `requestMappings` | object[] | Per-request conditional overrides (see below). |

Each `requestMappings` entry:

| Field | Type | Description |
| --- | --- | --- |
| `param` | string | Request parameter to match on. |
| `match` | string | `*` (any), an exact value, or a regular expression. |
| `typeHeader` | string | JWS `typ` header applied when the mapping matches. |
| `claims` | object | Claims applied when the mapping matches. |

**Response**

| Field | Type | Description |
| --- | --- | --- |
| `scenarioId` | string | Identifier of the enqueued scenario. |
| `queueDepth` | number | Number of scenarios in the queue after enqueue. |

### `GET /_mock/scenarios`

Lists the queued scenarios non-destructively.

| Field | Type | Description |
| --- | --- | --- |
| `queueDepth` | number | Number of scenarios currently queued. |
| `scenarios` | object[] | One entry per queued scenario: `{ issuer, kind }`. |

Each `scenarios` entry:

| Field | Type | Description |
| --- | --- | --- |
| `issuer` | string | Issuer the scenario matches. |
| `kind` | string | `default` or `requestMapping`. |

### `DELETE /_mock/scenarios`

Clears the scenario queue.

| Field | Type | Description |
| --- | --- | --- |
| `queueDepth` | number | Always `0`. |

## Request capture

`mock-oidc` records every inbound protocol request except the routes on the
[capture blacklist](#capture-blacklist). These endpoints read the log.

### `POST /_mock/requests/take`

A destructive FIFO long-poll. Blocks until a request matches the filter (or the
timeout elapses), then removes and returns the oldest match. On timeout it
returns `404` — a clean miss, not an error.

**Request**

| Field | Type | Description |
| --- | --- | --- |
| `timeoutMs` | number | Maximum time to block waiting for a match, in milliseconds. |
| `issuer` | string | Issuer to match. |
| `endpoint` | string | Endpoint to match (see the [endpoint enum](#endpoint-enum)). |

**Response** — a single [`CapturedRequest`](#capturedrequest), or `404` on timeout.

### `GET /_mock/requests`

A non-destructive snapshot. Both query parameters are optional; omitting them
returns every recorded request.

| Query parameter | Description |
| --- | --- |
| `issuer` | Filter to a single issuer. |
| `endpoint` | Filter to a single [endpoint](#endpoint-enum). |

**Response**

| Field | Type | Description |
| --- | --- | --- |
| `count` | number | Number of requests returned. |
| `requests` | object[] | An array of [`CapturedRequest`](#capturedrequest). |

### `DELETE /_mock/requests`

Clears the request log.

| Field | Type | Description |
| --- | --- | --- |
| `cleared` | boolean | Always `true`. |

### CapturedRequest

| Field | Type | Description |
| --- | --- | --- |
| `id` | string | Unique id of the captured request. |
| `receivedAt` | string | Timestamp the request was received. |
| `issuer` | string | Issuer the request targeted. |
| `method` | string | HTTP method. |
| `path` | string | Request path. |
| `url` | string | Full request URL. |
| `query` | object | Query parameters (name to values). |
| `headers` | object | Request headers (name to values). |
| `bodyBase64` | string | Exact raw request body bytes, base64-encoded — preserves order, duplicate keys, and `+` / `%` encoding. |
| `body` | string | Best-effort UTF-8 decode of the body, for convenience. |

### Endpoint enum

The `endpoint` field of `take` and the `endpoint` query parameter of `GET
/_mock/requests` accept exactly these values:

| Value | Protocol endpoint |
| --- | --- |
| `authorize` | `GET`/`POST /{issuer}/authorize` |
| `token` | `POST /{issuer}/token` |
| `userinfo` | `GET /{issuer}/userinfo` |
| `introspect` | `POST /{issuer}/introspect` |
| `revoke` | `POST /{issuer}/revoke` |
| `endsession` | `GET`/`POST /{issuer}/endsession` |
| `jwks` | `GET /{issuer}/jwks` |

### Capture blacklist

The recorder never captures the control plane or the operational surface. These
routes are always excluded:

- `/_mock/*`
- `/healthz`, `/readyz`, `/isalive`
- `/metrics`
- `/openapi*`, `/docs`
- `/favicon.ico`

Request-capture usage is covered in
[Capture and assert requests](../how-to/capture-and-assert-requests.md).

## Clock

A single clock drives both token issuance and verification. Freezing or
advancing it can make a previously-valid token introspect `active:false` and
fail at `userinfo`.

### `GET /_mock/clock`

Reports the current clock state.

| Field | Type | Description |
| --- | --- | --- |
| `frozen` | boolean | Whether the clock is frozen. |
| `now` | string | The clock's current instant. |

### `PUT /_mock/clock`

Sets the clock state.

| Field | Type | Description |
| --- | --- | --- |
| `frozen` | boolean | Whether to freeze the clock. |
| `instant` | string (optional) | The instant to freeze at. Required when `frozen` is `true`. |

### `POST /_mock/clock/advance`

Freezes the clock, then advances it by a duration.

| Field | Type | Description |
| --- | --- | --- |
| `duration` | string | A Go duration string, for example `90s`, `5m`, or `1h1m`. |

Time-simulation usage is covered in
[Simulate expiry and time](../how-to/simulate-expiry-and-time.md).

## Reset

### `POST /_mock/reset`

Clears the scenario queue and the request log and unfreezes the clock. Signing
keys are preserved, so already-fetched JWKS still verifies. The request body is
empty.

| Field | Type | Description |
| --- | --- | --- |
| — | — | Returns an empty success response. |

The full request and response schemas are also published in the
[API Reference](../api.md).
