// Package httpapi is the HTTP transport adapter for the todo resource: its
// request/response DTOs, domain<->DTO mapping, error translation, and the Huma
// operation registrations. It depends inward on the todo domain and plugs into
// the generic transport (internal/adapter/http) through its Registrar seam.
package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/template-go-api/internal/authz"
	"github.com/meigma/template-go-api/internal/todo"
	todoauthz "github.com/meigma/template-go-api/internal/todo/authz"
)

// todoIDParam is the path parameter naming the todo id. The by-id operations
// bind it so the authz middleware sets Resource = Todo::"<id>" straight from the
// matched route, enabling instance-level policies with no load.
const todoIDParam = "id"

// tagTodos groups the todo operations in the OpenAPI document.
const tagTodos = "Todos"

// handlers adapts the todo service to Huma operation handlers.
type handlers struct {
	service *todo.Service
}

// Register mounts the todo operations on api, backed by service. API routes go
// through Huma, so they are validated and appear in the OpenAPI specification.
func Register(api huma.API, service *todo.Service) {
	h := &handlers{service: service}

	huma.Register(api, huma.Operation{
		OperationID:   "create-todo",
		Method:        http.MethodPost,
		Path:          "/todos",
		Summary:       "Create a todo",
		Tags:          []string{tagTodos},
		DefaultStatus: http.StatusCreated,
		// Collection operation: the resource is type-level (Todo), so no id is bound.
		Metadata: authz.Require(todoauthz.ActionCreate),
	}, h.create)

	huma.Register(api, huma.Operation{
		OperationID: "list-todos",
		Method:      http.MethodGet,
		Path:        "/todos",
		Summary:     "List todos",
		Tags:        []string{tagTodos},
		Metadata:    authz.Require(todoauthz.ActionList),
	}, h.list)

	huma.Register(api, huma.Operation{
		OperationID: "get-todo",
		Method:      http.MethodGet,
		Path:        "/todos/{id}",
		Summary:     "Get a todo by id",
		Tags:        []string{tagTodos},
		Errors:      []int{http.StatusNotFound},
		// Item operation: bind the {id} path param so Resource = Todo::"<id>".
		Metadata: authz.Require(todoauthz.ActionRead, todoIDParam),
	}, h.get)

	huma.Register(api, huma.Operation{
		OperationID: "complete-todo",
		Method:      http.MethodPost,
		Path:        "/todos/{id}/complete",
		Summary:     "Mark a todo as completed",
		Tags:        []string{tagTodos},
		Errors:      []int{http.StatusNotFound},
		// Completing a todo mutates it, so it requires the update action; the
		// {id} path param binds the instance resource.
		Metadata: authz.Require(todoauthz.ActionUpdate, todoIDParam),
	}, h.complete)
}

func (h *handlers) create(ctx context.Context, input *CreateTodoInput) (*TodoOutput, error) {
	created, err := h.service.Create(ctx, input.Body.Title)
	if err != nil {
		return nil, toHumaError(err)
	}

	return &TodoOutput{Body: toTodoDTO(created)}, nil
}

func (h *handlers) get(ctx context.Context, input *GetTodoInput) (*TodoOutput, error) {
	found, err := h.service.Get(ctx, input.ID)
	if err != nil {
		return nil, toHumaError(err)
	}

	return &TodoOutput{Body: toTodoDTO(found)}, nil
}

func (h *handlers) list(ctx context.Context, input *ListTodosInput) (*ListTodosOutput, error) {
	after, err := decodeCursor(input.Cursor)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("invalid cursor")
	}

	result, err := h.service.List(ctx, todo.PageQuery{Limit: input.Limit, After: after})
	if err != nil {
		return nil, toHumaError(err)
	}

	out := &ListTodosOutput{}
	out.Body.Todos = toTodoDTOs(result.Todos)
	out.Body.NextCursor = encodeCursor(result.Next)

	return out, nil
}

func (h *handlers) complete(ctx context.Context, input *CompleteTodoInput) (*TodoOutput, error) {
	completed, err := h.service.Complete(ctx, input.ID)
	if err != nil {
		return nil, toHumaError(err)
	}

	return &TodoOutput{Body: toTodoDTO(completed)}, nil
}
