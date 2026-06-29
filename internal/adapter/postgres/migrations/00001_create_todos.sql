-- +goose Up
CREATE TABLE todos (
    id           uuid        PRIMARY KEY,
    title        text        NOT NULL,
    status       text        NOT NULL DEFAULT 'open'
                             CHECK (status IN ('open', 'completed')),
    created_at   timestamptz NOT NULL,
    completed_at timestamptz
);

-- Composite index backing the keyset-paginated list query
-- (internal/todo/postgres/queries/todos.sql, ListTodos), which both orders by
-- and seeks on (created_at, id).
CREATE INDEX todos_created_at_id_idx ON todos (created_at, id);

-- +goose Down
DROP TABLE todos;
