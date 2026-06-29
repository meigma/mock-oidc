// Package postgres provides a PostgreSQL implementation of the todo outbound
// port, backed by pgx/v5 and sqlc-generated queries.
package postgres

import (
	"context"
	"fmt"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the settings required to connect a connection pool.
type Config struct {
	// URL is the libpq-style connection string (for example
	// postgres://user:pass@host:5432/dbname).
	URL string
	// MaxConns caps the pool size. Zero leaves pgx's default in place.
	MaxConns int32
	// Tracing installs the OpenTelemetry pgx query tracer on the pool so each
	// query becomes a child span of the request. It uses the global tracer
	// provider; leave it false to add no tracing overhead.
	Tracing bool
}

// Connect parses cfg, applies pool tuning, and opens a connection pool,
// verifying connectivity with a ping before returning. The caller owns the pool
// and must Close it.
func Connect(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.Tracing {
		// Trace every query as a span under the active request span. The tracer
		// resolves the global tracer provider at query time, so it is inert until
		// a real provider is installed.
		poolCfg.ConnConfig.Tracer = otelpgx.NewTracer()
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
