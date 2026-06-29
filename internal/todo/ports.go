package todo

import "context"

// Repository is the outbound port the todo service depends on for persistence.
// It is declared here, by its consumer, and implemented by adapters (for example,
// a PostgreSQL store).
type Repository interface {
	// Save persists the todo, inserting or replacing any existing entry.
	Save(ctx context.Context, todo Todo) error
	// FindByID returns the todo with the given id, or ErrNotFound if absent.
	FindByID(ctx context.Context, id string) (Todo, error)
	// List returns a bounded page of todos in (created_at, id) order, resuming
	// after page.After (nil = first page) and returning at most page.Limit rows
	// plus the cursor for the next page (nil when the page is the last). Callers
	// pass page.Limit >= 1; the service guarantees this by clamping.
	List(ctx context.Context, page PageQuery) (PageResult, error)
}
