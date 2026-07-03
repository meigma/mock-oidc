---
title: Get tokens for every grant
description: Copy-pasteable curl recipes to mint a token via each of mock-oidc's six OAuth2 grants.
---

# Get tokens for every grant

Every grant is a form-encoded `POST` to `http://localhost:8080/{issuer}/token`.
These recipes use the zero-config `default` issuer; swap `default` for any id and
it materializes on first touch. Each curl uses `-d` (which sends
`application/x-www-form-urlencoded`).

!!! warning "Testing only — secrets are never validated"
    `mock-oidc` accepts any `client_secret` (or none) for every grant and never
    checks it. Passwords, assertion signatures, and subject-token signatures are
    not verified either. This is deliberate; the server must never front real
    traffic.

At a glance, this is what each grant hands back:

| Grant | Tokens returned | Default `sub` |
|-------|-----------------|---------------|
| `client_credentials` | access | `client_id` |
| `password` | id + access | `username` |
| `authorization_code` | id + access + refresh | login user / configured / random UUID |
| `refresh_token` | access (+ id if a nonce was cached) | same as original |
| `jwt-bearer` | access | assertion's `sub` |
| `token-exchange` | access | subject token's `sub` |

For the full claim rules (`aud` precedence, `azp`/`tid`, `typ`) see
[Tokens and claims](../reference/tokens-and-claims.md).

## `client_credentials`

```sh
curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=client_credentials \
  -d client_id=orders-service \
  -d client_secret=unchecked \
  -d scope=api://orders
#   => { "token_type":"Bearer", "access_token":"eyJ...", "expires_in":3600,
#   =>   "scope":"api://orders" }
```

Returns an **access token only** — no `id_token`, no `refresh_token`. `sub`
defaults to `client_id`. The non-OIDC `scope` value becomes the access token's
`aud`.

## `password` (ROPC)

```sh
curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=password \
  -d username=alice \
  -d password=anything \
  -d scope=openid
#   => { "token_type":"Bearer", "access_token":"eyJ...", "id_token":"eyJ...",
#   =>   "expires_in":3600, "scope":"openid" }
```

Returns an **id token + access token, but no refresh token**. `sub == username`,
and **any password is accepted**.

## `refresh_token`

There is no direct way to request a refresh token — only the
`authorization_code` grant issues one. Get a code, exchange it, then redeem the
refresh token it returns.

First mint a code. With `interactiveLogin` off (the default) a bare
`GET /authorize` auto-issues one via redirect:

```sh
curl -sS -i "http://localhost:8080/default/authorize?response_type=code&client_id=web-app&redirect_uri=http://localhost:3000/callback&scope=openid&state=xyz"
#   => HTTP/1.1 302 Found
#   => Location: http://localhost:3000/callback?code=THE_CODE&state=xyz
```

Exchange the code for the token set (this is where the refresh token comes from):

```sh
curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=authorization_code \
  -d code=THE_CODE \
  -d client_id=web-app \
  -d redirect_uri=http://localhost:3000/callback
#   => { "access_token":"eyJ...", "id_token":"eyJ...", "refresh_token":"THE_REFRESH", ... }
```

Now redeem the refresh token as often as you need:

```sh
curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=refresh_token \
  -d refresh_token=THE_REFRESH \
  -d client_id=web-app \
  -d scope=openid
#   => { "token_type":"Bearer", "access_token":"eyJ...", "expires_in":3600, "scope":"openid" }
```

Each redemption re-mints a fresh access token (new `jti`/`iat`/`exp`, same
`sub`). Rotation is **off by default**, so the same `refresh_token` keeps
working and no new one is returned. An `id_token` comes back only if the original
`/authorize` request carried a `nonce` (add `&nonce=...` above to get one on
every refresh).

An unknown token returns `invalid_grant`; redeeming a token minted by a
different issuer returns `invalid_grant` with `"different issuer"` in the
description.

## `authorization_code`

Shown as the first two steps of the refresh recipe above: obtain a code from
`/authorize`, then `POST` it to `/token` with `grant_type=authorization_code`.
It returns **id + access + refresh** tokens, and it is the only grant that adds
`azp == client_id`. The code is **single-use** and is burned even on a failed
PKCE check.

