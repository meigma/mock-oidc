---
title: Shape token claims
description: Control the subject, audience, claims, typ, and expiry that mock-oidc stamps into a minted token.
---

# Shape token claims

mock-oidc gives you three ways to control what a token carries, from most ad-hoc
to most declarative:

1. **Mint directly** — `POST /_mock/mint` builds one token from an explicit body.
2. **Override the next grant** — `POST /_mock/scenarios` rewrites the next token
   an issuer mints, then reverts.
3. **Seed at boot** — `tokenCallbacks` in the JSON config customises an issuer
   for the whole run.

All three accept `subject`, `audience`, `claims`, `typ`, and `expirySeconds`.
Pick the one that matches how permanent the change should be.

!!! warning "For testing only"
    Every endpoint below mints signed tokens for arbitrary identities. The
    `/_mock` control plane exists purely to script tests; never expose it to
    real traffic.

## Mint a one-off token directly

The fastest path when you just need a token with exact claims and don't care
that it never went through a grant. `POST /_mock/mint`:

```sh
curl -sS http://localhost:8080/_mock/mint \
  -H 'Content-Type: application/json' \
  -d '{
    "issuer": "default",
    "subject": "alice@example.com",
    "audience": ["my-api"],
    "scope": ["openid", "profile"],
    "clientId": "web-app",
    "kind": "access_token",
    "typ": "at+jwt",
    "claims": {"role": "admin", "tenant": "acme"},
    "expirySeconds": 300
  }'
#   => {"token":"eyJ...","kid":"default","algorithm":"RS256",
#       "issuer":"http://localhost:8080/default","expiresAt":"...","claims":{...}}
```

`kind` selects `access_token` vs `id_token`. The returned token is
byte-identical to one from a real grant: it verifies against the issuer's JWKS
and is accepted at `/userinfo`.

```sh
TOKEN=$(curl -sS http://localhost:8080/_mock/mint -H 'Content-Type: application/json' \
  -d '{"issuer":"default","subject":"alice","kind":"access_token","typ":"at+jwt"}' \
  | jq -r .token)
curl -sS http://localhost:8080/default/userinfo -H "Authorization: Bearer $TOKEN"
#   => 200 {"sub":"alice",...}
```

Reserved-prefix issuers (for example `_mock`) are rejected. `mint` is a
self-contained path — you supply every field in the body; it does not run the
grant machinery.

## Override the next token for an issuer

When you want the token to come out of a real grant (so the client's
`authorize` / `token` round trip is exercised), enqueue a **one-shot scenario**.
It rewrites the next token that issuer mints, then reverts automatically. The
body is the same shape as a config `tokenCallbacks` entry.

```sh
curl -sS http://localhost:8080/_mock/scenarios \
  -H 'Content-Type: application/json' \
  -d '{
    "issuer": "default",
    "subject": "bob@example.com",
    "audience": ["my-api"],
    "claims": {"role": "editor"},
    "typ": "at+jwt",
    "expirySeconds": 600
  }'
#   => {"scenarioId":"...","queueDepth":1}
```

The next grant against `default` now carries those values:

```sh
curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=client_credentials -d client_id=web-app -d scope=openid
#   => access_token with sub=bob@example.com, aud=[my-api], typ=at+jwt
```

The scenario is **single-use**: a second grant falls back to normal behaviour.
The `refresh_token` grant consults the same queue. Inspect or clear pending
scenarios:

```sh
curl -sS http://localhost:8080/_mock/scenarios
#   => {"queueDepth":1,"scenarios":[{"issuer":"default","kind":"default"}]}
curl -sS -X DELETE http://localhost:8080/_mock/scenarios
#   => {"queueDepth":0}
```

### Template claims from request parameters

To derive claims from the incoming request instead of hard-coding them, add
`requestMappings`. Each mapping tests one form `param`, and any `${key}` in a
string claim leaf is substituted with that request's form value.

