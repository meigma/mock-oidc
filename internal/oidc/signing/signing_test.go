package signing_test

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/signing"
)

// TestSupportedAlgorithmsMatchDomainConstant is the constant-sync guard: the
// signing adapter must produce exactly the algorithm set discovery advertises
// (oidc.SupportedSigningAlgorithms). Drift in either list fails the build.
func TestSupportedAlgorithmsMatchDomainConstant(t *testing.T) {
	t.Parallel()

	assert.Equal(t, oidc.SupportedSigningAlgorithms(), signing.SupportedAlgorithms())

	// And discovery, built from the domain constant, advertises exactly what the
	// signer declares it can mint.
	base, err := oidc.NewBaseURL(oidc.SchemeHTTP, "localhost", 8080)
	require.NoError(t, err)
	doc := oidc.NewDiscoveryDocument(base, "default", oidc.SupportedSigningAlgorithms())
	assert.Equal(t, signing.SupportedAlgorithms(), doc.IDTokenSigningAlgValuesSupported)
}

// TestProviderSignsVerifiableRS256 proves the compact JWS the adapter emits
// verifies against the public key it publishes — the end-to-end crypto contract
// the R2/R3 client-library checks depend on.
func TestProviderSignsVerifiableRS256(t *testing.T) {
	t.Parallel()

	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	ctx := context.Background()
	id := oidc.IssuerID("default")
	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	tenant := "default"
	claims := oidc.ClaimSet{
		Subject:   "app",
		Audience:  oidc.Audience{"default"},
		Issuer:    "http://localhost:8080/default",
		IssuedAt:  now,
		NotBefore: now,
		Expiry:    now.Add(time.Hour),
		JWTID:     "jti-1",
		Tenant:    &tenant,
	}
	tok := oidc.NewToken(id, oidc.RS256, oidc.DefaultJOSEType, claims)

	signed, err := p.Sign(ctx, id, tok)
	require.NoError(t, err)

	parts := strings.Split(string(signed), ".")
	require.Len(t, parts, 3, "compact JWS has three segments")

	header := decodeJSON(t, parts[0])
	assert.Equal(t, "RS256", header["alg"])
	assert.Equal(t, "JWT", header["typ"])
	assert.Equal(t, "default", header["kid"])

	payload := decodeJSON(t, parts[1])
	assert.Equal(t, "app", payload["sub"])
	assert.Equal(t, "http://localhost:8080/default", payload["iss"])
	assert.Equal(t, []any{"default"}, payload["aud"])
	assert.Equal(t, "default", payload["tid"])
	assert.Equal(t, "jti-1", payload["jti"])
	assert.InDelta(t, float64(now.Unix()), payload["iat"], 0)
	assert.InDelta(t, float64(now.Unix()), payload["nbf"], 0)
	assert.InDelta(t, float64(now.Add(time.Hour).Unix()), payload["exp"], 0)

	// The published public key verifies the signature.
	sk, err := p.SigningKey(ctx, id)
	require.NoError(t, err)
	pub := rsaPublic(t, sk.Public)
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	require.NoError(t, rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig))
}

// TestProviderKeysAreStableAndPerIssuer verifies the deterministic keying
// contract: kid == issuer id, one key per issuer, stable across references, and
// distinct issuers draw distinct seed keys.
func TestProviderKeysAreStableAndPerIssuer(t *testing.T) {
	t.Parallel()

	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)
	ctx := context.Background()

	jwks, err := p.PublicKeys(ctx, "default")
	require.NoError(t, err)
	require.Len(t, jwks.Keys, 1)
	assert.Equal(t, oidc.KeyID("default"), jwks.Keys[0].KeyID)
	assert.Equal(t, "sig", jwks.Keys[0].Use)
	assert.Equal(t, oidc.RS256, jwks.Keys[0].Algorithm)
	assert.Equal(t, oidc.KeyTypeRSA, jwks.Keys[0].KeyType)

	first, err := p.SigningKey(ctx, "default")
	require.NoError(t, err)
	again, err := p.SigningKey(ctx, "default")
	require.NoError(t, err)
	assert.Equal(t, first, again, "same issuer yields a stable key")

	other, err := p.SigningKey(ctx, "tenant-b")
	require.NoError(t, err)
	assert.Equal(t, oidc.KeyID("tenant-b"), other.Public.KeyID)
	assert.NotEqual(t, first.Public.Params, other.Public.Params, "distinct issuers draw distinct keys")
}

func TestNewProviderRejectsUnsupportedAlgorithm(t *testing.T) {
	t.Parallel()

	_, err := signing.NewProvider("EdDSA", nil)
	require.Error(t, err)
}

func decodeJSON(t *testing.T, seg string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func rsaPublic(t *testing.T, jwk oidc.JWK) *rsa.PublicKey {
	t.Helper()
	params, ok := jwk.Params.(oidc.RSAPublicParams)
	require.True(t, ok, "expected RSA public params")
	nBytes, err := base64.RawURLEncoding.DecodeString(params.N)
	require.NoError(t, err)
	eBytes, err := base64.RawURLEncoding.DecodeString(params.E)
	require.NoError(t, err)
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}
}
