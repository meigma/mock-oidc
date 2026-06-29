package postgres

import "embed"

// migrationsFS holds the embedded goose migration files, so the migrate
// subcommand and the integration tests (both via Migrate) run exactly the
// migrations committed alongside the adapter.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsDir is the directory within migrationsFS that holds the goose
// migration files. goose reads migrations relative to this path.
const migrationsDir = "migrations"
