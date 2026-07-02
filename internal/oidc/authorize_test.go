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

// baseAuthorizeRequest is a minimal valid response_type=code request the trigger
// matrix mutates per case.
func baseAuthorizeRequest() oidc.AuthorizeRequest {
	return oidc.AuthorizeRequest{
		Issuer:       "default",
		Client:       oidc.Client{ID: "app"},
		ResponseType: oidc.ResponseTypeCode,
		RedirectURI:  "https://app.example/cb",
		Scopes:       oidc.ParseScopes("openid"),
		State:        "xyz",
		ResponseMode: oidc.ResponseModeQuery,
	}
}

// TestAuthorizeTriggerMatrix covers the login-vs-code decision: the config flag
// OR an interactive prompt forces the login page (no code cached), while
// prompt=none and the empty prompt issue a code directly.
func TestAuthorizeTriggerMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		interactiveLogin bool
		prompt           oidc.Prompt
		wantShowLogin    bool
	}{
		{"config flag forces login", true, "", true},
		{"prompt=login forces login", false, oidc.PromptLogin, true},
		{"prompt=consent forces login", false, oidc.PromptConsent, true},
		{"prompt=none issues code", false, oidc.PromptNone, false},
		{"empty prompt issues code", false, "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			codes := mocks.NewCodeStore(t)
			if !tc.wantShowLogin {
				// A direct-code decision caches exactly one record; the login page never does.
				codes.EXPECT().Save(mock.Anything, oidc.AuthorizationCode("code-1"), mock.Anything).Return(nil)
			}
			clock := oidc.NewFixedClock(oidc.NewInstant(time.Unix(1_700_000_000, 0)))
			svc := oidc.NewAuthorizeService(codes, clock, tc.interactiveLogin,
				oidc.WithAuthorizeCodeID(func() string { return "code-1" }))

			req := baseAuthorizeRequest()
			req.Prompt = tc.prompt
			res, err := svc.Authorize(context.Background(), req)
			require.NoError(t, err)

			if tc.wantShowLogin {
				assert.Equal(t, oidc.AuthorizeShowLogin, res.Kind)
				assert.Equal(t, req, res.Request, "the request is echoed for the login page to re-submit")
				assert.Empty(t, res.Code, "no code is minted when showing the login page")
				return
			}
			assert.Equal(t, oidc.AuthorizeRedirect, res.Kind)
			assert.Equal(t, oidc.AuthorizationCode("code-1"), res.Code)
			assert.Equal(t, "xyz", res.State)
			assert.Equal(t, "https://app.example/cb", res.RedirectURI)
			assert.Equal(t, oidc.ResponseModeQuery, res.Mode)
		})
	}
}

// TestAuthorizeRejectsNonCodeResponseType asserts only response_type=code is
// dispatched; the advertised-but-unimplemented members are invalid_grant.
func TestAuthorizeRejectsNonCodeResponseType(t *testing.T) {
	t.Parallel()

	codes := mocks.NewCodeStore(t) // Save must never be called for a rejected request.
	clock := oidc.NewFixedClock(oidc.NewInstant(time.Unix(1_700_000_000, 0)))
	svc := oidc.NewAuthorizeService(codes, clock, false)

	req := baseAuthorizeRequest()
	req.ResponseType = oidc.ResponseTypeIDToken
	_, err := svc.Authorize(context.Background(), req)

	var perr *oidc.ProtocolError
	require.ErrorAs(t, err, &perr)
	assert.Equal(t, oidc.CodeInvalidGrant, perr.Code)
}

// TestAuthorizeFormPostKind confirms response_mode=form_post selects the
// auto-submit outcome while still caching the code.
func TestAuthorizeFormPostKind(t *testing.T) {
	t.Parallel()

	codes := mocks.NewCodeStore(t)
	codes.EXPECT().Save(mock.Anything, oidc.AuthorizationCode("code-fp"), mock.Anything).Return(nil)
	clock := oidc.NewFixedClock(oidc.NewInstant(time.Unix(1_700_000_000, 0)))
	svc := oidc.NewAuthorizeService(codes, clock, false,
		oidc.WithAuthorizeCodeID(func() string { return "code-fp" }))

	req := baseAuthorizeRequest()
	req.ResponseMode = oidc.ResponseModeFormPost
	res, err := svc.Authorize(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, oidc.AuthorizeFormPost, res.Kind)
	assert.Equal(t, oidc.ResponseModeFormPost, res.Mode)
}

// TestSubmitLoginCachesLogin confirms POST /authorize caches the login snapshot
// (username + claims, nonce, PKCE challenge) under the minted code.
func TestSubmitLoginCachesLogin(t *testing.T) {
	t.Parallel()

	var saved oidc.CodeRecord
	codes := mocks.NewCodeStore(t)
	codes.EXPECT().
		Save(mock.Anything, oidc.AuthorizationCode("code-login"), mock.Anything).
		Run(func(_ context.Context, _ oidc.AuthorizationCode, rec oidc.CodeRecord) { saved = rec }).
		Return(nil)
	clock := oidc.NewFixedClock(oidc.NewInstant(time.Unix(1_700_000_000, 0)))
	svc := oidc.NewAuthorizeService(codes, clock, true,
		oidc.WithAuthorizeCodeID(func() string { return "code-login" }))

	var claims oidc.CustomClaims
	claims.Set("email", "alice@example.com")
	login, err := oidc.NewLoginSubmission("alice", claims)
	require.NoError(t, err)

	nonce := oidc.Nonce("n-123")
	req := baseAuthorizeRequest()
	req.Nonce = &nonce
	req.PKCE = &oidc.PKCEChallenge{Challenge: "abc", Method: oidc.ChallengeS256}

	res, err := svc.SubmitLogin(context.Background(), req, login)
	require.NoError(t, err)
	assert.Equal(t, oidc.AuthorizeRedirect, res.Kind)

	require.NotNil(t, saved.Login)
	assert.Equal(t, oidc.Subject("alice"), saved.Login.Username)
	email, ok := saved.Login.Claims.Get("email")
	assert.True(t, ok)
	assert.Equal(t, "alice@example.com", email)
	require.NotNil(t, saved.Nonce)
	assert.Equal(t, oidc.Nonce("n-123"), *saved.Nonce)
	require.NotNil(t, saved.Challenge)
	assert.Equal(t, oidc.ChallengeS256, saved.Challenge.Method)
}
