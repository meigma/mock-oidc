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

## Project status

The current tree is the walking skeleton (Slice 0): the transport, observability,
CLI, and config boot in a container and serve only the infrastructure routes
below. The OIDC domain is an empty, layering-gated hexagon; discovery, JWKS, and
the token endpoints land in the following slices.

## Quick start

The server is DB-less and needs no configuration. Build and run it, or run the
shipped container:

```sh
moon run root:build          # or: go build -o bin/mock-oidc ./cmd/mock-oidc
./bin/mock-oidc serve        # serve is the default subcommand; listens on :8080
curl -sS localhost:8080/isalive

# or the container:
mise run image-local
docker run --rm -p 8080:8080 -p 9090:9090 mock-oidc:dev
```

See the [README](https://github.com/meigma/mock-oidc#readme) for the full
quickstart and the configuration reference.

## API reference

The [API Reference](api.md) is generated from the OpenAPI specification. A
running server also serves interactive docs at `/docs` and the live spec at
`/openapi.yaml`.

## Operating notes

- Liveness: `GET /isalive` (upstream-parity alias) and `GET /healthz`
- Readiness: `GET /readyz` (reports named per-check results; empty and
  unconditionally ready in the skeleton — the server is DB-less)
- Metrics: `GET /metrics` on a dedicated listener (`--metrics-addr`, default `:9090`)
- Configuration is via flags or `MOCK_OIDC_*` environment variables; the
  server boots with zero configuration.

## Support and security

- Issues and contributions: see [CONTRIBUTING.md](https://github.com/meigma/mock-oidc/blob/master/CONTRIBUTING.md).
- Security reports: see [SECURITY.md](https://github.com/meigma/mock-oidc/blob/master/SECURITY.md).
