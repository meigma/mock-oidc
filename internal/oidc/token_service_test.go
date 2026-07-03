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

// TestTokenServiceIssueClientCredentials asserts the default-claim policy and the
// client_credentials matrix (access token only): sub defaults to client_id, aud
// follows the 4-step precedence (→ ["default"], or the non-OIDC scopes), and
// iss/iat/nbf/exp/jti/tid all come from the one Clock + issuer.
func TestTokenServiceIssueClientCredentials(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}
	const wantIssuer = "http://localhost:8080/default"

	tests := []struct {
		name     string
		clientID oidc.ClientID
		scopes   oidc.Scopes
		wantSub  oidc.Subject
		wantAud  oidc.Audience
	}{
		{"no scope defaults aud", "svc-a", nil, "svc-a", oidc.Audience{"default"}},
		{"oidc-only scope defaults aud", "svc-b", oidc.Scopes{"openid", "profile"}, "svc-b", oidc.Audience{"default"}},
		{"non-oidc scope becomes aud", "svc-c", oidc.Scopes{"api:read", "openid"}, "svc-c", oidc.Audience{"api:read"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := oidc.IssuerID("default")
			registry := mocks.NewIssuerRegistry(t)
			registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{ID: id}, nil)
			keys := mocks.NewKeyStore(t)
			keys.EXPECT().
				SigningKey(mock.Anything, id).
				Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)

			var signed oidc.Token
			signer := mocks.NewSigner(t)
			signer.EXPECT().
				Sign(mock.Anything, id, mock.Anything).
				Run(func(_ context.Context, _ oidc.IssuerID, tok oidc.Token) { signed = tok }).
				Return(oidc.SignedToken("signed.jwt.value"), nil)

			svc := oidc.NewTokenService(
				registry, keys, signer, oidc.NewFixedClock(now),
				oidc.WithTokenID(func() string { return "jti-fixed" }),
			)
			req := oidc.NewTokenRequest(
				id, oidc.GrantClientCredentials,
				oidc.Client{ID: tc.clientID, Auth: oidc.ClientAuthNone},
			).WithScopes(tc.scopes)

			resp, err := svc.Issue(context.Background(), origin, req)
			require.NoError(t, err)

			// client_credentials matrix: a Bearer access token only (no id/refresh
			// token exists on the cc response), the echoed scope, and expires_in
			// derived from the same Clock as exp.
			assert.Equal(t, oidc.TokenTypeBearer, resp.TokenType)
			assert.Equal(t, oidc.SignedToken("signed.jwt.value"), resp.AccessToken)
			assert.Equal(t, int64(3600), resp.ExpiresIn)
			assert.Equal(t, tc.scopes, resp.Scope)

			// Signed header: alg from the issuer key, kid == issuer, typ default JWT.
			assert.Equal(t, oidc.RS256, signed.Header.Algorithm)
			assert.Equal(t, oidc.KeyID("default"), signed.Header.KeyID)
			assert.Equal(t, oidc.DefaultJOSEType, signed.Header.Type)

			// Default claims.
			claims := signed.Claims
			assert.Equal(t, tc.wantSub, claims.Subject)
			assert.Equal(t, tc.wantAud, claims.Audience)
			assert.Equal(t, wantIssuer, claims.Issuer)
			assert.Equal(t, now, claims.IssuedAt)
			assert.Equal(t, now, claims.NotBefore)
			assert.Equal(t, now.Add(time.Hour), claims.Expiry)
			assert.Equal(t, "jti-fixed", claims.JWTID)
			require.NotNil(t, claims.Tenant)
			assert.Equal(t, "default", *claims.Tenant)
		})
	}
}

// TestTokenServiceIssueUnsupportedGrant asserts a valid-but-unwired grant is
// reported as a typed invalid_grant (never a 500), after the issuer resolves.
func TestTokenServiceIssueUnsupportedGrant(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}
	id := oidc.IssuerID("default")

	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{ID: id}, nil)
	keys := mocks.NewKeyStore(t)
	keys.EXPECT().
		SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)
	signer := mocks.NewSigner(t) // Sign must not be called for an unsupported grant.

	svc := oidc.NewTokenService(registry, keys, signer, oidc.NewFixedClock(now))
	req := oidc.NewTokenRequest(id, oidc.GrantAuthorizationCode, oidc.Client{ID: "app"})

	_, err := svc.Issue(context.Background(), origin, req)

	var perr *oidc.ProtocolError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, oidc.CodeInvalidGrant, perr.Code)
	assert.ErrorIs(t, err, oidc.ErrUnsupportedGrantType)
}

// TestTokenServiceIssuePropagatesResolveError asserts a registry failure surfaces
// as an error and short-circuits before any key or signing work.
func TestTokenServiceIssuePropagatesResolveError(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}
	id := oidc.IssuerID("default")

	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{}, assert.AnError)
	keys := mocks.NewKeyStore(t) // SigningKey must not be reached.
	signer := mocks.NewSigner(t)

	svc := oidc.NewTokenService(registry, keys, signer, oidc.NewFixedClock(now))
	req := oidc.NewTokenRequest(id, oidc.GrantClientCredentials, oidc.Client{ID: "app"})

	_, err := svc.Issue(context.Background(), origin, req)
	require.ErrorIs(t, err, assert.AnError)
}

