// Package todo contains the core domain for the example todo resource: the
// entity, its business rules, the use-case service, and the outbound port it
// depends on. It has no knowledge of transport, storage, or configuration.
package todo

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// maxTitleLength bounds the number of characters allowed in a todo title.
const maxTitleLength = 200

// ErrInvalidTitle indicates a todo title failed validation.
var ErrInvalidTitle = errors.New("invalid todo title")

// ErrNotFound indicates a requested todo does not exist.
var ErrNotFound = errors.New("todo not found")

// Status enumerates the lifecycle states of a todo.
type Status string

const (
	// StatusOpen marks a todo that has not yet been completed.
	StatusOpen Status = "open"
	// StatusCompleted marks a todo that has been completed.
	StatusCompleted Status = "completed"
)

// Valid reports whether s is a recognized status.
func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusCompleted:
		return true
	default:
		return false
	}
}

// Todo is the core entity of the domain.
type Todo struct {
	// ID uniquely identifies the todo.
	ID string
	// Title is the human-readable description of the todo.
	Title string
	// Status is the current lifecycle state.
	Status Status
	// CreatedAt is when the todo was created.
	CreatedAt time.Time
	// CompletedAt is when the todo was completed, or nil while it is open.
	CompletedAt *time.Time
}

// NewTodo creates an open todo, validating the title. It returns ErrInvalidTitle
// if the title is empty or exceeds the maximum length.
func NewTodo(id, title string, now time.Time) (Todo, error) {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return Todo{}, fmt.Errorf("%w: title must not be empty", ErrInvalidTitle)
	}
	if utf8.RuneCountInString(trimmed) > maxTitleLength {
		return Todo{}, fmt.Errorf("%w: title exceeds %d characters", ErrInvalidTitle, maxTitleLength)
	}

	return Todo{
		ID:          id,
		Title:       trimmed,
		Status:      StatusOpen,
		CreatedAt:   now,
		CompletedAt: nil,
	}, nil
}

// Complete returns a copy of the todo marked completed at now. Completing an
// already-completed todo is idempotent and preserves the original completion time.
func (t Todo) Complete(now time.Time) Todo {
	if t.Status == StatusCompleted {
		return t
	}

	completedAt := now
	t.Status = StatusCompleted
	t.CompletedAt = &completedAt

	return t
}
