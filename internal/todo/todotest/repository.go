// Package todotest provides test-only doubles for the todo domain ports.
//
// Repository here is a real, stateful in-memory store for tests that exercise a
// create-then-read flow end to end (for example the HTTP functional tests). For
// interaction or error-injection assertions, prefer the generated mock in
// internal/todo/mocks instead.
package todotest

import (
	"context"
	"sort"
	"sync"

	"github.com/meigma/template-go-api/internal/todo"
)

// Repository is an in-memory todo.Repository that is safe for concurrent use.
type Repository struct {
	mu    sync.RWMutex
	todos map[string]todo.Todo
}

// NewRepository constructs an empty Repository.
func NewRepository() *Repository {
	return &Repository{
		mu:    sync.RWMutex{},
		todos: make(map[string]todo.Todo),
	}
}

// Save inserts or replaces the todo.
func (r *Repository) Save(_ context.Context, t todo.Todo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.todos[t.ID] = t

	return nil
}

// FindByID returns the stored todo or todo.ErrNotFound.
func (r *Repository) FindByID(_ context.Context, id string) (todo.Todo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stored, ok := r.todos[id]
	if !ok {
		return todo.Todo{}, todo.ErrNotFound
	}

	return stored, nil
}

// List returns a bounded page of todos, mirroring the keyset semantics of the
// PostgreSQL adapter: it sorts by (CreatedAt, ID), resumes strictly after
// page.After, and over-fetches by one to compute the next cursor.
func (r *Repository) List(_ context.Context, page todo.PageQuery) (todo.PageResult, error) {
	r.mu.RLock()
	all := make([]todo.Todo, 0, len(r.todos))
	for _, t := range r.todos {
		all = append(all, t)
	}
	r.mu.RUnlock()

	sort.Slice(all, func(i, j int) bool { return less(all[i], all[j]) })

	// Skip everything up to and including the cursor position.
	start := 0
	if page.After != nil {
		for start < len(all) && !afterCursor(all[start], *page.After) {
			start++
		}
	}
	window := all[start:]

	var next *todo.Cursor
	if page.Limit > 0 && len(window) > page.Limit {
		window = window[:page.Limit]
		last := window[len(window)-1]
		next = &todo.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}

	return todo.PageResult{Todos: append([]todo.Todo(nil), window...), Next: next}, nil
}

// less orders todos by (CreatedAt, ID), the same total order the list query uses.
func less(a, b todo.Todo) bool {
	if a.CreatedAt.Equal(b.CreatedAt) {
		return a.ID < b.ID
	}

	return a.CreatedAt.Before(b.CreatedAt)
}

// afterCursor reports whether t sorts strictly after c in (CreatedAt, ID) order.
func afterCursor(t todo.Todo, c todo.Cursor) bool {
	if t.CreatedAt.Equal(c.CreatedAt) {
		return t.ID > c.ID
	}

	return t.CreatedAt.After(c.CreatedAt)
}