// TestMintEqualsGrant proves "Mint ≡ grant": a token from TokenService.Mint and a
// client_credentials grant for the same subject/audience drive the SAME KeyStore
// and Signer and produce a byte-identical token shape (same alg/kid/typ, iss, and
// default registered claims) — only jti/exp policy differ by construction, so we
// pin jti and align the audience. This is the control-plane guarantee that a
// minted token verifies exactly like a granted one.
func TestMintEqualsGrant(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}
	id := oidc.IssuerID("default")

	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().Materialize(mock.Anything, id).Return(oidc.IssuerRecord{ID: id}, nil)
	keys := mocks.NewKeyStore(t)
	keys.EXPECT().
		SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)

	var captured []oidc.Token
	signer := mocks.NewSigner(t)
	signer.EXPECT().
		Sign(mock.Anything, id, mock.Anything).
		Run(func(_ context.Context, _ oidc.IssuerID, tok oidc.Token) { captured = append(captured, tok) }).
		Return(oidc.SignedToken("signed.jwt.value"), nil)

	svc := oidc.NewTokenService(
		registry, keys, signer, oidc.NewFixedClock(now),
		oidc.WithTokenID(func() string { return "jti-fixed" }),
	)

	// Grant path: client_credentials for client "svc" (sub = client_id, aud = default).
	grantReq := oidc.NewTokenRequest(id, oidc.GrantClientCredentials, oidc.Client{ID: "svc", Auth: oidc.ClientAuthNone})
	_, err := svc.Issue(context.Background(), origin, grantReq)
	require.NoError(t, err)

	// Mint path: same subject/audience, resolved to the same base URL.
	base, err := oidc.NewBaseURL(oidc.SchemeHTTP, "localhost", 8080)
	require.NoError(t, err)
	signed, claims, err := svc.Mint(context.Background(), oidc.MintSpec{
		Issuer:   id,
		BaseURL:  base,
		Subject:  "svc",
		Audience: oidc.Audience{"default"},
		Kind:     oidc.MintAccessToken,
		Typ:      oidc.DefaultJOSEType,
		Expiry:   time.Hour,
	})
	require.NoError(t, err)
	assert.Equal(t, oidc.SignedToken("signed.jwt.value"), signed)
	assert.Equal(t, oidc.Subject("svc"), claims.Subject)

	require.Len(t, captured, 2)
	grantTok, mintTok := captured[0], captured[1]

	// Same JWS header (alg/kid/typ) — the identical Signer/KeyStore drives both.
	assert.Equal(t, grantTok.Header, mintTok.Header)
	// Same default registered claims.
	assert.Equal(t, grantTok.Claims.Subject, mintTok.Claims.Subject)
	assert.Equal(t, grantTok.Claims.Issuer, mintTok.Claims.Issuer)
	assert.Equal(t, grantTok.Claims.Audience, mintTok.Claims.Audience)
	assert.Equal(t, grantTok.Claims.IssuedAt, mintTok.Claims.IssuedAt)
	assert.Equal(t, grantTok.Claims.Expiry, mintTok.Claims.Expiry)
	require.NotNil(t, grantTok.Claims.Tenant)
	require.NotNil(t, mintTok.Claims.Tenant)
	assert.Equal(t, *grantTok.Claims.Tenant, *mintTok.Claims.Tenant)
}

// TestConfiguredMappingCallbackShapesToken confirms that a configured
// RequestMappingCallback resolved for a grant folds its templated custom claims
// into the token AND, unlike the default callback, adds no tid.
func TestConfiguredMappingCallbackShapesToken(t *testing.T) {
	t.Parallel()

	now := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	origin := oidc.RequestOrigin{Scheme: oidc.SchemeHTTP, Host: "localhost", Port: 8080}
	id := oidc.IssuerID("default")

	var claims oidc.CustomClaims
	claims.Set("acr", "Level4")
	mappingCB, err := oidc.NewRequestMappingCallback("default", time.Hour, []oidc.RequestMapping{
		{Param: "client_id", Match: "*", Claims: claims},
	})
	require.NoError(t, err)

	registry := mocks.NewIssuerRegistry(t)
	registry.EXPECT().
		Materialize(mock.Anything, id).
		Return(oidc.IssuerRecord{ID: id, Callbacks: []oidc.TokenCallback{mappingCB}}, nil)
	keys := mocks.NewKeyStore(t)
	keys.EXPECT().
		SigningKey(mock.Anything, id).
		Return(oidc.SigningKey{KeyID: id.KeyID(), Algorithm: oidc.RS256}, nil)

	var signed oidc.Token
	signer := mocks.NewSigner(t)
	signer.EXPECT().
		Sign(mock.Anything, id, mock.Anything).
		Run(func(_ context.Context, _ oidc.IssuerID, tok oidc.Token) { signed = tok }).
		Return(oidc.SignedToken("signed.jwt.value"), nil)

	svc := oidc.NewTokenService(registry, keys, signer, oidc.NewFixedClock(now))
	req := oidc.NewTokenRequest(id, oidc.GrantClientCredentials, oidc.Client{ID: "app", Auth: oidc.ClientAuthNone})
	_, err = svc.Issue(context.Background(), origin, req)
	require.NoError(t, err)

	acr, ok := signed.Claims.Custom.Get("acr")
	require.True(t, ok, "the mapping's custom claim is folded into the token")
	assert.Equal(t, "Level4", acr)
	assert.Nil(t, signed.Claims.Tenant, "a RequestMappingCallback never stamps tid")
}
