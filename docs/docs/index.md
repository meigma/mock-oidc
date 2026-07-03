---
title: Home
description: Standalone, container-first mock OIDC/OAuth2 authorization server for testing.
slug: /
---

# mock-oidc

`mock-oidc` is a standalone, container-first mock OIDC/OAuth2 authorization
server **for testing only**. It mints real, cryptographically-signed tokens for
arbitrary identities, so a test suite can drive a full sign-in against an
unmodified OAuth2/OIDC client with no real identity provider. It is a Go
reimplementation of [navikt/mock-oauth2-server](https://github.com/navikt/mock-oauth2-server),
is DB-less, and boots with zero configuration.

!!! warning "For testing only"
    mock-oidc signs a token for any identity on request and never validates
    client secrets. It must never front production traffic; the server logs a
    "FOR TESTING ONLY" banner on every startup.

## 30 seconds to a token

The server needs no configuration. Run the published container, read discovery
for the zero-config `default` issuer, and mint an access token:

```sh
docker run --rm -p 8080:8080 ghcr.io/meigma/mock-oidc

# Discovery for the "default" issuer (materializes on first touch)
curl -sS http://localhost:8080/default/.well-known/openid-configuration
#   => {"issuer":"http://localhost:8080/default", ...}

# client_credentials grant — client secrets are never validated
curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=client_credentials \
  -d client_id=test-client \
  -d scope=api
#   => {"token_type":"Bearer","access_token":"eyJ...","expires_in":3600, ...}
```

## Find your way

This site follows the [Diátaxis](https://diataxis.fr/) framework. Pick the
section that matches what you need right now.

- **Learning the tool** — Start with the tutorial,
  [Your first mock sign-in](tutorials/first-mock-sign-in.md), a guided
  end-to-end run.

- **Getting a specific task done** — The [how-to guides](how-to/get-tokens-for-every-grant.md)
  are goal-oriented recipes:
  [get tokens for every grant](how-to/get-tokens-for-every-grant.md),
  [drive the authorization-code flow](how-to/drive-the-authorization-code-flow.md),
  [shape token claims](how-to/shape-token-claims.md),
  [simulate expiry and time](how-to/simulate-expiry-and-time.md),
  [capture and assert requests](how-to/capture-and-assert-requests.md),
  [use multiple issuers](how-to/use-multiple-issuers.md),
  [serve over TLS](how-to/serve-over-tls.md),
  [run behind a proxy or in Docker](how-to/run-behind-a-proxy-or-in-docker.md),
  [lock down the control plane](how-to/lock-down-the-control-plane.md),
  [migrate from mock-oauth2-server](how-to/migrate-from-mock-oauth2-server.md),
  and [verify released artifacts](how-to/verify-released-artifacts.md).

- **Looking something up** — The reference pages describe the software exactly:
  [Configuration](reference/configuration.md),
  [Tokens and claims](reference/tokens-and-claims.md),
  [Control plane (`/_mock`)](reference/control-plane.md),
  [CLI](reference/cli.md),
  [Observability](reference/observability.md), and the
  [API Reference](api.md).

- **Understanding why** — The explanation pages cover design and rationale:
  [the security model](explanation/security-model.md),
  [issuers and advertised identity](explanation/issuers-and-identity.md),
  [parity with mock-oauth2-server](explanation/parity.md), and
  [architecture and distribution](explanation/architecture-and-distribution.md).

## Support and security

- Contributions and issues: [CONTRIBUTING.md](https://github.com/meigma/mock-oidc/blob/master/CONTRIBUTING.md)
- Security reports: [SECURITY.md](https://github.com/meigma/mock-oidc/blob/master/SECURITY.md)
