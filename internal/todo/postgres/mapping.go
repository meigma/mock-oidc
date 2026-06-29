package postgres

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/meigma/template-go-api/internal/todo"
	"github.com/meigma/template-go-api/internal/todo/postgres/sqlc"
)

// uuidParse parses a string id used to look up an existing row. A syntactically
// invalid id can never match a stored uuid, so it maps to todo.ErrNotFound.
func uuidParse(id string) (uuid.UUID, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return uuid.UUID{}, todo.ErrNotFound
	}

	return uid, nil
}

// toUpsertParams maps a domain todo to the sqlc UpsertTodo parameters, parsing
// the string ID into the uuid the column stores.
func toUpsertParams(t todo.Todo) (sqlc.UpsertTodoParams, error) {
	id, err := uuid.Parse(t.ID)
	if err != nil {
		return sqlc.UpsertTodoParams{}, fmt.Errorf("parse todo id %q: %w", t.ID, err)
	}

	return sqlc.UpsertTodoParams{
		ID:          id,
		Title:       t.Title,
		Status:      string(t.Status),
		CreatedAt:   t.CreatedAt,
		CompletedAt: t.CompletedAt,
	}, nil
}

// fromRow maps a sqlc row to a domain todo, converting the uuid back to a
// string and validating the persisted status against the domain enum.
func fromRow(row sqlc.Todo) (todo.Todo, error) {
	status := todo.Status(row.Status)
	if !status.Valid() {
		return todo.Todo{}, fmt.Errorf("invalid stored status %q for todo %s", row.Status, row.ID)
	}

	return todo.Todo{
		ID:          row.ID.String(),
		Title:       row.Title,
		Status:      status,
		CreatedAt:   row.CreatedAt,
		CompletedAt: row.CompletedAt,
	}, nil
}
