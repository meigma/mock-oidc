# hack/sql

SQL applied to the **local Docker Compose database** after migrations run.

The `seed` service in `compose.yaml` runs every `*.sql` file in this directory,
in sorted filename order, once the schema exists. Use it to prepopulate local
data (todos, fixtures, reference rows) without writing a migration or adding
setup code to the server.

- Runs **after** `migrate up`, so the schema (e.g. the `todos` table) exists.
- Applied with `psql -v ON_ERROR_STOP=1`, so a failing statement fails the stack.
- The Compose database is ephemeral: every `docker compose up` rebuilds it and
  re-applies these files, so seeds do not need to be idempotent.
- This directory is **local-development only** — it is not part of the schema,
  the migrations, or any release artifact. Prefix files numerically (`0001_…`,
  `0002_…`) to control order.