```sh
curl -sS http://localhost:8080/_mock/scenarios \
  -H 'Content-Type: application/json' \
  -d '{
    "issuer": "default",
    "requestMappings": [
      {
        "param": "scope",
        "match": "*",
        "typeHeader": "at+jwt",
        "claims": {"sub": "${username}", "acr": "urn:level:${acr}"}
      }
    ]
  }'
```

A grant that supplies those params gets the substituted claims:

```sh
curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=password -d username=carol -d password=x -d scope=openid -d acr=high
#   => token with sub=carol, acr=urn:level:high, typ=at+jwt
```

Details that trip people up:

- `match` decides when the mapping fires against the value of `param`: `"*"`
  matches any value, an exact string matches equality, anything else is treated
  as a regex. An invalid regex silently never matches (no panic).
- Only **string** claim leaves are templated. Unknown `${keys}` stay literal.
- `client_id` / `clientId` can never be shadowed by a same-named form param.
- `typeHeader` sets the JWS `typ` for the mapping.
- A `requestMapping` callback adds neither `tid` nor `azp` (unlike the default
  callback).

## Seed claims at boot

For claims you always want on a given issuer, put a `tokenCallbacks` entry in the
JSON config rather than enqueuing at runtime. Each entry is the exact same shape
as a scenario body and applies for the whole run.

```sh
JSON_CONFIG='{
  "tokenCallbacks": [
    {
      "issuer": "default",
      "subject": "service-account",
      "audience": ["my-api"],
      "claims": {"role": "admin"},
      "typ": "at+jwt"
    }
  ]
}' ./bin/mock-oidc serve
```

Every token that issuer mints now defaults to those values until a scenario
overrides them. See [Configuration](../reference/configuration.md) for config
precedence (`JSON_CONFIG` > `JSON_CONFIG_PATH` > `./config.json`).

## Resolution priority

When more than one mechanism could apply to a grant, the token content resolves
in this order:

1. An enqueued one-shot **scenario** matching the issuer (head of the queue) —
   single-use, highest priority.
2. A config **`tokenCallbacks`** entry — first match by issuer.
3. The built-in **default** callback — supplies `iss`/`iat`/`exp`/`jti`, a
   random-UUID `sub` fallback, `tid`, and (on `authorization_code`) `azp`.

`POST /_mock/mint` is outside this chain: it builds the token straight from its
own body.

## Set the token type (`typ`)

`typ` defaults to `JWT`. Set `at+jwt` for RFC 9068 access tokens — they still
self-verify against this server:

```sh
TOKEN=$(curl -sS http://localhost:8080/_mock/mint -H 'Content-Type: application/json' \
  -d '{"issuer":"default","subject":"alice","kind":"access_token","typ":"at+jwt"}' \
  | jq -r .token)
curl -sS -o /dev/null -w '%{http_code}\n' \
  http://localhost:8080/default/userinfo -H "Authorization: Bearer $TOKEN"
#   => 200
```

!!! note
    A genuinely foreign `typ` (for example `foo+jwt`) is minted fine but does
    **not** self-verify here: `/userinfo` returns 401 and introspection reports
    `active:false`. `JWT` and `at+jwt` do self-verify.

## Control the audience (`aud`)

`aud` does not follow the fields above uniformly:

- An **`id_token`** `aud` is always `[client_id]` — `audience` in your body does
  not change it.
- An **`access_token`** `aud` follows a four-step precedence:
    1. A configured / scenario `audience` (an explicit empty list `[]` counts and
       wins).
    2. The token-exchange `audience` form param.
    3. Non-OIDC `scope` values (after stripping
       `openid`/`profile`/`email`/`address`/`phone`/`offline_access`).
    4. `["default"]` as a last resort.

So to pin an access token's `aud`, set `audience` in your mint / scenario /
callback body; to clear it, pass `"audience": []`.

For every default claim and the full `aud` rules, see
[Tokens and claims](../reference/tokens-and-claims.md). For the exact
request/response DTO shapes of `/_mock/mint` and `/_mock/scenarios`, see the
[Control plane reference](../reference/control-plane.md).
