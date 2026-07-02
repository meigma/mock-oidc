package oidc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/mocks"
)

// sessionFixture wires a SessionService over mocked verifier/refresh ports and a
// fixed clock, returning the service and its mocks for per-test expectations.
type sessionFixture struct {
	svc      *oidc.SessionService
	verifier *mocks.TokenVerifier
	refresh  *mocks.RefreshTokenStore
	now      oidc.Instant
}

func newSessionFixture(t *testing.T) sessionFixture {
	t.Helper()
	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	verifier := mocks.NewTokenVerifier(t)
	refresh := mocks.NewRefreshTokenStore(t)
	return sessionFixture{
		svc:      oidc.NewSessionService(verifier, refresh, oidc.NewFixedClock(now)),
		verifier: verifier,
		refresh:  refresh,
		now:      now,
	}
}

// TestSessionUserInfoSuccess: a verifiable token yields the ENTIRE claim set
// verbatim, and Verify is threaded the injected clock instant.
func TestSessionUserInfoSuccess(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)
	id := oidc.IssuerID("default")
	tok := oidc.SignedToken("header.payload.sig")
	want := oidc.ClaimSet{Subject: "alice", Issuer: "http://localhost:8080/default"}

	f.verifier.EXPECT().Verify(mock.Anything, id, tok, f.now).Return(want, nil)

	got, err := f.svc.UserInfo(context.Background(), oidc.UserInfoRequest{Issuer: id, Token: tok})
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestSessionUserInfoVerifyFail: a verification failure surfaces as invalid_token
// at 401, wrapping the verifier cause for [errors.Is].
func TestSessionUserInfoVerifyFail(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)
	id := oidc.IssuerID("default")
	tok := oidc.SignedToken("bad.token")
	cause := errors.New("signature does not verify")

	f.verifier.EXPECT().Verify(mock.Anything, id, tok, f.now).Return(oidc.ClaimSet{}, cause)

	_, err := f.svc.UserInfo(context.Background(), oidc.UserInfoRequest{Issuer: id, Token: tok})

	var perr *oidc.ProtocolError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, oidc.CodeInvalidToken, perr.Code)
	assert.Equal(t, 401, perr.HTTPStatus)
	require.ErrorIs(t, err, cause)
}

// TestSessionIntrospectInactive: an UNVERIFIABLE token introspects {active:false}
// at 200 — NEVER a Go error (RFC 7662 / parity).
func TestSessionIntrospectInactive(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)
	id := oidc.IssuerID("default")
	tok := oidc.SignedToken("bad.token")

	f.verifier.EXPECT().
		Verify(mock.Anything, id, tok, f.now).
		Return(oidc.ClaimSet{}, errors.New("signature does not verify"))

	res, err := f.svc.Introspect(context.Background(), oidc.IntrospectionRequest{Issuer: id, Token: tok})
	require.NoError(t, err)
	assert.False(t, res.Active)
	assert.Equal(t, oidc.InactiveIntrospection(), res)
}

// TestSessionIntrospectActive: a verifiable token introspects active with the
// verified claims and the default Bearer token_type.
func TestSessionIntrospectActive(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)
	id := oidc.IssuerID("default")
	tok := oidc.SignedToken("good.token")
	claims := oidc.ClaimSet{Subject: "alice", Audience: oidc.Audience{"api"}}

	f.verifier.EXPECT().Verify(mock.Anything, id, tok, f.now).Return(claims, nil)

	res, err := f.svc.Introspect(context.Background(), oidc.IntrospectionRequest{Issuer: id, Token: tok})
	require.NoError(t, err)
	assert.True(t, res.Active)
	assert.Equal(t, oidc.TokenTypeBearer, res.TokenType)
	assert.Equal(t, claims, res.Claims)
}

// TestSessionRevokeRefreshToken: the refresh_token hint removes the token and
// succeeds (idempotency is the store's contract).
func TestSessionRevokeRefreshToken(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)
	tok := oidc.RefreshToken("refresh-abc")

	f.refresh.EXPECT().Remove(mock.Anything, tok).Return(nil)

	err := f.svc.Revoke(context.Background(), oidc.RevocationRequest{
		Issuer: "default", Token: tok, Hint: oidc.TokenHintRefreshToken,
	})
	require.NoError(t, err)
}

// TestSessionRevokeBadHint: any hint other than refresh_token is
// unsupported_token_type at 400, and the store is never touched.
func TestSessionRevokeBadHint(t *testing.T) {
	t.Parallel()

	for _, hint := range []oidc.TokenTypeHint{oidc.TokenHintAccessToken, "", "id_token"} {
		t.Run(string(hint), func(t *testing.T) {
			t.Parallel()

			f := newSessionFixture(t) // refresh mock asserts Remove is NEVER called
			err := f.svc.Revoke(context.Background(), oidc.RevocationRequest{
				Issuer: "default", Token: "refresh-abc", Hint: hint,
			})

			var perr *oidc.ProtocolError
			require.ErrorAs(t, err, &perr)
			assert.Equal(t, oidc.CodeUnsupportedTokenType, perr.Code)
			assert.Equal(t, 400, perr.HTTPStatus)
		})
	}
}

// TestSessionEndSession: pure logout — a redirect URI yields the redirect outcome
// carrying the state; no redirect URI yields the plain logged-out outcome.
func TestSessionEndSession(t *testing.T) {
	t.Parallel()

	f := newSessionFixture(t)

	withURI, err := f.svc.EndSession(context.Background(), oidc.EndSessionRequest{
		Issuer: "default", PostLogoutRedirectURI: "https://app.example/after", State: "xyz",
	})
	require.NoError(t, err)
	assert.True(t, withURI.Redirect())
	assert.Equal(t, "https://app.example/after", withURI.RedirectURI)
	assert.Equal(t, "xyz", withURI.State)

	plain, err := f.svc.EndSession(context.Background(), oidc.EndSessionRequest{Issuer: "default"})
	require.NoError(t, err)
	assert.False(t, plain.Redirect())
}
