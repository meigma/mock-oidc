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

// authorizeTestTemplates builds the two-template collection the login_hint
// cases share: admin-alice carries claims, basic-bob has none.
func authorizeTestTemplates(t *testing.T) oidc.LoginTemplates {
	t.Helper()

	var claims oidc.CustomClaims
	claims.Set("email", "alice@example.com")
	admin, err := oidc.NewLoginTemplate("admin-alice", "alice", claims)
	require.NoError(t, err)
	basic, err := oidc.NewLoginTemplate("basic-bob", "bob", oidc.CustomClaims{})
	require.NoError(t, err)

	templates, err := oidc.NewLoginTemplates(admin, basic)
	require.NoError(t, err)
	return templates
}

// TestAuthorizeLoginHintMatrix covers the login_hint decision layered over the
// login-vs-code matrix: a known template wins over interactiveLogin AND
// prompt=login (headless code), an unknown name is a hard invalid_request while
// templates are configured, and the hint is ignored entirely when no templates
// exist or when it is empty.
func TestAuthorizeLoginHintMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		templates        bool
		hint             string
		interactiveLogin bool
		prompt           oidc.Prompt
		wantKind         oidc.AuthorizeResultKind
		wantErr          bool
	}{
		{"known hint issues code non-interactive", true, "admin-alice", false, "", oidc.AuthorizeRedirect, false},
		{"known hint beats interactiveLogin", true, "admin-alice", true, "", oidc.AuthorizeRedirect, false},
		{"known hint beats prompt=login", true, "admin-alice", true, oidc.PromptLogin, oidc.AuthorizeRedirect, false},
		{"unknown hint is invalid_request", true, "nobody", false, "", 0, true},
		{"hint ignored without templates (interactive)", false, "admin-alice", true, "", oidc.AuthorizeShowLogin, false},
		{"hint ignored without templates (direct code)", false, "admin-alice", false, "", oidc.AuthorizeRedirect, false},
		{"empty hint with templates shows login", true, "", true, "", oidc.AuthorizeShowLogin, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			codes := mocks.NewCodeStore(t)
			if !tc.wantErr && tc.wantKind != oidc.AuthorizeShowLogin {
				codes.EXPECT().Save(mock.Anything, oidc.AuthorizationCode("code-h"), mock.Anything).Return(nil)
			}
			clock := oidc.NewFixedClock(oidc.NewInstant(time.Unix(1_700_000_000, 0)))
			opts := []oidc.AuthorizeOption{oidc.WithAuthorizeCodeID(func() string { return "code-h" })}
			if tc.templates {
				opts = append(opts, oidc.WithLoginTemplates(authorizeTestTemplates(t)))
			}
			svc := oidc.NewAuthorizeService(codes, clock, tc.interactiveLogin, opts...)

			req := baseAuthorizeRequest()
			req.Prompt = tc.prompt
			req.LoginHint = tc.hint
			res, err := svc.Authorize(context.Background(), req)

			if tc.wantErr {
				var perr *oidc.ProtocolError
				require.ErrorAs(t, err, &perr)
				assert.Equal(t, oidc.CodeInvalidRequest, perr.Code)
				assert.Contains(t, perr.Description, "nobody")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantKind, res.Kind)
		})
	}
}

// TestAuthorizeLoginHintCachesTemplateSubmission confirms a resolved template
// lands in the cached CodeRecord as an ordinary login snapshot (subject +
// claims), so mint-time putIfAbsent merging applies to template claims exactly
// as it does to interactively-typed ones.
func TestAuthorizeLoginHintCachesTemplateSubmission(t *testing.T) {
	t.Parallel()

	var saved oidc.CodeRecord
	codes := mocks.NewCodeStore(t)
	codes.EXPECT().
		Save(mock.Anything, oidc.AuthorizationCode("code-t"), mock.Anything).
		Run(func(_ context.Context, _ oidc.AuthorizationCode, rec oidc.CodeRecord) { saved = rec }).
		Return(nil)
	clock := oidc.NewFixedClock(oidc.NewInstant(time.Unix(1_700_000_000, 0)))
	svc := oidc.NewAuthorizeService(codes, clock, true,
		oidc.WithAuthorizeCodeID(func() string { return "code-t" }),
		oidc.WithLoginTemplates(authorizeTestTemplates(t)))

	req := baseAuthorizeRequest()
	req.LoginHint = "admin-alice"
	res, err := svc.Authorize(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, oidc.AuthorizeRedirect, res.Kind)

	require.NotNil(t, saved.Login)
	assert.Equal(t, oidc.Subject("alice"), saved.Login.Username)
	email, ok := saved.Login.Claims.Get("email")
	assert.True(t, ok)
	assert.Equal(t, "alice@example.com", email)
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
