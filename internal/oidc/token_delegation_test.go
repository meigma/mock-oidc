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

// delegationHarness wires the mocked ports the delegation grants drive: an issuer
// registry (optionally seeded with configured callbacks), a key store, and a
// signer whose Sign calls are captured in order.
type delegationHarness struct {
	svc    *oidc.TokenService
	signer *mocks.Signer
	signed *[]oidc.Token
}

func newDelegationHarness(t *testing.T, now oidc.Instant, callbacks ...oidc.TokenCallback) delegationHarness {
	t.Helper()
	id := oidc.IssuerID("default")

	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().
		Materialize(mock.Anything, id).
		Return(oidc.IssuerRecord{ID: id, Callbacks: callbacks}, nil)

	keys := mocks.NewKeyStore(t)
	keys.EXPECT().
		SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)

	captured := make([]oidc.Token, 0, 2)
	signer := mocks.NewSigner(t)

	svc := oidc.NewTokenService(registry, keys, signer, oidc.NewFixedClock(now))
	return delegationHarness{svc: svc, signer: signer, signed: &captured}
}

// expectSign records each signed token in order and returns a distinct compact
// value per call, so the caller can tell the id_token from the access_token.
func (h delegationHarness) expectSign(values ...oidc.SignedToken) {
	call := h.signer.EXPECT().Sign(mock.Anything, oidc.IssuerID("default"), mock.Anything)
	idx := 0
	call.RunAndReturn(func(_ context.Context, _ oidc.IssuerID, tok oidc.Token) (oidc.SignedToken, error) {
		*h.signed = append(*h.signed, tok)
		v := oidc.SignedToken("signed")
		if idx < len(values) {
			v = values[idx]
		}
		idx++
		return v, nil
	})
}

func delegationOrigin() oidc.RequestOrigin {
	return oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}
}

const delegationIssuerURL = "http://localhost:8080/default"

// configuredCallback is a test TokenCallback with a fixed, configured audience
// that Matches every input — the "callback audience configured" precedence row.
type configuredCallback struct {
	issuer oidc.IssuerID
	aud    oidc.Audience
}

func (c configuredCallback) IssuerID() oidc.IssuerID                     { return c.issuer }
func (c configuredCallback) Subject(in oidc.CallbackInput) oidc.Subject  { return in.Subject }
func (c configuredCallback) Audience(oidc.CallbackInput) oidc.Audience   { return c.aud }
func (c configuredCallback) TypeHeader(oidc.CallbackInput) oidc.JOSEType { return oidc.DefaultJOSEType }
func (c configuredCallback) ExtraClaims(oidc.CallbackInput) oidc.ClaimSet {
	return oidc.ClaimSet{}
}
func (c configuredCallback) Expiry() time.Duration           { return time.Hour }
func (c configuredCallback) Matches(oidc.CallbackInput) bool { return true }

// TestTokenServicePassword asserts the ROPC matrix: an id_token AND an
// access_token (no refresh), sub = username, id_token aud = [client_id] with no
// nonce/azp, scope echoed. The password never crosses the edge, so there is no
// value to validate here — any request mints.
func TestTokenServicePassword(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	h := newDelegationHarness(t, now)
	h.expectSign("id.jwt", "access.jwt")

	req := oidc.NewTokenRequest(
		"default", oidc.GrantPassword, oidc.Client{ID: "app", Auth: oidc.ClientAuthNone},
	).WithPassword("alice", oidc.Scopes{"openid", "api:read"})

	resp, err := h.svc.Issue(context.Background(), delegationOrigin(), req)
	require.NoError(t, err)

	assert.Equal(t, oidc.TokenTypeBearer, resp.TokenType)
	assert.Equal(t, oidc.SignedToken("id.jwt"), resp.IDToken)
	assert.Equal(t, oidc.SignedToken("access.jwt"), resp.AccessToken)
	assert.Empty(t, resp.RefreshToken)    // password never issues a refresh
	assert.Empty(t, resp.IssuedTokenType) // not a token-exchange
	assert.Equal(t, oidc.Scopes{"openid", "api:read"}, resp.Scope)

	require.Len(t, *h.signed, 2)
	idClaims := (*h.signed)[0].Claims
	accClaims := (*h.signed)[1].Claims
	assert.Equal(t, oidc.Subject("alice"), idClaims.Subject)
	assert.Equal(t, oidc.Subject("alice"), accClaims.Subject)
	assert.Equal(t, oidc.Audience{"app"}, idClaims.Audience) // id aud = [client_id]
	assert.Nil(t, idClaims.Nonce)                            // nonce=null
	assert.Nil(t, idClaims.Azp)                              // no azp (only authcode adds it)
	assert.Equal(t, oidc.Audience{"api:read"}, accClaims.Audience)
}

