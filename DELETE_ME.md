# Welcome to the Meigma Go API Template

This repository was generated from `template-go-api`, the standard starter for Meigma Go web API services.
It is meant to give new repositories a working baseline on day one: a small Go entrypoint scaffold by default, Moon task orchestration, pinned CI, dependency automation, repository security defaults, and an enabled release pipeline that has already been exercised by the template application.

Delete this file after you finish the first-repository setup checklist below.
It is only here to orient the initial project owner.

## What This Template Provides

- A runnable hexagonal HTTP API server (chi + Huma) at `github.com/meigma/template-go-api`, with a `todo` example resource served under a `/v1` URL version prefix, RFC 9457 errors, unversioned `/healthz`, `/readyz`, and `/metrics`, runtime API docs at `/docs`, and an `openapi` spec-export command.
- A PostgreSQL persistence adapter (pgx + sqlc typed queries + goose migrations) behind the domain's `todo.Repository` port: a `migrate` subcommand, a committed-and-drift-guarded sqlc layer, a real `/readyz` check, and container-backed integration tests. The port is the seam — implement it to back the template with a different datastore.
- An authorization tier (Cedar via `cedar-go`) with a deny-by-default Huma middleware, a modular per-resource authz slice pattern, and authentication deferred to the integrator: a placeholder API-key authenticator (`X-API-Key`/Bearer, backed by an `api_keys` table) and dev-only mock keys seeded for the Compose demo. Replace the authenticator with real authn — see step 6.
- Per-client rate limiting (on by default): a Huma middleware that throttles by client IP **before** authentication and returns RFC 9457 `429` with `Retry-After`. The shipped limiter is in-process (token bucket, `golang.org/x/time/rate`) behind a `ratelimit.Limiter` port — the seam for a distributed (for example, Redis-backed) limiter. See the README's [Rate limiting](README.md#rate-limiting) section.
- OpenTelemetry distributed tracing (opt-in via `--tracing-enabled`): inbound HTTP server spans (otelhttp, named by operation) and PostgreSQL query spans (otelpgx), exported over OTLP/HTTP and configured through the standard `OTEL_*` env vars. Off by default since it needs a collector. See the README's [Tracing](README.md#tracing) section.
- A Cobra/Viper entrypoint under `cmd/template-go-api` and `internal/cli` exposing `serve` (default), `version`, `openapi`, and `migrate`.
- Moon tasks for `format`, `lint`, `build`, `test`, and `check`, plus `sqlc` / `sqlc-check` (regenerate and drift-guard the typed query layer), `mockery` / `mockery-check` (regenerate and drift-guard the testify mocks), `migrate` (run database migrations), and `test-integration` (container-backed adapter tests).
- `golangci-lint`, `sqlc`, `goose`, and `mockery` wired through Proto and Moon.
- CI that delegates to `moon ci --summary minimal` with pinned actions, dependency caches, and minimal token permissions.
- A scheduled container vulnerability scan that uploads SARIF results to GitHub code scanning.
- Dependabot coverage for GitHub Actions, Docker base images, Go modules, and the docs uv project.
- MkDocs Material docs under `docs/` that render the exported OpenAPI spec as an API reference (neoteroi OAD), published to GitHub Pages, with a CI drift-guard that fails if the committed spec falls out of sync with the code.
- Repository settings for signed commits, squash-only merges, immutable releases, private vulnerability reporting, and protected tags.
- Release workflows for Release Please, GoReleaser binary assets, GHCR container images, checksums, SBOMs, and GitHub artifact attestations.
- A root `ghd.toml` package manifest so released binaries can be installed with `ghd`.

## How It Works

Moon is the main entrypoint for local development and CI:

```sh
moon run root:check
```

That aggregate check runs the Go formatter/linter/build/tests plus the docs build.
The GitHub Actions CI workflow runs the same path through:

```sh
moon ci --summary minimal
```

The workflow caches Go modules, Go build artifacts, golangci-lint state, and uv's download cache through GitHub Actions. If that is not enough for a larger generated repository, add Moon remote caching later with Depot or another Bazel Remote Execution-compatible backend and repository credentials.

The `GitHub Pages` workflow builds the MkDocs site on pull requests and deploys the default-branch `docs/build` output to Pages. The repository settings manifest defaults Pages to workflow-based publishing with HTTPS enforcement.

