---
title: Tokens and claims
description: Reference for the token response shape, default claims, audience resolution, and signing behavior.
---

# Tokens and claims

This page describes the content of the tokens `mock-oidc` mints: the token
response JSON, which grants yield which tokens, the default registered claims,
how the subject and audiences are resolved, the JWS `typ` header, and the signing
algorithms and key identifier. It documents defaults exactly as the server
produces them. For the procedures to change any of this, see
[Shape token claims](../how-to/shape-token-claims.md).

## Token response

`POST /{issuer}/token` returns a JSON object on success. Every field except
`token_type`, `access_token`, and `expires_in` is emitted only when present
(`omitempty`).

| Field | Type | Presence | Description |
| --- | --- | --- | --- |
| `token_type` | string | always | Always the literal `"Bearer"`. |
| `access_token` | string | always | The signed JWS access token. |
| `expires_in` | number | always | Access-token lifetime in seconds (default `3600`). |
| `id_token` | string | grant-dependent | The signed JWS ID token. |
| `refresh_token` | string | grant-dependent | Refresh token; issued only by the `authorization_code` grant. |
| `scope` | string | grant-dependent | Space-delimited granted scope. |
| `issued_token_type` | string | token-exchange only | `urn:ietf:params:oauth:token-type:access_token`. |

```json
{
  "token_type": "Bearer",
  "access_token": "eyJ…",
  "id_token": "eyJ…",
  "refresh_token": "…",
  "expires_in": 3600,
  "scope": "openid profile"
}
```

## Grants and tokens issued

| `grant_type` | `access_token` | `id_token` | `refresh_token` | `issued_token_type` |
| --- | :---: | :---: | :---: | :---: |
| `client_credentials` | ✓ | — | — | — |
| `authorization_code` | ✓ | ✓ | ✓ | — |
| `password` | ✓ | ✓ | — | — |
| `refresh_token` | ✓ | conditional | — | — |
| `urn:ietf:params:oauth:grant-type:jwt-bearer` | ✓ | — | — | — |
| `urn:ietf:params:oauth:grant-type:token-exchange` | ✓ | — | — | ✓ |

The `refresh_token` grant re-mints an `id_token` only when the underlying code
record carried a `nonce`; otherwise it returns an access token alone. The
`token-exchange` grant returns no `scope` field.

## Registered claims

Every minted token (access or ID) carries the following claims.

| Claim | Value |
| --- | --- |
| `sub` | Subject. Resolved per the order below. |
| `aud` | Audience. Resolved per the rules below (differs between ID and access tokens). |
| `iss` | Issuer URL: the resolved host root, `+ "/" +` the issuer id (e.g. `http://localhost:8080/default`). |
| `iat` | Issued-at, set to the current clock time. |
| `nbf` | Not-before, set to the current clock time. |
| `exp` | Expiry: `iat` + the token lifetime (default `3600` seconds). |
| `jti` | A random UUID, unique per minted token. |
| `nonce` | Present only when a `nonce` was supplied to the authorize request. |

The `iss` value is derived per request from the resolved external address (see
[Issuers and advertised identity](../explanation/issuers-and-identity.md)). The
same clock drives both issuance and verification; freezing or advancing it
affects `iat`/`nbf`/`exp` and whether an existing token still verifies (see
[Simulate expiry and time](../how-to/simulate-expiry-and-time.md)).

## Subject resolution

`sub` is resolved by the first rule that applies, in order:

1. `client_credentials` grant → the `client_id`.
2. `password` grant → the `username`.
3. Interactive login → the submitted login username (or a `sub` supplied via a
   request mapping).
4. A `subject` configured on the matching token callback.
5. A per-callback random UUID fallback.

Rule 5 guarantees that zero-config authorization-code tokens always carry a
`sub`.

## Default-callback claims

The following claims are added by the built-in default callback only. A
`requestMapping` callback adds neither.

| Claim | Value | Condition |
| --- | --- | --- |
| `tid` | The issuer id. | Default callback; user-overridable. |
| `azp` | The `client_id`. | `authorization_code` grant only; default callback. |

## Audience

### ID token

The `id_token` `aud` is always `[client_id]`.

### Access token

The `access_token` `aud` is resolved by the first rule that applies, in order:

1. A configured or scenario audience — the `audience` list on the matching
   token callback, including an explicitly configured empty list (`[]`).
2. The token-exchange `audience` request parameter (the `token-exchange` grant
   only, and only when no callback audience is configured).
3. The non-OIDC scopes remaining after the OIDC scopes `openid`, `profile`,
   `email`, `address`, `phone`, and `offline_access` are stripped from the
   requested `scope`.
4. `["default"]`.

## Token type header (`typ`)

The JWS `typ` header controls whether the server accepts its own token back at
`userinfo` and `introspect`.

| `typ` | Self-verifies | `GET /userinfo` | `POST /introspect` |
| --- | :---: | --- | --- |
| `JWT` (default) | ✓ | `200` | `{"active": true}` |
| `at+jwt` (RFC 9068) | ✓ | `200` | `{"active": true}` |
| any other, e.g. `foo+jwt` | — | `401` | `{"active": false}` |

A foreign `typ` produces a well-formed, signed token that the server treats as
unverifiable when presented back to it.

## Signing

### Algorithms

Each issuer signs with one algorithm, selectable per issuer.

| Algorithm | Default |
| --- | :---: |
| `RS256` | ✓ |
| `RS384` | |
| `RS512` | |
| `PS256` | |
| `PS384` | |
| `PS512` | |
| `ES256` | |
| `ES384` | |

`alg=none` is rejected on verification.

### Key identifier

The signing key's `kid` equals the issuer id. It appears in the JWS header of
minted tokens and in the issuer's `GET /{issuer}/jwks` entry (with `use=sig` and
the `alg`, public members only).

## Introspection audience serialization

`POST /{issuer}/introspect` serializes a single-element `aud` as a bare string
rather than a one-element array. A multi-element `aud` is serialized as a JSON
array. See the [API Reference](../api.md) for the full introspection response
contract.

To change any default on this page — claims, subject, audience, `typ`, or
algorithm — see [Shape token claims](../how-to/shape-token-claims.md).
