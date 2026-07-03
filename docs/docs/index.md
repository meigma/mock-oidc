---
title: mock-oidc Docs
slug: /
description: Standalone mock OIDC/OAuth2 authorization server for testing.
---

# mock-oidc

`mock-oidc` is a standalone, container-first **mock OIDC/OAuth2 authorization
server for testing**. It issues real, cryptographically-verifiable tokens for
arbitrary identities so a test suite can exercise a full sign-in flow against an
unmodified OAuth2/OIDC client — no real identity provider required. It is a Go
reimplementation of [navikt/mock-oauth2-server](https://github.com/navikt/mock-oauth2-server)
built on chi + Huma, with a hexagonal architecture and a strong supply-chain
baseline.

!!! warning "For testing only"
    mock-oidc mints signed tokens for any identity on request. It must never
    front production traffic; the server logs this positioning banner on every
    startup.

## What it does

Point an OAuth2/OIDC client at a running `mock-oidc` and it behaves like a real
authorization server: it publishes discovery and a JWKS and mints real, signed
tokens for any identity, all namespaced under an **issuer**. With zero
configuration a single `default` issuer is served at
`http://localhost:8080/default`, exposing discovery, `authorize`, `token`,
`jwks`, `userinfo`, `introspect`, `revoke`, and `endsession`. A test-time
control plane is mounted at `/_mock`.

## Quick start

The server is DB-less and needs no configuration. Run the published container:

```sh
docker run --rm -p 8080:8080 ghcr.io/meigma/mock-oidc
curl -sS localhost:8080/default/.well-known/openid-configuration
#   => { "issuer": "http://localhost:8080/default", ... }
```

Or build and run from source:

```sh
moon run root:build          # or: go build -o bin/mock-oidc ./cmd/mock-oidc
./bin/mock-oidc serve        # serve is the default subcommand; listens on :8080
```

See the [README](https://github.com/meigma/mock-oidc#readme) for the full
configuration reference, including TLS (`httpServer.ssl`), running behind a
proxy / `host.docker.internal`, the named nested-issuer parity gap, and
artifact verification.

## API reference

The [API Reference](api.md) is generated from the OpenAPI specification. A
running server also serves interactive docs at `/docs` and the live spec at
`/openapi.yaml`.

## Operating notes

- Liveness: `GET /isalive` (upstream-parity alias) and `GET /healthz`
- Readiness: `GET /readyz` (reports named per-check results; the server is
  DB-less, so it is unconditionally ready)
- Metrics: `GET /metrics` on a dedicated listener (`--metrics-addr`, default `:9090`)
- Configuration is via flags or `MOCK_OIDC_*` environment variables; the
  server boots with zero configuration.

## Support and security

- Issues and contributions: see [CONTRIBUTING.md](https://github.com/meigma/mock-oidc/blob/master/CONTRIBUTING.md).
- Security reports: see [SECURITY.md](https://github.com/meigma/mock-oidc/blob/master/SECURITY.md).