The release machinery is intentionally enabled in the template repository so the starter app proves Release Please, GoReleaser binary releases, native-runner container image builds, artifact validation, and attestations before generated projects inherit the setup.
The nominal generated-project path is an HTTP service with both a downloadable binary and a container image. If the new project is binary-only, container-only, or a pure Go library, trim the release files as described below before the first release.

## First Setup Checklist

1. Rename the Go module:

   ```sh
   go mod edit -module github.com/meigma/YOUR_REPO
   ```

2. Choose the project shape.

   Most applications should keep both the binary and container paths. For other shapes:

   - Binary plus container: keep the default layout and update names.
   - Binary only: keep GoReleaser and `ghd.toml`; remove the container release jobs and `melange.yaml`/`apko.yaml` if the project will not ship images.
   - Container only: keep `melange.yaml`/`apko.yaml` and the container jobs; remove GoReleaser release assets and `ghd.toml` if users should not install a standalone binary.
   - Library only: remove the CLI, `melange.yaml`/`apko.yaml`, GoReleaser, `ghd.toml`, and publish workflow pieces. Keep Release Please only if the library should still get changelogs, tags, and draft GitHub releases.

3. For a binary-producing project, rename the binary directory:

   ```sh
   mv cmd/template-go-api cmd/YOUR_BINARY
   ```

   For a library-only project, delete `cmd/template-go-api`, remove or rewrite `internal/cli`, and remove Cobra/Viper dependencies that are no longer used.

4. Replace template placeholders:

   ```sh
   rg "template-go-api|TEMPLATE_GO_API|github.com/meigma/template-go-api"
   ```

   Update Go imports, Moon metadata, README text, docs text, and CLI environment variable prefixes. For release-bearing projects, also update `.goreleaser.yaml`, `release-please-config.json`, `ghd.toml`, `melange.yaml`, `apko.yaml`, and `.github/workflows/release*.yml` as applicable.
   Update `docs/mkdocs.yml` with the generated repository's GitHub Pages URL, usually `https://OWNER.github.io/REPO/`.
   Remember the `TEMPLATE_GO_API_` environment-variable prefix is set in `internal/cli/root.go` (`SetEnvPrefix`); rename it to match the new module.

