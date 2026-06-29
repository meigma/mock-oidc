package httpapi

import (
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/template-go-api/internal/todo"
)

// toHumaError maps domain and service errors to RFC 9457 HTTP error responses.
// Huma renders the returned error as an application/problem+json body.
func toHumaError(err error) error {
	switch {
	case errors.Is(err, todo.ErrNotFound):
		return huma.Error404NotFound("todo not found")
	case errors.Is(err, todo.ErrInvalidTitle):
		return huma.Error422UnprocessableEntity("invalid todo", err)
	case errors.Is(err, todo.ErrInvalidCursor):
		return huma.Error422UnprocessableEntity("invalid cursor")
	default:
		return huma.Error500InternalServerError("internal server error")
	}
}