For the full browser round trip — interactive login, PKCE (`plain`/`S256`),
`response_mode`, and `nonce` — see
[Drive the authorization-code flow](drive-the-authorization-code-flow.md).

## `jwt-bearer`

The assertion JWT is **parsed, not signature-verified**, so a literal dummy
signature works. Build `base64url(header).base64url(payload).dummy` yourself:

```sh
# Base64url-encode stdin, no padding. Needs openssl (preinstalled on macOS and most Linux).
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

header=$(printf '%s' '{"alg":"RS256","typ":"JWT"}' | b64url)
payload=$(printf '%s' '{"sub":"svc-account","scope":"api://reports"}' | b64url)
assertion="$header.$payload.dummy"          # third segment is a literal dummy signature

curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer \
  --data-urlencode "assertion=$assertion" \
  -d scope=api://reports
#   => { "token_type":"Bearer", "access_token":"eyJ...", "expires_in":3600,
#   =>   "scope":"api://reports" }
```

Returns an **access token only**; `issued_token_type` is omitted. All assertion
claims are copied into the token, then `iss`/`exp`/`nbf`/`iat`/`jti`/`aud` are
re-stamped.

Scope resolves as request `scope` → the assertion's `scope` claim →
`invalid_request`. Drop `-d scope=...` above and the token picks up
`"scope":"api://reports"` from the assertion payload instead. A blank
`assertion` returns `invalid_request`.

## `token-exchange`

The `subject_token` is also parsed, not verified — reuse the `b64url` helper and
header from the previous recipe. **Client authentication is required**; the
simplest form is `client_id` + `client_secret` fields.

```sh
subject_payload=$(printf '%s' '{"sub":"alice","email":"alice@example.com"}' | b64url)
subject_token="$header.$subject_payload.dummy"   # $header reused from the jwt-bearer recipe

curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=urn:ietf:params:oauth:grant-type:token-exchange \
  --data-urlencode "subject_token=$subject_token" \
  -d subject_token_type=urn:ietf:params:oauth:token-type:access_token \
  -d client_id=exchange-client \
  -d client_secret=unchecked \
  -d audience=https://api.internal.example
#   => { "token_type":"Bearer", "access_token":"eyJ...", "expires_in":3600,
#   =>   "issued_token_type":"urn:ietf:params:oauth:token-type:access_token" }
```

Returns an **access token only**, with
`issued_token_type=urn:ietf:params:oauth:token-type:access_token` and **no
`scope` field**. The `audience` param sets the token's `aud` — but only when the
matched issuer has no configured callback audience (a configured audience wins).

!!! warning "Client auth is not optional here"
    Omit `client_id`/`client_secret` and the request fails with
    `invalid_request` mentioning `ClientAuthentication`. Only this grant enforces
    that a client authenticates (the secret is still discarded).

## grant_type errors

- A **blank** `grant_type` returns `invalid_request`.
- An **unknown** `grant_type` returns `invalid_grant`.

Both use the OAuth2 error envelope: `{"error":"...","error_description":"..."}`.

## Skip the flow entirely with /_mock/mint

When you just need a token and do not care which grant produced it, mint one
directly through the control plane. The result is byte-identical to a granted
token: it verifies against `/default/jwks` and is accepted at `/default/userinfo`.

```sh
curl -sS -X POST http://localhost:8080/_mock/mint \
  -H 'Content-Type: application/json' \
  -d '{"issuer":"default","subject":"alice","audience":["api://orders"],
       "scope":["openid"],"clientId":"web-app","kind":"access_token"}'
#   => { "token":"eyJ...", "kid":"default", "algorithm":"RS256", "issuer":"...", ... }
```

See [Control plane](../reference/control-plane.md) for the full `/_mock/mint`
body, plus scenarios that shape claims on the next real grant.

## Related

- [Tokens and claims](../reference/tokens-and-claims.md) — claim rules, `aud`
  precedence, and default token content
- [Drive the authorization-code flow](drive-the-authorization-code-flow.md) — the
  full interactive flow with PKCE
- [Control plane](../reference/control-plane.md) — `/_mock/mint` and scenarios
