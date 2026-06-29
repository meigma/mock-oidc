package authz_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/cedar-policy/cedar-go/types"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/template-go-api/internal/authz"
	"github.com/meigma/template-go-api/internal/authz/mocks"
)

// actionRead is the action used across the middleware tests.
func actionRead() types.EntityUID { return types.NewEntityUID("Action", "todo:read") }

// allowAlicePolicy permits the user alice to perform todo:read, so the tests can
// exercise an Allow decision distinct from the base admin override.
const allowAlicePolicy = `permit (
    principal == User::"alice",
    action == Action::"todo:read",
    resource
);`

// user returns a non-anonymous principal with the given subject and roles.
func user(subject string, roles ...string) authz.Principal {
	values := make([]types.Value, 0, len(roles))
	for _, role := range roles {
		values = append(values, types.String(role))
	}

	return authz.Principal{
		UID: types.NewEntityUID("User", types.String(subject)),
		Claims: types.NewRecord(types.RecordMap{
			authz.RolesClaim: types.NewSet(values...),
		}),
	}
}

// newTestAPI builds a humatest API with the authz middleware installed over the
// given authenticator and a real authorizer carrying the alice policy, then
// registers three operations: a Require(todo:read), a Public, and an undeclared
// operation.
func newTestAPI(t *testing.T, authenticator authz.Authenticator) humatest.TestAPI {
	t.Helper()

	authorizer, err := authz.New([]authz.Contribution{{Policies: []byte(allowAlicePolicy)}})
	require.NoError(t, err)

	_, api := humatest.New(t)
	logger := slog.New(slog.DiscardHandler)
	authz.NewMiddleware(api, authenticator, authorizer, logger, true).Install()

	huma.Register(api, huma.Operation{
		OperationID: "protected",
		Method:      http.MethodGet,
		Path:        "/protected",
		Metadata:    authz.Require(actionRead()),
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "public",
		Method:      http.MethodGet,
		Path:        "/public",
		Metadata:    authz.Public(),
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "undeclared",
		Method:      http.MethodGet,
		Path:        "/undeclared",
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api
}

func TestMiddlewareAllowsAuthorizedPrincipal(t *testing.T) {
	t.Parallel()

	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(user("alice"), nil)

	resp := newTestAPI(t, authn).Get("/protected")
	assert.Equal(t, http.StatusNoContent, resp.Code)
}

func TestMiddlewareAllowsAdminViaBasePolicy(t *testing.T) {
	t.Parallel()

	// bob is not named in the alice policy, but the admin role grants the base
	// override — exercising the merged base+slice policy set and the principal
	// resolver's role projection.
	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(user("bob", "admin"), nil)

	resp := newTestAPI(t, authn).Get("/protected")
	assert.Equal(t, http.StatusNoContent, resp.Code)
}

func TestMiddlewareForbidsAuthenticatedButUnauthorized(t *testing.T) {
	t.Parallel()

	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(user("mallory"), nil)

	resp := newTestAPI(t, authn).Get("/protected")
	assert.Equal(t, http.StatusForbidden, resp.Code)
}

func TestMiddlewareRejectsAnonymousWith401(t *testing.T) {
	t.Parallel()

	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(authz.Anonymous(), nil)

	resp := newTestAPI(t, authn).Get("/protected")
	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestMiddlewareMapsInvalidCredentialTo401(t *testing.T) {
	t.Parallel()

	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(authz.Anonymous(), errors.New("bad credential"))

	resp := newTestAPI(t, authn).Get("/protected")
	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestMiddlewareAllowsPublicOperation(t *testing.T) {
	t.Parallel()

	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(authz.Anonymous(), nil)

	resp := newTestAPI(t, authn).Get("/public")
	assert.Equal(t, http.StatusNoContent, resp.Code)
}

func TestMiddlewareDeniesUndeclaredOperation(t *testing.T) {
	t.Parallel()

	// An authenticated caller hitting an undeclared route is denied 403 by the
	// fail-closed default (a declared-but-unauthorized would be the same code; the
	// point is that forgetting a declaration does not open the route).
	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(user("alice"), nil)

	resp := newTestAPI(t, authn).Get("/undeclared")
	assert.Equal(t, http.StatusForbidden, resp.Code)
}

func TestMiddlewareDeniesUndeclaredOperationForAnonymousWith403(t *testing.T) {
	t.Parallel()

	// An undeclared route is a server-side omission, not a missing-credential
	// case: an anonymous caller is denied 403, not 401, because no credential can
	// ever satisfy a route that declares no requirement.
	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(authz.Anonymous(), nil)

	resp := newTestAPI(t, authn).Get("/undeclared")
	assert.Equal(t, http.StatusForbidden, resp.Code)
}

// failingResolver owns the Todo type and reports a load error whenever Cedar
// dereferences an entity, so the fail-closed path can be exercised.
type failingResolver struct {
	ctx context.Context
}

func (r *failingResolver) Types() []string { return []string{"Todo"} }

func (r *failingResolver) Resolve(_ types.EntityUID) (types.Entity, bool) {
	authz.RecordLoadError(r.ctx, errors.New("database unavailable"))

	return types.Entity{}, false
}

func TestMiddlewareFailsClosedOnLoadError(t *testing.T) {
	t.Parallel()

	// This policy dereferences a resource attribute, forcing Cedar to load the
	// resource entity — which the resolver fails. The captured error must surface
	// as a 500 rather than a (false) decision on missing data.
	const attrPolicy = `permit (
    principal,
    action == Action::"todo:read",
    resource
) when { resource.owner == "anyone" };`

	contribution := authz.Contribution{
		Policies: []byte(attrPolicy),
		Types:    []string{"Todo"},
		Resolver: func(ctx context.Context, _ authz.Principal) authz.EntityResolver {
			return &failingResolver{ctx: ctx}
		},
	}

	authorizer, err := authz.New([]authz.Contribution{contribution})
	require.NoError(t, err)

	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(user("alice"), nil)

	_, api := humatest.New(t)
	logger := slog.New(slog.DiscardHandler)
	authz.NewMiddleware(api, authn, authorizer, logger, true).Install()

	huma.Register(api, huma.Operation{
		OperationID: "protected",
		Method:      http.MethodGet,
		Path:        "/protected",
		Metadata:    authz.Require(actionRead()),
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	resp := api.Get("/protected")
	assert.Equal(t, http.StatusInternalServerError, resp.Code)
}

func TestMiddlewareDisabledIsPassThrough(t *testing.T) {
	t.Parallel()

	authorizer, err := authz.New(nil)
	require.NoError(t, err)

	// A disabled middleware never authenticates or authorizes: the undeclared
	// route (which would be denied when enabled) succeeds, proving Install is a
	// no-op. The authenticator must not be called.
	authn := mocks.NewAuthenticator(t)

	_, api := humatest.New(t)
	logger := slog.New(slog.DiscardHandler)
	authz.NewMiddleware(api, authn, authorizer, logger, false).Install()

	huma.Register(api, huma.Operation{
		OperationID: "undeclared",
		Method:      http.MethodGet,
		Path:        "/undeclared",
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	resp := api.Get("/undeclared")
	assert.Equal(t, http.StatusNoContent, resp.Code)
}

// idInput binds the {id} path parameter so the route is matched with a path
// value the authz middleware can read via ctx.Param.
type idInput struct {
	ID string `path:"id"`
}

func TestMiddlewareBindsURLIDToResource(t *testing.T) {
	t.Parallel()

	// This policy permits the action only on the specific instance Todo::"42",
	// so an Allow proves the middleware bound the {id} path value into
	// Request.Resource (Todo::"<id>") — with no entity load.
	const instancePolicy = `permit (
    principal,
    action == Action::"todo:read",
    resource == Todo::"42"
);`

	authorizer, err := authz.New([]authz.Contribution{{Policies: []byte(instancePolicy)}})
	require.NoError(t, err)

	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(user("alice"), nil)

	_, api := humatest.New(t)
	logger := slog.New(slog.DiscardHandler)
	authz.NewMiddleware(api, authn, authorizer, logger, true).Install()

	huma.Register(api, huma.Operation{
		OperationID: "get-item",
		Method:      http.MethodGet,
		Path:        "/todos/{id}",
		Metadata:    authz.Require(actionRead(), "id"),
	}, func(_ context.Context, _ *idInput) (*struct{}, error) {
		return &struct{}{}, nil
	})

	// The matching instance is allowed: the path id resolved to Todo::"42".
	allowed := api.Get("/todos/42")
	assert.Equal(t, http.StatusNoContent, allowed.Code, "the bound instance Todo::\"42\" must be allowed")

	// A different instance is denied: the path id resolved to Todo::"99", which
	// the instance policy does not permit, proving the binding is per-request.
	denied := api.Get("/todos/99")
	assert.Equal(t, http.StatusForbidden, denied.Code, "a non-matching instance must be denied")
}
