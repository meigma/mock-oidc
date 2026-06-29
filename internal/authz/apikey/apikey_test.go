package apikey_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cedar-policy/cedar-go/types"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/template-go-api/internal/authz"
	"github.com/meigma/template-go-api/internal/authz/apikey"
	"github.com/meigma/template-go-api/internal/authz/apikey/mocks"
)

// contextWith builds a huma.Context carrying the given headers, so the
// authenticator's credential extraction can be exercised without a server.
func contextWith(headers map[string]string) huma.Context {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}

	return humatest.NewContext(&huma.Operation{}, r, httptest.NewRecorder())
}

func TestAuthenticateResolvesAPIKeyHeader(t *testing.T) {
	t.Parallel()

	store := mocks.NewAPIKeyStore(t)
	store.EXPECT().Lookup(mock.Anything, "secret-key").
		Return(apikey.Identity{Subject: "alice", Roles: []string{"admin"}}, true, nil)

	auth := apikey.NewAuthenticator(store)
	principal, err := auth.Authenticate(contextWith(map[string]string{authz.APIKeyHeader: "secret-key"}))
	require.NoError(t, err)

	assert.Equal(t, types.NewEntityUID("User", "alice"), principal.UID)
	assert.False(t, principal.IsAnonymous())

	roles, ok := principal.Claims.Get(authz.RolesClaim)
	require.True(t, ok, "resolved roles must be recorded on the claims")
	assert.Equal(t, types.NewSet(types.String("admin")), roles)
}

func TestAuthenticateAcceptsBearerCredential(t *testing.T) {
	t.Parallel()

	store := mocks.NewAPIKeyStore(t)
	store.EXPECT().Lookup(mock.Anything, "bearer-key").
		Return(apikey.Identity{Subject: "bob"}, true, nil)

	auth := apikey.NewAuthenticator(store)
	principal, err := auth.Authenticate(contextWith(map[string]string{"Authorization": "Bearer bearer-key"}))
	require.NoError(t, err)
	assert.Equal(t, types.NewEntityUID("User", "bob"), principal.UID)
}

func TestAuthenticatePrefersAPIKeyHeaderOverBearer(t *testing.T) {
	t.Parallel()

	store := mocks.NewAPIKeyStore(t)
	store.EXPECT().Lookup(mock.Anything, "header-key").
		Return(apikey.Identity{Subject: "alice"}, true, nil)

	auth := apikey.NewAuthenticator(store)
	_, err := auth.Authenticate(contextWith(map[string]string{
		authz.APIKeyHeader: "header-key",
		"Authorization":    "Bearer ignored",
	}))
	require.NoError(t, err)
}

func TestAuthenticateWithoutCredentialIsAnonymous(t *testing.T) {
	t.Parallel()

	// No Lookup is expected: absence of a credential must not hit the store.
	store := mocks.NewAPIKeyStore(t)

	auth := apikey.NewAuthenticator(store)
	principal, err := auth.Authenticate(contextWith(nil))
	require.NoError(t, err)
	assert.True(t, principal.IsAnonymous())
}

func TestAuthenticateUnknownKeyIsInvalid(t *testing.T) {
	t.Parallel()

	store := mocks.NewAPIKeyStore(t)
	store.EXPECT().Lookup(mock.Anything, "nope").Return(apikey.Identity{}, false, nil)

	auth := apikey.NewAuthenticator(store)
	principal, err := auth.Authenticate(contextWith(map[string]string{authz.APIKeyHeader: "nope"}))
	require.ErrorIs(t, err, apikey.ErrInvalidKey)
	assert.True(t, principal.IsAnonymous())
}

func TestAuthenticateStoreFailureIsError(t *testing.T) {
	t.Parallel()

	store := mocks.NewAPIKeyStore(t)
	store.EXPECT().Lookup(mock.Anything, "boom").Return(apikey.Identity{}, false, errors.New("db down"))

	auth := apikey.NewAuthenticator(store)
	principal, err := auth.Authenticate(contextWith(map[string]string{authz.APIKeyHeader: "boom"}))
	require.Error(t, err)
	require.NotErrorIs(t, err, apikey.ErrInvalidKey)
	assert.True(t, principal.IsAnonymous())
	// The returned error must not contain the key.
	assert.NotContains(t, err.Error(), "boom")
}
