package oidc_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/mocks"
)

// authCodeFixture wires a TokenService over mocks for the authorization_code
// path, returning the service plus the code and refresh mocks so a test can set
// the Take result and inspect the persisted RefreshRecord. The Signer returns a
// distinct value per token kind (id / access / refresh) so the response fields
// are unambiguous, capturing each minted Token for claim assertions.
type authCodeCapture struct {
	id, access, refresh oidc.Token
	sawRefresh          bool
	savedRefresh        oidc.RefreshRecord
}

func newAuthCodeService(t *testing.T, now oidc.Instant, capt *authCodeCapture) (
	*oidc.TokenService, *mocks.CodeStore,
) {
	t.Helper()

	id := oidc.IssuerID("default")
	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{ID: id}, nil)
	keys := mocks.NewKeyStore(t)
	keys.EXPECT().
		SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)

	signer := mocks.NewSigner(t)
	signer.EXPECT().Sign(mock.Anything, id, mock.Anything).RunAndReturn(
		func(_ context.Context, _ oidc.IssuerID, tok oidc.Token) (oidc.SignedToken, error) {
			switch {
			case tok.Header.Algorithm == oidc.AlgNone:
				capt.refresh = tok
				return "refresh.jwt", nil
			case tok.Claims.Azp != nil:
				capt.id = tok
				return "id.jwt", nil
			default:
				capt.access = tok
				return "access.jwt", nil
			}
		}).Maybe() // a failed PKCE check returns before any signing

	codes := mocks.NewCodeStore(t)
	refresh := mocks.NewRefreshTokenStore(t)

	// A refresh record is persisted on every successful exchange; capture it.
	refresh.EXPECT().Save(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ context.Context, _ oidc.IssuerID, _ oidc.RefreshToken, rec oidc.RefreshRecord) {
			capt.sawRefresh = true
			capt.savedRefresh = rec
		}).Return(nil).Maybe()

	svc := oidc.NewTokenService(registry, keys, signer, oidc.NewFixedClock(now),
		oidc.WithCodeStore(codes), oidc.WithRefreshStore(refresh),
		oidc.WithTokenID(func() string { return "jti-fixed" }))
	return svc, codes
}

