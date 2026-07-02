package signing_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/signing"
)

// verifyNow is the fixed issuance/verification instant these tests thread through
// both minting and Verify (the injected-Clock contract).
func verifyNow() oidc.Instant { return oidc.NewInstant(time.Unix(1_700_000_000, 0)) }

// mintAccessToken signs an access token for id with the given typ and issuer URL,
// carrying one custom claim so the parser's Custom-fold is exercised.
func mintAccessToken(
	t *testing.T,
	p *signing.Provider,
	id oidc.IssuerID,
	alg oidc.SigningAlgorithm,
	typ oidc.JOSEType,
	iss string,
) oidc.SignedToken {
	t.Helper()
	now := verifyNow()
	var custom oidc.CustomClaims
	custom.Set("role", "admin")
	claims := oidc.ClaimSet{
		Subject:   "alice",
		Audience:  oidc.Audience{"api"},
		Issuer:    iss,
		IssuedAt:  now,
		NotBefore: now,
		Expiry:    now.Add(time.Hour),
		JWTID:     "jti-1",
		Custom:    custom,
	}
	signed, err := p.Sign(context.Background(), id, oidc.NewToken(id, alg, typ, claims))
	require.NoError(t, err)
	return signed
}

// retypeAlg re-encodes the protected header with a forged alg, keeping the
// original payload and signature — the alg-confusion attack vector.
func retypeAlg(t *testing.T, compact oidc.SignedToken, forged string) oidc.SignedToken {
	t.Helper()
	parts := strings.Split(string(compact), ".")
	require.Len(t, parts, 3)
	hdr := decodeJSON(t, parts[0])
	hdr["alg"] = forged
	raw, err := json.Marshal(hdr)
	require.NoError(t, err)
	return oidc.SignedToken(base64.RawURLEncoding.EncodeToString(raw) + "." + parts[1] + "." + parts[2])
}

// TestVerifyAcceptsOwnToken proves the happy path: a server-minted RS256 token
// verifies and its claims parse back into the typed ClaimSet — and the SAME
// compact JWS independently verifies under golang-jwt/jwt/v5 (cross-implementation
// proof), threading the same injected clock so the exp check agrees.
func TestVerifyAcceptsOwnToken(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := oidc.IssuerID("default")
	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	signed := mintAccessToken(t, p, id, oidc.RS256, oidc.DefaultJOSEType, "http://localhost:8080/default")

	claims, err := p.Verify(ctx, id, signed, verifyNow())
	require.NoError(t, err)
	assert.Equal(t, oidc.Subject("alice"), claims.Subject)
	assert.Equal(t, "http://localhost:8080/default", claims.Issuer)
	assert.Equal(t, oidc.Audience{"api"}, claims.Audience)
	role, ok := claims.Custom.Get("role")
	assert.True(t, ok)
	assert.Equal(t, "admin", role)

	// Independent cross-verification with golang-jwt/jwt/v5.
	sk, err := p.SigningKey(ctx, id)
	require.NoError(t, err)
	parsed, err := jwt.Parse(
		string(signed),
		func(_ *jwt.Token) (any, error) { return rsaPublic(t, sk.Public), nil },
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithTimeFunc(verifyNow().Time),
	)
	require.NoError(t, err)
	assert.True(t, parsed.Valid)
}

