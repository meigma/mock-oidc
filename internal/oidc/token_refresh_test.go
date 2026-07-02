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

// refreshFixture wires a TokenService over mocked registry/key/signer/refresh
// ports with a fixed clock and a deterministic jti source, capturing every Token
// handed to the Signer so the re-mint claims can be asserted.
type refreshFixture struct {
	svc     *oidc.TokenService
	refresh *mocks.RefreshTokenStore
	signed  *[]oidc.Token
	now     oidc.Instant
	origin  oidc.RequestOrigin
}

func newRefreshFixture(t *testing.T, id oidc.IssuerID, opts ...oidc.TokenOption) refreshFixture {
	t.Helper()
	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))

	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{ID: id}, nil).Maybe()

	keys := mocks.NewKeyStore(t)
	keys.EXPECT().
		SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil).
		Maybe()

	captured := make([]oidc.Token, 0, 2)
	signer := mocks.NewSigner(t)
	signer.EXPECT().
		Sign(mock.Anything, id, mock.Anything).
		Run(func(_ context.Context, _ oidc.IssuerID, tok oidc.Token) { captured = append(captured, tok) }).
		Return(oidc.SignedToken("signed.jwt.value"), nil).
		Maybe()

	refresh := mocks.NewRefreshTokenStore(t)

	var counter int
	newID := func() string { counter++; return "id-" + string(rune('0'+counter)) }

	base := []oidc.TokenOption{oidc.WithRefreshStore(refresh), oidc.WithTokenID(newID)}
	svc := oidc.NewTokenService(
		registry, keys, signer, oidc.NewFixedClock(now),
		append(base, opts...)...,
	)
	return refreshFixture{
		svc:     svc,
		refresh: refresh,
		signed:  &captured,
		now:     now,
		origin:  oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080},
	}
}

// TestRefreshGrantReMintsAccessToken proves the redemption re-mints an access
// token with a FRESH jti/iat/exp but the SAME subject from the stored record. A
// record with no nonce mints no id_token, and without rotation the same refresh
// token is echoed back.
func TestRefreshGrantReMintsAccessToken(t *testing.T) {
	t.Parallel()

	id := oidc.IssuerID("default")
	f := newRefreshFixture(t, id)
	oldTok := oidc.RefreshToken("refresh-old")

	f.refresh.EXPECT().Lookup(mock.Anything, id, oldTok).Return(oidc.RefreshRecord{
		Issuer:   id,
		Subject:  "alice",
		Nonce:    nil,
		Format:   oidc.RefreshBareUUID,
		Callback: oidc.NewDefaultTokenCallback(id),
	}, nil)

	req := oidc.NewTokenRequest(id, oidc.GrantRefreshToken, oidc.Client{ID: "web"}).
		WithRefreshToken(oldTok)
	resp, err := f.svc.Issue(context.Background(), f.origin, req)
	require.NoError(t, err)

	// One Sign call (access token only — no nonce, no id_token).
	require.Len(t, *f.signed, 1)
	acc := (*f.signed)[0].Claims
	assert.Equal(t, oidc.Subject("alice"), acc.Subject, "same subject from the record")
	assert.Equal(t, f.now, acc.IssuedAt, "fresh iat from the clock")
	assert.Equal(t, f.now.Add(time.Hour), acc.Expiry, "fresh exp = now + default expiry")
	assert.Equal(t, "id-1", acc.JWTID, "fresh jti from the injected source")

	assert.NotEmpty(t, resp.AccessToken)
	assert.Empty(t, resp.IDToken, "no id_token without a nonce")
	assert.Equal(t, oldTok, resp.RefreshToken, "same refresh token echoed without rotation")
}

