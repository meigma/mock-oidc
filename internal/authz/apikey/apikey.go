// Package apikey is the template's default Authenticator: it reads an API-key
// credential from a request and resolves it to an authz.Principal through the
// APIKeyStore port. It is the deferred-authn placeholder — a real but minimal
// mechanism that demonstrates the full authorization flow and is trivially
// removable (delete this package and the api_keys migration). Integrators
// replace it with a real verifier (JWT/OIDC/session).
//
// The shipped store is PostgreSQL-backed (store.go); keys live in an api_keys
// table since the template is postgres-only. Only a SHA-256 hash of each key is
// stored (key_hash); the Store hashes the presented credential before lookup, so
// a table dump exposes no replayable credentials. The package hand-writes its
// single query rather than adding a second sqlc package, so removal stays
// surgical for the Go code. The api_keys table lives in the shared migrations
// directory, but sqlc.yaml sets omit_unused_structs, so the todo sqlc package
// emits no ApiKey model and removing the feature needs no sqlc regeneration.
package apikey

import (
	"context"
	"errors"
	"strings"

	"github.com/cedar-policy/cedar-go/types"
	"github.com/danielgtaylor/huma/v2"

	"github.com/meigma/template-go-api/internal/authz"
)

// bearerPrefix is the scheme prefix of an Authorization: Bearer credential.
const bearerPrefix = "Bearer "

// ErrInvalidKey is returned when a credential is present but does not resolve to
// a known principal. The authn middleware maps it to 401. A request with no
// credential is not an error — it yields the anonymous principal.
var ErrInvalidKey = errors.New("invalid api key")

// APIKeyStore is the outbound port that resolves an API key to its principal. It
// is declared here, by its consumer, and implemented by adapters (the shipped
// PostgreSQL Store, or a mock in tests).
//
//nolint:revive // APIKeyStore is the name fixed by the authz design for this port.
type APIKeyStore interface {
	// Lookup returns the subject and roles bound to key. The boolean is false
	// when no row matches the key (an unknown key, not an error); err is
	// non-nil only on a store failure. Implementations must never log the key.
	Lookup(ctx context.Context, key string) (Identity, bool, error)
}

// Identity is the principal data a store binds to an API key: the caller's
// subject and the roles granted to it.
type Identity struct {
	// Subject identifies the caller (becomes the principal entity's id).
	Subject string
	// Roles are the caller's role names (projected onto the principal's parents
	// as Role::"<name>" and recorded under the roles claim).
	Roles []string
}

// Authenticator resolves an API-key credential to an authz.Principal via a
// store. It satisfies authz.Authenticator.
type Authenticator struct {
	store APIKeyStore
}

// NewAuthenticator constructs an Authenticator backed by store.
func NewAuthenticator(store APIKeyStore) *Authenticator {
	return &Authenticator{store: store}
}

// Authenticate reads the API key from the request and resolves it. With no
// credential it returns the anonymous principal and no error, so public
// operations still work; with an unknown or store-failed credential it returns
// an error, which the middleware maps to 401.
func (a *Authenticator) Authenticate(ctx huma.Context) (authz.Principal, error) {
	key := credentialFrom(ctx)
	if key == "" {
		return authz.Anonymous(), nil
	}

	identity, ok, err := a.store.Lookup(ctx.Context(), key)
	if err != nil {
		// Wrap without the key: the key must never reach a log line.
		return authz.Anonymous(), errors.New("api key lookup failed")
	}
	if !ok {
		return authz.Anonymous(), ErrInvalidKey
	}

	return toPrincipal(identity), nil
}

// credentialFrom extracts the API key from the request, preferring the X-API-Key
// header and falling back to an Authorization: Bearer credential. It returns the
// empty string when neither is present.
func credentialFrom(ctx huma.Context) string {
	if key := strings.TrimSpace(ctx.Header(authz.APIKeyHeader)); key != "" {
		return key
	}

	authorization := strings.TrimSpace(ctx.Header("Authorization"))
	if rest, found := strings.CutPrefix(authorization, bearerPrefix); found {
		return strings.TrimSpace(rest)
	}

	return ""
}

// toPrincipal maps a resolved Identity to an authz.Principal: the subject
// becomes the User entity and the roles are recorded under the shared roles
// claim, which the base principal resolver projects onto the principal entity's
// Role parents so policies can match `principal in Role::"…"` with no load.
func toPrincipal(identity Identity) authz.Principal {
	roleValues := make([]types.Value, 0, len(identity.Roles))
	for _, role := range identity.Roles {
		roleValues = append(roleValues, types.String(role))
	}

	return authz.Principal{
		// Mint the principal under authz.PrincipalType: the composite getter routes
		// principal lookups by the principal's UID type, so the type the
		// authenticator stamps must be the one the base principal resolver owns.
		UID: types.NewEntityUID(authz.PrincipalType, types.String(identity.Subject)),
		Claims: types.NewRecord(types.RecordMap{
			authz.RolesClaim: types.NewSet(roleValues...),
		}),
	}
}