// TestAuthorizationCodeMint asserts the id/access/refresh triple: id_token aud is
// [client_id] with azp and the CACHED nonce; access_token aud follows the 4-step
// chain with the same nonce and no azp; login claims merge add-only (a login
// "sub" cannot shadow the resolved subject); the refresh token is the alg=none
// PlainJWT form (nonce present) and its record is persisted.
func TestAuthorizationCodeMint(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}
	var capt authCodeCapture
	svc, codes := newAuthCodeService(t, now, &capt)

	nonce := oidc.Nonce("n-abc")
	var loginClaims oidc.CustomClaims
	loginClaims.Set("email", "alice@example.com")
	loginClaims.Set("sub", "should-not-shadow") // registered name: must be dropped
	rec := oidc.CodeRecord{
		Issuer: "default",
		Client: oidc.Client{ID: "app"},
		Scope:  oidc.ParseScopes("openid api"),
		Nonce:  &nonce,
		Login:  &oidc.LoginSubmission{Username: "alice", Claims: loginClaims},
	}
	codes.EXPECT().Take(mock.Anything, oidc.AuthorizationCode("code-xyz")).Return(rec, nil)

	req := oidc.NewTokenRequest("default", oidc.GrantAuthorizationCode, oidc.Client{ID: "app"}).
		WithScopes(oidc.ParseScopes("openid api")).
		WithAuthorizationCode("code-xyz", "", "https://app.example/cb")

	resp, err := svc.Issue(context.Background(), origin, req)
	require.NoError(t, err)

	// Response wiring: the three tokens land in their distinct fields.
	assert.Equal(t, oidc.SignedToken("id.jwt"), resp.IDToken)
	assert.Equal(t, oidc.SignedToken("access.jwt"), resp.AccessToken)
	assert.Equal(t, oidc.RefreshToken("refresh.jwt"), resp.RefreshToken)
	assert.Equal(t, oidc.TokenTypeBearer, resp.TokenType)

	// id_token: aud == [client_id], azp == client_id, nonce from the cache.
	assert.Equal(t, oidc.Subject("alice"), capt.id.Claims.Subject)
	assert.Equal(t, oidc.Audience{"app"}, capt.id.Claims.Audience)
	require.NotNil(t, capt.id.Claims.Azp)
	assert.Equal(t, oidc.ClientID("app"), *capt.id.Claims.Azp)
	require.NotNil(t, capt.id.Claims.Nonce)
	assert.Equal(t, oidc.Nonce("n-abc"), *capt.id.Claims.Nonce)

	// access_token: 4-step aud (non-OIDC scope "api"), same nonce, NO azp.
	assert.Equal(t, oidc.Audience{"api"}, capt.access.Claims.Audience)
	require.NotNil(t, capt.access.Claims.Nonce)
	assert.Equal(t, oidc.Nonce("n-abc"), *capt.access.Claims.Nonce)
	assert.Nil(t, capt.access.Claims.Azp, "azp is only on the id_token")

	// Login claims merge add-only: email lands; "sub" never shadows the subject.
	email, ok := capt.id.Claims.Custom.Get("email")
	assert.True(t, ok)
	assert.Equal(t, "alice@example.com", email)
	_, shadowed := capt.id.Claims.Custom.Get("sub")
	assert.False(t, shadowed, "a login claim named sub is dropped (registered wins)")
	emailAcc, ok := capt.access.Claims.Custom.Get("email")
	assert.True(t, ok, "login claims land on the access token too")
	assert.Equal(t, "alice@example.com", emailAcc)

	// Refresh: nonce present => alg=none PlainJWT; record persisted for Slice 3.
	assert.Equal(t, oidc.AlgNone, capt.refresh.Header.Algorithm)
	require.True(t, capt.sawRefresh)
	assert.Equal(t, oidc.RefreshPlainJWT, capt.savedRefresh.Format)
	assert.Equal(t, oidc.IssuerID("default"), capt.savedRefresh.Issuer)
	assert.Equal(t, oidc.Subject("alice"), capt.savedRefresh.Subject)
	require.NotNil(t, capt.savedRefresh.Nonce)
	assert.Equal(t, oidc.Nonce("n-abc"), *capt.savedRefresh.Nonce)
}

// TestAuthorizationCodeRefreshFormatNoNonce asserts that without a nonce the
// refresh token is a bare UUID from the ID source (the Signer is NOT invoked for
// it) and the persisted record records the RefreshBareUUID form.
func TestAuthorizationCodeRefreshFormatNoNonce(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}

	id := oidc.IssuerID("default")
	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{ID: id}, nil)
	keys := mocks.NewKeyStore(t)
	keys.EXPECT().SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)

	// A counter so jti(id), jti(access), and the bare-UUID refresh are distinct.
	var n int
	ids := []string{"jti-id", "jti-access", "refresh-uuid"}
	signCalls := 0
	signer := mocks.NewSigner(t)
	signer.EXPECT().Sign(mock.Anything, id, mock.Anything).RunAndReturn(
		func(_ context.Context, _ oidc.IssuerID, tok oidc.Token) (oidc.SignedToken, error) {
			signCalls++
			assert.NotEqual(t, oidc.AlgNone, tok.Header.Algorithm, "no alg=none token without a nonce")
			return "signed", nil
		})

	var savedFormat oidc.RefreshFormat
	refresh := mocks.NewRefreshTokenStore(t)
	refresh.EXPECT().Save(mock.Anything, mock.Anything, oidc.RefreshToken("refresh-uuid"), mock.Anything).
		Run(func(_ context.Context, _ oidc.IssuerID, _ oidc.RefreshToken, rec oidc.RefreshRecord) {
			savedFormat = rec.Format
		}).
		Return(nil)

	codes := mocks.NewCodeStore(t)
	codes.EXPECT().Take(mock.Anything, oidc.AuthorizationCode("c1")).
		Return(oidc.CodeRecord{Issuer: "default", Client: oidc.Client{ID: "app"}}, nil)

	svc := oidc.NewTokenService(registry, keys, signer, oidc.NewFixedClock(now),
		oidc.WithCodeStore(codes), oidc.WithRefreshStore(refresh),
		oidc.WithTokenID(func() string { v := ids[n]; n++; return v }))

	req := oidc.NewTokenRequest("default", oidc.GrantAuthorizationCode, oidc.Client{ID: "app"}).
		WithAuthorizationCode("c1", "", "")
	resp, err := svc.Issue(context.Background(), origin, req)
	require.NoError(t, err)

	assert.Equal(t, oidc.RefreshToken("refresh-uuid"), resp.RefreshToken)
	assert.Equal(t, oidc.RefreshBareUUID, savedFormat)
	assert.Equal(t, 2, signCalls, "only id + access are signed; the bare-UUID refresh is not")
}

