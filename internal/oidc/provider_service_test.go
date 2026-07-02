package oidc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/mocks"
)

// TestProviderServiceDiscovery asserts the discovery document is built for the
// proxy-resolved base URL, with endpoints joined at the issuer segment and the
// advertised algorithm set sourced from the domain constant.
func TestProviderServiceDiscovery(t *testing.T) {
	t.Parallel()

	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}
	id := oidc.IssuerID("default")

	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{ID: id}, nil)
	keys := mocks.NewKeyStore(t)
	keys.EXPECT().
		SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)

	svc := oidc.NewProviderService(registry, keys)
	doc, err := svc.Discovery(context.Background(), id, origin)
	require.NoError(t, err)

	assert.Equal(t, "http://localhost:8080/default", doc.Issuer)
	assert.Equal(t, "http://localhost:8080/default/token", doc.TokenEndpoint)
	assert.Equal(t, "http://localhost:8080/default/jwks", doc.JWKSURI)
	assert.Equal(t, "http://localhost:8080/default/authorize", doc.AuthorizationEndpoint)
	assert.Equal(t, oidc.SupportedSigningAlgorithms(), doc.IDTokenSigningAlgValuesSupported)
}

// TestProviderServiceJWKSForcesMaterialization asserts JWKS goes straight to the
// KeyStore (which materializes the key) and does not consult the registry.
func TestProviderServiceJWKSForcesMaterialization(t *testing.T) {
	t.Parallel()

	id := oidc.IssuerID("default")
	want := oidc.JWKS{Keys: []oidc.JWK{{
		KeyID:     id.KeyID(),
		Algorithm: oidc.RS256,
		KeyType:   oidc.KeyTypeRSA,
		Use:       "sig",
		Params:    oidc.RSAPublicParams{N: "n", E: "AQAB"},
	}}}

	keys := mocks.NewKeyStore(t)
	keys.EXPECT().PublicKeys(mock.Anything, id).Return(want, nil)
	registry := mocks.NewIssuerRegistry(t) // Materialize must not be called for JWKS.

	svc := oidc.NewProviderService(registry, keys)
	got, err := svc.JWKS(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}
