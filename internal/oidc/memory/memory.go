package memory

import (
	"context"
	"errors"
	"sync"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// ErrCodeNotFound is the sentinel CodeStore.Take returns for an unknown or
// already-used authorization code. The token service maps it to invalid_grant
// "unknown or already-used authorization code"; callers match it with
// [errors.Is].
var ErrCodeNotFound = errors.New("authorization code not found")

// IssuerRegistry is the in-memory [oidc.IssuerRegistry]: any non-reserved issuer
// id becomes live on first reference (computeIfAbsent), and the config seed may
// pre-populate records that carry configured callbacks. It is concurrency-safe
// under an RWMutex.
type IssuerRegistry struct {
	mu      sync.RWMutex
	records map[oidc.IssuerID]oidc.IssuerRecord
}

// NewIssuerRegistry builds the registry, pre-populating any seeded records
// (issuers configured with callbacks). Zero-config issuers need no seed.
func NewIssuerRegistry(seed ...oidc.IssuerRecord) *IssuerRegistry {
	records := make(map[oidc.IssuerID]oidc.IssuerRecord, len(seed))
	for _, rec := range seed {
		records[rec.ID] = rec
	}
	return &IssuerRegistry{records: records}
}

// Materialize records id as a live issuer on first reference and returns its
// record. It is idempotent for the process lifetime.
func (r *IssuerRegistry) Materialize(_ context.Context, id oidc.IssuerID) (oidc.IssuerRecord, error) {
	r.mu.RLock()
	rec, ok := r.records[id]
	r.mu.RUnlock()
	if ok {
		return rec, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.records[id]; ok { // re-check under the write lock
		return existing, nil
	}
	rec = oidc.IssuerRecord{ID: id, Callbacks: nil}
	r.records[id] = rec
	return rec, nil
}

// Known returns every materialized issuer id, for control-plane enumeration.
func (r *IssuerRegistry) Known(_ context.Context) ([]oidc.IssuerID, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]oidc.IssuerID, 0, len(r.records))
	for id := range r.records {
		ids = append(ids, id)
	}
	return ids, nil
}

// CodeStore is the in-memory, single-use [oidc.CodeStore]: it caches a
// CodeRecord under an authorization code at /authorize and burns it on the first
// Take at /token. It is concurrency-safe under a Mutex.
type CodeStore struct {
	mu      sync.Mutex
	records map[oidc.AuthorizationCode]oidc.CodeRecord
}

// NewCodeStore builds an empty code store.
func NewCodeStore() *CodeStore {
	return &CodeStore{records: make(map[oidc.AuthorizationCode]oidc.CodeRecord)}
}

// Save stores rec under code, overwriting any prior record for the same code.
func (s *CodeStore) Save(_ context.Context, code oidc.AuthorizationCode, rec oidc.CodeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[code] = rec
	return nil
}

// Take atomically returns and removes the record for code, enforcing single use:
// a second Take of the same code — or a Take of an unknown code — returns
// [ErrCodeNotFound]. The delete happens before the caller runs any PKCE check,
// so a failed exchange still burns the code.
func (s *CodeStore) Take(_ context.Context, code oidc.AuthorizationCode) (oidc.CodeRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[code]
	if !ok {
		return oidc.CodeRecord{}, ErrCodeNotFound
	}
	delete(s.records, code)
	return rec, nil
}