// TestTokenServiceJWTBearer asserts the OBO matrix: an access token only (no
// id/refresh, no issued_token_type), the assertion's claims copied verbatim with
// a fresh iss/exp, and scope resolved request ?? assertion.
func TestTokenServiceJWTBearer(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))

	t.Run("request scope wins and claims are copied", func(t *testing.T) {
		t.Parallel()
		h := newDelegationHarness(t, now)

		var custom oidc.CustomClaims
		custom.Set("groups", "admins")
		inbound := oidc.ClaimSet{
			Subject: "upstream-user",
			Issuer:  "https://other.example/tenant",
			JWTID:   "inbound-jti",
			Custom:  custom,
		}
		h.signer.EXPECT().
			ParseUnverified(mock.Anything, oidc.SignedToken("assertion.jws")).
			Return(inbound, nil)
		h.expectSign("access.jwt")

		req, err := oidc.NewTokenRequest("default", oidc.GrantJWTBearer, oidc.Client{ID: "app"}).
			WithAssertion("assertion.jws", oidc.Scopes{"api:read"})
		require.NoError(t, err)

		resp, err := h.svc.Issue(context.Background(), delegationOrigin(), req)
		require.NoError(t, err)

		assert.Equal(t, oidc.SignedToken("access.jwt"), resp.AccessToken)
		assert.Empty(t, resp.IDToken)
		assert.Empty(t, resp.RefreshToken)
		assert.Empty(t, resp.IssuedTokenType) // jwt-bearer omits issued_token_type
		assert.Equal(t, oidc.Scopes{"api:read"}, resp.Scope)

		require.Len(t, *h.signed, 1)
		claims := (*h.signed)[0].Claims
		assert.Equal(t, oidc.Subject("upstream-user"), claims.Subject) // copied verbatim
		v, ok := claims.Custom.Get("groups")
		assert.True(t, ok)
		assert.Equal(t, "admins", v)
		assert.Equal(t, delegationIssuerURL, claims.Issuer) // iss overridden
		assert.NotEqual(t, "inbound-jti", claims.JWTID)     // fresh jti
		assert.Equal(t, now.Add(time.Hour), claims.Expiry)  // fresh exp
	})

	t.Run("falls back to the assertion scope claim", func(t *testing.T) {
		t.Parallel()
		h := newDelegationHarness(t, now)
		inbound := oidc.ClaimSet{Subject: "u", Scope: oidc.Scopes{"assert:scope"}}
		h.signer.EXPECT().
			ParseUnverified(mock.Anything, oidc.SignedToken("a.jws")).
			Return(inbound, nil)
		h.expectSign("access.jwt")

		req, err := oidc.NewTokenRequest("default", oidc.GrantJWTBearer, oidc.Client{ID: "app"}).
			WithAssertion("a.jws", nil) // no request scope
		require.NoError(t, err)

		resp, err := h.svc.Issue(context.Background(), delegationOrigin(), req)
		require.NoError(t, err)
		assert.Equal(t, oidc.Scopes{"assert:scope"}, resp.Scope)
	})

	t.Run("no scope anywhere is invalid_request", func(t *testing.T) {
		t.Parallel()
		h := newDelegationHarness(t, now)
		h.signer.EXPECT().
			ParseUnverified(mock.Anything, oidc.SignedToken("a.jws")).
			Return(oidc.ClaimSet{Subject: "u"}, nil)
		// Sign must NOT be called.

		req, err := oidc.NewTokenRequest("default", oidc.GrantJWTBearer, oidc.Client{ID: "app"}).
			WithAssertion("a.jws", nil)
		require.NoError(t, err)

		_, err = h.svc.Issue(context.Background(), delegationOrigin(), req)
		var perr *oidc.ProtocolError
		require.ErrorAs(t, err, &perr)
		assert.Equal(t, oidc.CodeInvalidRequest, perr.Code)
	})
}

