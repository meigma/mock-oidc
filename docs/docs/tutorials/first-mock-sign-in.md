---
title: Your first mock sign-in
description: Drive a complete authorization-code sign-in against a zero-config mock-oidc server using nothing but curl.
---

# Your first mock sign-in

In this tutorial we'll take a freshly started `mock-oidc` server and drive a
complete OpenID Connect sign-in against it, start to finish, using nothing but
`curl`. We'll fetch the discovery document a real client would read, run the
authorization-code flow to get a code, exchange that code for tokens, decode the
ID token to see who we signed in as, and confirm the identity at the userinfo
endpoint. To finish, we'll mint a token in a single request using the built-in
control plane.

By the end you'll have seen every moving part of an OIDC sign-in with your own
eyes, and you'll have the mental model to explore the rest of the docs.

Everything here runs against a **zero-config** server: no configuration file, no
registration step, no real identity provider.

## Prerequisites

- Docker, to run the container.
- `curl` and `python3` (both are used only for making requests and decoding
  JSON — no OAuth client libraries anywhere).

## Step 1: Start the server

Start the published container. It listens on port `8080`:

```sh
docker run --rm -p 8080:8080 ghcr.io/meigma/mock-oidc
```

This stays in the foreground and streams its logs. On every startup it prints a
hard-to-miss **FOR TESTING ONLY** warning banner — something like:

```text
mock-oidc — FOR TESTING ONLY
Never front production traffic with this server.
```

!!! warning "For testing only"
    `mock-oidc` mints signed tokens for **any** identity on request. That is
    exactly what makes it useful for tests — and exactly why it must never sit
    in front of real users.

Leave that terminal running and open a **second terminal** for the rest of the
tutorial. Confirm the server is alive:

```sh
curl -sS http://localhost:8080/isalive
#   => {"status":"ok"}
```

There's our server.

## Step 2: See what a client sees — the discovery document

An OIDC client's first move is to read the issuer's discovery document. Every
issuer lives under a single path segment; the zero-config default issuer is
named `default`. Ask for its discovery document:

```sh
curl -sS http://localhost:8080/default/.well-known/openid-configuration | python3 -m json.tool
```

```json
{
    "issuer": "http://localhost:8080/default",
    "authorization_endpoint": "http://localhost:8080/default/authorize",
    "token_endpoint": "http://localhost:8080/default/token",
    "userinfo_endpoint": "http://localhost:8080/default/userinfo",
    "jwks_uri": "http://localhost:8080/default/jwks"
}
```

(Trimmed for brevity — the real document lists more fields.) Notice the
`issuer` and the four endpoints we'll use next: **authorize** to start the flow,
**token** to exchange the code, **userinfo** to read the identity, and
**jwks** to publish the public verification key.

## Step 3: Fetch the JWKS

The `jwks_uri` is where clients fetch the public key that verifies our tokens.
Fetch it:

```sh
curl -sS http://localhost:8080/default/jwks | python3 -m json.tool
```

```json
{
    "keys": [
        {
            "kty": "RSA",
            "kid": "default",
            "use": "sig",
            "alg": "RS256",
            "n": "...",
            "e": "AQAB"
        }
    ]
}
```

Notice the `kid` is `default` — the same name as the issuer. The default issuer
was created, with its own signing key, the moment we first touched it. There was
no registration step. To learn how identity and keys fit together, see
[Issuers and identity](../explanation/issuers-and-identity.md).

## Step 4: Run the authorization-code flow

Now the sign-in itself. We send the client's browser to the `authorize`
endpoint. Because interactive login is off by default, the server skips the
login page and immediately hands back an authorization code as a `302`
redirect. Use `-i` so `curl` shows us the response headers:

```sh
curl -sS -i "http://localhost:8080/default/authorize?response_type=code&client_id=demo&redirect_uri=http://localhost:8080/callback&scope=openid%20profile&state=xyz"
#   => HTTP/1.1 302 Found
#   => Location: http://localhost:8080/callback?code=<code>&state=xyz
```

There's the code, tucked into the `Location` header the browser would follow.
Our `state=xyz` is echoed back untouched. Let's capture that code into a shell
variable so we can use it in the next step:

```sh
CODE=$(curl -sS -D - -o /dev/null \
  "http://localhost:8080/default/authorize?response_type=code&client_id=demo&redirect_uri=http://localhost:8080/callback&scope=openid%20profile&state=xyz" \
  | grep -i '^location:' | sed -E 's/.*code=([^&]+).*/\1/' | tr -d '\r')
echo "$CODE"
#   => 8f14e45f-ce9a-4c1b-9a0d-1f2b3c4d5e6f   (a fresh code every run)
```

## Step 5: Exchange the code for tokens

The client now takes that code straight to the `token` endpoint. This is a
form-encoded `POST`. Capture the whole JSON response so we can pick it apart:

```sh
TOKENS=$(curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=authorization_code \
  -d code="$CODE" \
  -d client_id=demo \
  -d redirect_uri=http://localhost:8080/callback \
  -d "scope=openid profile")
echo "$TOKENS" | python3 -m json.tool
```

