//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/template-go-api/internal/app"
	"github.com/meigma/template-go-api/internal/authz"
	"github.com/meigma/template-go-api/internal/config"
	"github.com/meigma/template-go-api/internal/observability"
)

// e2eHeader names a request header to attach to an authz end-to-end request,
// such as the X-API-Key credential.
type e2eHeader struct{ key, value string }

// e2eResult is the slice of an HTTP response the authz assertions care about.
type e2eResult struct {
	status int
	body   string
}

// e2eServer wires the FULL application against the container database with
// authorization ENABLED. It deliberately does not inject a repository or an
// authenticator: app.New takes the real composition path, so the request runs
// through the real PostgreSQL todo repository, the real PostgreSQL-backed api-key
// Authenticator, the real Cedar Authorizer (base.cedar admin override + the todo
// slice policy), and the real Huma authn/authz middleware chain. Anything less
// would not be an end-to-end authz test.
func e2eServer(ctx context.Context, t *testing.T, databaseURL string) *httptest.Server {
	t.Helper()

	vp := viper.New()
	vp.Set("database-url", databaseURL)
	// Rate limiting is orthogonal here, and its per-IP bucket would be shared
	// across this suite's rapid requests from one loopback client; disable it so
	// the authz assertions stay deterministic. Rate limiting is covered by its
	// own tests in internal/ratelimit.
	vp.Set("rate-limit-enabled", false)
	cfg := config.Load(vp)
	require.NoError(t, cfg.Validate())
	// Guard the premise of the whole suite: authz must be enabled by default now
	// that the routes are tagged, otherwise the middleware would be inert.
	require.True(t, cfg.AuthzEnabled, "authz must be enabled for the e2e suite to mean anything")

	logger := observability.NewLogger(io.Discard, slog.LevelError, "json")
	application, err := app.New(ctx, cfg, logger, "test")
	require.NoError(t, err)

	srv := httptest.NewServer(application.Handler())
	t.Cleanup(srv.Close)

	return srv
}

// e2eRequest drives one request against the wired server, attaching the given
// headers (credential, content-type), and returns its status and body.
func e2eRequest(
	t *testing.T,
	srv *httptest.Server,
	method, path, body string,
	headers ...e2eHeader,
) e2eResult {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, h := range headers {
		req.Header.Set(h.key, h.value)
	}

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return e2eResult{status: resp.StatusCode, body: string(data)}
}

// apiKey returns the X-API-Key header for key.
func apiKey(key string) e2eHeader { return e2eHeader{key: authz.APIKeyHeader, value: key} }

// bearer returns the Authorization: Bearer header for key.
func bearer(key string) e2eHeader { return e2eHeader{key: "Authorization", value: "Bearer " + key} }

// createdID parses the id out of a TodoOutput body, asserting the response was a
// successful create/get so a later by-id request can target a real instance.
func createdID(t *testing.T, body string) string {
	t.Helper()

	var todo struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &todo))
	require.NotEmpty(t, todo.ID, "expected a todo id in: %s", body)

	return todo.ID
}

