//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	// stdlib registers the "pgx" database/sql driver. Importing it lets the
	// testcontainers postgres module take snapshots through pgx instead of the
	// slower `docker exec psql` fallback.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/meigma/template-go-api/internal/adapter/postgres"
	todopostgres "github.com/meigma/template-go-api/internal/todo/postgres"
)

// postgresImage pins the container image so the suite is reproducible across
// machines and CI.
const postgresImage = "postgres:17-alpine"

// Non-default credentials and database name. The database must not be named
// "postgres": the snapshot/restore feature drops and recreates the target
// database, which is impossible for the postgres system database.
const (
	testDatabase = "todos_test"
	testUsername = "todos"
	testPassword = "todos-secret"
)

// fixture wraps a migrated, snapshotted Postgres container. Reset restores the
// clean post-migration state and hands back a fresh repository, so one container
// serves every test cheaply.
type fixture struct {
	container *tcpostgres.PostgresContainer
	url       string
}

// setupPostgres starts a Postgres container, applies the embedded migrations,
// and snapshots the clean schema. The container is torn down via t.Cleanup. It
// is a shared helper so every test exercises the adapter against the real
// committed schema.
func setupPostgres(ctx context.Context, t *testing.T) *fixture {
	t.Helper()

	container, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase(testDatabase),
		tcpostgres.WithUsername(testUsername),
		tcpostgres.WithPassword(testPassword),
		// Snapshot/restore runs through this driver; without it the module falls
		// back to `docker exec psql`.
		tcpostgres.WithSQLDriver("pgx"),
		tcpostgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	url := container.MustConnectionString(ctx, "sslmode=disable")

	// Apply the embedded migrations through the same goose path the migrate
	// subcommand uses, so the tests cover exactly the committed schema.
	require.NoError(t, postgres.Migrate(ctx, url, "up"))

	// Snapshot the migrated-but-empty database once; Reset restores it between
	// tests for fast isolation without re-running migrations.
	require.NoError(t, container.Snapshot(ctx))

	return &fixture{container: container, url: url}
}

// Reset restores the database to its clean post-migration state and returns a
// repository bound to a fresh pool. Restore drops and recreates the target
// database (WITH FORCE), terminating any existing connections, so the pool must
// be opened after the restore — hence a new repo per call rather than a shared
// pool. The pool is closed via t.Cleanup.
func (f *fixture) Reset(ctx context.Context, t *testing.T) *todopostgres.TodoRepository {
	t.Helper()

	return todopostgres.NewTodoRepository(f.ResetPool(ctx, t))
}

// ResetPool restores the database to its clean post-migration state and returns
// a fresh pgx pool bound to it. Restore drops and recreates the target database
// (WITH FORCE), terminating any existing connections, so the pool must be opened
// after the restore — hence a new pool per call. The pool is closed via
// t.Cleanup. The authz suites use it directly to seed api_keys rows and to share
// the container URL with app.New.
func (f *fixture) ResetPool(ctx context.Context, t *testing.T) *pgxpool.Pool {
	t.Helper()

	require.NoError(t, f.container.Restore(ctx))

	pool, err := postgres.Connect(ctx, postgres.Config{URL: f.url})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}
