---
title: Simulate expiry and time
description: Use the /_mock clock to freeze, advance, and reset server time so time-dependent token behavior is deterministic.
---

# Simulate expiry and time

The server runs on a single logical clock that drives **both** token issuance and
token verification. Freeze it and every new token's `iat`/`nbf`/`exp` is pinned to
that instant; advance it and a token minted earlier slides past its `exp` and starts
failing verification. That one clock is what makes expiry tests deterministic — no
`sleep` calls, no flaky wall-clock races.

All clock operations live on the `/_mock` control plane, co-located on the `:8080`
API listener by default. If you have configured a control token, add
`-H "X-Mock-Control-Token: <token>"` to every request below.

!!! warning "FOR TESTING ONLY"
    The `/_mock` clock rewrites the server's notion of time for _all_ issuers and
    _all_ callers at once. Never expose it outside a test environment.

## Read the current clock

```bash
curl -s http://localhost:8080/_mock/clock
#   => {"frozen":false,"now":"2026-07-03T14:20:05Z"}
```

`frozen:false` means the clock tracks real wall time; `now` is the instant the
server would stamp into a token issued right now.

## Freeze the clock at an instant

Send `frozen:true` with the `instant` you want (RFC 3339). `instant` is required
whenever `frozen` is true.

```bash
curl -s -X PUT http://localhost:8080/_mock/clock \
  -H 'Content-Type: application/json' \
  -d '{"frozen":true,"instant":"2030-01-01T00:00:00Z"}'
#   => {"frozen":true,"now":"2030-01-01T00:00:00Z"}
```

Now mint a token and observe that its timestamps are pinned to the frozen instant —
not to real time:

```bash
curl -s -X POST http://localhost:8080/_mock/mint \
  -H 'Content-Type: application/json' \
  -d '{"issuer":"default","subject":"alice","kind":"access_token"}'
#   => {
#        "token": "eyJ...",
#        "kid": "default",
#        "algorithm": "RS256",
#        "issuer": "http://localhost:8080/default",
#        "expiresAt": "2030-01-01T01:00:00Z",
#        "claims": { "sub":"alice", "iat":1893456000, "nbf":1893456000, "exp":1893459600, ... }
#      }
```

`iat` and `nbf` are pinned to `2030-01-01T00:00:00Z` and, with the default 3600s
lifetime, `exp` lands exactly one hour later. Everything you issue while frozen
shares that instant, so timestamps across a whole test are reproducible.

## Advance time to expire a live token

`POST /_mock/clock/advance` freezes the clock (if it is not already) and moves it
forward by a Go duration string (`90s`, `5m`, `2h`, `1h1m`). Use it to push an
already-issued token past its `exp` without waiting.

Mint (or grant) a token with the default one-hour lifetime, then jump two hours
ahead:

```bash
# Capture a live token
TOKEN=$(curl -s -X POST http://localhost:8080/_mock/mint \
  -H 'Content-Type: application/json' \
  -d '{"issuer":"default","subject":"alice","kind":"access_token"}' | jq -r .token)

# Move the clock past its exp
curl -s -X POST http://localhost:8080/_mock/clock/advance \
  -H 'Content-Type: application/json' \
  -d '{"duration":"2h"}'
#   => {"frozen":true,"now":"...T16:20:05Z"}
```

Because the same clock now governs verification, that token reads as expired
everywhere:

```bash
# Introspection flips to inactive (still HTTP 200, not an error)
curl -s -X POST http://localhost:8080/default/introspect \
  -u any:any \
  --data-urlencode "token=$TOKEN"
#   => {"active":false}

# userinfo rejects it
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/default/userinfo
#   => 401
```

The 401 carries `WWW-Authenticate: Bearer error="invalid_token"` and a body of
`{"error":"invalid_token"}`.

!!! note
    `introspect` requires _any_ non-empty `Authorization` header — the `-u any:any`
    above just satisfies that; the credentials themselves are never validated. See
    the [control-plane reference](../reference/control-plane.md) for the full
    request contracts.

## Unfreeze the clock

Return to real wall time by clearing `frozen`. No `instant` is needed.

```bash
curl -s -X PUT http://localhost:8080/_mock/clock \
  -H 'Content-Type: application/json' \
  -d '{"frozen":false}'
#   => {"frozen":false,"now":"2026-07-03T14:20:07Z"}
```

## Reset unfreezes as part of cleanup

If a test may leave the clock frozen, you do not have to unfreeze it explicitly.
`POST /_mock/reset` clears the scenario queue and the request log **and** unfreezes
the clock in one call — a good teardown hook. Signing keys are preserved, so any
JWKS a client already fetched keeps verifying.

```bash
curl -s -X POST http://localhost:8080/_mock/reset
#   => clock unfrozen, scenario queue and request log cleared, signing keys kept
```

## Set a token's own lifetime with expirySeconds

Advancing the clock moves _everyone_ forward. When you instead want a specific token
to be short-lived while global time keeps running, set its own lifetime with
`expirySeconds`. The field is accepted in the same shape everywhere a token is
produced:

- `POST /_mock/mint` — `{"issuer":"default","subject":"alice","kind":"access_token","expirySeconds":60}`
- `POST /_mock/scenarios` — the one-shot callback body takes `expirySeconds`
- config `tokenCallbacks[]` — a pre-seeded callback entry takes `expirySeconds`

```bash
curl -s -X POST http://localhost:8080/_mock/mint \
  -H 'Content-Type: application/json' \
  -d '{"issuer":"default","subject":"alice","kind":"access_token","expirySeconds":60}'
#   => expiresAt is now + 60s instead of the default now + 3600s
```

Combine the two for fully deterministic expiry: freeze, mint with a short
`expirySeconds`, then `advance` just past it. See
[Tokens and claims](../reference/tokens-and-claims.md) for how `iat`/`nbf`/`exp` are
derived.

## Freeze at boot with systemTime

To start the server already frozen — for example, to pin a golden test fixture — set
`tokenProvider.systemTime` (RFC 3339) in the JSON config. The clock boots frozen at
that instant; you can still advance or unfreeze it later via `/_mock`.

```json
{
  "tokenProvider": {
    "systemTime": "2030-01-01T00:00:00Z"
  }
}
```

Load it with any of the config sources (`JSON_CONFIG`, `JSON_CONFIG_PATH`, or
`./config.json`). Every token minted before you touch the clock will carry the
`2030-01-01T00:00:00Z` timestamps shown above.
