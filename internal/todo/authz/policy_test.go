package authz_test

import (
	"context"
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
	todoauthz "github.com/meigma/template-go-api/internal/todo/authz"
	"github.com/meigma/template-go-api/internal/todo/todotest"
)

// principal returns a non-anonymous principal carrying the given roles, mirroring
// what the API-key authenticator builds from a resolved key.
func principal(subject string, roles ...string) authz.Principal {
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

// newAPI builds a humatest API with the authz middleware installed over the
// merged base + todo-slice policy set, and registers a single create operation
// tagged with the todo create action. This drives policy decisions through the
// same merged engine the server uses (base.cedar admin override + the slice's
// coarse user-role grant + the principal resolver projecting roles).
func newAPI(t *testing.T, authenticator authz.Authenticator) humatest.TestAPI {
	t.Helper()

	authorizer, err := authz.New([]authz.Contribution{todoauthz.Contribution(todotest.NewRepository())})
	require.NoError(t, err)

	_, api := humatest.New(t)
	authz.NewMiddleware(api, authenticator, authorizer, nil, true).Install()

	huma.Register(api, huma.Operation{
		OperationID: "create-todo",
		Method:      http.MethodPost,
		Path:        "/todos",
		Metadata:    authz.Require(todoauthz.ActionCreate),
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api
}

func TestPolicyAllowsUserRole(t *testing.T) {
	t.Parallel()

	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(principal("alice", "user"), nil)

	resp := newAPI(t, authn).Post("/todos")
	assert.Equal(t, http.StatusNoContent, resp.Code, "the coarse policy grants the user role")
}

func TestPolicyAllowsAdminViaBasePolicy(t *testing.T) {
	t.Parallel()

	// The admin grant lives in base.cedar, not the slice policy, so an admin with
	// no user role still passes — exercising the merged base + slice set.
	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(principal("root", "admin"), nil)

	resp := newAPI(t, authn).Post("/todos")
	assert.Equal(t, http.StatusNoContent, resp.Code, "the base admin override grants any todo action")
}

func TestPolicyForbidsInsufficientRole(t *testing.T) {
	t.Parallel()

	// An authenticated caller with a role neither policy grants is forbidden.
	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(principal("guest", "viewer"), nil)

	resp := newAPI(t, authn).Post("/todos")
	assert.Equal(t, http.StatusForbidden, resp.Code, "a role the policies do not grant is denied 403")
}

func TestPolicyRejectsAnonymousWith401(t *testing.T) {
	t.Parallel()

	authn := mocks.NewAuthenticator(t)
	authn.EXPECT().Authenticate(mock.Anything).Return(authz.Anonymous(), nil)

	resp := newAPI(t, authn).Post("/todos")
	assert.Equal(t, http.StatusUnauthorized, resp.Code, "an anonymous caller is denied 401, not 403")
}