```json
{
    "token_type": "Bearer",
    "access_token": "eyJhbGciOiJSUzI1NiIs...",
    "id_token": "eyJhbGciOiJSUzI1NiIs...",
    "refresh_token": "eyJhbGciOiJSUzI1NiIs...",
    "expires_in": 3600,
    "scope": "openid profile"
}
```

We signed in. The `authorization_code` grant hands back all three: an
**id_token** (who signed in), an **access_token** (to call APIs), and a
**refresh_token** (to get more). The code is single-use — try Step 5 again with
the same `$CODE` and you'll get an `invalid_grant` error, which is exactly how a
real server behaves.

Pull the ID token and access token into variables:

```sh
ID_TOKEN=$(printf '%s' "$TOKENS" | python3 -c 'import sys,json; print(json.load(sys.stdin)["id_token"])')
ACCESS_TOKEN=$(printf '%s' "$TOKENS" | python3 -c 'import sys,json; print(json.load(sys.stdin)["access_token"])')
```

## Step 6: Decode the ID token

A JWT is three base64url segments separated by dots: header, payload, signature.
The identity lives in the middle segment. Let's cut it out and decode it:

```sh
printf '%s' "$ID_TOKEN" | cut -d. -f2 | \
  python3 -c 'import sys,base64,json; s=sys.stdin.read().strip(); s+="="*(-len(s)%4); print(json.dumps(json.loads(base64.urlsafe_b64decode(s)), indent=2))'
```

```json
{
    "sub": "6b1e...-a random uuid",
    "aud": ["demo"],
    "iss": "http://localhost:8080/default",
    "iat": 1751500000,
    "nbf": 1751500000,
    "exp": 1751503600,
    "jti": "...",
    "azp": "demo",
    "tid": "default"
}
```

Read the payload like a client would:

- `sub` — the subject, who signed in. We never named anyone, so the server gave
  us a random UUID.
- `iss` — the issuer, matching the discovery document exactly.
- `aud` — the audience, `["demo"]`, the `client_id` we asked with.

These are real, signed claims — the signature over the token verifies against
the JWKS we fetched in Step 3.

## Step 7: Confirm the identity at userinfo

A client can also ask the server directly who a token belongs to. The `userinfo`
endpoint takes the access token as a Bearer credential, verifies its signature,
and returns the claim set:

```sh
curl -sS http://localhost:8080/default/userinfo -H "Authorization: Bearer $ACCESS_TOKEN"
```

```json
{"sub":"6b1e...-a random uuid","iss":"http://localhost:8080/default","aud":"default", "...": "..."}
```

The `sub` matches the one we decoded from the ID token — same identity, arriving
two different ways. Hand `userinfo` a garbled or expired token instead and it
answers `401` with `error="invalid_token"`. To understand how that verification
works, see [Issuers and identity](../explanation/issuers-and-identity.md).

## Step 8: A first taste of the control plane

The full flow is great for exercising a client end to end. But when a test just
needs *a valid token for a specific person*, there's a shortcut: the `/_mock`
control plane. Mint a token in one request:

```sh
curl -sS -X POST http://localhost:8080/_mock/mint \
  -H 'Content-Type: application/json' \
  -d '{"issuer":"default","subject":"alice","audience":["demo"],"scope":["openid","profile"],"clientId":"demo","kind":"access_token"}'
```

```json
{
    "token": "eyJhbGciOiJSUzI1NiIs...",
    "kid": "default",
    "algorithm": "RS256",
    "issuer": "http://localhost:8080/default",
    "expiresAt": "2026-07-03T12:00:00Z",
    "claims": {"sub": "alice", "aud": ["demo"], "iss": "http://localhost:8080/default"}
}
```

A minted token is byte-identical to one from the flow — same signing key, same
verification. Prove it by handing this one to `userinfo`:

```sh
MINTED=$(curl -sS -X POST http://localhost:8080/_mock/mint \
  -H 'Content-Type: application/json' \
  -d '{"issuer":"default","subject":"alice","audience":["demo"],"scope":["openid","profile"],"clientId":"demo","kind":"access_token"}' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
curl -sS http://localhost:8080/default/userinfo -H "Authorization: Bearer $MINTED"
#   => {"sub":"alice", ...}
```

We asked for `alice`, and `alice` is who the server signs in. If minting any
identity on demand makes you wonder what stops this from being dangerous, that's
the right question to ask — read [The security model](../explanation/security-model.md).

## What we did

In one sitting, using only `curl`, we:

- Started a zero-config server and confirmed it was alive.
- Read the issuer's discovery document and JWKS, the way a real client does.
- Ran the authorization-code flow to obtain a code, then exchanged it for an
  ID token, access token, and refresh token.
- Decoded the ID token and confirmed the same identity at `userinfo`.
- Minted a token for a named subject in a single control-plane request.

You now have the mental model: **issuers materialize on first touch, every token
is really signed, and `/_mock` is your shortcut when you don't need the full
flow.**

## Where to next

- [Get tokens for every grant](../how-to/get-tokens-for-every-grant.md) — the
  other five grant types, each as a ready-to-run recipe.
- [Shape token claims](../how-to/shape-token-claims.md) — put whatever `sub`,
  audience, and custom claims your test needs into the tokens.
- [The security model](../explanation/security-model.md) — why a server that
  signs anything for anyone is safe for testing and only for testing.
