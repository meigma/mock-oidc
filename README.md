# template-go-api

`template-go-api` is the Meigma starter for building Go web (HTTP) API services.
It ships a runnable, hexagonal API server — built on [chi](https://github.com/go-chi/chi)
and [Huma](https://huma.rocks) with a `todo` example resource — plus the shared
Meigma repository baseline: Moon tasks, pinned CI, Dependabot, baseline security
settings, and an enabled Release Please and GoReleaser release layer.

Persistence is a PostgreSQL adapter behind the domain's `todo.Repository` port
([pgx](https://github.com/jackc/pgx) + [sqlc](https://sqlc.dev) typed queries,
[goose](https://github.com/pressly/goose) migrations). The port stays the seam:
implement it to back the template with a different datastore without touching the
domain or transport.

The example resource is a reference slice, not a product feature: swap it for
your own resource and keep the persistence wiring.

## Prerequisites

- [mise](https://mise.jdx.dev) — provisions every pinned tool from `mise.toml` +
  `mise.lock`: Go, Moon, Python + uv (for the MkDocs docs project), the
  `golangci-lint`/`sqlc`/`mockery`/`goose` CLIs, and `melange`/`apko`/`cosign` for
  releases. Run `mise install` once; there is nothing else to install by hand.
- Docker (to run a local PostgreSQL for the server — see [Persistence](#persistence)
  and [Local stack](#local-stack-docker-compose) — and for the container-backed
  integration tests)

Tool versions live in `mise.toml`; `mise.lock` records a per-platform download URL
and checksum for each (and, for the aqua-backed CLIs, cosign/SLSA/GitHub-attestation
verification). `mise install` runs with `locked = true`, so it **fails closed** if a
tool lacks a pre-resolved, checksummed entry for the current platform — replacing the
former Proto `checksum-url` pins and the bespoke `.moon/proto/sqlc.sha256` digest
(sqlc publishes no upstream checksum). Moon runs every task against these tools as
`system` binaries on PATH and manages no toolchain itself. To bump a tool, edit its
version in `mise.toml`, run
`mise lock --platform linux-x64,linux-arm64,macos-x64,macos-arm64`, and commit
`mise.toml` + `mise.lock`.

> **New repository from this template?** Work through [DELETE_ME.md](DELETE_ME.md)
> first — it covers renaming the module, binary, image, and env prefix, and
> replacing the example resource.

## Quickstart

The server persists to PostgreSQL, so running it needs a database. The fastest
way to bring up the whole stack — database, migrations, seed data, and the API —
is Docker Compose:

```sh
mise run stack-up     # API on :8080, /metrics on :9090 (see "Local stack" below)
```

To build the binary and run it against your own PostgreSQL instead, see
[Persistence](#persistence) (`serve` is the default subcommand and needs
`--database-url`):

```sh
moon run root:build          # or: go build -o bin/template-go-api ./cmd/template-go-api
```

With the stack up, exercise the example `todo` API. The todo routes are
protected by the [Authorization](#authorization) tier (on by default), so each
request carries one of the dev keys the Compose stack seeds — `dev-user-key`
sent via the `X-API-Key` header:

```sh
# Without a key, protected routes reject the request:
curl -sS -o /dev/null -w '%{http_code}\n' localhost:8080/v1/todos
# => 401

# Create a todo (the user key has the role the todo policy requires):
curl -sS -X POST localhost:8080/v1/todos \
  -H 'X-API-Key: dev-user-key' \
  -H 'content-type: application/json' \
  -d '{"title":"buy milk"}'
# => 201 {"$schema":"...","id":"...","title":"buy milk","status":"open","createdAt":"..."}

curl -sS -H 'X-API-Key: dev-user-key' localhost:8080/v1/todos                          # list (first page, default 20)
curl -sS -H 'X-API-Key: dev-user-key' 'localhost:8080/v1/todos?limit=50&cursor=<next>' # next page
curl -sS -H 'X-API-Key: dev-user-key' localhost:8080/v1/todos/<id>          # fetch one (404 if unknown)
curl -sS -X POST -H 'X-API-Key: dev-user-key' localhost:8080/v1/todos/<id>/complete   # mark complete

# Validation and not-found errors use RFC 9457 problem+json:
curl -sS -i -X POST localhost:8080/v1/todos \
  -H 'X-API-Key: dev-user-key' -H 'content-type: application/json' -d '{"title":""}'
# => 422 application/problem+json
```

These mock keys are dev-only seeds; see [Authorization](#authorization) for how
authn/authz work and how to plug in a real authenticator.

Resource routes are served under a `/v1` URL version prefix; the operational
endpoints below (`/healthz`, `/readyz`, `/metrics`, `/docs`, `/openapi.*`) are
not part of the versioned contract and stay at the root. See
[API versioning](#api-versioning) for how a later `/v2` is added.

`GET /v1/todos` is **keyset-paginated**: it returns at most `limit` todos (default
20, max 100; an out-of-range `limit` is a 422) ordered by `(createdAt, id)`, plus
an opaque `nextCursor`. Pass that cursor back as `?cursor=` to fetch the next
page; the last page omits it. The bound is applied even when `limit` is absent,
so a single request can never materialize the whole table — the reference
pattern for a bounded collection endpoint. Cross-cutting **rate limiting** is a
deliberate future seam and is not shipped.

Operational endpoints:

```sh
curl -sS localhost:8080/healthz   # liveness  => {"status":"ok"}
curl -sS localhost:8080/readyz    # readiness => {"status":"ready","checks":{}}
curl -sS localhost:9090/metrics   # Prometheus exposition (separate listener)
```

`/metrics` is served on a dedicated listener (`--metrics-addr`, default `:9090`)
so it stays off the public API surface and outside the API middleware chain; set
`--metrics-addr ""` to co-locate it on the API port instead.

The running server also serves interactive API docs at `/docs` (Stoplight
Elements) and the live spec at `/openapi.yaml` and `/openapi.json`.

## Local stack (Docker Compose)

`mise run stack-up` brings up the **full** template against PostgreSQL —
no local Go toolchain or database setup required. The stack also seeds the
dev-only mock API keys (`hack/sql/0002_seed_api_keys.sql`), so the
[Authorization](#authorization) tier is exercised end to end out of the box:

```sh
mise run stack-up

# Authorization is on by default. With no key, a protected route returns 401:
curl -sS -o /dev/null -w '%{http_code}\n' localhost:8080/v1/todos   # => 401

# With the seeded dev user key, the same route returns 200 and the seeded todos:
curl -sS -H 'X-API-Key: dev-user-key' localhost:8080/v1/todos       # => 200, the seeded todos

curl -sS localhost:8080/readyz   # => {"status":"ready","checks":{"postgres":"ok"}}
```

`/readyz` (and the other operational endpoints) are raw routes outside the Huma
authorization middleware, so they need no key.

Startup is an ordered DAG, because migrations are explicit (the server never runs
them) and the seed data needs the schema to exist first:

| Step | Service    | What it does                                                          |
|------|------------|----------------------------------------------------------------------|
| 1    | `postgres` | PostgreSQL 17 with a known config; the stack waits for `pg_isready`.  |
| 2    | `migrate`  | One-shot `migrate up` — applies the embedded goose migrations.        |
| 3    | `seed`     | One-shot — applies every `hack/sql/*.sql` (sorted) with `psql`.       |
| 4    | `api`      | Serves the API against the prebaked connection string.               |

The database is **ephemeral and reproducible**: no volume is persisted, so every
`up` rebuilds a clean database, re-runs migrations, and re-applies the seeds;
`docker compose down` discards it.

Prepopulate local data by dropping SQL files in [`hack/sql/`](hack/sql/) — they
run after the schema exists, so you can `INSERT` straight into tables like `todos`
without touching migrations or adding setup code to the server. The bundled
`hack/sql/0001_seed_todos.sql` seeds a few todos so the API returns data on the
first request, and `hack/sql/0002_seed_api_keys.sql` seeds the **dev-only mock
API keys** (`dev-user-key`, `dev-admin-key`) that the [Authorization](#authorization)
demo uses. These seeds are local-development only and never reach a real
deployment (migrations run everywhere; `hack/sql/` does not).

## Commands

| Command | Description |
| --- | --- |
| `serve` (default) | Run the HTTP API server. |
| `version` | Print version, commit, and build date. |
| `openapi` | Write the OpenAPI 3.0.3 spec to stdout or a file (`--output/-o`). |
| `migrate up\|down\|status` | Apply, roll back, or report the embedded PostgreSQL migrations against `--database-url`. |

```sh
./bin/template-go-api openapi -o docs/docs/openapi.yaml
./bin/template-go-api version
./bin/template-go-api migrate status --database-url postgres://app:app@localhost:5432/app?sslmode=disable
```

## Configuration

Flags bind to Viper, so every setting is also a `TEMPLATE_GO_API_*` environment
variable (uppercase, dashes become underscores). Precedence is flag > env >
default.

| Flag | Env var | Default | Description |
| --- | --- | --- | --- |
| `--addr` | `TEMPLATE_GO_API_ADDR` | `:8080` | host:port the API listens on |
| `--metrics-addr` | `TEMPLATE_GO_API_METRICS_ADDR` | `:9090` | dedicated `/metrics` listener; empty serves `/metrics` on `--addr` |
| `--log-level` | `TEMPLATE_GO_API_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `--log-format` | `TEMPLATE_GO_API_LOG_FORMAT` | `json` | `json` or `text` |
| `--read-timeout` | `TEMPLATE_GO_API_READ_TIMEOUT` | `5s` | reading an entire request |
| `--read-header-timeout` | `TEMPLATE_GO_API_READ_HEADER_TIMEOUT` | `5s` | reading request headers |
| `--write-timeout` | `TEMPLATE_GO_API_WRITE_TIMEOUT` | `10s` | writing the response |
| `--idle-timeout` | `TEMPLATE_GO_API_IDLE_TIMEOUT` | `120s` | idle keep-alive connections |
| `--request-timeout` | `TEMPLATE_GO_API_REQUEST_TIMEOUT` | `15s` | per-request processing |
| `--shutdown-grace` | `TEMPLATE_GO_API_SHUTDOWN_GRACE` | `15s` | graceful shutdown window |
| `--cors-allowed-origins` | `TEMPLATE_GO_API_CORS_ALLOWED_ORIGINS` | _(none)_ | allowed CORS origins (comma-separated); empty disables CORS |
| `--trusted-proxy-header` | `TEMPLATE_GO_API_TRUSTED_PROXY_HEADER` | _(none)_ | proxy header to read the client IP from (e.g. `X-Real-IP`); empty trusts the TCP peer |
| `--database-url` | `TEMPLATE_GO_API_DATABASE_URL` | _(none)_ | PostgreSQL connection URL (**required**) |
| `--db-max-conns` | `TEMPLATE_GO_API_DB_MAX_CONNS` | `0` | maximum PostgreSQL pool connections; `0` uses the driver default |
| `--authz-enabled` | `TEMPLATE_GO_API_AUTHZ_ENABLED` | `true` | enable the [authorization](#authorization) middleware (deny-by-default); `false` bypasses it entirely |
| `--authz-policy-dir` | `TEMPLATE_GO_API_AUTHZ_POLICY_DIR` | _(none)_ | directory of `.cedar` files to load instead of the embedded policies; empty uses the embedded set |
| `--rate-limit-enabled` | `TEMPLATE_GO_API_RATE_LIMIT_ENABLED` | `true` | enable per-client [rate limiting](#rate-limiting); `false` disables throttling entirely |
| `--rate-limit-rps` | `TEMPLATE_GO_API_RATE_LIMIT_RPS` | `10` | sustained per-client request rate (requests/second) |
| `--rate-limit-burst` | `TEMPLATE_GO_API_RATE_LIMIT_BURST` | `20` | per-client burst size (token-bucket depth) |
| `--tracing-enabled` | `TEMPLATE_GO_API_TRACING_ENABLED` | `false` | enable OpenTelemetry [tracing](#tracing); the OTLP exporter is configured via the standard `OTEL_*` env vars |

CORS is off until you set origins. Client IP is read from the direct TCP peer
unless you opt into a trusted proxy header — never from `X-Forwarded-For`
implicitly — so the default is not spoofable.

`--database-url` is **required**; the server rejects a missing URL at startup.

## Persistence

The `todo.Repository` port is implemented by a PostgreSQL adapter under
`internal/todo/postgres`: [sqlc](https://sqlc.dev) type-safe queries over a
[pgx](https://github.com/jackc/pgx) connection pool, with
[goose](https://github.com/pressly/goose) migrations. The shared connection pool
and migration machinery stay under `internal/adapter/postgres` as database-level
concerns. The composition root in `internal/app/app.go` wires the adapter and
registers a `postgres` readiness check so `/readyz` reflects database
connectivity.

The port is the extension seam: to back the template with a different datastore,
implement `todo.Repository` in a new adapter and wire it in `app.go` (or inject it
via `app.WithRepository`) — the domain and transport layers stay untouched.

### Running with PostgreSQL

Start a database (any reachable PostgreSQL works; this is just an example):

```sh
docker run --rm -d --name template-pg \
  -e POSTGRES_PASSWORD=app -e POSTGRES_USER=app -e POSTGRES_DB=app \
  -p 5432:5432 postgres:17-alpine
export TEMPLATE_GO_API_DATABASE_URL='postgres://app:app@localhost:5432/app?sslmode=disable'
```

Apply migrations, then serve against the database:

```sh
./bin/template-go-api migrate up           # create the schema
./bin/template-go-api serve                # reads TEMPLATE_GO_API_DATABASE_URL
curl -sS localhost:8080/readyz             # => {"status":"ready","checks":{"postgres":"ok"}}
```

`--database-url` (env `TEMPLATE_GO_API_DATABASE_URL`) is shared by `serve` and
`migrate`. The connection URL can also be passed as a flag instead of an
environment variable.

### Migrations

Migrations live in `internal/adapter/postgres/migrations/*.sql` (goose format) and
are embedded in the binary. They are **explicit** — `serve` never runs them, which
avoids multi-replica races. The `migrate` subcommand drives goose as a library:

```sh
./bin/template-go-api migrate up       # apply all pending migrations
./bin/template-go-api migrate status   # show applied/pending versions
./bin/template-go-api migrate down     # roll back the most recent migration
```

Moon wraps the same command for local dev (arguments after `--` pass through):

```sh
moon run root:migrate -- up --database-url "$TEMPLATE_GO_API_DATABASE_URL"
```

Scaffold a new migration file with the goose CLI (provided by mise), then edit its
`-- +goose Up` / `-- +goose Down` sections:

```sh
goose -dir internal/adapter/postgres/migrations create add_something sql
```

Because sqlc reads the migrations directory as its schema, a schema change means
regenerating the typed query layer (below).

### Type-safe queries (sqlc)

Hand-written queries live in `internal/todo/postgres/queries/todos.sql`; sqlc
generates the typed Go in `internal/todo/postgres/sqlc/` from those queries and
the migration schema. The generated package is **committed and drift-guarded**
(mirroring the `openapi` / `openapi-check` pattern). After changing a migration or
a query, regenerate and commit:

```sh
moon run root:sqlc       # regenerate internal/todo/postgres/sqlc/
```

`moon run root:sqlc-check` (part of `root:check`) regenerates into a throwaway
directory and fails if the committed code is stale, so the generated layer can
never drift from the schema and queries. The `sqlc.yaml` config maps the `uuid`
column to `github.com/google/uuid.UUID` and `timestamptz` to `time.Time` /
`*time.Time`; the adapter converts to and from the domain's `string` ID and
`Status` at the mapping boundary.

### Integration tests

Integration tests live in their own package, `internal/integration` (package
`integration`, `//go:build integration`), separate from the unit tests that sit
beside the code, and drive the adapters through their public APIs. The suite is
container-backed and behind the build tag, so the default `go test ./...` and
`moon run root:check` stay hermetic (no Docker). It uses
[testcontainers](https://golang.testcontainers.org/) to spin a throwaway
`postgres:17-alpine`, applies the embedded migrations, and snapshots the clean
schema for fast per-test isolation. It requires a running Docker daemon:

```sh
moon run root:test-integration   # or: go test -tags integration ./internal/integration/...
```

The container-backed integration suite runs in CI through `moon ci` on the
Docker-capable `ubuntu-latest` runner. It stays out of the hermetic
`moon run root:check` aggregate, so a local `check` never needs Docker.

### Dynamic queries

This template ships **no query-builder dependency**. The three port methods are
fixed queries, and `List` is parameter-free. For optional filtering that sqlc can
still type-check, use `sqlc.narg()` (nullable named arguments) — a commented
example lives in `queries/todos.sql`: a `NULL` argument disables the filter, a
non-`NULL` value applies it, all in one prepared statement.

When you genuinely need queries assembled at runtime (variable column sets,
arbitrary `AND`/`OR` trees), keep that complexity **inside the adapter, behind the
port**. Define a criteria struct on the port and translate it to SQL in the
PostgreSQL adapter, reaching for [Squirrel](https://github.com/Masterminds/squirrel)
or [Bob](https://github.com/stephenafamo/bob) *there*:

```go
// In the domain port (internal/todo) — no builder types appear here.
type TodoFilter struct {
    Status *Status // nil means "any status"
}

// List(ctx context.Context, f TodoFilter) ([]Todo, error)
```

The rule: query-builder types must never appear in a port signature. The domain
speaks in domain criteria; only the adapter knows SQL. Swapping Squirrel for Bob,
or back to plain sqlc, then stays a change inside one package.

## Authorization

The template ships an authorization tier built on
[AWS Cedar](https://www.cedarpolicy.com/) via
[`cedar-policy/cedar-go`](https://github.com/cedar-policy/cedar-go) as the
embedded policy engine. Policies are real `.cedar` source, embedded in the binary
(or loaded from a directory with `--authz-policy-dir`), and evaluated by one
global Huma middleware.

The posture is **deny-by-default**: every Huma API operation must declare its
authorization requirement, and an operation that declares none is denied
(fail-closed) and logged. Denials are returned as RFC 9457 problem responses —
`401` when no/invalid credential was presented, `403` when an authenticated
caller lacks the required role. The operational routes (`/healthz`, `/readyz`,
`/metrics`, `/openapi.*`, `/docs`) are raw chi routes outside the Huma
middleware, so they are never gated.

`--authz-enabled` (default `true`) is the master switch; setting it `false`
bypasses the middleware entirely (an escape hatch for incremental adoption or
local debugging). The declaration also populates each operation's OpenAPI
`security`, so protection is visible in the generated spec and at `/docs`.

### Authentication is deferred to the integrator

Authentication is **not** something the template decides for you. It ships the
*seam* — an `Authenticator` interface (`internal/authz`) — plus one **replaceable
starting point**: an API-key authenticator (`internal/authz/apikey`).

The shipped authenticator reads a key from the `X-API-Key` header or an
`Authorization: Bearer <key>` credential and resolves it through an `APIKeyStore`
port to a principal (subject + roles). The shipped store is PostgreSQL-backed:
the `api_keys` table (created by migration `00002_create_api_keys.sql`) stores
only a **SHA-256 hash** of each key (`key_hash`), never the key itself, and the
store hashes the presented credential before lookup — so a table or backup
disclosure exposes no replayable credentials. This is still a real but minimal
mechanism — enough to demonstrate the full flow — **not** production authn:
replacing it with a real verifier is called out in [DELETE_ME.md](DELETE_ME.md).

For local development the Compose stack seeds two mock keys via
`hack/sql/0002_seed_api_keys.sql`:

| Key            | Roles   | Authorized for                                              |
| -------------- | ------- | ---------------------------------------------------------- |
| `dev-user-key` | `user`  | all todo actions (the todo slice's policy)                 |
| `dev-admin-key`| `admin` | everything (the cross-cutting admin override, `base.cedar`)|

These are **insecure, public, dev-only** credentials; real deployments insert
their own `api_keys` rows and never apply `hack/sql/`.

To swap in real authn (JWT/OIDC/session), implement `authz.Authenticator` and
inject it with `app.WithAuthenticator`; nothing else in the tier changes.

### Modular, per-resource authorization

Authorization is **modular** in the same way the HTTP transport is: each domain
contributes an *authz slice* and the composition root merges all contributions
into one runtime engine. The todo slice lives at `internal/todo/authz` and
contributes three things via an `authz.Contribution`:

- **Policies** — embedded `policy.cedar` source (merged with `base.cedar` and
  every other slice's policies into one Cedar `PolicySet`).
- **Actions** — typed Cedar action identifiers (`todoauthz.ActionCreate`,
  `ActionRead`, `ActionUpdate`, `ActionList`), each of the form
  `Action::"todo:<verb>"`.
- **A fact resolver** — maps a `Todo` entity to its Cedar attributes/parents,
  loaded **lazily** (only when a policy dereferences a todo, which the shipped
  coarse policy never does).

The HTTP slice tags each operation with its action at registration, using
`authz.Require` (or `authz.Public` to opt out):

```go
huma.Register(api, huma.Operation{
    OperationID: "get-todo",
    Method:      http.MethodGet,
    Path:        "/todos/{id}",
    // Item operation: bind the {id} path param so the middleware sets
    // Resource = Todo::"<id>" straight from the matched route (no load).
    Metadata: authz.Require(todoauthz.ActionRead, "id"),
}, h.get)

huma.Register(api, huma.Operation{
    OperationID: "list-todos",
    Method:      http.MethodGet,
    Path:        "/todos",
    // Collection operation: type-level resource, no id bound.
    Metadata: authz.Require(todoauthz.ActionList),
}, h.list)
```

To add authorization to a **new** resource, mirror the todo slice:

1. Add `internal/<resource>/authz` with `policy.cedar` (embedded), typed action
   constants, a fact resolver, and a `Contribution(repo)` function.
2. Tag the resource's `httpapi` operations with `authz.Require(<action>[, idParam])`
   (or `authz.Public()` for an unauthenticated operation).
3. Add the slice's `Contribution(...)` to the slice list passed to `authz.New`
   in `internal/app/app.go` (alongside `todoauthz.Contribution(repo)`).

At runtime there is one merged `PolicySet` over one shared entity space, so a
policy in one slice can reference shared principal roles (`principal in
Role::"admin"`) or another slice's entities. Cross-cutting rules and shared
principal/role types live in the base package's `base.cedar`.

## Rate limiting

The API is rate limited per client out of the box (`--rate-limit-enabled`,
default true). The limiter is a Huma middleware installed **before**
authentication, so an over-limit request is rejected with `429 Too Many
Requests` before it reaches the credential store — protecting the auth path and
database from anonymous floods. The infrastructure routes (`/healthz`,
`/readyz`, `/metrics`) bypass Huma and are never limited.

Requests are keyed by **client IP** (the spoof-safe IP the
[`--trusted-proxy-header`](#configuration) logic resolves), allowing a burst of
`--rate-limit-burst` and a sustained `--rate-limit-rps` per second (a token
bucket). A throttled response is RFC 9457 `application/problem+json` and carries
a `Retry-After` header (whole seconds).

The shipped limiter is **in-process** (`golang.org/x/time/rate`), with per-key
buckets evicted after an idle period to bound memory. That is the right default
for a single instance; behind multiple replicas a shared backend keeps the limit
global. The `ratelimit.Limiter` port is the seam: implement `Allow` over a store
such as Redis and wire your adapter in `internal/app/app.go` — the middleware and
key function are unchanged. To limit authenticated callers instead of IPs, swap
the key function (`adapterhttp.ClientIPKeyFunc`) for one that reads the principal.

> The IETF [RateLimit header fields](https://datatracker.ietf.org/doc/draft-ietf-httpapi-ratelimit-headers/)
> (`RateLimit`/`RateLimit-Policy`) are still a draft and map only loosely onto a
> token bucket, so the template advertises the limit with the stable `Retry-After`
> header and leaves those headers as a documented enhancement.

## Tracing

Distributed tracing is [OpenTelemetry](https://opentelemetry.io)-based and
**opt-in** (`--tracing-enabled`, default false) because it needs an external
collector. When enabled, the server exports spans over **OTLP/HTTP** and is
configured entirely through the standard `OTEL_*` environment variables — there
are no bespoke endpoint or sampler flags:

```sh
TEMPLATE_GO_API_TRACING_ENABLED=true \
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
OTEL_SERVICE_NAME=template-go-api \
OTEL_TRACES_SAMPLER=parentbased_traceidratio OTEL_TRACES_SAMPLER_ARG=0.1 \
  ./bin/template-go-api serve --database-url ...
```

What is instrumented out of the box:

- **Inbound HTTP** — every request is a server span (`otelhttp`) that extracts
  W3C trace context for propagation. Spans are named by operation (for example
  `get-todo`) for low cardinality. The infrastructure routes (`/healthz`,
  `/readyz`, `/metrics`) are excluded so health checks and scrapes do not flood
  the backend.
- **PostgreSQL** — each query is a child span ([`otelpgx`](https://github.com/exaring/otelpgx)
  on the pool), so a trace shows the SQL under the request that issued it.

`service.name`/`service.version` default to the app name and build version and
are overridable via `OTEL_SERVICE_NAME` / `OTEL_RESOURCE_ATTRIBUTES`. The tracer
provider is flushed on graceful shutdown. To trace your own domain logic, start
child spans with the global tracer (`otel.Tracer(...)`) inside the service layer.

## Testing

Unit tests sit beside the code and use [Testify](https://github.com/stretchr/testify)
(`assert` / `require`). Repository doubles are **mockery-generated** testify mocks,
committed under `internal/todo/mocks` and drift-guarded like the sqlc layer:

```sh
moon run root:mockery        # regenerate internal/todo/mocks from the ports
```

`moon run root:mockery-check` (part of `root:check`) regenerates into a throwaway
directory and fails if the committed mocks are stale, so they can never drift from
the interfaces. Add a port to `.mockery.yaml`, then regenerate and commit. Use the
generated mock for interaction and error-injection assertions — see
`internal/todo/service_test.go`.

For tests that need a real, stateful store end to end — such as the HTTP
functional tests that create a todo and read it back — a small in-memory fake
lives in `internal/todo/todotest`, kept deliberately separate from the generated
mocks. The container-backed [integration tests](#integration-tests) cover the
PostgreSQL adapter against a real database.

## Project layout

The server follows pragmatic hexagonal (ports & adapters) layering: the domain
core depends on nothing in the adapters, and dependencies point inward.

```
cmd/template-go-api/        thin main; builds the Cobra root and executes
internal/
  cli/                      serve / version / openapi / migrate commands, Viper wiring
  config/                   server runtime config (flags + TEMPLATE_GO_API_* env)
  todo/                     domain: entity, Repository port, Service (the example)
    httpapi/                inbound transport: the todo resource's DTOs, mapping, handlers
    postgres/               outbound adapter: PostgreSQL Repository (pgx + sqlc)
      queries/              hand-written sqlc queries
      sqlc/                 generated, committed, drift-guarded query layer
    authz/                  the todo authz slice: policy.cedar, actions, fact resolver
    mocks/                  generated testify mock of the Repository port (mockery)
    todotest/               in-memory Repository fake for tests
  authz/                    base authz engine: Cedar Authorizer, deny-by-default middleware,
                            Require/Public declarations, Principal/Authenticator seam, base.cedar
    apikey/                 the shipped API-key Authenticator + PostgreSQL APIKeyStore
  adapter/                  shared, cross-domain infrastructure (not domain-specific)
    http/                   generic transport: chi router, middleware, RFC 9457 errors,
                            /healthz /readyz /metrics, OpenAPI export, Registrar seam
    postgres/               connection pool (Connect) + goose migrate library
      migrations/           embedded goose migrations (also sqlc's schema source)
  observability/            slog logger, request logging, Prometheus metrics
  logctx/                   carries the request-scoped logger on the context
  app/                      composition root: wires everything and runs the server
  integration/              container-backed integration tests (build tag: integration)
compose.yaml                day-one local stack: postgres + migrate + seed + api
hack/sql/                   *.sql seeds applied to the Compose database (local dev)
docs/                       MkDocs site; docs/docs/openapi.yaml is the exported spec
sqlc.yaml                   sqlc generation config (repo root)
.mockery.yaml               mockery generation config (repo root)
```

## Adding a resource

Replace or extend the `todo` example by following the same seams:

Each resource owns its code under `internal/<resource>/`: the domain core at the
package root, with its adapters nested beneath it.

1. Add a domain package `internal/<resource>` (entity + `Repository` port + `Service`), mirroring `internal/todo`.
2. Implement the port in a nested adapter — mirror `internal/todo/postgres` for a PostgreSQL-backed datastore (see [Persistence](#persistence) for the sqlc/goose workflow). sqlc generates one package per `sql:` block, so add a second `sql:` entry in `sqlc.yaml` for the new resource and update the literal paths in the `sqlc-check`, `mockery`, and `mockery-check` tasks (`moon.yml`) so the new generated/mocked packages stay drift-guarded.
3. Add a transport adapter `internal/<resource>/httpapi` (DTOs, domain mapping, error translation, and a `Register` function), mirroring `internal/todo/httpapi`.
4. Add an authz slice `internal/<resource>/authz` (policies, actions, fact resolver) and tag the `httpapi` operations with `authz.Require`/`authz.Public`, then merge its `Contribution` in `internal/app/app.go`. See [Authorization](#authorization) — deny-by-default means an untagged operation is rejected.
5. Add one `Register` call in `registerResources` in `internal/app/app.go`, onto the `/v1` version group (see [API versioning](#api-versioning)).

Shared, cross-domain infrastructure needs no changes: the generic transport in
`internal/adapter/http` and the connection pool / migrations in
`internal/adapter/postgres`. Because each resource's `postgres` package shares its
name with that shared infra (and, across resources, with each other), import the
per-resource adapters with aliases in `app.go` — as the `todopostgres` import
already shows. After changing the API, run `moon run root:openapi` to refresh the
committed spec (CI fails if it drifts). If you back the resource with PostgreSQL,
also add its readiness check to the `Readiness` slice in `internal/app/app.go` so
`/readyz` reflects it.

## API versioning

Resource routes are served under a URL version prefix (`/v1`); the operational
endpoints (`/healthz`, `/readyz`, `/metrics`, `/docs`, `/openapi.*`) are not part
of the versioned contract and stay at the root. The prefix is applied once, in
`registerResources` (`internal/app/app.go`), via Huma's group seam:

```go
v1 := huma.NewGroup(api, "/v1")
httpapi.Register(v1, todoService)
```

A `huma.Group` *is* a `huma.API`, so a resource's `Register` is identical whether
it mounts on a version group or the root API — and the authz middleware installed
on the root API is inherited by every grouped route. Each resource adapter keeps
declaring unprefixed paths (`Path: "/todos"`); the group prepends `/v1`, so the
running router and the committed OpenAPI document both report `/v1/todos`.

Introduce a breaking revision by adding a sibling group and registering the
changed resources on it; unchanged resources keep registering on `v1`:

```go
v1 := huma.NewGroup(api, "/v1")
v2 := huma.NewGroup(api, "/v2")
httpapi.Register(v1, todoService)        // unchanged endpoints stay on v1
todohttpapiv2.Register(v2, todoService)  // only the revised ones move to v2
```

Both versions then serve side by side and share one OpenAPI document. URL-path
versioning is the default a reference template should teach: it is discoverable,
cache- and proxy-friendly, and explicit in both the URL and the spec.

## Documentation

The MkDocs site publishes to GitHub Pages at
<https://meigma.github.io/template-go-api/>, including a generated
[API Reference](https://meigma.github.io/template-go-api/api/) rendered from the
OpenAPI spec. Build it locally with `moon run docs:build` or preview with
`moon run docs:serve`.

## Common tasks

Moon is the standard task front door:

```sh
moon run root:format
moon run root:lint
moon run root:build
moon run root:test
moon run root:check    # the aggregate gate CI runs via `moon ci --summary minimal`
```

Persistence- and testing-related tasks (see [Persistence](#persistence) and
[Testing](#testing)):

```sh
moon run root:sqlc              # regenerate the committed sqlc query layer
moon run root:mockery           # regenerate the committed testify mocks
moon run root:migrate -- up     # apply migrations (pass --database-url after --)
moon run root:test-integration  # container-backed adapter tests (needs Docker)
```

`root:check` already runs `sqlc-check` and `mockery-check` (drift guards for the
generated query layer and mocks) alongside the formatter, linter, build, tests,
and OpenAPI drift guard.

## Container Image

The image is built **without a Dockerfile**:
[melange](https://github.com/chainguard-dev/melange) compiles the binary into a
signed [Wolfi](https://github.com/wolfi-dev) apk (`melange.yaml`), and
[apko](https://github.com/chainguard-dev/apko) assembles it into a minimal,
multi-arch, non-root runtime image (`apko.yaml`) — the modern equivalent of the
former distroless image (uid 65532, ca-certificates, tzdata, no shell). Each
architecture builds natively (no QEMU). Build and run it locally with the bundled
mise task (it uses melange's Docker runner, so Docker must be running):

```sh
mise run image-local              # build the host-arch image, load as template-go-api:dev
docker run --rm -p 8080:8080 \
  -e TEMPLATE_GO_API_DATABASE_URL='postgres://<user>:<password>@host.docker.internal:5432/<db>' \
  template-go-api:dev
```

The server requires a database, so pass `TEMPLATE_GO_API_DATABASE_URL` pointing at
a PostgreSQL reachable from inside the container — on Docker Desktop,
`host.docker.internal` resolves to a database running on the host (see
[Running with PostgreSQL](#running-with-postgresql)). For a fully wired
API + PostgreSQL run, use the Compose stack (`mise run stack-up`) instead.

The Wolfi base intentionally floats to the latest packages (fresh CA bundle and
timezones, low CVE surface); the exact resolved versions are recorded in the
per-build SBOM and provenance attestation rather than pinned. `version`, `commit`,
and `date` are stamped into the binary via melange `--vars-file` — the release
workflow supplies the real values, and `mise run image-local` uses `dev`.

## CI and Security

The default CI workflow keeps permissions minimal, pins external actions, disables checkout credential persistence, and delegates checks to Moon.
It uses GitHub-hosted dependency caches for Go, golangci-lint, and uv download artifacts while leaving Moon remote caching as an optional follow-up for repositories that need a shared task-output cache.
The docs workflow builds the MkDocs site on pull requests and deploys `docs/build` to GitHub Pages from the default branch.
The scheduled security scan workflow builds the local container image weekly, scans it for high/critical fixed vulnerabilities, and uploads SARIF results to GitHub code scanning.
Dependabot covers GitHub Actions, Docker base images, the root Go module, and the docs uv project.

The build CLIs are pinned in `mise.toml` and locked in `mise.lock`, which records a per-platform download URL and checksum for every tool. `mise install` runs with `locked = true`, so it fails closed if any tool lacks a pre-resolved, checksummed entry for the current platform; the aqua-backed CLIs (`golangci-lint`, `sqlc`, `mockery`, `goose`, `moon`, `melange`, `apko`, `cosign`) additionally verify cosign signatures, SLSA provenance, and GitHub artifact attestations at install time. This supersedes the former Proto `checksum-url` pins and the repository-committed `.moon/proto/sqlc.sha256` digest that existed only because sqlc publishes no upstream checksum.

Repository settings live in `.github/repository-settings.toml`.
They default to immutable releases, private vulnerability reporting, signed commits, squash-only merges, GitHub Pages workflow publishing, and protected tags.

## Release Layer

Release automation is enabled for the template application so this repository proves the full binary and container release lifecycle before generated projects inherit it.
Repositories generated from the template should update the release app credentials, package names, asset patterns, container image name, and `ghd.toml` signer workflow before cutting their first release.

The release path is:

- Release Please opens and maintains the release PR.
- Release Please creates a draft GitHub release and tag after merge.
- Release Dry Run rehearses the GoReleaser binary path and the native-runner melange/apko container build path on pull requests.
- GoReleaser builds binaries, checksums, and SBOMs without publishing directly.
- The release workflow uploads assets to the draft release; a separate, isolated reusable workflow (`attest.yml`) generates the GitHub-hosted provenance attestation for the binary checksums.
- The release workflow builds amd64 and arm64 apks with melange on native GitHub-hosted runners, assembles and publishes `ghcr.io/meigma/template-go-api:vX.Y.Z` as a multi-platform manifest with apko, signs it with keyless cosign, and attaches a syft SBOM attestation; the isolated `attest.yml` workflow then creates the GitHub-native provenance attestation for the manifest digest.
- Generating both provenance attestations in the isolated `attest.yml` reusable workflow (not in the build job) keeps the signing identity unreachable by build steps — the SLSA Build L3 isolation requirement — while staying on GitHub's attestation API (verify with `gh attestation verify --signer-workflow …/attest.yml`).
- A human inspects the draft release before publication.

The root `ghd.toml` matches the default GoReleaser output so generated projects can be installed with `ghd` once the release workflow runs.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution guidelines, local setup expectations, and pull request workflow.

## Security

See [SECURITY.md](SECURITY.md) for supported versions and the private vulnerability reporting path.

## License

Add the repository license before publishing a project generated from this template.