// TestRefreshGrantCrossIssuer proves the strict binding: a token minted by issuer
// A presented to issuer B fails with invalid_grant carrying the EXACT corrected
// text, and nothing is signed.
func TestRefreshGrantCrossIssuer(t *testing.T) {
	t.Parallel()

	id := oidc.IssuerID("tenant-b")
	f := newRefreshFixture(t, id)
	tok := oidc.RefreshToken("refresh-from-a")

	f.refresh.EXPECT().Lookup(mock.Anything, id, tok).Return(oidc.RefreshRecord{
		Issuer:   oidc.IssuerID("tenant-a"),
		Subject:  "alice",
		Callback: oidc.NewDefaultTokenCallback("tenant-a"),
	}, nil)

	req := oidc.NewTokenRequest(id, oidc.GrantRefreshToken, oidc.Client{ID: "web"}).
		WithRefreshToken(tok)
	_, err := f.svc.Issue(context.Background(), f.origin, req)

	var perr *oidc.ProtocolError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, oidc.CodeInvalidGrant, perr.Code)
	assert.Equal(t, "refresh_token was issued by a different issuer", perr.Description)
	assert.Empty(t, *f.signed, "cross-issuer redemption signs nothing")
}

// TestRefreshGrantUnknownToken proves a store miss maps to invalid_grant.
func TestRefreshGrantUnknownToken(t *testing.T) {
	t.Parallel()

	id := oidc.IssuerID("default")
	f := newRefreshFixture(t, id)
	tok := oidc.RefreshToken("nope")

	f.refresh.EXPECT().
		Lookup(mock.Anything, id, tok).
		Return(oidc.RefreshRecord{}, errors.New("refresh token not found"))

	req := oidc.NewTokenRequest(id, oidc.GrantRefreshToken, oidc.Client{ID: "web"}).
		WithRefreshToken(tok)
	_, err := f.svc.Issue(context.Background(), f.origin, req)

	var perr *oidc.ProtocolError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, oidc.CodeInvalidGrant, perr.Code)
}

// TestRefreshGrantRotation proves the rotation path: the old token is removed, a
// fresh RefreshBareUUID record is saved with the nonce DROPPED, and the response
// carries the new token. Because the redeemed record carried a nonce, this
// redemption still mints an id_token (aud=[client_id] + azp).
func TestRefreshGrantRotation(t *testing.T) {
	t.Parallel()

	id := oidc.IssuerID("default")
	f := newRefreshFixture(t, id, oidc.WithRefreshRotation(true))
	oldTok := oidc.RefreshToken("refresh-old")
	nonce := oidc.Nonce("n-123")

	f.refresh.EXPECT().Lookup(mock.Anything, id, oldTok).Return(oidc.RefreshRecord{
		Issuer:   id,
		Subject:  "alice",
		Nonce:    &nonce,
		Format:   oidc.RefreshPlainJWT,
		Callback: oidc.NewDefaultTokenCallback(id),
	}, nil)
	f.refresh.EXPECT().Remove(mock.Anything, oldTok).Return(nil)

	var savedTok oidc.RefreshToken
	var savedRec oidc.RefreshRecord
	f.refresh.EXPECT().
		Save(mock.Anything, id, mock.Anything, mock.Anything).
		Run(func(_ context.Context, _ oidc.IssuerID, tok oidc.RefreshToken, rec oidc.RefreshRecord) {
			savedTok = tok
			savedRec = rec
		}).
		Return(nil)

	req := oidc.NewTokenRequest(id, oidc.GrantRefreshToken, oidc.Client{ID: "web"}).
		WithRefreshToken(oldTok)
	resp, err := f.svc.Issue(context.Background(), f.origin, req)
	require.NoError(t, err)

	// access + id_token were both minted (the record carried a nonce).
	require.Len(t, *f.signed, 2)
	assert.NotEmpty(t, resp.IDToken, "id_token minted because the record carried a nonce")

	assert.NotEmpty(t, savedTok)
	assert.NotEqual(t, oldTok, savedTok, "rotation issues a new token")
	assert.Equal(t, savedTok, resp.RefreshToken, "response carries the rotated token")
	assert.Equal(t, id, savedRec.Issuer)
	assert.Equal(t, oidc.Subject("alice"), savedRec.Subject)
	assert.Nil(t, savedRec.Nonce, "rotation drops the nonce")
	assert.Equal(t, oidc.RefreshBareUUID, savedRec.Format)
}
