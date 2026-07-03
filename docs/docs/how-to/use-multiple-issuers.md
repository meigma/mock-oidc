---
title: Use multiple issuers
description: Make one running mock-oidc server behave as several independent identity providers.
---

# Use multiple issuers

One server is many identity providers. Every namespace under a single path
segment — `http://localhost:8080/{issuer}/...` — is an independent issuer with
its own signing key and its own discovery document. There is no registration
step: an issuer exists the moment you touch it. Use this to test tenant
isolation, multi-IdP federation, or a token-audience matrix from a single
container.

All examples below use base `http://localhost:8080` and two issuers, `acme` and
`beta`.

## Materialize an issuer by touching it

Hit any issuer-scoped endpoint with a name you choose. The issuer springs into
existence with a lazily generated signing key:

```bash
curl -s http://localhost:8080/acme/.well-known/openid-configuration
#   => "issuer":"http://localhost:8080/acme",
#      "authorization_endpoint":"http://localhost:8080/acme/authorize",
#      "token_endpoint":"http://localhost:8080/acme/token", ...  (all under /acme/)
```

Do the same for `beta` — no config, no restart:

```bash
curl -s http://localhost:8080/beta/.well-known/openid-configuration
#   => "issuer":"http://localhost:8080/beta", ...  (all under /beta/)
```

Each issuer's signing key uses `kid == <issuer id>`. Fetch the two JWKS and the
key sets are distinct:

```bash
curl -s http://localhost:8080/acme/jwks
#   => {"keys":[{"kty":"RSA","kid":"acme","use":"sig","alg":"RS256", ...}]}

curl -s http://localhost:8080/beta/jwks
#   => {"keys":[{"kty":"RSA","kid":"beta","use":"sig","alg":"RS256", ...}]}
```

!!! note
    JWKS exposes public key members only (no `d`, `p`, `q`, ...). The `kid`
    always equals the issuer id, so a verifier can key its trust off the issuer
    name alone.

## Prove keys are isolated

Each issuer is a self-contained trust domain: a token signed by one is
worthless to another. Mint an access token under `acme` (see the
[control plane reference](../reference/control-plane.md) for the full
`/_mock/mint` body):

```bash
ACME_TOKEN=$(curl -s http://localhost:8080/_mock/mint \
  -H 'content-type: application/json' \
  -d '{"issuer":"acme","subject":"alice","audience":["acme-api"],"clientId":"web","kind":"access_token"}' \
  | jq -r .token)
```

It verifies at `acme`'s userinfo:

```bash
curl -s http://localhost:8080/acme/userinfo -H "Authorization: Bearer $ACME_TOKEN"
#   => 200  {"sub":"alice","aud":["acme-api"], ...}
```

The **same** token is rejected by `beta`, whose JWKS advertises only `kid=beta`:

```bash
curl -si http://localhost:8080/beta/userinfo -H "Authorization: Bearer $ACME_TOKEN"
#   => HTTP/1.1 401 Unauthorized
#      WWW-Authenticate: Bearer error="invalid_token"
#      {"error":"invalid_token"}
```

A minted token is byte-identical to a granted one, so the same isolation holds
for tokens obtained through `/{issuer}/token` or the authorization-code flow: a
token minted or granted under one issuer will not pass `userinfo` or
`introspect` under another.

## Pre-seed issuer-specific behavior

To give each issuer its own default claims, audience, or `typ` before any
traffic arrives, use config `tokenCallbacks`. Each entry is keyed by `issuer`
and applies to grants for that issuer only; the first matching entry wins.

```json
{
  "tokenCallbacks": [
    {
      "issuer": "acme",
      "audience": ["acme-api"],
      "claims": { "tenant": "acme", "roles": ["admin"] }
    },
    {
      "issuer": "beta",
      "audience": ["beta-api"],
      "claims": { "tenant": "beta" }
    }
  ]
}
```

Start the server with that file (for example `JSON_CONFIG_PATH=./config.json`;
see the [configuration reference](../reference/configuration.md) for the full
shape and precedence), then a plain grant against each issuer carries its own
seeded claims:

```bash
ACME_AT=$(curl -s http://localhost:8080/acme/token \
  -d grant_type=client_credentials -d client_id=web | jq -r .access_token)

curl -s http://localhost:8080/acme/userinfo -H "Authorization: Bearer $ACME_AT"
#   => {"sub":"web","aud":["acme-api"],"tenant":"acme","roles":["admin"], ...}
```

A `beta` grant instead reports `"tenant":"beta"` and `aud:["beta-api"]`.

!!! tip
    A `tokenCallbacks` entry is the same shape as a `/_mock/scenarios` body, so
    you can seed a durable default in config and still enqueue a one-shot
    override at runtime for the same issuer. Issuers that are **not** named in
    the config still work — they materialize on first touch with the built-in
    default callback.

## Constraints you will hit

**Issuer ids are a single path segment.** No `/` is allowed. A request to
`/tenants/acme/authorize` treats `tenants` as the issuer and `acme/authorize`
as a path beneath it — it does not create a nested `tenants/acme` issuer.
Nested, multi-segment (Azure-style) issuers are a deliberate, documented parity
gap; see [Issuers and advertised identity](../explanation/issuers-and-identity.md)
for why and what to do instead.

**`_mock` is reserved.** It is the control plane, so you cannot use it as an
issuer id — issuer-scoped routes under that prefix return `404` with the OAuth2
error `not_found`, and `/_mock/mint` rejects a reserved-prefix `issuer`:

```bash
curl -si http://localhost:8080/_mock/.well-known/openid-configuration
#   => HTTP/1.1 404 Not Found
#      {"error":"not_found", ...}
```

!!! warning "FOR TESTING ONLY"
    Materialize-on-touch means any string becomes a trusted issuer with a valid
    signing key. That is exactly what makes this useful for tests and exactly
    why it must never front production traffic.
