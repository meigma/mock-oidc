---
title: Run behind a proxy or in Docker
description: Make the advertised issuer identity match the address clients actually reach, behind a reverse proxy or in containers.
---

# Run behind a proxy or in Docker

An OIDC client fetches discovery and JWKS from the advertised `issuer` URL and
rejects tokens whose `iss` does not match it, so the mock's advertised identity
must equal the address your clients actually reach. mock-oidc derives every
advertised URL — `issuer`, every `*_endpoint`, and `jwks_uri` — **per request**
from the `X-Forwarded-Proto`, `X-Forwarded-Host`, and `X-Forwarded-Port`
headers, falling back to the `Host` header. Point those at the external address
and the identity follows. For why identity is computed per request rather than
pinned at startup, see
[Issuers and advertised identity](../explanation/issuers-and-identity.md).

## Behind a reverse proxy

Terminate TLS (or route traffic) at your proxy and have it forward the standard
`X-Forwarded-*` headers to mock-oidc. The advertised URLs then reflect the
external address the proxy answers on, not the mock's internal listener.

```sh
curl -sS http://localhost:8080/default/.well-known/openid-configuration \
  -H 'X-Forwarded-Proto: https' \
  -H 'X-Forwarded-Host: idp.example.com'
#   => {
#   =>   "issuer": "https://idp.example.com/default",
#   =>   "authorization_endpoint": "https://idp.example.com/default/authorize",
#   =>   "token_endpoint": "https://idp.example.com/default/token",
#   =>   "jwks_uri": "https://idp.example.com/default/jwks",
#   =>   ...
#   => }
```

Every advertised URL — and the `iss` claim stamped into minted tokens — is now
`https://idp.example.com/default`. A real proxy (nginx, Traefik, Envoy) sets
these headers on the upstream request for you; the curl above just simulates one
hop so you can confirm the behavior.

!!! tip "Non-standard external ports"
    When your proxy answers on a port other than the scheme default, add
    `X-Forwarded-Port` and that port appears in every advertised URL. For
    example, `X-Forwarded-Port: 8443` yields
    `https://idp.example.com:8443/default`.

If mock-oidc itself terminates TLS instead of a proxy, see
[Serve over TLS](serve-over-tls.md) — over HTTPS every advertised URL is
`https` without any forwarded headers.

## In Docker

Container-backed tests have two callers that must agree on one issuer URL: the
**app under test** fetches discovery and JWKS from it, and the **browser** is
redirected to `authorize` on it. If they reach the mock by different names
(say, the app uses a Docker network alias the host browser cannot resolve), the
advertised `iss` will not match what one side used and verification fails.

Give both sides the same reachable name. On Docker Desktop, expose the host
gateway and publish the port:

```sh
docker run --rm \
  --add-host=host.docker.internal:host-gateway \
  -p 8080:8080 \
  ghcr.io/meigma/mock-oidc
```

Then configure the app under test **and** the browser to use the same issuer:

```
http://host.docker.internal:8080/default
```

Because the mock derives its identity from the incoming `Host`, a request to
`host.docker.internal:8080` advertises
`iss: http://host.docker.internal:8080/default` — exactly the address both the
in-container app (via the host gateway) and the host browser reach it on.

!!! note "Port remapping just works"
    Publishing to a different host port (for example `-p 9000:8080`) needs no
    mock-oidc configuration. Have both sides use
    `http://host.docker.internal:9000/default`; the mock sees `Host:
    host.docker.internal:9000` and advertises that URL. The advertised `iss`
    always follows the `Host` / `X-Forwarded-*` the mock is reached on.
