---
title: template-go-api Docs
slug: /
description: Meigma starter for Go web (HTTP) API services.
---

# template-go-api

`template-go-api` is the Meigma starter for building Go web (HTTP) API services.
It ships a runnable, hexagonal API server (chi + Huma) with a `todo` example
resource, alongside the shared Meigma repository baseline (Moon tasks, pinned CI,
Dependabot, and an enabled release layer). Persistence is a PostgreSQL adapter
(pgx + sqlc + goose) behind the domain's `todo.Repository` port — implement that
port to back the template with a different datastore.

## Quick start

The server persists to PostgreSQL, so running it needs a database. The fastest
path is Docker Compose, which brings up the database, migrations, seed data, and
the API together. The Compose stack also seeds dev-only mock API keys, because
the todo routes are protected by the authorization tier (on by default):

```sh
mise run stack-up

# Authorization is on: without a key, a protected route returns 401.
curl -sS -o /dev/null -w '%{http_code}\n' localhost:8080/v1/todos   # => 401

# Use the seeded dev user key (sent via the X-API-Key header):
curl -sS -X POST localhost:8080/v1/todos \
  -H 'X-API-Key: dev-user-key' \
  -H 'content-type: application/json' \
  -d '{"title":"buy milk"}'                                       # => 201
curl -sS -H 'X-API-Key: dev-user-key' localhost:8080/v1/todos       # => 200, first page (keyset-paginated)
```

`GET /v1/todos` is keyset-paginated — it returns at most `limit` todos (default
20, max 100) plus an opaque `nextCursor`; pass that back as `?cursor=` for the
next page. The bound applies even without `limit`, so one request can never pull
the whole table.

Resource routes are served under a `/v1` URL version prefix; the operational
endpoints (`/healthz`, `/readyz`, `/metrics`, `/docs`, `/openapi.*`) are
unversioned. See the README's [API versioning](https://github.com/meigma/template-go-api#api-versioning)
section for how a later `/v2` is added.

The stack seeds two mock keys: `dev-user-key` (role `user`, authorized for the
todo actions) and `dev-admin-key` (role `admin`, authorized for everything).
These are insecure, dev-only credentials — real deployments insert their own
keys and never apply `hack/sql/`. The operational endpoints (`/healthz`,
`/readyz`, `/metrics`) sit outside the authorization middleware and need no key.

To build the binary and run it against your own PostgreSQL instead:

```sh
# start a throwaway PostgreSQL (or point at your own)
docker run --rm -d -p 5432:5432 \
  -e POSTGRES_USER=app -e POSTGRES_PASSWORD=app -e POSTGRES_DB=app postgres:17-alpine
export TEMPLATE_GO_API_DATABASE_URL='postgres://app:app@localhost:5432/app?sslmode=disable'
moon run root:build
./bin/template-go-api migrate up   # create the schema (incl. the api_keys table)
./bin/template-go-api serve        # listens on :8080
```

Running the binary directly applies the schema but not the `hack/sql/` seeds, so
the `api_keys` table starts empty. Insert a key yourself — the table stores a
SHA-256 hash, so write the digest into `key_hash`, e.g.
`INSERT INTO api_keys (key_hash, subject, roles) VALUES (encode(sha256('my-key'::bytea), 'hex'), 'me', ARRAY['user'])`
— or set `TEMPLATE_GO_API_AUTHZ_ENABLED=false` to bypass authorization while
developing.

See the [README](https://github.com/meigma/template-go-api#readme) for the full
quickstart, configuration reference, the
[Persistence](https://github.com/meigma/template-go-api#persistence) workflow
(migrations, sqlc regeneration, integration tests, dynamic queries), the
[Authorization](https://github.com/meigma/template-go-api#authorization) tier
(Cedar policies, the deferred-authn seam, the modular slice pattern), and
guidance on replacing the example resource.

## API reference

The [API Reference](api.md) is generated from the OpenAPI specification. A
running server also serves interactive docs at `/docs` and the live spec at
`/openapi.yaml`.

## Operating notes

- Liveness: `GET /healthz`
- Readiness: `GET /readyz` (reports named per-check results; the PostgreSQL adapter adds a `postgres` connectivity check)
- Metrics: `GET /metrics` on a dedicated listener (`--metrics-addr`, default `:9090`)
- Migrations are explicit: `serve` never runs them; use the `migrate up|down|status` subcommand.
- Authorization is deny-by-default and on by default (`--authz-enabled`, env `TEMPLATE_GO_API_AUTHZ_ENABLED`); the operational endpoints above are outside the authorization middleware. Set it `false` to bypass authorization entirely.

## Support and security

- Issues and contributions: see [CONTRIBUTING.md](https://github.com/meigma/template-go-api/blob/master/CONTRIBUTING.md).
- Security reports: see [SECURITY.md](https://github.com/meigma/template-go-api/blob/master/SECURITY.md).