// TestAuthzEndToEnd drives real HTTP traffic through the full stack with
// authorization enabled and a real PostgreSQL-backed api-key authenticator. It
// seeds a user-role key, an admin-role key, and a roleless key directly into the
// api_keys table, then asserts the real decision matrix: no credential -> 401,
// an unauthorized role -> 403, a user key -> 2xx on every granted todo route, and
// an admin key -> allowed via base.cedar. The by-id routes (get/complete) confirm
// those routes are reachable, authorized for the granted role, and return the
// correct instance; the Resource = Todo::"<id>" binding itself is guarded by the
// unit test TestMiddlewareBindsURLIDToResource, since the coarse policy here is
// resource-agnostic and cannot distinguish the bound id.
//
// It shares one migrated container and seeds the api_keys rows once after a clean
// restore; the app connects its own pool to the same database, so the request
// path resolves keys against the seeded rows exactly as production would.
func TestAuthzEndToEnd(t *testing.T) {
	ctx := context.Background()
	fix := setupPostgres(ctx, t)

	// Restore the clean schema, then seed the keys through a dedicated pool. The
	// app opens its own pool to the same database below.
	pool := fix.ResetPool(ctx, t)
	seedAPIKey(ctx, t, pool, "user-key", "alice", []string{"user"})
	seedAPIKey(ctx, t, pool, "admin-key", "root", []string{"admin"})
	seedAPIKey(ctx, t, pool, "guest-key", "guest", []string{"guest"})

	srv := e2eServer(ctx, t, fix.url)

	t.Run("NoCredentialIsUnauthorized", func(t *testing.T) {
		// Anonymous on a protected route -> 401 (no principal + deny path), not 403.
		got := e2eRequest(t, srv, http.MethodGet, "/v1/todos", "")
		assert.Equal(t, http.StatusUnauthorized, got.status, got.body)

		got = e2eRequest(t, srv, http.MethodPost, "/v1/todos", `{"title":"x"}`)
		assert.Equal(t, http.StatusUnauthorized, got.status, got.body)
	})

	t.Run("UnauthorizedRoleIsForbidden", func(t *testing.T) {
		// guest-key authenticates (so it is not anonymous) but carries neither the
		// user nor admin role the policies grant -> 403, not 401.
		got := e2eRequest(t, srv, http.MethodGet, "/v1/todos", "", apiKey("guest-key"))
		assert.Equal(t, http.StatusForbidden, got.status, got.body)

		got = e2eRequest(t, srv, http.MethodPost, "/v1/todos", `{"title":"x"}`, apiKey("guest-key"))
		assert.Equal(t, http.StatusForbidden, got.status, got.body)
	})

	t.Run("UserKeyIsAllowedAcrossGrantedRoutes", func(t *testing.T) {
		// create (collection, type-level resource) -> 201.
		created := e2eRequest(t, srv, http.MethodPost, "/v1/todos", `{"title":"buy milk"}`, apiKey("user-key"))
		require.Equal(t, http.StatusCreated, created.status, created.body)
		id := createdID(t, created.body)

		// list (collection) -> 200.
		listed := e2eRequest(t, srv, http.MethodGet, "/v1/todos", "", apiKey("user-key"))
		assert.Equal(t, http.StatusOK, listed.status, listed.body)

		// get by id -> 200 for the todo created above: the by-id route is reachable,
		// authorized for the user role, and returns the same instance. (The
		// Resource = Todo::"<id>" binding is proven in TestMiddlewareBindsURLIDToResource;
		// the coarse policy here allows any resource.)
		fetched := e2eRequest(t, srv, http.MethodGet, "/v1/todos/"+id, "", apiKey("user-key"))
		assert.Equal(t, http.StatusOK, fetched.status, fetched.body)
		assert.Equal(t, id, createdID(t, fetched.body))

		// complete by id (URL-identity, update action) -> 200.
		completed := e2eRequest(t, srv, http.MethodPost, "/v1/todos/"+id+"/complete", "", apiKey("user-key"))
		assert.Equal(t, http.StatusOK, completed.status, completed.body)
		var out struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.Unmarshal([]byte(completed.body), &out))
		assert.Equal(t, "completed", out.Status)
	})

	t.Run("AdminKeyIsAllowedViaBasePolicy", func(t *testing.T) {
		// The admin role is granted everything by base.cedar's admin override, not
		// by the todo slice policy, so this proves the merged PolicySet evaluates
		// the cross-cutting rule too.
		created := e2eRequest(t, srv, http.MethodPost, "/v1/todos", `{"title":"admin task"}`, apiKey("admin-key"))
		require.Equal(t, http.StatusCreated, created.status, created.body)
		id := createdID(t, created.body)

		fetched := e2eRequest(t, srv, http.MethodGet, "/v1/todos/"+id, "", apiKey("admin-key"))
		assert.Equal(t, http.StatusOK, fetched.status, fetched.body)

		listed := e2eRequest(t, srv, http.MethodGet, "/v1/todos", "", apiKey("admin-key"))
		assert.Equal(t, http.StatusOK, listed.status, listed.body)
	})

	t.Run("BearerCredentialAlsoAuthenticates", func(t *testing.T) {
		// The same user key presented as an Authorization: Bearer credential must
		// resolve identically, proving the second accepted credential carrier.
		listed := e2eRequest(t, srv, http.MethodGet, "/v1/todos", "", bearer("user-key"))
		assert.Equal(t, http.StatusOK, listed.status, listed.body)
	})

	t.Run("UnknownKeyIsUnauthorized", func(t *testing.T) {
		// A present-but-unknown credential is a 401 (invalid credential), distinct
		// from the missing-credential and wrong-role cases above.
		got := e2eRequest(t, srv, http.MethodGet, "/v1/todos", "", apiKey("not-a-real-key"))
		assert.Equal(t, http.StatusUnauthorized, got.status, got.body)
	})
}
