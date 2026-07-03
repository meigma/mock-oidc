---
title: Capture and assert requests
description: Assert on the exact requests a client sent, using the /_mock request-capture API.
---

# Capture and assert requests

`mock-oidc` records every inbound protocol request. Use the `/_mock`
request-capture API to pull a request back out and assert that your client sent
exactly what you expected — down to the raw bytes.

Two ways to read the log:

- **Take** (`POST /_mock/requests/take`) — a destructive FIFO long-poll. Blocks
  until a matching request arrives (or times out), then removes and returns it.
  This is the one to use from a test.
- **List** (`GET /_mock/requests`) — a non-destructive snapshot of everything
  recorded so far.

All commands assume the default control plane, co-located on `:8080`.

## Take the next matching request

`POST /_mock/requests/take` with the issuer and endpoint you want. `endpoint` is
one of `authorize`, `token`, `userinfo`, `introspect`, `revoke`, `endsession`,
`jwks`. `timeoutMs` is how long to block waiting for a match:

```sh
curl -sS -X POST http://localhost:8080/_mock/requests/take \
  -H 'Content-Type: application/json' \
  -d '{"timeoutMs": 2000, "issuer": "default", "endpoint": "token"}'
```

It returns a single `CapturedRequest` and removes it from the log (FIFO — you
get the oldest unmatched request first):

```json
{
  "id": "…",
  "receivedAt": "2026-07-03T12:00:00Z",
  "issuer": "default",
  "method": "POST",
  "path": "/default/token",
  "url": "http://localhost:8080/default/token",
  "query": {},
  "headers": { "Content-Type": ["application/x-www-form-urlencoded"] },
  "bodyBase64": "Z3JhbnRfdHlwZT1jbGllbnRfY3JlZGVudGlhbHMm…",
  "body": "grant_type=client_credentials&client_id=my-app&scope=read+write"
}
```

See the [control-plane reference](../reference/control-plane.md) for every
`CapturedRequest` field.

!!! note "A timeout is a clean miss, not an error"
    If no matching request arrives within `timeoutMs`, `take` returns **404** —
    a clean empty result, not a failure. Treat 404 as "nothing captured yet",
    not as an error to retry blindly.

## Assert on the exact bytes

`body` is a best-effort UTF-8 decode, convenient for eyeballing. For assertions,
prefer **`bodyBase64`**: it is the raw request body verbatim, so it preserves
parameter **order**, **duplicate keys**, and `+` / `%` encoding exactly as the
client sent them. Decode it and compare:

```sh
curl -sS -X POST http://localhost:8080/_mock/requests/take \
  -H 'Content-Type: application/json' \
  -d '{"timeoutMs": 2000, "issuer": "default", "endpoint": "token"}' \
  | jq -r .bodyBase64 | base64 -d
#   => grant_type=client_credentials&client_id=my-app&scope=read+write
```

Because these are raw bytes, `scope=read+write` stays literal — the `+` is not
folded into a space, and a form that sends `scope` twice keeps both copies in
order. That is the difference that lets you assert on wire format, not just on a
parsed map.

## Worked example: assert a token request body

Drive a request with a body you control, then take it and check it.

1. Make the request (here, a `client_credentials` token grant):

    ```sh
    curl -sS -X POST http://localhost:8080/default/token \
      -H 'Content-Type: application/x-www-form-urlencoded' \
      -d 'grant_type=client_credentials&client_id=my-app&scope=read+write'
    ```

2. Take it back and decode the body:

    ```sh
    BODY=$(curl -sS -X POST http://localhost:8080/_mock/requests/take \
      -H 'Content-Type: application/json' \
      -d '{"timeoutMs": 2000, "issuer": "default", "endpoint": "token"}' \
      | jq -r .bodyBase64 | base64 -d)

    [ "$BODY" = 'grant_type=client_credentials&client_id=my-app&scope=read+write' ] \
      && echo PASS || echo "FAIL: $BODY"
    #   => PASS
    ```

The token endpoint also captures `query` and `headers`, so you can assert on the
client-auth header, `Content-Type`, or any query parameter the same way.

## Peek without consuming (list, then clear)

To inspect what has been recorded without removing anything, use the
non-destructive list. Both filters are optional; omit them to see everything:

```sh
curl -sS 'http://localhost:8080/_mock/requests?issuer=default&endpoint=token'
#   => { "count": 1, "requests": [ { … CapturedRequest … } ] }
```

Clear the whole log between test cases:

```sh
curl -sS -X DELETE http://localhost:8080/_mock/requests
#   => { "cleared": true }
```

!!! tip "Choose take vs. list per test shape"
    Use `take` when a test expects exactly one request and should block for it.
    Use `GET` + `DELETE` when you want to snapshot several requests at once and
    reset the log around a test.

## What is never captured

The recorder logs inbound protocol traffic only. It never records the control
plane or the operational surface, so your assertions stay free of your own
test-harness noise. These are always excluded:

- `/_mock/*` (the control plane itself)
- `/healthz`, `/readyz`, `/isalive`
- `/metrics`
- `/openapi*`, `/docs`
- `/favicon.ico`

This self-isolation means a `take` or `GET /_mock/requests` will only ever
return requests your client made against an issuer's OIDC endpoints — polling
the control plane to read the log does not pollute it.

!!! note "Dedicated control-listener mode"
    If you run the control plane on its own address, the API listener carries no
    request-recording middleware and this API records nothing. Request capture
    is available in the default co-located mode. See
    [Lock down the control plane](lock-down-the-control-plane.md).
