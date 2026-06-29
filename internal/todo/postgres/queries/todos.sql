-- name: UpsertTodo :exec
-- Insert-or-replace by primary key.
INSERT INTO todos (id, title, status, created_at, completed_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE
  SET title = EXCLUDED.title,
      status = EXCLUDED.status,
      created_at = EXCLUDED.created_at,
      completed_at = EXCLUDED.completed_at;

-- name: GetTodo :one
SELECT id, title, status, created_at, completed_at FROM todos WHERE id = $1;

-- name: ListTodos :many
-- Keyset (cursor) pagination over the (created_at, id) ordering. The after_*
-- bound is NULL on the first page; otherwise the row-value comparison resumes
-- strictly after the prior page's last row. page_limit caps the rows scanned;
-- callers fetch one extra to detect whether a further page exists. The composite
-- index on (created_at, id) (see 00001_create_todos.sql) makes the seek
-- index-backed.
SELECT id, title, status, created_at, completed_at FROM todos
WHERE (
    sqlc.narg('after_created_at')::timestamptz IS NULL
    OR (created_at, id) > (sqlc.narg('after_created_at')::timestamptz, sqlc.narg('after_id')::uuid)
)
ORDER BY created_at, id
LIMIT sqlc.arg('page_limit');

-- Optional status filter via sqlc.narg: a NULL argument disables the filter, a
-- non-NULL value restricts to that status. Uncomment and regenerate to use.
--
-- -- name: ListTodosByStatus :many
-- SELECT id, title, status, created_at, completed_at FROM todos
-- WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))
-- ORDER BY created_at, id;
