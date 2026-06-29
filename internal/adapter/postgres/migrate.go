package postgres

import (
	"context"
	"database/sql"
	"fmt"

	// stdlib registers the "pgx" database/sql driver that goose runs against.
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// dbDriver is the database/sql driver name registered by pgx's stdlib package.
const dbDriver = "pgx"

// dialect is the goose SQL dialect for PostgreSQL.
const dialect = "postgres"

// Migrate runs a goose migration command ("up", "down", or "status") against
// databaseURL using the embedded migration files. It opens a short-lived
// database/sql connection (goose's API) independent of the application's pgx
// pool.
func Migrate(ctx context.Context, databaseURL, command string, args ...string) error {
	db, err := sql.Open(dbDriver, databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.RunContext(ctx, command, db, migrationsDir, args...); err != nil {
		return fmt.Errorf("goose %s: %w", command, err)
	}

	return nil
}
