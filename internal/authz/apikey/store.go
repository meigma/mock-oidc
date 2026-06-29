package apikey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// lookupQuery resolves an API key to its subject and roles. It is hand-written
// and parameterized (so the key is never interpolated) to keep this package
// self-contained and trivially removable — it deliberately does not introduce a
// second sqlc package.
//
// SECURITY: the api_keys table stores only a SHA-256 hash of each key
// (key_hash), never the key itself; Lookup hashes the presented credential the
// same way and matches on the digest. A leaked table dump therefore reveals no
// replayable credentials. No constant-time compare is needed: the match is an
// indexed equality on a preimage-resistant 256-bit digest, and a caller controls
// only the key (the preimage), not the stored hash, so the lookup is not a
// practical timing oracle — there is no secret string compared in process.
const lookupQuery = `SELECT subject, roles FROM api_keys WHERE key_hash = $1`

// hashKey returns the lowercase-hex SHA-256 digest of key. The dev seed and the
// integration fixtures compute the identical digest in SQL via
// encode(sha256($1::bytea), 'hex'), so a key stored by either path resolves here.
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Store is the PostgreSQL-backed APIKeyStore. It resolves keys against the
// api_keys table using the shared pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore constructs a Store over the shared connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Lookup resolves key to its subject and roles. It returns (Identity, false, nil)
// when no row matches (an unknown key, not an error) and a non-nil error only on
// a query failure. The key is hashed before the query, so only its digest — never
// the key itself — is bound as a parameter, and the key is never logged.
func (s *Store) Lookup(ctx context.Context, key string) (Identity, bool, error) {
	var (
		subject string
		roles   []string
	)

	err := s.pool.QueryRow(ctx, lookupQuery, hashKey(key)).Scan(&subject, &roles)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Identity{}, false, nil
		}

		// Do not include key in the error: it must never reach a log line.
		return Identity{}, false, fmt.Errorf("query api key: %w", err)
	}

	return Identity{Subject: subject, Roles: roles}, true, nil
}