// TestTokenServiceTokenExchange asserts the RFC 8693 matrix: an access token only,
// issued_token_type set to the access-token URN, scope ABSENT, and the subject
// token's claims copied verbatim with overridden iss/exp/jti.
func TestTokenServiceTokenExchange(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	h := newDelegationHarness(t, now)

	inbound := oidc.ClaimSet{Subject: "exchanged-user", Issuer: "https://other.example/t", JWTID: "old"}
	h.signer.EXPECT().
		ParseUnverified(mock.Anything, oidc.SignedToken("subject.jws")).
		Return(inbound, nil)
	h.expectSign("access.jwt")

	req, err := oidc.NewTokenRequest("default", oidc.GrantTokenExchange, oidc.Client{ID: "app"}).
		WithSubjectToken("subject.jws", "urn:ietf:params:oauth:token-type:access_token", "https://rs.example")
	require.NoError(t, err)

	resp, err := h.svc.Issue(context.Background(), delegationOrigin(), req)
	require.NoError(t, err)

	assert.Equal(t, oidc.SignedToken("access.jwt"), resp.AccessToken)
	assert.Empty(t, resp.IDToken)
	assert.Empty(t, resp.RefreshToken)
	assert.Equal(t, oidc.IssuedTokenAccessToken, resp.IssuedTokenType)
	assert.Empty(t, resp.Scope) // token-exchange never echoes scope

	require.Len(t, *h.signed, 1)
	claims := (*h.signed)[0].Claims
	assert.Equal(t, oidc.Subject("exchanged-user"), claims.Subject) // copied verbatim
	assert.Equal(t, delegationIssuerURL, claims.Issuer)             // overridden
	assert.NotEqual(t, "old", claims.JWTID)                         // fresh jti
	// zero-config default issuer has no configured audience → request audience wins.
	assert.Equal(t, oidc.Audience{"https://rs.example"}, claims.Audience)
}

// TestTokenExchangeAudiencePrecedence pins catalog line 98: the request `audience`
// param wins only when the resolved callback has no configured audience; a
// configured callback audience stands even when a differing param is sent.
func TestTokenExchangeAudiencePrecedence(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))

	t.Run("configured callback audience wins over the param", func(t *testing.T) {
		t.Parallel()
		cb := configuredCallback{issuer: "default", aud: oidc.Audience{"https://configured.example"}}
		h := newDelegationHarness(t, now, cb)
		h.signer.EXPECT().
			ParseUnverified(mock.Anything, oidc.SignedToken("s.jws")).
			Return(oidc.ClaimSet{Subject: "u"}, nil)
		h.expectSign("access.jwt")

		req, err := oidc.NewTokenRequest("default", oidc.GrantTokenExchange, oidc.Client{ID: "app"}).
			WithSubjectToken("s.jws", "", "https://param.example")
		require.NoError(t, err)

		_, err = h.svc.Issue(context.Background(), delegationOrigin(), req)
		require.NoError(t, err)
		require.Len(t, *h.signed, 1)
		assert.Equal(t, oidc.Audience{"https://configured.example"}, (*h.signed)[0].Claims.Audience)
	})

	t.Run("param wins when no callback audience is configured", func(t *testing.T) {
		t.Parallel()
		h := newDelegationHarness(t, now) // default callback, no configured audience
		h.signer.EXPECT().
			ParseUnverified(mock.Anything, oidc.SignedToken("s.jws")).
			Return(oidc.ClaimSet{Subject: "u"}, nil)
		h.expectSign("access.jwt")

		req, err := oidc.NewTokenRequest("default", oidc.GrantTokenExchange, oidc.Client{ID: "app"}).
			WithSubjectToken("s.jws", "", "https://param.example")
		require.NoError(t, err)

		_, err = h.svc.Issue(context.Background(), delegationOrigin(), req)
		require.NoError(t, err)
		require.Len(t, *h.signed, 1)
		assert.Equal(t, oidc.Audience{"https://param.example"}, (*h.signed)[0].Claims.Audience)
	})
}

