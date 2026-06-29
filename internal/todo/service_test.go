package todo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/template-go-api/internal/todo"
	"github.com/meigma/template-go-api/internal/todo/mocks"
)

const fixedID = "fixed-id"

// fixedClock is the deterministic time source the service uses in these tests.
func fixedClock() time.Time {
	return time.Date(2026, time.June, 22, 13, 0, 0, 0, time.UTC)
}

// serviceFixture bundles the mocked repository and the service under test.
type serviceFixture struct {
	repo    *mocks.Repository
	service *todo.Service
}

// newServiceFixture builds a Service backed by a strict mock repository with a
// fixed clock and ID generator, so persisted entities are fully deterministic.
func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()

	repo := mocks.NewRepository(t)
	service := todo.NewService(repo, nil,
		todo.WithClock(fixedClock),
		todo.WithIDGenerator(func() string { return fixedID }),
	)

	return &serviceFixture{repo: repo, service: service}
}

func TestServiceCreate(t *testing.T) {
	t.Parallel()

	fix := newServiceFixture(t)
	fix.repo.EXPECT().
		Save(mock.Anything, mock.MatchedBy(func(saved todo.Todo) bool {
			return saved.ID == fixedID && saved.Status == todo.StatusOpen && saved.Title == "buy milk"
		})).
		Return(nil)

	got, err := fix.service.Create(context.Background(), "buy milk")
	require.NoError(t, err)
	assert.Equal(t, fixedID, got.ID)
	assert.Equal(t, todo.StatusOpen, got.Status)
	assert.Equal(t, "buy milk", got.Title)
}

func TestServiceCreateInvalid(t *testing.T) {
	t.Parallel()

	fix := newServiceFixture(t)
	// No Save expectation: the strict mock fails the test if an invalid title is
	// ever persisted.
	_, err := fix.service.Create(context.Background(), "   ")
	require.ErrorIs(t, err, todo.ErrInvalidTitle)
}

func TestServiceGetNotFound(t *testing.T) {
	t.Parallel()

	fix := newServiceFixture(t)
	fix.repo.EXPECT().FindByID(mock.Anything, "missing").Return(todo.Todo{}, todo.ErrNotFound)

	_, err := fix.service.Get(context.Background(), "missing")
	require.ErrorIs(t, err, todo.ErrNotFound)
}

func TestServiceComplete(t *testing.T) {
	t.Parallel()

	fix := newServiceFixture(t)
	existing := todo.Todo{
		ID:        fixedID,
		Title:     "buy milk",
		Status:    todo.StatusOpen,
		CreatedAt: fixedClock(),
	}
	fix.repo.EXPECT().FindByID(mock.Anything, fixedID).Return(existing, nil)
	fix.repo.EXPECT().
		Save(mock.Anything, mock.MatchedBy(func(saved todo.Todo) bool {
			return saved.ID == fixedID && saved.Status == todo.StatusCompleted && saved.CompletedAt != nil
		})).
		Return(nil)

	completed, err := fix.service.Complete(context.Background(), fixedID)
	require.NoError(t, err)
	assert.Equal(t, todo.StatusCompleted, completed.Status)
	require.NotNil(t, completed.CompletedAt)
}

func TestServiceCompleteNotFound(t *testing.T) {
	t.Parallel()

	fix := newServiceFixture(t)
	fix.repo.EXPECT().FindByID(mock.Anything, "missing").Return(todo.Todo{}, todo.ErrNotFound)

	_, err := fix.service.Complete(context.Background(), "missing")
	require.ErrorIs(t, err, todo.ErrNotFound)
}

func TestServiceListPropagatesError(t *testing.T) {
	t.Parallel()

	fix := newServiceFixture(t)
	fix.repo.EXPECT().List(mock.Anything, mock.Anything).Return(todo.PageResult{}, errors.New("boom"))

	_, err := fix.service.List(context.Background(), todo.PageQuery{Limit: 10})
	require.Error(t, err)
}

func TestServiceListClampsLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		requested int
		want      int
	}{
		{"zero uses the default", 0, todo.DefaultPageSize},
		{"negative uses the default", -5, todo.DefaultPageSize},
		{"over the max is capped", todo.MaxPageSize + 50, todo.MaxPageSize},
		{"within range is unchanged", 10, 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fix := newServiceFixture(t)
			// The repository only ever sees a clamped limit, so per-request work is
			// bounded even for this direct (non-HTTP) caller.
			fix.repo.EXPECT().
				List(mock.Anything, mock.MatchedBy(func(p todo.PageQuery) bool {
					return p.Limit == tc.want
				})).
				Return(todo.PageResult{}, nil)

			_, err := fix.service.List(context.Background(), todo.PageQuery{Limit: tc.requested})
			require.NoError(t, err)
		})
	}
}
