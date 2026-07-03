---
title: Migrate from mock-oauth2-server
description: Switch a navikt/mock-oauth2-server test setup to mock-oidc with minimal changes.
---

# Migrate from mock-oauth2-server

Move a test suite off `navikt/mock-oauth2-server` and onto mock-oidc while
keeping your existing environment variables, JSON config, and issuer URLs
working. mock-oidc targets intent-parity with upstream, so most setups migrate
by swapping how the server runs — not by rewriting your tests.

## Run the container in place of the embedded server

The core change is operational: mock-oauth2-server runs as an in-process JVM
test library (`MockOAuth2Server`), while mock-oidc runs as a standalone
container or binary. Start it and point your client at it exactly as before:

```sh
docker run --rm -p 8080:8080 ghcr.io/meigma/mock-oidc
curl -sS http://localhost:8080/default/.well-known/openid-configuration
#   => { "issuer": "http://localhost:8080/default", ... }
```

Issuers still materialize on first touch, so no registration step is needed —
hitting `/{issuer}/...` for any id creates it with a lazily generated signing
key.

## Keep your environment variables

mock-oidc honors the upstream environment variables **unprefixed**, in addition
to its own `MOCK_OIDC_*` variables. Leave your existing container/env wiring in
place:

| Upstream variable | Effect in mock-oidc |
|---|---|
| `SERVER_HOSTNAME` | Listen host |
| `SERVER_PORT` | Listen port |
| `PORT` | Listen port (fallback) |
| `JSON_CONFIG` | Inline JSON config string |
| `JSON_CONFIG_PATH` | Path to a JSON config file |
| `LOG_LEVEL` | Log level (`debug`\|`info`\|`warn`\|`error`) |
| `LOGBACK_CONFIG` | Accepted and ignored (no-op) |

The listen address resolves by precedence, highest first:

```text
--addr (MOCK_OIDC_ADDR)  >  SERVER_HOSTNAME / SERVER_PORT  >  PORT  >  :8080
```

## Keep your JSON config

The JSON config shape is upstream-compatible and **unknown keys are silently
ignored**, so most `config.json` files load unchanged. These keys carry over:

- `interactiveLogin`
- `tokenCallbacks[]`, including `requestMappings[]`
- `staticAssetsPath`
- `httpServer.ssl` (in-process self-signed localhost cert)
- `tokenProvider` (`systemTime`, `keyProvider`), `rotateRefreshToken`

Load it the same way you already do — inline or by path:

```sh
# By path (mounted into the container)
docker run --rm -p 8080:8080 \
  -e JSON_CONFIG_PATH=/config.json \
  -v "$(pwd)/config.json:/config.json:ro" \
  ghcr.io/meigma/mock-oidc

# Or inline
docker run --rm -p 8080:8080 -e JSON_CONFIG="$(cat config.json)" ghcr.io/meigma/mock-oidc
```

A typical upstream callback config works as-is:

```json
{
  "interactiveLogin": true,
  "tokenCallbacks": [
    {
      "issuer": "default",
      "subject": "alice",
      "audience": ["my-api"],
      "claims": { "acr": "Level4" },
      "requestMappings": [
        { "param": "scope", "match": "admin", "claims": { "role": "admin" } }
      ]
    }
  ]
}
```

See [Configuration](../reference/configuration.md) for the full key list and
precedence rules.

## Replace embedded-API calls with the control plane

The upstream embedded library API has no in-process equivalent — mock-oidc is
container-first. The `/_mock` control plane is its replacement: drive it over
HTTP from your test harness instead of calling JVM methods.

| Upstream (embedded) | mock-oidc (`/_mock`) |
|---|---|
| `enqueueCallback(...)` | `POST /_mock/scenarios` |
| `takeRequest()` | `POST /_mock/requests/take` |
| direct token issue | `POST /_mock/mint` |

Enqueue a one-shot, issuer-matched callback — the body is the same shape as a
`tokenCallbacks` entry, and it alters only the next matching token:

```sh
curl -sS -X POST http://localhost:8080/_mock/scenarios \
  -H 'content-type: application/json' \
  -d '{"issuer":"default","subject":"alice","claims":{"role":"admin"}}'
#   => {"scenarioId":"...","queueDepth":1}
```

Take the next recorded request for an endpoint (destructive FIFO long-poll):

```sh
curl -sS -X POST http://localhost:8080/_mock/requests/take \
  -H 'content-type: application/json' \
  -d '{"issuer":"default","endpoint":"token","timeoutMs":2000}'
#   => {"id":"...","method":"POST","path":"/default/token", ...}
#   404 on timeout is a clean miss, not an error.
```

Mint a token directly — byte-identical to a granted one, so it verifies against
`/{issuer}/jwks` and is accepted at `/userinfo`:

```sh
curl -sS -X POST http://localhost:8080/_mock/mint \
  -H 'content-type: application/json' \
  -d '{"issuer":"default","subject":"alice","audience":["my-api"],"kind":"access_token"}'
#   => {"token":"eyJ...","kid":"default","algorithm":"RS256", ...}
```

See [Control plane (`/_mock`)](../reference/control-plane.md) for every field.

## What's different — check these

Most tests migrate untouched, but review these before you run:

- **Path-param routing.** Issuers are matched as a single route parameter at
  `/{issuer}/...`, not by suffix. `http://localhost:8080/default/token` behaves
  as before; assertions that depended on suffix-style URL construction should be
  checked.
- **Single-segment issuers only.** An issuer id may not contain `/`. Nested,
  Azure-style multi-segment issuers (e.g. `tenant/v2.0`) are a named, documented
  parity gap and are unsupported by design. The `_mock` prefix is reserved.
- **Corrected upstream quirks may break brittle assertions.** mock-oidc fixes
  defects rather than copying them:
    - OAuth2 error codes keep correct case (e.g. `invalid_request`, not
      lowercased variants).
    - `form_post` without `state` is tolerated (no 500).
    - No 302→400 status coercion on protocol errors.
    - `at+jwt` access tokens self-verify (`userinfo` 200, `introspect`
      `active:true`).
    - The login page has no Google Fonts / Raleway network dependency (inline
      CSS), so it renders offline.

!!! warning "Intentionally not provided"
    The in-process **embedded library API** (use the container + `/_mock`
    instead) and **arbitrary raw-response injection** are deliberately absent.
    Tests that reached into either will not port directly.

## Related

- [Parity with mock-oauth2-server](../explanation/parity.md) — the full
  philosophy and the complete list of corrected and unreproduced behaviors.
- [Configuration](../reference/configuration.md) — every config key, env
  alias, and precedence rule.
