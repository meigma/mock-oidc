package oidc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRegistry is a minimal IssuerRegistry for the resolver test.
type fakeRegistry struct {
	rec IssuerRecord
	err error
}

func (f fakeRegistry) Materialize(_ context.Context, _ IssuerID) (IssuerRecord, error) {
	return f.rec, f.err
}

func (f fakeRegistry) Known(_ context.Context) ([]IssuerID, error) { return nil, nil }

// fakeKeyStore is a minimal KeyStore for the resolver test.
type fakeKeyStore struct {
	key SigningKey
	err error
}

func (f fakeKeyStore) SigningKey(_ context.Context, _ IssuerID) (SigningKey, error) {
	return f.key, f.err
}

func (f fakeKeyStore) PublicKeys(_ context.Context, _ IssuerID) (JWKS, error) {
	return JWKS{Keys: []JWK{f.key.Public}}, nil
}

// fakeQueue models the memory CallbackQueue's issuer-matched-head semantics for
// resolver tests: DequeueFor pops the head only when its issuer matches, so a
// queued scenario for issuer A blocks issuer B even if B asks first.
type fakeQueue struct {
	items []Scenario
}

func (q *fakeQueue) DequeueFor(_ context.Context, id IssuerID) (Scenario, bool, error) {
	if len(q.items) == 0 {
		return Scenario{}, false, nil
	}
	if q.items[0].IssuerID() != id {
		return Scenario{}, false, nil
	}
	head := q.items[0]
	q.items = q.items[1:]
	return head, true, nil
}

// matchingCallback is a RequestMappingCallback for "default" that always matches
// (a "*" mapping on client_id) and stamps a marker claim, standing in for a
// configured callback in the resolution-priority table.
func matchingCallback(t *testing.T) RequestMappingCallback {
	t.Helper()
	claims := CustomClaims{}
	claims.Set("src", "config")
	cb, err := NewRequestMappingCallback("default", 0, []RequestMapping{
		{Param: "client_id", Match: "*", Claims: claims},
	})
	require.NoError(t, err)
	return cb
}

// TestResolveCallbackPriority is the resolution-priority table: an enqueued
// scenario wins over a configured callback, which wins over the default; the
// refresh grant consults the SAME queue; and an issuer-matched head for another
// issuer blocks consumption without being dropped.
func TestResolveCallbackPriority(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const id = IssuerID("default")
	config := matchingCallback(t)
	in := CallbackInput{Grant: GrantClientCredentials, Client: Client{ID: "app"}}

	scenarioCB := NewDefaultTokenCallback(id)
	scenario, err := NewScenario(scenarioCB)
	require.NoError(t, err)

	t.Run("default when no queue and no config", func(t *testing.T) {
		t.Parallel()
		s := &TokenService{}
		issuer := NewIssuer(id, BaseURL{}, SigningKey{}, nil)
		cb, err := s.resolveCallback(ctx, issuer, in)
		require.NoError(t, err)
		assert.IsType(t, DefaultTokenCallback{}, cb)
	})

	t.Run("config beats default", func(t *testing.T) {
		t.Parallel()
		s := &TokenService{}
		issuer := NewIssuer(id, BaseURL{}, SigningKey{}, []TokenCallback{config})
		cb, err := s.resolveCallback(ctx, issuer, in)
		require.NoError(t, err)
		assert.IsType(t, RequestMappingCallback{}, cb)
	})

	t.Run("enqueued scenario beats config", func(t *testing.T) {
		t.Parallel()
		q := &fakeQueue{items: []Scenario{scenario}}
		s := &TokenService{scenarios: q}
		issuer := NewIssuer(id, BaseURL{}, SigningKey{}, []TokenCallback{config})
		cb, err := s.resolveCallback(ctx, issuer, in)
		require.NoError(t, err)
		assert.IsType(t, DefaultTokenCallback{}, cb, "the enqueued scenario is consumed")
		assert.Empty(t, q.items, "the one-shot scenario is single-use")
	})

	t.Run("refresh grant consults the same queue", func(t *testing.T) {
		t.Parallel()
		q := &fakeQueue{items: []Scenario{scenario}}
		s := &TokenService{scenarios: q}
		rec := RefreshRecord{Issuer: id, Subject: "alice", Callback: config}
		cb, err := s.resolveRefreshCallback(ctx, id, rec)
		require.NoError(t, err)
		assert.IsType(t, DefaultTokenCallback{}, cb, "the scenario wins over the stored callback")
		assert.Empty(t, q.items)
	})

	t.Run("refresh falls back to the stored callback without a scenario", func(t *testing.T) {
		t.Parallel()
		s := &TokenService{scenarios: &fakeQueue{}}
		rec := RefreshRecord{Issuer: id, Subject: "alice", Callback: config}
		cb, err := s.resolveRefreshCallback(ctx, id, rec)
		require.NoError(t, err)
		assert.IsType(t, RequestMappingCallback{}, cb)
	})

	t.Run("issuer-matched head blocks another issuer", func(t *testing.T) {
		t.Parallel()
		otherScenario, err := NewScenario(NewDefaultTokenCallback("other"))
		require.NoError(t, err)
		q := &fakeQueue{items: []Scenario{otherScenario}}
		s := &TokenService{scenarios: q}
		issuer := NewIssuer(id, BaseURL{}, SigningKey{}, []TokenCallback{config})
		cb, err := s.resolveCallback(ctx, issuer, in)
		require.NoError(t, err)
		assert.IsType(t, RequestMappingCallback{}, cb, "the other issuer's head is not consumed here")
		require.Len(t, q.items, 1, "the blocked head stays queued")
	})
}

// TestIssuerResolverResolve exercises the domain-internal collaborator that
// composes Materialize + SigningKey + ResolveBaseURL into an Issuer aggregate.
func TestIssuerResolverResolve(t *testing.T) {
	t.Parallel()

	key := SigningKey{
		KeyID:     KeyID("default"),
		Algorithm: RS256,
		Public: JWK{
			KeyID:     "default",
			Algorithm: RS256,
			KeyType:   KeyTypeRSA,
			Use:       "sig",
			Params:    RSAPublicParams{N: "n", E: "AQAB"},
		},
	}
	r := issuerResolver{
		registry: fakeRegistry{rec: IssuerRecord{ID: "default", Callbacks: nil}, err: nil},
		keys:     fakeKeyStore{key: key, err: nil},
	}

	issuer, err := r.resolve(context.Background(), "default", RequestOrigin{
		Scheme: SchemeHTTP, Host: "localhost", Port: 8080,
	})
	require.NoError(t, err)
	assert.Equal(t, IssuerID("default"), issuer.ID)
	assert.Equal(t, key, issuer.Key)
	assert.Equal(t, "http://localhost:8080/default", issuer.BaseURL.IssuerURL("default"))
}

// TestIssuerResolverPropagatesErrors confirms each composed step's error is
// wrapped and returned.
func TestIssuerResolverPropagatesErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("boom")
	r := issuerResolver{
		registry: fakeRegistry{rec: IssuerRecord{}, err: sentinel},
		keys:     fakeKeyStore{key: SigningKey{}, err: nil},
	}

	_, err := r.resolve(context.Background(), "default", RequestOrigin{Scheme: SchemeHTTP, Host: "h"})
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}