// TestAuthorizationCodeUnknownCode asserts any Take failure maps to the exact
// single-use invalid_grant text, with no signing or refresh work.
func TestAuthorizationCodeUnknownCode(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}

	id := oidc.IssuerID("default")
	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{ID: id}, nil)
	keys := mocks.NewKeyStore(t)
	keys.EXPECT().SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)
	signer := mocks.NewSigner(t)             // Sign must not be called.
	refresh := mocks.NewRefreshTokenStore(t) // Save must not be called.

	codes := mocks.NewCodeStore(t)
	codes.EXPECT().Take(mock.Anything, oidc.AuthorizationCode("gone")).
		Return(oidc.CodeRecord{}, assert.AnError)

	svc := oidc.NewTokenService(registry, keys, signer, oidc.NewFixedClock(now),
		oidc.WithCodeStore(codes), oidc.WithRefreshStore(refresh))

	req := oidc.NewTokenRequest("default", oidc.GrantAuthorizationCode, oidc.Client{ID: "app"}).
		WithAuthorizationCode("gone", "", "")
	_, err := svc.Issue(context.Background(), origin, req)

	var perr *oidc.ProtocolError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, oidc.CodeInvalidGrant, perr.Code)
	assert.Equal(t, "unknown or already-used authorization code", perr.Description)
}

// TestAuthorizationCodePKCE covers the S256 round-trip and the tampered-verifier
// rejection. On failure the code is already burned (Take ran before the check),
// so nothing is signed.
func TestAuthorizationCodePKCE(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}

	tests := []struct {
		name     string
		verifier string
		wantErr  bool
	}{
		{"valid verifier exchanges", rfcVerifier, false},
		{"tampered verifier rejected", "tampered-verifier", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var capt authCodeCapture
			svc, codes := newAuthCodeService(t, now, &capt)
			codes.EXPECT().Take(mock.Anything, oidc.AuthorizationCode("pkce-code")).Return(
				oidc.CodeRecord{
					Issuer:    "default",
					Client:    oidc.Client{ID: "app"},
					Challenge: &oidc.PKCEChallenge{Challenge: rfcChallenge, Method: oidc.ChallengeS256},
				}, nil)

			req := oidc.NewTokenRequest("default", oidc.GrantAuthorizationCode, oidc.Client{ID: "app"}).
				WithAuthorizationCode("pkce-code", tc.verifier, "")
			_, err := svc.Issue(context.Background(), origin, req)

			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			var perr *oidc.ProtocolError
			require.ErrorAs(t, err, &perr)
			assert.Equal(t, oidc.CodeInvalidGrant, perr.Code)
			assert.Contains(t, perr.Description, "invalid_pkce")
		})
	}
}

// TestAuthorizationCodeGrantNotWired confirms the grant is refused when the
// stores are not injected, so an un-wired service never nil-panics.
func TestAuthorizationCodeGrantNotWired(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}
	id := oidc.IssuerID("default")

	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{ID: id}, nil)
	keys := mocks.NewKeyStore(t)
	keys.EXPECT().SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)
	signer := mocks.NewSigner(t)

	svc := oidc.NewTokenService(registry, keys, signer, oidc.NewFixedClock(now))
	req := oidc.NewTokenRequest("default", oidc.GrantAuthorizationCode, oidc.Client{ID: "app"}).
		WithAuthorizationCode("c", "", "")
	_, err := svc.Issue(context.Background(), origin, req)

	var perr *oidc.ProtocolError
	require.ErrorAs(t, err, &perr)
	assert.ErrorIs(t, err, oidc.ErrUnsupportedGrantType)
}
