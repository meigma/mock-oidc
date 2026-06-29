//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cedar-policy/cedar-go/types"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/template-go-api/internal/authz"
	"github.com/meigma/template-go-api/internal/authz/apikey"
)

// seedAPIKey inserts an api_keys row directly through the pool, so the store is
// resolved against rows it did not write — proving the real query and the real
// schema (text[] roles column) round-trip together. It takes the plaintext key
// and stores its SHA-256 digest via encode(sha256(...::bytea), 'hex'); the store
// hashes the presented key in Go, so a successful lookup proves the SQL-side and
// Go-side hashing agree.
func seedAPIKey(ctx context.Context, t *testing.T, pool *pgxpool.Pool, key, subject string, roles []string) {
	t.Helper()

	_, err := pool.Exec(ctx,
		`INSERT INTO api_keys (key_hash, subject, roles) VALUES (encode(sha256($1::bytea), 'hex'), $2, $3)`,
		key, subject, roles,
	)
	require.NoError(t, err)
}

// apiKeyContext builds a huma.Context carrying the X-API-Key header so the real
// Authenticator's credential extraction runs end to end against the store.
func apiKeyContext(key string) huma.Context {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(authz.APIKeyHeader, key)

	return humatest.NewContext(&huma.Operation{}, r, httptest.NewRecorder())
}

// TestAPIKeyStoreAdapter exercises the real PostgreSQL-backed apikey.Store and
// the apikey.Authenticator against the container database — the only place the
// hand-written lookup query and the text[] roles column run against real
// PostgreSQL. Rows are inserted directly, then resolved through the shipped store
// and authenticator, so the lookup query, the text[] roles column, and the
// principal mapping are all exercised together. It shares one migrated container
// (the fixture applies migration 00002, so the api_keys table exists) and restores
// the clean snapshot between subtests for isolation, so the subtests run
// sequentially rather than in parallel.
func TestAPIKeyStoreAdapter(t *testing.T) {
	ctx := context.Background()
	fix := setupPostgres(ctx, t)

	t.Run("LookupResolvesSubjectAndRoles", func(t *testing.T) {
		pool := fix.ResetPool(ctx, t)
		seedAPIKey(ctx, t, pool, "user-key", "alice", []string{"user"})
		seedAPIKey(ctx, t, pool, "admin-key", "root", []string{"admin", "user"})

		store := apikey.NewStore(pool)

		// A user-role key resolves to its subject and single role.
		user, ok, err := store.Lookup(ctx, "user-key")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "alice", user.Subject)
		assert.Equal(t, []string{"user"}, user.Roles)

		// An admin key with multiple roles parses the whole text[] array, proving
		// the roles[] column round-trips through the scan in order.
		admin, ok, err := store.Lookup(ctx, "admin-key")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "root", admin.Subject)
		assert.Equal(t, []string{"admin", "user"}, admin.Roles)
	})

	t.Run("UnknownKeyIsMissNotError", func(t *testing.T) {
		pool := fix.ResetPool(ctx, t)
		seedAPIKey(ctx, t, pool, "user-key", "alice", []string{"user"})

		store := apikey.NewStore(pool)

		// An unknown key is a miss (false), never an error — the authn middleware
		// maps it to 401, not 500.
		identity, ok, err := store.Lookup(ctx, "does-not-exist")
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Equal(t, apikey.Identity{}, identity)
	})

	t.Run("KeyIsMatchedExactly", func(t *testing.T) {
		pool := fix.ResetPool(ctx, t)
		seedAPIKey(ctx, t, pool, "secret-key", "alice", []string{"user"})

		store := apikey.NewStore(pool)

		// A prefix of a stored key must not match: the lookup hashes the presented
		// key and matches the digest on the key_hash primary key — a different
		// input yields a different SHA-256, never a prefix/LIKE match.
		_, ok, err := store.Lookup(ctx, "secret")
		require.NoError(t, err)
		assert.False(t, ok, "a partial key must not resolve to a stored row")

		// A trailing-space variant is likewise a distinct key (distinct digest) and
		// must miss.
		_, ok, err = store.Lookup(ctx, "secret-key ")
		require.NoError(t, err)
		assert.False(t, ok, "a key with trailing whitespace must not match a stored row")
	})

	t.Run("EmptyRolesResolveToEmptySlice", func(t *testing.T) {
		pool := fix.ResetPool(ctx, t)
		// roles defaults to '{}' (the migration's column default), so a row with no
		// roles resolves to an empty, non-nil slice — a principal with no role.
		_, err := pool.Exec(ctx,
			`INSERT INTO api_keys (key_hash, subject) VALUES (encode(sha256($1::bytea), 'hex'), $2)`,
			"no-roles", "nobody")
		require.NoError(t, err)

		store := apikey.NewStore(pool)

		identity, ok, err := store.Lookup(ctx, "no-roles")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "nobody", identity.Subject)
		assert.Empty(t, identity.Roles)
	})

	t.Run("AuthenticatorResolvesPrincipalThroughStore", func(t *testing.T) {
		pool := fix.ResetPool(ctx, t)
		seedAPIKey(ctx, t, pool, "alice-key", "alice", []string{"user", "auditor"})

		// The full apikey path: the real store wired into the real Authenticator,
		// resolving an X-API-Key credential to a Principal (subject -> User entity,
		// roles -> the roles claim the base principal resolver projects onto Role
		// parents).
		auth := apikey.NewAuthenticator(apikey.NewStore(pool))

		principal, err := auth.Authenticate(apiKeyContext("alice-key"))
		require.NoError(t, err)
		assert.False(t, principal.IsAnonymous())
		assert.Equal(t, types.NewEntityUID(authz.PrincipalType, "alice"), principal.UID)

		roles, ok := principal.Claims.Get(authz.RolesClaim)
		require.True(t, ok, "resolved roles must be recorded on the claims")
		assert.Equal(t, types.NewSet(types.String("user"), types.String("auditor")), roles)
	})

	t.Run("AuthenticatorUnknownKeyIsInvalid", func(t *testing.T) {
		pool := fix.ResetPool(ctx, t)

		auth := apikey.NewAuthenticator(apikey.NewStore(pool))

		principal, err := auth.Authenticate(apiKeyContext("nope"))
		require.ErrorIs(t, err, apikey.ErrInvalidKey)
		assert.True(t, principal.IsAnonymous())
	})
}
