# mock-oidc

`mock-oidc` is a standalone, container-first **mock OIDC/OAuth2 authorization
server for testing**. It issues real, cryptographically-verifiable tokens for
arbitrary identities so a test suite can exercise a full sign-in flow against an
unmodified OAuth2/OIDC client — no real identity provider required. It is a Go
reimplementation of [navikt/mock-oauth2-server](https://github.com/navikt/mock-oauth2-server)
with a hexagonal architecture, first-class container delivery, and a strong
supply-chain/provenance baseline (pinned CI, signed multi-arch images, SBOMs).

> **FOR TESTING ONLY.** mock-oidc mints signed tokens for any identity on
> request. It must never front production traffic. The server logs this
> positioning banner on every startup.

The server is built on [chi](https://github.com/go-chi/chi) and
[Huma](https://huma.rocks), is DB-less, and boots with zero configuration.

## Project status

This repository is being built in vertical slices. The current tree is the
**walking skeleton** (Slice 0): the transport, observability, CLI, and config
boot in a container and serve only the infrastructure routes below. The OIDC
domain (`internal/oidc`) is an empty, layering-gated hexagon; discovery, JWKS,
and the token endpoints land in the following slices.

Infrastructure routes served today:

```sh
curl -sS localhost:8080/isalive   # liveness alias  => 200
curl -sS localhost:8080/healthz   # liveness        => {"status":"ok"}
curl -sS localhost:8080/readyz    # readiness       => {"status":"ready","checks":{}}
curl -sS localhost:9090/metrics   # Prometheus exposition (dedicated listener)
```

## Prerequisites

- [mise](https://mise.jdx.dev) — provisions every pinned tool from `mise.toml` +
  `mise.lock`: Go, Moon, Python + uv (for the MkDocs docs project), the
  `golangci-lint`/`mockery` CLIs, and `melange`/`apko`/`cosign` for releases. Run
  `mise install` once; there is nothing else to install by hand.
- Docker (to build and run the container image, and for the container-backed
  integration tests).

Tool versions live in `mise.toml`; `mise.lock` records a per-platform download
URL and checksum for each (and, for the aqua-backed CLIs, cosign/SLSA/GitHub-attestation
verification). `mise install` runs with `locked = true`, so it **fails closed**
if a tool lacks a pre-resolved, checksummed entry for the current platform. Moon
runs every task against these tools as `system` binaries on PATH and manages no
toolchain itself. To bump a tool, edit its version in `mise.toml`, run
`mise lock --platform linux-x64,linux-arm64,macos-x64,macos-arm64`, and commit
`mise.toml` + `mise.lock`.

## Quickstart

The server is DB-less and needs no configuration. Build and run it:

```sh
moon run root:build          # or: go build -o bin/mock-oidc ./cmd/mock-oidc
./bin/mock-oidc serve        # serve is the default subcommand; listens on :8080
curl -sS localhost:8080/isalive
```

Or run the shipped container (see [Container image](#container-image)):

```sh
mise run image-local                       # build the host-arch image as mock-oidc:dev
docker run --rm -p 8080:8080 -p 9090:9090 mock-oidc:dev
```

`mise run stack-up` brings up the same image via Docker Compose.

## Commands

| Command | Description |
| --- | --- |
| `serve` (default) | Run the HTTP server. |
| `version` | Print version, commit, and build date. |
| `openapi` | Write the OpenAPI 3.0.3 spec to stdout or a file (`--output/-o`). |

```sh
./bin/mock-oidc openapi -o docs/docs/openapi.yaml
./bin/mock-oidc version
```

## Configuration

Flags bind to Viper, so every setting is also a `MOCK_OIDC_*` environment
variable (uppercase, dashes become underscores). Precedence is flag > env >
default.

| Flag | Env var | Default | Description |
| --- | --- | --- | --- |
| `--addr` | `MOCK_OIDC_ADDR` | `:8080` | host:port the HTTP server listens on |
| `--metrics-addr` | `MOCK_OIDC_METRICS_ADDR` | `:9090` | dedicated `/metrics` listener; empty serves `/metrics` on `--addr` |
| `--log-level` | `MOCK_OIDC_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `--log-format` | `MOCK_OIDC_LOG_FORMAT` | `json` | `json` or `text` |
| `--read-timeout` | `MOCK_OIDC_READ_TIMEOUT` | `5s` | reading an entire request |
| `--read-header-timeout` | `MOCK_OIDC_READ_HEADER_TIMEOUT` | `5s` | reading request headers |
| `--write-timeout` | `MOCK_OIDC_WRITE_TIMEOUT` | `10s` | writing the response |
| `--idle-timeout` | `MOCK_OIDC_IDLE_TIMEOUT` | `120s` | idle keep-alive connections |
| `--request-timeout` | `MOCK_OIDC_REQUEST_TIMEOUT` | `15s` | per-request processing |
| `--shutdown-grace` | `MOCK_OIDC_SHUTDOWN_GRACE` | `15s` | graceful shutdown window |
| `--cors-allowed-origins` | `MOCK_OIDC_CORS_ALLOWED_ORIGINS` | _(none)_ | allowed CORS origins (comma-separated); empty disables CORS |
| `--trusted-proxy-header` | `MOCK_OIDC_TRUSTED_PROXY_HEADER` | _(none)_ | proxy header to read the client IP from (e.g. `X-Real-IP`); empty trusts the TCP peer |
| `--rate-limit-enabled` | `MOCK_OIDC_RATE_LIMIT_ENABLED` | `false` | enable per-client rate limiting; **off by default** so test traffic is never throttled |
| `--rate-limit-rps` | `MOCK_OIDC_RATE_LIMIT_RPS` | `10` | sustained per-client request rate (requests/second) |
| `--rate-limit-burst` | `MOCK_OIDC_RATE_LIMIT_BURST` | `20` | per-client burst size (token-bucket depth) |
| `--tracing-enabled` | `MOCK_OIDC_TRACING_ENABLED` | `false` | enable OpenTelemetry [tracing](#tracing); the OTLP exporter is configured via the standard `OTEL_*` env vars |

For compatibility with the upstream `mock-oauth2-server`, the unprefixed
`SERVER_HOSTNAME`, `SERVER_PORT`, `PORT`, `JSON_CONFIG`, `JSON_CONFIG_PATH`, and
`LOG_LEVEL` environment variables are also bound (with `LOGBACK_CONFIG` accepted
as a no-op). The seed/listen-address wiring that consumes the port and
JSON-config aliases lands with the OIDC slices.

CORS is off until you set origins. Client IP is read from the direct TCP peer
unless you opt into a trusted proxy header — never from `X-Forwarded-For`
implicitly — so the default is not spoofable. Rate limiting is **disabled by
default** because a for-testing server is hammered by container-backed suites.

## Tracing

Distributed tracing is [OpenTelemetry](https://opentelemetry.io)-based and
**opt-in** (`--tracing-enabled`, default false) because it needs an external
collector. When enabled, the server exports spans over **OTLP/HTTP** and is
configured entirely through the standard `OTEL_*` environment variables:

```sh
MOCK_OIDC_TRACING_ENABLED=true \
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
OTEL_SERVICE_NAME=mock-oidc \
OTEL_TRACES_SAMPLER=parentbased_traceidratio OTEL_TRACES_SAMPLER_ARG=0.1 \
  ./bin/mock-oidc serve
```

Inbound HTTP requests are server spans (`otelhttp`) that extract W3C trace
context; the infrastructure routes (`/isalive`, `/healthz`, `/readyz`,
`/metrics`) are excluded so health checks and scrapes do not flood the backend.
`service.name`/`service.version` default to `mock-oidc` and the build version and
are overridable via `OTEL_SERVICE_NAME` / `OTEL_RESOURCE_ATTRIBUTES`. The tracer
provider is flushed on graceful shutdown.

## Testing

Unit tests sit beside the code and use [Testify](https://github.com/stretchr/testify)
(`assert` / `require`). Outbound-port doubles are **mockery-generated** testify
mocks, drift-guarded by `moon run root:mockery-check`. The OIDC hexagon has no
ports yet, so `.mockery.yaml` `packages:` is empty; later slices repoint it at
`internal/oidc`.

The domain core's layering is enforced two ways: the `oidc-core` depguard rule in
`.golangci.yml` and the `TestCoreImportsAreClean` architecture test — both fail if
`internal/oidc` reaches transport, framework, or key-bearing signing packages.

The container-backed [integration suite](internal/integration) is behind the
`integration` build tag, so the default `go test ./...` and `moon run root:check`
stay hermetic (no Docker). It boots the shipped `mock-oidc:dev` image with
testcontainers and asserts the infra routes and the boot banner; it skips loudly
if the image is not present:

```sh
mise run image-local             # build mock-oidc:dev first
moon run root:test-integration   # or: go test -tags integration ./internal/integration/...
```

## Project layout

The server follows pragmatic hexagonal (ports & adapters) layering: the domain
core depends on nothing in the adapters, and dependencies point inward.

```
cmd/mock-oidc/              thin main; builds the Cobra root and executes
internal/
  cli/                      serve / version / openapi commands, Viper wiring
  config/                   server runtime config (flags + MOCK_OIDC_* env)
  oidc/                     domain core: OIDC/OAuth2 types, ports, services (layering-gated)
    signing/                driven adapter: real key-bearing signing (Signer/KeyStore)
    memory/                 driven adapter: in-memory stores
    httpapi/                driving adapter: the OAuth2/OIDC HTTP endpoints
    controlapi/             driving adapter: the /_mock test-control plane
  adapter/
    http/                   generic transport: chi router, middleware, RFC 9457 errors,
                            /isalive /healthz /readyz /metrics, OpenAPI export, Registrar seam
  observability/            slog logger, request logging, Prometheus metrics
  logctx/                   carries the request-scoped logger on the context
  ratelimit/                in-process per-client rate limiter (disabled by default)
  app/                      composition root: wires everything and runs the server
  integration/              container-backed integration tests (build tag: integration)
compose.yaml                day-one local stack: the mock-oidc API service
.mockery.yaml               mockery generation config (repo root)
docs/                       MkDocs site; docs/docs/openapi.yaml is the exported spec
```

## Documentation

The MkDocs site publishes to GitHub Pages at
<https://meigma.github.io/mock-oidc/>, including a generated
[API Reference](https://meigma.github.io/mock-oidc/api/) rendered from the
OpenAPI spec. Build it locally with `moon run docs:build` or preview with
`moon run docs:serve`.

## Common tasks

Moon is the standard task front door:

```sh
moon run root:format
moon run root:lint
moon run root:build
moon run root:test
moon run root:mockery           # regenerate the committed testify mocks
moon run root:test-integration  # container-backed tests (needs Docker + mock-oidc:dev)
moon run root:check             # the aggregate gate CI runs via `moon ci --summary minimal`
```

## Container image

The image is built **without a Dockerfile**:
[melange](https://github.com/chainguard-dev/melange) compiles the binary into a
signed [Wolfi](https://github.com/wolfi-dev) apk (`melange.yaml`), and
[apko](https://github.com/chainguard-dev/apko) assembles it into a minimal,
multi-arch, non-root runtime image (`apko.yaml`) — uid 65532, ca-certificates,
tzdata, no shell. Each architecture builds natively (no QEMU). Build and run it
locally with the bundled mise task (it uses melange's Docker runner, so Docker
must be running):

```sh
mise run image-local              # build the host-arch image, load as mock-oidc:dev
docker run --rm -p 8080:8080 -p 9090:9090 mock-oidc:dev
```

The server needs no configuration; it boots and serves the infra routes
immediately. The Wolfi base intentionally floats to the latest packages (fresh CA
bundle and timezones, low CVE surface); the exact resolved versions are recorded
in the per-build SBOM and provenance attestation rather than pinned. `version`,
`commit`, and `date` are stamped into the binary via melange `--vars-file` — the
release workflow supplies the real values, and `mise run image-local` uses `dev`.

## CI and Security

The default CI workflow keeps permissions minimal, pins external actions, disables
checkout credential persistence, and delegates checks to Moon. It uses
GitHub-hosted dependency caches for Go, golangci-lint, and uv download artifacts.
The docs workflow builds the MkDocs site on pull requests and deploys `docs/build`
to GitHub Pages from the default branch. The scheduled security scan workflow
builds the local container image weekly, scans it for high/critical fixed
vulnerabilities, and uploads SARIF results to GitHub code scanning. Dependabot
covers GitHub Actions, Docker base images, the root Go module, and the docs uv
project.

The build CLIs are pinned in `mise.toml` and locked in `mise.lock`, which records
a per-platform download URL and checksum for every tool. `mise install` runs with
`locked = true`, so it fails closed if any tool lacks a pre-resolved, checksummed
entry for the current platform; the aqua-backed CLIs additionally verify cosign
signatures, SLSA provenance, and GitHub artifact attestations at install time.

Repository settings live in `.github/repository-settings.toml`. They default to
immutable releases, private vulnerability reporting, signed commits, squash-only
merges, GitHub Pages workflow publishing, and protected tags.

## Release Layer

Release automation is enabled so this repository proves the full binary and
container release lifecycle. The release path is:

- Release Please opens and maintains the release PR, then creates a draft GitHub
  release and tag after merge.
- Release Dry Run rehearses the GoReleaser binary path and the native-runner
  melange/apko container build path on pull requests.
- GoReleaser builds binaries, checksums, and SBOMs without publishing directly.
- The release workflow uploads assets to the draft release; a separate, isolated
  reusable workflow (`attest.yml`) generates the GitHub-hosted provenance
  attestation for the binary checksums.
- The release workflow builds amd64 and arm64 apks with melange on native
  GitHub-hosted runners, assembles and publishes
  `ghcr.io/meigma/mock-oidc:vX.Y.Z` as a multi-platform manifest with apko, signs
  it with keyless cosign, and attaches a syft SBOM attestation; the isolated
  `attest.yml` workflow then creates the GitHub-native provenance attestation for
  the manifest digest.
- Generating both provenance attestations in the isolated `attest.yml` reusable
  workflow (not in the build job) keeps the signing identity unreachable by build
  steps — the SLSA Build L3 isolation requirement.
- A human inspects the draft release before publication.

The root `ghd.toml` matches the default GoReleaser output so the binary can be
installed with `ghd` once the release workflow runs.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines, local setup
expectations, and pull request workflow.

## Security

See [SECURITY.md](SECURITY.md) for supported versions and the private
vulnerability reporting path.

## License

Add the repository license before publishing.
