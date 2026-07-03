---
title: Lock down the control plane
description: Restrict the /_mock control plane with a token, move it to a dedicated listener, or disable it entirely.
---

# Lock down the control plane

The `/_mock` control plane is **on by default** and **co-located on the API
listener** (`:8080`). Anyone who can reach the server can mint tokens, freeze the
clock, and read captured requests through it. This guide shows the three ways to
restrict it: require a token, move it to its own listener, or turn it off.

!!! warning "For testing only"
    Even locked down, `mock-oidc` mints signed tokens for arbitrary identities.
    None of these controls make it safe to expose; they exist so a shared test
    environment does not hand its control plane to every client on the network.

Every control response — including rejections — carries the header
`X-Mock-Oidc: testing-only`, so you can positively identify the control plane in
traffic captures regardless of which mode it runs in.

## Require a control token

Set a token at startup. `mock-oidc` then rejects any `/_mock` request that does
not present it in the `X-Mock-Control-Token` header (compared in constant time).

```sh
./bin/mock-oidc serve --control-token s3cr3t
# equivalently: MOCK_OIDC_CONTROL_TOKEN=s3cr3t ./bin/mock-oidc serve
```

A call with no token — or the wrong one — gets `401` with an RFC 9457 body:

```sh
curl -sS -i http://localhost:8080/_mock/clock
#   => HTTP/1.1 401 Unauthorized
#   => Content-Type: application/problem+json
#   => X-Mock-Oidc: testing-only
#   => {"title":"Unauthorized","status":401,"detail":"missing or invalid control token"}
```

Present the header and the request succeeds:

```sh
curl -sS http://localhost:8080/_mock/clock \
  -H 'X-Mock-Control-Token: s3cr3t'
#   => {"frozen":false,"now":"2026-07-03T12:00:00Z"}
```

The gate applies to the whole `/_mock` surface (mint, scenarios, requests,
clock, reset) and works the same whether the plane is co-located or on a
dedicated listener. Leaving `--control-token` empty (the default) disables the
gate entirely.

## Move it to a dedicated listener

To keep `/_mock` off the public API surface, bind it to its own address with
`--control-addr`. The plane then leaves the API listener completely.

```sh
./bin/mock-oidc serve --control-addr :8090
# equivalently: MOCK_OIDC_CONTROL_ADDR=:8090 ./bin/mock-oidc serve
```

`/_mock` now answers only on the control address; the API listener 404s it:

```sh
curl -sS -o /dev/null -w '%{http_code}\n' http://localhost:8080/_mock/clock
#   => 404

curl -sS http://localhost:8090/_mock/clock
#   => {"frozen":false,"now":"2026-07-03T12:00:00Z"}
```

Combine this with `--control-token` to also require the header on the dedicated
listener.

!!! note
    The control address must differ from `--addr` and `--metrics-addr`; a
    collision fails startup. The dedicated control listener carries **no
    request-recording middleware** of its own. Protocol traffic is still
    recorded on the API listener, so `/_mock/requests` and
    `/_mock/requests/take` keep working — the control listener simply never
    records its own `/_mock` calls.

## Disable it entirely

To serve only the public OIDC protocol and no control plane at all, turn it off:

```sh
./bin/mock-oidc serve --control-enabled=false
# equivalently: MOCK_OIDC_CONTROL_ENABLED=false ./bin/mock-oidc serve
```

Every `/_mock` path now 404s, with or without a token:

```sh
curl -sS -o /dev/null -w '%{http_code}\n' http://localhost:8080/_mock/clock
#   => 404
```

With the control plane off there is no direct token minting, no scenario queue,
no clock steering, and no request capture — the server behaves purely as an
authorization server driven through its normal `authorize`/`token` endpoints.

## See also

- [Configuration reference](../reference/configuration.md) — every flag and
  `MOCK_OIDC_*` variable, including `control-enabled`, `control-token`, and
  `control-addr`.
- [Control plane reference](../reference/control-plane.md) — the full `/_mock`
  endpoint catalog these controls gate.
