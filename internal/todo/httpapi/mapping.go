package httpapi

import "github.com/meigma/template-go-api/internal/todo"

// toTodoDTO maps a domain todo to its transport representation.
func toTodoDTO(t todo.Todo) TodoDTO {
	return TodoDTO{
		ID:          t.ID,
		Title:       t.Title,
		Status:      string(t.Status),
		CreatedAt:   t.CreatedAt,
		CompletedAt: t.CompletedAt,
	}
}

// toTodoDTOs maps a slice of domain todos to their transport representations.
func toTodoDTOs(todos []todo.Todo) []TodoDTO {
	dtos := make([]TodoDTO, 0, len(todos))
	for _, t := range todos {
		dtos = append(dtos, toTodoDTO(t))
	}

	return dtos
}
