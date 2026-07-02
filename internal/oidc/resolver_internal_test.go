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
