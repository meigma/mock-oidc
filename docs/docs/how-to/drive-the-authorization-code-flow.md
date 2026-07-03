---
title: Drive the authorization-code flow
description: Run the authorization-code flow and its variations — auto-issue, interactive login, PKCE, response modes, state, and nonce.
---

# Drive the authorization-code flow

This guide runs the authorization-code flow against a running server and covers
the variations you actually hit: getting a code with or without a login page,
adding PKCE, choosing a response mode, and pinning `state` and `nonce`. Examples
use the zero-config `default` issuer at `http://localhost:8080`.

For the shape of the tokens you get back, see
[Tokens and claims](../reference/tokens-and-claims.md). For how the advertised
issuer URL is derived, see
[Issuers and advertised identity](../explanation/issuers-and-identity.md).

## Get a code without a login page (the default)

`interactiveLogin` is off by default, so a bare `GET /authorize` **auto-issues**
a code and returns a `302` — there is no login page to click through. Read the
code straight off the `Location` header with `curl -i`:

```sh
curl -i "http://localhost:8080/default/authorize?\
response_type=code&\
client_id=test-client&\
redirect_uri=http://localhost:3000/callback&\
scope=openid&\
state=xyz"
#   => HTTP/1.1 302 Found
#   => Location: http://localhost:3000/callback?code=<code>&state=xyz
```

To script it, capture the headers and pull the `code` out of `Location`:

```sh
code=$(curl -s -o /dev/null -D - "http://localhost:8080/default/authorize?\
response_type=code&client_id=test-client&\
redirect_uri=http://localhost:3000/callback&scope=openid&state=xyz" \
  | grep -i '^location:' | sed -E 's/.*[?&]code=([^&[:space:]]+).*/\1/')
```

## Exchange the code for tokens

`POST` the code to the token endpoint with the `authorization_code` grant. The
code is **single-use**; redeeming it (or a failed PKCE attempt — see below)
burns it, so a retry needs a fresh `authorize` call.

```sh
curl -s http://localhost:8080/default/token \
  -d grant_type=authorization_code \
  -d code="$code" \
  -d redirect_uri=http://localhost:3000/callback \
  -d client_id=test-client
#   => {"token_type":"Bearer","access_token":"...","id_token":"...",
#   =>  "refresh_token":"...","expires_in":3600}
```

This is the only grant that returns an `id_token`, an `access_token`, and a
`refresh_token` together. For the other grants, see
[Get tokens for every grant](get-tokens-for-every-grant.md).

## Use the interactive login page

Render a login form instead of auto-issuing when you want to choose the identity
per request. There are two ways to turn it on:

- **Per request:** add `prompt=login` (also `consent` or `select_account`) to
  the `authorize` query. No config change needed.
- **Always:** seed `interactiveLogin: true` so every `GET /authorize` shows the
  form.

```json
{ "interactiveLogin": true }
```

A `GET /authorize` that triggers the form returns `200` with an HTML page
instead of a `302`. Submit the identity by `POST`ing back to the **same**
`authorize` URL, query preserved. `username` is required and becomes the token
`sub`; `claims` is an optional JSON object merged into the token:

```sh
curl -i "http://localhost:8080/default/authorize?\
response_type=code&client_id=test-client&\
redirect_uri=http://localhost:3000/callback&scope=openid&state=xyz&prompt=login" \
  --data-urlencode 'username=alice' \
  --data-urlencode 'claims={"acr":"Level4","groups":["admin"]}'
#   => HTTP/1.1 302 Found
#   => Location: http://localhost:3000/callback?code=<code>&state=xyz
```

A missing `username` returns `400` with `error: invalid_request`.

## Add PKCE

PKCE is optional and supports both `S256` and `plain`. Generate a verifier and
its `S256` challenge (`base64url(SHA-256(verifier))`) with one line:

```sh
verifier=$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')
challenge=$(printf '%s' "$verifier" | openssl dgst -binary -sha256 \
  | openssl base64 | tr '+/' '-_' | tr -d '=')
```

Send `code_challenge` and `code_challenge_method=S256` on `authorize`:

```sh
curl -si "http://localhost:8080/default/authorize?\
response_type=code&client_id=test-client&\
redirect_uri=http://localhost:3000/callback&scope=openid&state=xyz&\
code_challenge=$challenge&code_challenge_method=S256" \
  | grep -i '^location:'
```

Then send the matching `code_verifier` on the token exchange:

```sh
curl -s http://localhost:8080/default/token \
  -d grant_type=authorization_code \
  -d code="$code" \
  -d code_verifier="$verifier" \
  -d redirect_uri=http://localhost:3000/callback \
  -d client_id=test-client
```

Notes on the branches:

- `plain` is also supported, and an omitted `code_challenge_method` **defaults
  to `plain`**.
- A challenge without a verifier (or a verifier without a challenge) returns
  `invalid_grant`.
- A mismatch returns `invalid_grant` with `invalid_pkce` in the description —
  and the code is **burned even on that failed attempt**, so re-run `authorize`
  to get a fresh code.

## Choose a response mode

Add `response_mode` to the `authorize` request to control how the code comes
back. All three are supported:

- **`query`** (default): `302` to `redirect_uri?code=<code>&state=<state>`.
- **`fragment`**: `302` to `redirect_uri#code=<code>&state=<state>`.
- **`form_post`**: `200` with a **self-submitting HTML page** that `POST`s
  `code` and `state` to `redirect_uri`. In a browser it auto-submits; with
  `curl` you receive the HTML form body rather than a `Location` header.

```sh
curl -si "http://localhost:8080/default/authorize?\
response_type=code&client_id=test-client&\
redirect_uri=http://localhost:3000/callback&scope=openid&state=xyz&\
response_mode=fragment" | grep -i '^location:'
#   => location: http://localhost:3000/callback#code=<code>&state=xyz
```

## Echo state and pin a nonce

`state` is echoed back **verbatim** in the redirect (and omitted entirely when
empty) — use it for CSRF checks and to correlate the callback with the request.

`nonce` is different: pass it on `authorize` and it is **cached server-side**
into the code record, then stamped into the `id_token` at exchange. A token
request cannot supply or forge a `nonce` — it only comes from the original
authorize call.

```sh
curl -si "http://localhost:8080/default/authorize?\
response_type=code&client_id=test-client&\
redirect_uri=http://localhost:3000/callback&scope=openid&\
state=xyz&nonce=n-0S6_WzA2Mj" | grep -i '^location:'
```

After exchanging the resulting code, the `id_token` carries `nonce: n-0S6_WzA2Mj`.

## Explore it in a browser

For a click-through round trip, point a browser at the built-in playground:

```text
http://localhost:8080/default/debugger
```

It ships a prefilled form, runs a real authorization-code + PKCE exchange, and
renders the decoded tokens — handy for eyeballing claims without wiring up curl.

!!! note "Two constraints to know"
    - **`redirect_uri` is never validated.** Any value is accepted and captured
      verbatim; there is no allowlist to register.
    - **Only `response_type=code` dispatches.** `none`, `id_token`, and `token`
      appear in discovery for compatibility but return an error if requested.
