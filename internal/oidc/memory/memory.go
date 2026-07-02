package memory

import (
	"context"
	"sync"

	"github.com/meigma/mock-oidc/internal/oidc"
)

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
