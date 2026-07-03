# Contributing

Thank you for your interest in contributing.
This repository is `mock-oidc`, a standalone, container-first mock OIDC/OAuth2 authorization server for testing, so changes should keep it simple, predictable, and honest about its FOR TESTING ONLY stance.
For private vulnerability reporting, use [SECURITY.md](SECURITY.md) instead of public channels.

## Reporting Bugs

Report non-security bugs through GitHub issues.
Include the following details when possible:

- version, commit, or environment details
- steps to reproduce
- expected behavior
- actual behavior
- logs, screenshots, or a minimal reproduction

If you are reporting a security issue, stop and follow [SECURITY.md](SECURITY.md) instead.

## Pull Requests

Contributors should:

1. Keep changes focused and scoped to a single problem.
2. Add or update tests when behavior changes.
3. Update documentation when user-facing behavior changes.
4. Use Conventional Commit subjects, such as `feat: add config loader` or `fix: handle empty input`.
5. Make sure `moon run root:check` passes before requesting review.

## Local Setup

```sh
mise install         # provision the pinned toolchain (Go, Moon, the dev CLIs)
moon run root:check
```

Useful project commands:

```sh
moon run root:format
moon run root:lint
moon run root:build
moon run root:test
moon run docs:build              # build the docs site (renders the OpenAPI spec)

mise run stack-up                # run the mock-oidc server via Docker Compose (Ctrl-C to stop)
curl -sS http://localhost:8080/isalive  # smoke-test the running server (in another shell)
```

The server is DB-less and needs no configuration: `./bin/mock-oidc serve` boots
and serves the infrastructure routes immediately.

## Project Layout

The codebase uses a pragmatic hexagonal (ports-and-adapters) layout. Dependencies
point inward, and the domain core depends on nothing in the adapters.

- `cmd/mock-oidc` — thin `main` entrypoint.
- `internal/cli` — the `serve` / `version` / `openapi` subcommands.
- `internal/config` — configuration loading and precedence.
- `internal/oidc` — the pure domain core (layering-gated). Driven adapters live
  under `signing/` (the real key-bearing signer) and `memory/` (in-memory
  stores); driving adapters under `httpapi/` (the OAuth2/OIDC endpoints) and
  `controlapi/` (the `/_mock` control plane).
- `internal/adapter/http` — generic chi transport: router and middleware,
  RFC 9457 problem errors, infrastructure routes, and OpenAPI export.
- `internal/observability` — logging, metrics, and tracing wiring.
- `internal/app` — the composition root that wires everything together.
- `internal/integration` — container-backed tests behind the `integration`
  build tag.

The throwaway browser acceptance console under `webtest/` is a repo-internal
testing tool, not a shipped product.

## Tests

- Unit tests live beside the code they cover and use Testify.
- The OIDC core's outbound ports are doubled with mockery-generated mocks in
  `internal/oidc/mocks`, drift-guarded by `moon run root:mockery-check`.
- The container-backed integration suite is behind the `integration` build tag,
  so `go test ./...` and `moon run root:check` stay hermetic (no Docker). Run it
  with `mise run image-local` then `moon run root:test-integration`.
- The core's layering is enforced two ways: the `oidc-core` depguard rule in
  `.golangci.yml` and the `TestCoreImportsAreClean` architecture test.

## Common Tasks

```sh
moon run root:format            # format
moon run root:lint              # lint
moon run root:build             # build the binary
moon run root:test              # unit tests (hermetic)
moon run root:mockery           # regenerate the OIDC core mocks
moon run root:test-integration  # container-backed suite (run mise run image-local first)
moon run root:check             # aggregate gate; CI runs it via `moon ci --summary minimal`

moon run docs:build             # build the docs site
moon run docs:serve             # serve the docs site locally
```

## Release Changes

Release Please reads Conventional Commit subjects to build changelogs and release PRs.
Keep release-impacting commits clear; routine docs, CI, and maintenance commits should use the appropriate non-release type.
