package oidc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// TestNewDiscoveryDocument confirms every endpoint is joined under the issuer URL
// and the advertised algorithm set flows through from SupportedSigningAlgorithms.
func TestNewDiscoveryDocument(t *testing.T) {
	t.Parallel()

	base, err := oidc.NewBaseURL(oidc.SchemeHTTP, "localhost", 8080)
	require.NoError(t, err)

	doc := oidc.NewDiscoveryDocument(base, "default", oidc.SupportedSigningAlgorithms())

	const issuerURL = "http://localhost:8080/default"
	assert.Equal(t, issuerURL, doc.Issuer)
	assert.Equal(t, issuerURL+"/authorize", doc.AuthorizationEndpoint)
	assert.Equal(t, issuerURL+"/endsession", doc.EndSessionEndpoint)
	assert.Equal(t, issuerURL+"/revoke", doc.RevocationEndpoint)
	assert.Equal(t, issuerURL+"/token", doc.TokenEndpoint)
	assert.Equal(t, issuerURL+"/userinfo", doc.UserinfoEndpoint)
	assert.Equal(t, issuerURL+"/jwks", doc.JWKSURI)
	assert.Equal(t, issuerURL+"/introspect", doc.IntrospectionEndpoint)

	assert.Equal(t, oidc.SupportedSigningAlgorithms(), doc.IDTokenSigningAlgValuesSupported)
	assert.Equal(t, []string{"code", "none", "id_token", "token"}, doc.ResponseTypesSupported)
	assert.Equal(t, []string{"query", "fragment", "form_post"}, doc.ResponseModesSupported)
	assert.Equal(t, []string{"public"}, doc.SubjectTypesSupported)
	assert.Equal(t, []string{"plain", "S256"}, doc.CodeChallengeMethodsSupported)
}