// TestTokenExchangeMalformedSubjectToken asserts a subject_token the signer cannot
// parse surfaces as the port's typed invalid_request, never a 500, and no token is
// signed.
func TestTokenExchangeMalformedSubjectToken(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	h := newDelegationHarness(t, now)
	h.signer.EXPECT().
		ParseUnverified(mock.Anything, oidc.SignedToken("garbage")).
		Return(oidc.ClaimSet{}, oidc.MalformedRequest("malformed token: expected a compact JWS"))

	req, err := oidc.NewTokenRequest("default", oidc.GrantTokenExchange, oidc.Client{ID: "app"}).
		WithSubjectToken("garbage", "", "")
	require.NoError(t, err)

	_, err = h.svc.Issue(context.Background(), delegationOrigin(), req)
	var perr *oidc.ProtocolError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, oidc.CodeInvalidRequest, perr.Code)
}

// TestCopyWithOverrides asserts the delegation copy transform: it overrides only
// iss/iat/nbf/exp/jti/aud, copies every other inbound claim verbatim, stamps
// NEITHER azp NOR tid, and never mutates the receiver.
func TestCopyWithOverrides(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	base, err := oidc.NewBaseURL(oidc.SchemeHTTP, "localhost", 8080)
	require.NoError(t, err)
	issuer := oidc.NewIssuer("default", base, oidc.SigningKey{KeyID: "default", Algorithm: oidc.RS256}, nil)
	cb := oidc.NewDefaultTokenCallback("default")

	var custom oidc.CustomClaims
	custom.Set("role", "admin")
	inbound := oidc.ClaimSet{
		Subject:  "subject-user",
		Issuer:   "https://origin.example/tenant",
		Audience: oidc.Audience{"origin-aud"},
		IssuedAt: oidc.NewInstant(time.Unix(1, 0)),
		Expiry:   oidc.NewInstant(time.Unix(2, 0)),
		JWTID:    "inbound-jti",
		Custom:   custom,
	}

	got := inbound.CopyWithOverrides(issuer, cb, now)

	// Overridden registered fields.
	assert.Equal(t, delegationIssuerURL, got.Issuer)
	assert.Equal(t, now, got.IssuedAt)
	assert.Equal(t, now, got.NotBefore)
	assert.Equal(t, now.Add(time.Hour), got.Expiry)
	assert.NotEqual(t, "inbound-jti", got.JWTID)
	assert.NotEmpty(t, got.JWTID)

	// Copied verbatim.
	assert.Equal(t, oidc.Subject("subject-user"), got.Subject)
	role, ok := got.Custom.Get("role")
	assert.True(t, ok)
	assert.Equal(t, "admin", role)

	// No azp/tid stamping.
	assert.Nil(t, got.Azp)
	assert.Nil(t, got.Tenant)

	// The receiver is unchanged (copy-on-write).
	assert.Equal(t, "https://origin.example/tenant", inbound.Issuer)
	assert.Equal(t, "inbound-jti", inbound.JWTID)
	assert.Equal(t, oidc.NewInstant(time.Unix(1, 0)), inbound.IssuedAt)
	assert.Equal(t, oidc.Audience{"origin-aud"}, inbound.Audience)
}
