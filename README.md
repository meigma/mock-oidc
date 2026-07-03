# mock-oidc

`mock-oidc` is a standalone, container-first **mock OIDC/OAuth2 authorization
server for testing**. It issues real, cryptographically-signed tokens for
arbitrary identities so a test suite can drive a full sign-in flow against an
unmodified OAuth2/OIDC client — no real identity provider required. It is a Go
reimplementation of [navikt/mock-oauth2-server](https://github.com/navikt/mock-oauth2-server).

> **FOR TESTING ONLY.** mock-oidc mints signed tokens for any identity on
> request. It must never front production traffic. The server logs this
> positioning banner on every startup.

## Features

- All six OAuth2 grants (`client_credentials`, `authorization_code`, `password`,
  `refresh_token`, JWT-bearer, token-exchange) plus the auth-code flow with PKCE.
- Real, signed JWTs (ID, access, and refresh tokens) that verify against the
  JWKS the server publishes for each issuer.
- Multi-issuer: one server impersonates many identity providers, and issuers
  materialize on first touch — no registration step.
- A `/_mock` control plane to mint tokens directly, freeze or advance the clock,
  enqueue one-shot scenarios, and capture inbound requests for assertions.
- Drop-in compatibility with `mock-oauth2-server`: the same unprefixed
  environment variables and the same JSON configuration shape.
- Zero-config and DB-less — it boots instantly and serves a `default` issuer.
- Distributed as a signed, SBOM'd, multi-arch container image.

## Quickstart

The server needs no configuration. Run the published multi-arch image:

```sh
docker run --rm -p 8080:8080 ghcr.io/meigma/mock-oidc
```

Fetch discovery for the zero-config `default` issuer:

```sh
curl -sS http://localhost:8080/default/.well-known/openid-configuration
#   => { "issuer": "http://localhost:8080/default", "token_endpoint": ..., "jwks_uri": ... }
```

Get a signed access token with the client-credentials grant:

```sh
curl -sS -X POST http://localhost:8080/default/token \
  -d grant_type=client_credentials -d client_id=my-service -d scope=api
#   => {"token_type":"Bearer","access_token":"eyJ...","expires_in":3600,"scope":"api"}
```

Or build and run from source:

```sh
moon run root:build     # or: go build -o bin/mock-oidc ./cmd/mock-oidc
./bin/mock-oidc serve   # serve is the default subcommand; listens on :8080
```

## Documentation

The full documentation lives at <https://meigma.github.io/mock-oidc/>, organized
by what you need:

- **Learn** — start with the
  [Your first mock sign-in](https://meigma.github.io/mock-oidc/tutorials/first-mock-sign-in/)
  tutorial.
- **Do** — task-focused how-to guides:
  [get tokens for every grant](https://meigma.github.io/mock-oidc/how-to/get-tokens-for-every-grant/),
  [drive the authorization-code flow](https://meigma.github.io/mock-oidc/how-to/drive-the-authorization-code-flow/),
  [shape token claims](https://meigma.github.io/mock-oidc/how-to/shape-token-claims/),
  [simulate expiry and time](https://meigma.github.io/mock-oidc/how-to/simulate-expiry-and-time/),
  [capture and assert requests](https://meigma.github.io/mock-oidc/how-to/capture-and-assert-requests/),
  and [migrate from mock-oauth2-server](https://meigma.github.io/mock-oidc/how-to/migrate-from-mock-oauth2-server/).
- **Look up** —
  [Configuration](https://meigma.github.io/mock-oidc/reference/configuration/),
  [Tokens and claims](https://meigma.github.io/mock-oidc/reference/tokens-and-claims/),
  [Control plane](https://meigma.github.io/mock-oidc/reference/control-plane/),
  [CLI](https://meigma.github.io/mock-oidc/reference/cli/),
  [Observability](https://meigma.github.io/mock-oidc/reference/observability/),
  and the generated [API Reference](https://meigma.github.io/mock-oidc/api/).
- **Understand** —
  [the security model](https://meigma.github.io/mock-oidc/explanation/security-model/),
  [issuers and advertised identity](https://meigma.github.io/mock-oidc/explanation/issuers-and-identity/),
  [parity with mock-oauth2-server](https://meigma.github.io/mock-oidc/explanation/parity/),
  and [architecture and distribution](https://meigma.github.io/mock-oidc/explanation/architecture-and-distribution/).

## Development

Prerequisites are [mise](https://mise.jdx.dev) (provisions every pinned tool from
`mise.toml` + `mise.lock`) and Docker (for the container image and the
container-backed integration tests). Run `mise install` once, then
`moon run root:check` for the aggregate gate that CI runs. See
[CONTRIBUTING.md](CONTRIBUTING.md) for the full contributor guide.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines, local setup
expectations, and pull request workflow.

## Security

See [SECURITY.md](SECURITY.md) for supported versions and the private
vulnerability reporting path.

## License

Licensed under either of

- Apache License, Version 2.0 ([LICENSE-APACHE](LICENSE-APACHE) or
  <https://www.apache.org/licenses/LICENSE-2.0>)
- MIT license ([LICENSE-MIT](LICENSE-MIT) or
  <https://opensource.org/licenses/MIT>)

at your option.

### Contribution

Unless you explicitly state otherwise, any contribution intentionally
submitted for inclusion in the work by you, as defined in the Apache-2.0
license, shall be dual licensed as above, without any additional terms or
conditions.
