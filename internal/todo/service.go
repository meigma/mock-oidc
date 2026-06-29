package todo

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/meigma/template-go-api/internal/logctx"
)

// Service implements the todo use-cases on top of a Repository.
type Service struct {
	repo   Repository
	logger *slog.Logger
	now    func() time.Time
	newID  func() string
}

// Option configures a Service.
type Option func(*Service)

// WithClock overrides the time source used for timestamps (useful in tests).
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		s.now = now
	}
}

// WithIDGenerator overrides the identifier source (useful in tests).
func WithIDGenerator(newID func() string) Option {
	return func(s *Service) {
		s.newID = newID
	}
}

// NewService constructs a Service. If logger is nil, log output is discarded.
func NewService(repo Repository, logger *slog.Logger, opts ...Option) *Service {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	s := &Service{
		repo:   repo,
		logger: logger,
		now:    time.Now,
		newID:  uuid.NewString,
	}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// loggerFor returns the request-scoped logger carried on ctx when present,
// otherwise the logger injected at construction. This lets service logs inherit
// the request_id without coupling the domain to the transport layer.
func (s *Service) loggerFor(ctx context.Context) *slog.Logger {
	if logger, ok := logctx.From(ctx); ok {
		return logger
	}

	return s.logger
}

// Create validates and stores a new open todo.
func (s *Service) Create(ctx context.Context, title string) (Todo, error) {
	created, err := NewTodo(s.newID(), title, s.now())
	if err != nil {
		return Todo{}, err
	}
	if err := s.repo.Save(ctx, created); err != nil {
		return Todo{}, fmt.Errorf("save todo: %w", err)
	}

	s.loggerFor(ctx).InfoContext(ctx, "todo created", slog.String("todo_id", created.ID))

	return created, nil
}

// Get returns the todo with the given id, or ErrNotFound if it does not exist.
func (s *Service) Get(ctx context.Context, id string) (Todo, error) {
	found, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Todo{}, fmt.Errorf("find todo: %w", err)
	}

	return found, nil
}

// List returns a bounded page of todos. It clamps the requested limit into
// [1, MaxPageSize] (a non-positive limit uses DefaultPageSize) so the work per
// call is bounded regardless of the caller, then delegates to the repository.
func (s *Service) List(ctx context.Context, page PageQuery) (PageResult, error) {
	switch {
	case page.Limit <= 0:
		page.Limit = DefaultPageSize
	case page.Limit > MaxPageSize:
		page.Limit = MaxPageSize
	}

	result, err := s.repo.List(ctx, page)
	if err != nil {
		return PageResult{}, fmt.Errorf("list todos: %w", err)
	}

	return result, nil
}

// Complete marks the todo with the given id as completed. It is idempotent and
// returns ErrNotFound if the todo does not exist.
func (s *Service) Complete(ctx context.Context, id string) (Todo, error) {
	found, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Todo{}, fmt.Errorf("find todo: %w", err)
	}

	completed := found.Complete(s.now())
	if err := s.repo.Save(ctx, completed); err != nil {
		return Todo{}, fmt.Errorf("save todo: %w", err)
	}

	s.loggerFor(ctx).InfoContext(ctx, "todo completed", slog.String("todo_id", completed.ID))

	return completed, nil
}