// TestVerifyTypeGate covers Decision D-4: typ=JWT and typ=at+jwt self-verify; a
// genuinely foreign typ (foo+jwt) is rejected.
func TestVerifyTypeGate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := oidc.IssuerID("default")

	tests := []struct {
		name    string
		typ     oidc.JOSEType
		wantErr bool
	}{
		{"JWT accepted", oidc.DefaultJOSEType, false},
		{"at+jwt accepted", oidc.JOSEType("at+jwt"), false},
		{"foo+jwt rejected", oidc.JOSEType("foo+jwt"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p, err := signing.NewProvider(oidc.RS256, nil)
			require.NoError(t, err)
			signed := mintAccessToken(t, p, id, oidc.RS256, tc.typ, "http://localhost:8080/default")

			_, err = p.Verify(ctx, id, signed, verifyNow())
			if tc.wantErr {
				require.ErrorIs(t, err, signing.ErrTokenVerification)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestVerifyRejectsAlgNone proves alg=none is always refused for verification: the
// resolved key is a real signing key, so the unsecured header can never match.
func TestVerifyRejectsAlgNone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := oidc.IssuerID("default")
	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	// An alg=none unsecured JWT (the refresh-token wire form) must not verify.
	unsecured, err := p.Sign(ctx, id, oidc.NewToken(id, oidc.AlgNone, oidc.DefaultJOSEType, oidc.ClaimSet{JWTID: "x"}))
	require.NoError(t, err)

	_, err = p.Verify(ctx, id, unsecured, verifyNow())
	require.ErrorIs(t, err, signing.ErrTokenVerification)
}

// TestVerifyAlgConfusion proves the verification algorithm derives from the
// RESOLVED key, never the header: a token whose header alg is forged to disagree
// with the issuer's key algorithm is rejected in BOTH directions.
func TestVerifyAlgConfusion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := oidc.IssuerID("default")

	t.Run("ES256 key, header forged to RS256", func(t *testing.T) {
		t.Parallel()
		p, err := signing.NewProvider(oidc.ES256, nil)
		require.NoError(t, err)
		signed := mintAccessToken(t, p, id, oidc.ES256, oidc.DefaultJOSEType, "http://localhost:8080/default")
		forged := retypeAlg(t, signed, "RS256")

		_, err = p.Verify(ctx, id, forged, verifyNow())
		require.ErrorIs(t, err, signing.ErrTokenVerification)
	})

	t.Run("RS256 key, header forged to ES256", func(t *testing.T) {
		t.Parallel()
		p, err := signing.NewProvider(oidc.RS256, nil)
		require.NoError(t, err)
		signed := mintAccessToken(t, p, id, oidc.RS256, oidc.DefaultJOSEType, "http://localhost:8080/default")
		forged := retypeAlg(t, signed, "ES256")

		_, err = p.Verify(ctx, id, forged, verifyNow())
		require.ErrorIs(t, err, signing.ErrTokenVerification)
	})
}

// TestVerifyRejectsWrongIssuer proves the iss claim must name the resolved issuer
// even when the signature is valid.
func TestVerifyRejectsWrongIssuer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := oidc.IssuerID("default")
	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	// Signed by default's key, but the iss names a different issuer.
	signed := mintAccessToken(t, p, id, oidc.RS256, oidc.DefaultJOSEType, "http://localhost:8080/other")

	_, err = p.Verify(ctx, id, signed, verifyNow())
	require.ErrorIs(t, err, signing.ErrTokenVerification)
}

// TestVerifyRejectsCrossIssuerKey proves a token minted by issuer A does not
// verify against issuer B (different resolved key).
func TestVerifyRejectsCrossIssuerKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	signed := mintAccessToken(t, p, "tenant-a", oidc.RS256, oidc.DefaultJOSEType, "http://localhost:8080/tenant-a")

	_, err = p.Verify(ctx, "tenant-b", signed, verifyNow())
	require.ErrorIs(t, err, signing.ErrTokenVerification)
}

// TestVerifyClockInjection is the clock-injection proof: the SAME token is
// rejected once now is advanced past exp, yet verifies when now is within the
// validity window — proving Verify reads the passed instant, not [time.Now].
func TestVerifyClockInjection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := oidc.IssuerID("default")
	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	signed := mintAccessToken(t, p, id, oidc.RS256, oidc.DefaultJOSEType, "http://localhost:8080/default")

	// Within the window (iat .. exp): verifies.
	_, err = p.Verify(ctx, id, signed, verifyNow())
	require.NoError(t, err)

	// Advanced two hours past a one-hour token: expired.
	future := verifyNow().Add(2 * time.Hour)
	_, err = p.Verify(ctx, id, signed, future)
	require.ErrorIs(t, err, signing.ErrTokenVerification)
}

// TestVerifyRejectsTamperedSignature proves a single flipped signature byte fails
// verification.
func TestVerifyRejectsTamperedSignature(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id := oidc.IssuerID("default")
	p, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	signed := mintAccessToken(t, p, id, oidc.RS256, oidc.DefaultJOSEType, "http://localhost:8080/default")

	parts := strings.Split(string(signed), ".")
	require.Len(t, parts, 3)
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)
	sig[0] ^= 0xFF
	tampered := oidc.SignedToken(parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(sig))

	_, err = p.Verify(ctx, id, tampered, verifyNow())
	require.ErrorIs(t, err, signing.ErrTokenVerification)
}
