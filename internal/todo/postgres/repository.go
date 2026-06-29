package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meigma/template-go-api/internal/todo"
	"github.com/meigma/template-go-api/internal/todo/postgres/sqlc"
)

// TodoRepository is a PostgreSQL todo.Repository backed by a pgx connection pool
// and sqlc-generated queries.
type TodoRepository struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

// NewTodoRepository constructs a TodoRepository over the given pool.
func NewTodoRepository(pool *pgxpool.Pool) *TodoRepository {
	return &TodoRepository{
		pool:    pool,
		queries: sqlc.New(pool),
	}
}

// Save inserts or replaces the todo via an upsert.
func (r *TodoRepository) Save(ctx context.Context, t todo.Todo) error {
	params, err := toUpsertParams(t)
	if err != nil {
		return err
	}
	if err := r.queries.UpsertTodo(ctx, params); err != nil {
		return fmt.Errorf("upsert todo %s: %w", t.ID, err)
	}

	return nil
}

// FindByID returns the stored todo or todo.ErrNotFound when absent.
func (r *TodoRepository) FindByID(ctx context.Context, id string) (todo.Todo, error) {
	uid, err := uuidParse(id)
	if err != nil {
		return todo.Todo{}, err
	}

	row, err := r.queries.GetTodo(ctx, uid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return todo.Todo{}, todo.ErrNotFound
		}

		return todo.Todo{}, fmt.Errorf("get todo %s: %w", id, err)
	}

	return fromRow(row)
}

// List returns a bounded page of todos in (created_at, id) order, resuming after
// page.After. It over-fetches one row beyond page.Limit to detect whether a
// further page exists; if so it trims the extra and returns its predecessor as
// the next cursor.
func (r *TodoRepository) List(ctx context.Context, page todo.PageQuery) (todo.PageResult, error) {
	params := sqlc.ListTodosParams{
		// page.Limit is clamped to [1, MaxPageSize] by the service, so the +1
		// over-fetch (used to detect a further page) never overflows int32.
		PageLimit: int32(page.Limit) + 1, //nolint:gosec // bounded by the service clamp.
	}
	if page.After != nil {
		uid, err := uuid.Parse(page.After.ID)
		if err != nil {
			// A cursor we minted always carries a uuid; a malformed one is a
			// tampered/stale token, i.e. a client error, not a 500.
			return todo.PageResult{}, fmt.Errorf("parse cursor id %q: %w", page.After.ID, todo.ErrInvalidCursor)
		}
		after := page.After.CreatedAt
		params.AfterCreatedAt = &after
		params.AfterID = pgtype.UUID{Bytes: uid, Valid: true}
	}

	rows, err := r.queries.ListTodos(ctx, params)
	if err != nil {
		return todo.PageResult{}, fmt.Errorf("list todos: %w", err)
	}

	todos := make([]todo.Todo, 0, len(rows))
	for _, row := range rows {
		t, err := fromRow(row)
		if err != nil {
			return todo.PageResult{}, err
		}
		todos = append(todos, t)
	}

	var next *todo.Cursor
	if page.Limit > 0 && len(todos) > page.Limit {
		last := todos[page.Limit-1]
		next = &todo.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
		todos = todos[:page.Limit]
	}

	return todo.PageResult{Todos: todos, Next: next}, nil
}

// Ping verifies the pool can reach the database, for use as a readiness check.
func (r *TodoRepository) Ping(ctx context.Context) error {
	if err := r.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	return nil
}
