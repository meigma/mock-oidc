package signing_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
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
// signing adapter's advertised set must equal the domain constant discovery is
// built from (oidc.SupportedSigningAlgorithms). Drift in either list fails the
// build. Real producibility is asserted separately by
// TestSupportedAlgorithmsAreProducible.
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

// TestSupportedAlgorithmsAreProducible is the substantive constant-sync guard: it
// signs a probe token with EVERY advertised algorithm and verifies the emitted
// JWS against the public key the adapter publishes. This asserts real signing
// capability rather than comparing two static lists, so any algorithm advertised
// in discovery but lacking a working signer code path fails the build.
func TestSupportedAlgorithmsAreProducible(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := oidc.IssuerID("default")
	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))

	for _, alg := range signing.SupportedAlgorithms() {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			p, err := signing.NewProvider(alg, nil)
			require.NoError(t, err)

			claims := oidc.ClaimSet{
				Subject:   "app",
				Audience:  oidc.Audience{"default"},
				Issuer:    "http://localhost:8080/default",
				IssuedAt:  now,
				NotBefore: now,
				Expiry:    now.Add(time.Hour),
				JWTID:     "jti-1",
			}
			tok := oidc.NewToken(id, alg, oidc.DefaultJOSEType, claims)

			signed, err := p.Sign(ctx, id, tok)
			require.NoError(t, err)

			sk, err := p.SigningKey(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, alg, sk.Algorithm)

			verifyJWS(t, alg, sk.Public, string(signed))
		})
	}
}

// verifyJWS verifies the compact JWS against jwk using the algorithm's public
// primitive, proving the adapter really can produce alg.
func verifyJWS(t *testing.T, alg oidc.SigningAlgorithm, jwk oidc.JWK, compact string) {
	t.Helper()

	parts := strings.Split(compact, ".")
	require.Len(t, parts, 3, "compact JWS has three segments")
	input := []byte(parts[0] + "." + parts[1])
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)

	//nolint:exhaustive // verifyJWS is only called for the producible RS*/PS*/ES* families; alg=none is not a signature.
	switch alg {
	case oidc.RS256:
		verifyPKCS1(t, jwk, crypto.SHA256, input, sig)
	case oidc.RS384:
		verifyPKCS1(t, jwk, crypto.SHA384, input, sig)
	case oidc.RS512:
		verifyPKCS1(t, jwk, crypto.SHA512, input, sig)
	case oidc.PS256:
		verifyPSS(t, jwk, crypto.SHA256, input, sig)
	case oidc.PS384:
		verifyPSS(t, jwk, crypto.SHA384, input, sig)
	case oidc.PS512:
		verifyPSS(t, jwk, crypto.SHA512, input, sig)
	case oidc.ES256:
		verifyECDSA(t, jwk, crypto.SHA256, input, sig)
	case oidc.ES384:
		verifyECDSA(t, jwk, crypto.SHA384, input, sig)
	default:
		t.Fatalf("unexpected algorithm %q", alg)
	}
}

func verifyPKCS1(t *testing.T, jwk oidc.JWK, h crypto.Hash, input, sig []byte) {
	t.Helper()
	assert.Equal(t, oidc.KeyTypeRSA, jwk.KeyType)
	require.NoError(t, rsa.VerifyPKCS1v15(rsaPublic(t, jwk), h, digest(h, input), sig))
}

func verifyPSS(t *testing.T, jwk oidc.JWK, h crypto.Hash, input, sig []byte) {
	t.Helper()
	assert.Equal(t, oidc.KeyTypeRSA, jwk.KeyType)
	require.NoError(t, rsa.VerifyPSS(rsaPublic(t, jwk), h, digest(h, input), sig, &rsa.PSSOptions{
		SaltLength: rsa.PSSSaltLengthEqualsHash,
		Hash:       h,
	}))
}

func verifyECDSA(t *testing.T, jwk oidc.JWK, h crypto.Hash, input, sig []byte) {
	t.Helper()
	assert.Equal(t, oidc.KeyTypeEC, jwk.KeyType)
	pub := ecPublic(t, jwk)
	size := (pub.Curve.Params().BitSize + 7) / 8
	require.Len(t, sig, 2*size, "EC signature is fixed-width R || S")
	r := new(big.Int).SetBytes(sig[:size])
	s := new(big.Int).SetBytes(sig[size:])
	require.True(t, ecdsa.Verify(pub, digest(h, input), r, s))
}

//nolint:exhaustive // only the three JWS digest sizes are exercised here.
func digest(h crypto.Hash, input []byte) []byte {
	switch h {
	case crypto.SHA256:
		sum := sha256.Sum256(input)
		return sum[:]
	case crypto.SHA384:
		sum := sha512.Sum384(input)
		return sum[:]
	case crypto.SHA512:
		sum := sha512.Sum512(input)
		return sum[:]
	default:
		panic("unsupported hash")
	}
}

func ecPublic(t *testing.T, jwk oidc.JWK) *ecdsa.PublicKey {
	t.Helper()
	params, ok := jwk.Params.(oidc.ECPublicParams)
	require.True(t, ok, "expected EC public params")
	var curve elliptic.Curve
	switch params.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	default:
		t.Fatalf("unexpected curve %q", params.Crv)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(params.X)
	require.NoError(t, err)
	yBytes, err := base64.RawURLEncoding.DecodeString(params.Y)
	require.NoError(t, err)
	// Reconstruct the verification key from the published JWK coordinates.
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     new(big.Int).SetBytes(xBytes),
		Y:     new(big.Int).SetBytes(yBytes),
	}
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

// TestProviderSignsUnsecuredNone covers the alg=none refresh-token wire form: an
// RFC 7519 unsecured JWT with an empty signature and a payload of only jti +
// nonce, needing no issuer key.
func TestProviderSignsUnsecuredNone(t *testing.T) {
	t.Parallel()

	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	nonce := oidc.Nonce("n-xyz")
	claims := oidc.ClaimSet{JWTID: "rt-jti", Nonce: &nonce}
	tok := oidc.NewToken(oidc.IssuerID("default"), oidc.AlgNone, oidc.DefaultJOSEType, claims)

	signed, err := p.Sign(context.Background(), "default", tok)
	require.NoError(t, err)

	parts := strings.Split(string(signed), ".")
	require.Len(t, parts, 3, "unsecured JWT is header.payload. with an empty signature")
	assert.Empty(t, parts[2], "the signature segment is empty")

	header := decodeJSON(t, parts[0])
	assert.Equal(t, "none", header["alg"])
	assert.Equal(t, "JWT", header["typ"])
	_, hasKid := header["kid"]
	assert.False(t, hasKid, "an unsecured token carries no kid")

	payload := decodeJSON(t, parts[1])
	assert.Equal(t, "rt-jti", payload["jti"])
	assert.Equal(t, "n-xyz", payload["nonce"])
	_, hasIat := payload["iat"]
	assert.False(t, hasIat, "the refresh PlainJWT carries no timestamps")
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
