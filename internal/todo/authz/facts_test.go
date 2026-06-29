package authz_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cedar-policy/cedar-go/types"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/template-go-api/internal/authz"
	mockauthz "github.com/meigma/template-go-api/internal/authz/mocks"
	"github.com/meigma/template-go-api/internal/todo"
	todoauthz "github.com/meigma/template-go-api/internal/todo/authz"
	"github.com/meigma/template-go-api/internal/todo/mocks"
)

// resolverFor builds the slice's resolver bound to ctx, backed by repo.
func resolverFor(ctx context.Context, repo todo.Repository) authz.EntityResolver {
	return todoauthz.Contribution(repo).Resolver(ctx, authz.Anonymous())
}

func TestResolverOwnsTodoType(t *testing.T) {
	t.Parallel()

	resolver := resolverFor(context.Background(), mocks.NewRepository(t))
	assert.Equal(t, []string{string(todoauthz.TodoType)}, resolver.Types())
}

func TestResolverMapsTodoToEntity(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	completed := created.Add(time.Hour)
	stored := todo.Todo{
		ID:          "42",
		Title:       "buy milk",
		Status:      todo.StatusCompleted,
		CreatedAt:   created,
		CompletedAt: &completed,
	}

	repo := mocks.NewRepository(t)
	repo.EXPECT().FindByID(mock.Anything, "42").Return(stored, nil)

	resolver := resolverFor(context.Background(), repo)
	entity, ok := resolver.Resolve(types.NewEntityUID(todoauthz.TodoType, "42"))

	require.True(t, ok)
	assert.Equal(t, types.NewEntityUID(todoauthz.TodoType, "42"), entity.UID)

	title, _ := entity.Attributes.Get("title")
	assert.Equal(t, types.String("buy milk"), title)
	status, _ := entity.Attributes.Get("status")
	assert.Equal(t, types.String("completed"), status)
	createdAt, hasCreated := entity.Attributes.Get("createdAt")
	require.True(t, hasCreated)
	assert.Equal(t, types.NewDatetime(created), createdAt)
	completedAt, hasCompleted := entity.Attributes.Get("completedAt")
	require.True(t, hasCompleted, "a completed todo exposes its completedAt")
	assert.Equal(t, types.NewDatetime(completed), completedAt)
}

func TestResolverOmitsCompletedAtWhileOpen(t *testing.T) {
	t.Parallel()

	stored := todo.Todo{ID: "7", Title: "open", Status: todo.StatusOpen, CreatedAt: time.Now()}
	repo := mocks.NewRepository(t)
	repo.EXPECT().FindByID(mock.Anything, "7").Return(stored, nil)

	resolver := resolverFor(context.Background(), repo)
	entity, ok := resolver.Resolve(types.NewEntityUID(todoauthz.TodoType, "7"))

	require.True(t, ok)
	_, hasCompleted := entity.Attributes.Get("completedAt")
	assert.False(t, hasCompleted, "an open todo omits completedAt")
}

func TestResolverUnownedTypeIsMiss(t *testing.T) {
	t.Parallel()

	// A uid of a type the resolver does not own must not hit the repository.
	resolver := resolverFor(context.Background(), mocks.NewRepository(t))
	_, ok := resolver.Resolve(types.NewEntityUID("User", "alice"))
	assert.False(t, ok)
}

func TestResolverNotFoundIsMiss(t *testing.T) {
	t.Parallel()

	// A missing todo is a miss (false), not a recorded error: without an
	// attribute policy nothing dereferences it, and even an instance policy
	// treats an absent resource as not-matching rather than a backend failure.
	repo := mocks.NewRepository(t)
	repo.EXPECT().FindByID(mock.Anything, "404").Return(todo.Todo{}, todo.ErrNotFound)

	resolver := resolverFor(context.Background(), repo)
	_, ok := resolver.Resolve(types.NewEntityUID(todoauthz.TodoType, "404"))
	assert.False(t, ok)
}

// attrPolicy forces Cedar to load the resource entity (it reads resource.title),
// so a repository failure during the load is exercised. The condition value does
// not matter: the load itself triggers the fail-closed path.
const attrPolicy = `permit (
    principal,
    action == Action::"todo:read",
    resource
) when { resource.title == "anything" };`

// TestResolverBackendErrorFailsClosed proves the resolver reports a backend
// failure (not a miss) so the middleware returns 500 rather than deciding on
// missing data. It drives the real composite getter via the middleware, since
// the error sink is request-scoped and installed there; a bare Resolve call
// cannot observe RecordLoadError.
func TestResolverBackendErrorFailsClosed(t *testing.T) {
	t.Parallel()

	repo := mocks.NewRepository(t)
	repo.EXPECT().FindByID(mock.Anything, "42").Return(todo.Todo{}, errors.New("database unavailable"))

	authorizer, err := authz.New([]authz.Contribution{{
		Policies: []byte(attrPolicy),
		Types:    []string{string(todoauthz.TodoType)},
		Resolver: todoauthz.Contribution(repo).Resolver,
	}})
	require.NoError(t, err)

	authn := mockauthz.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(principal("alice"), nil)

	_, api := humatest.New(t)
	authz.NewMiddleware(api, authn, authorizer, nil, true).Install()

	huma.Register(api, huma.Operation{
		OperationID: "get-todo",
		Method:      http.MethodGet,
		Path:        "/todos/{id}",
		Metadata:    authz.Require(todoauthz.ActionRead, "id"),
	}, func(_ context.Context, _ *struct {
		ID string `path:"id"`
	}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	resp := api.Get("/todos/42")
	assert.Equal(t, http.StatusInternalServerError, resp.Code, "a backend load failure must fail closed with 500")
}