5. Replace the example resource.

   The `todo` resource is a reference slice that demonstrates the hexagonal seams, not a product feature. To make it your own:

   - Add a domain package `internal/<resource>` (entity, `Repository` port, and `Service`), mirroring `internal/todo`. Each resource owns its adapters nested beneath it (`internal/<resource>/{httpapi,postgres}`).
   - Implement the port in a nested adapter: mirror `internal/todo/postgres` (pgx + sqlc, on the shared pool/migrations in `internal/adapter/postgres`). The README's [Persistence](README.md#persistence) section covers the migration and sqlc-regeneration workflow. sqlc generates one package per `sql:` block, so add a second `sql:` entry in `sqlc.yaml` for the new resource and update the literal paths in the `sqlc-check`, `mockery`, and `mockery-check` tasks (`moon.yml`) so the new generated/mocked packages stay drift-guarded.
   - Add a transport adapter `internal/<resource>/httpapi` (DTOs, domain mapping, error translation, and a `Register` function), mirroring `internal/todo/httpapi`. **Bound any collection (list) endpoint:** mirror the todo keyset pagination — a `limit` query param (default/max from constants, surfaced as `minimum`/`maximum`/`default` tags) and an opaque `cursor`/`nextCursor` over a stable `(created_at, id)` order — so a single request can never materialize the whole table.
   - Add an authz slice `internal/<resource>/authz` (policies, actions, fact resolver), tag the `httpapi` operations with `authz.Require`/`authz.Public`, and merge its `Contribution` into `authz.New` in `internal/app/app.go`, mirroring `internal/todo/authz`. Authorization is deny-by-default, so an untagged operation is rejected (see the README's [Authorization](README.md#authorization) section). If you are dropping authorization, see step 6.
   - Add one `Register` call in `registerResources` in `internal/app/app.go`, mounting the resource on the `/v1` version group (see the README's [API versioning](README.md#api-versioning) section for how versions are grouped and how a later `/v2` is added).
   - When you wire a real datastore, add a readiness check to the `Readiness` slice in `internal/app/app.go` so `/readyz` reflects it (the PostgreSQL adapter shows the pattern with its `Ping` check).
   - Put cross-package integration tests in `internal/integration` (package `integration`, `//go:build integration`), run via `moon run root:test-integration`; keep fast unit tests beside the code they cover. Repository doubles come from the mockery-generated mocks in `internal/<resource>/mocks` (register the new port in `.mockery.yaml`); a stateful in-memory fake for end-to-end tests lives in `internal/todo/todotest`. The README's [Testing](README.md#testing) section covers the split.
   - Run `moon run root:openapi` to refresh `docs/docs/openapi.yaml` after changing the API, `moon run root:sqlc` after changing PostgreSQL migrations or queries, and `moon run root:mockery` after changing a mocked port (then commit each); all three CI drift-guards fail if the committed output is stale.

   If your project never needs SQL persistence, replace `internal/todo/postgres` with your own `todo.Repository` adapter (the port stays), and you can delete the shared `internal/adapter/postgres`, `internal/integration`, `sqlc.yaml`, the `sqlc`/`sqlc-check`/`migrate`/`test-integration` Moon tasks, the `migrate` subcommand, the `--database-url`/`--db-max-conns` config flags, the `sqlc` and `goose` tool pins in `mise.toml` (then re-run `mise lock`), the `run.build-tags` entry in `.golangci.yml`, and the pgx/goose/testcontainers Go dependencies, then run `go mod tidy` (the sqlc and goose CLIs are mise tools, not Go modules).

   Keep the generic transport in `internal/adapter/http` (router, middleware, `/healthz`/`/readyz`/`/metrics`, RFC 9457 fallbacks, the `Registrar` seam), `internal/config`, and `internal/observability` as-is unless you have a reason to change them.

6. Wire real authentication (and prune or keep the authorization tier).

   The template ships an authorization tier (Cedar via `cedar-go`, deny-by-default Huma middleware) with authentication **deferred to you**. The shipped API-key authenticator is a placeholder, not a security mechanism. Address it before the first real deployment:

   - **Replace the shipped API-key authenticator (first priority).** It is a placeholder meant only to demonstrate the flow — it has no key rotation, expiry, or scoping. Implement `authz.Authenticator` with a real verifier (JWT via `lestrrat-go/jwx`, OIDC via `coreos/go-oidc`, sessions, etc.) and inject it with `app.WithAuthenticator` in `internal/app/app.go`. Map the verified claims (subject, roles/groups) into the `authz.Principal`. The store already hashes keys at rest — the `api_keys` table holds only a SHA-256 digest (`key_hash`), and the store hashes the presented key before lookup (see `internal/authz/apikey/store.go`), so a table dump leaks no replayable credentials. If you keep the API-key store, mint each production key out of band and insert only its hash, e.g. `printf '%s' "$KEY" | sha256sum` (the hex digest) into `INSERT INTO api_keys (key_hash, subject, roles) VALUES ('<digest>', '<subject>', ARRAY['<role>'])`, and never seed the dev mock keys.
   - **Delete the dev mock-keys seed regardless:** remove `hack/sql/0002_seed_api_keys.sql` (insecure public credentials, dev-only). Real deployments insert their own `api_keys` rows out of band and never apply `hack/sql/`.

   To remove the authorization tier **entirely** (surgical, the slice pattern keeps it self-contained):

   - Delete the base engine `internal/authz` (this also removes `internal/authz/apikey`, the shipped authenticator + PostgreSQL `APIKeyStore`, and the base mockery doubles under `internal/authz/mocks` and `internal/authz/apikey/mocks`).
   - Delete the todo authz slice `internal/todo/authz` (its `policy.cedar`, actions, and fact resolver).
   - Delete the `api_keys` goose migration `internal/adapter/postgres/migrations/00002_create_api_keys.sql` and the dev seed `hack/sql/0002_seed_api_keys.sql`.
   - Untag the `httpapi` routes: remove the `Metadata: authz.Require(...)` lines and the `authz`/`todoauthz` imports from `internal/todo/httpapi/handler.go`.
   - Remove the authz wiring from the composition root `internal/app/app.go` (`authzInstaller`/`resolveAuthenticator`, the `WithAuthenticator` option, the `InstallAuthz`/`FinalizeAuthz` hooks, and the `DocumentSecurity` call in the spec exporter).
   - Remove the `--authz-enabled` and `--authz-policy-dir` flags (and the `AuthzEnabled`/`AuthzPolicyDir` fields) from `internal/config/config.go`.
   - Remove the new authz ports from `.mockery.yaml`.
   - Run `go mod tidy` to drop `github.com/cedar-policy/cedar-go`.
   - **No `sqlc` regen is needed:** `omit_unused_structs: true` in `sqlc.yaml` already keeps the todo sqlc package todo-only, so the `api_keys` table never produced any todo-sqlc output and dropping the migration changes nothing there.
   - Drop the authz integration/e2e coverage in `internal/integration` (the container-backed `APIKeyStore` test and the functional authz tests).

   As a quick alternative for incremental adoption, set `--authz-enabled=false` (env `TEMPLATE_GO_API_AUTHZ_ENABLED=false`) to bypass the middleware entirely without deleting anything.

7. Refresh module metadata:

   ```sh
   go mod tidy
   ```

8. Configure releases for the chosen shape.

   For the nominal binary plus container case:

   - Update `.goreleaser.yaml`: `project_name`, build `id`, `main`, binary name, archive name template, and any linked package paths.
   - Update `ghd.toml`: `provenance.signer_workflow`, package name, description, asset patterns, and installed binary path.
   - Update `apko.yaml` (entrypoint, OCI labels/annotations, packages) and `melange.yaml` (the `go/build` pipeline) — e.g. the binary path, image labels, or the runtime command if this is a service instead of a CLI.
   - Update `.github/workflows/release.yml`: `IMAGE_NAME`, binary validation names, container labels, summary commands, and verification examples.
   - Update `.github/workflows/release-dry-run.yml`: binary validation names, local container image name, and smoke-test commands.
   - Update `.github/workflows/security-scan.yml`: local container image name and scan category.
   - Update `.github/repository-settings.toml` only if required status-check names change.

   For binary-only projects:

   - Keep `.goreleaser.yaml`, `ghd.toml`, `Release Please`, `Binary Release Dry Run`, and the binary asset portions of `release.yml`.
   - Remove the `container-image-release` job, container verification summary text, and `Container Image Dry Run`.
   - Remove `melange.yaml`, `apko.yaml`, the `image-local`/`stack-up` mise tasks, and `.github/workflows/security-scan.yml` if no container build remains.
   - Remove `Container Image Dry Run` from required branch checks.

   For container-only projects:

   - Keep `Release Please`, `Container Image Dry Run`, `container-image-release`, `melange.yaml`, and `apko.yaml`.
   - Remove `.goreleaser.yaml`, `ghd.toml`, `binary-release-assets`, binary verification summary text, and `Binary Release Dry Run`.
   - Change `container-image-release` so it depends only on `resolve-release`.
   - Remove `Binary Release Dry Run` from required branch checks.

   For library-only projects:

   - Keep Release Please if version tags and changelogs are useful.
   - Delete `.github/workflows/release.yml`, `.github/workflows/release-dry-run.yml`, `.github/workflows/attest.yml`, `.github/workflows/security-scan.yml`, `.goreleaser.yaml`, `ghd.toml`, `melange.yaml`, and `apko.yaml` unless the library publishes some other artifact.
   - Remove release dry-run checks from `.github/repository-settings.toml`.
   - If the library should not create releases at all, delete `.github/workflows/release-please.yml`, `release-please-config.json`, `.release-please-manifest.json`, and `CHANGELOG.md`.

   In every release-bearing project, configure the release app credentials, protected-tag bypass, and repository package permissions before the first release. Run the release dry-run workflow after these edits and before merging the first release PR.

9. Run the full local check:

   ```sh
   moon run root:check
   ```

10. Update project-facing docs:

   - Rewrite `README.md` for the actual project.
   - Review `CONTRIBUTING.md` and `SECURITY.md`.
   - Add a real license before publishing the repository.

11. Decide on the agent-session tooling.

   The template ships Meigma's agent-session protocol as committed content: `.session.md`, `AGENTS.md` (with `CLAUDE.md` symlinked to it), the repo-local skills under `.agents/skills/`, and the journal seed under `scaffold/.journal/`. It is part of the standard Meigma workflow — keep it as-is if your project uses agent sessions. If it does not, remove these together: `AGENTS.md`/`CLAUDE.md` route agents into `.session.md`, and `CLAUDE.md` hard-fails when `.session.md` is absent, so do not delete one without the others.

12. Delete this file:

   ```sh
   rm DELETE_ME.md
   ```
