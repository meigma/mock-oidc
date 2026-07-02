package oidc_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// rfc7636 Appendix B test vector: the verifier and its S256 challenge.
const (
	rfcVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	rfcChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

// TestCodeChallengeMethodCompute covers the plain pass-through and the RFC 7636
// S256 base64url(sha256(verifier)) transform against the spec vector.
func TestCodeChallengeMethodCompute(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "abc123", oidc.ChallengePlain.Compute("abc123"), "plain echoes the verifier")
	assert.Equal(t, rfcChallenge, oidc.ChallengeS256.Compute(rfcVerifier), "S256 matches the RFC 7636 vector")
	assert.Empty(t, oidc.CodeChallengeMethod("unknown").Compute("x"), "unknown method fails closed")
}

// TestCodeChallengeMethodValid covers the closed-enum membership predicate.
func TestCodeChallengeMethodValid(t *testing.T) {
	t.Parallel()

	assert.True(t, oidc.ChallengePlain.Valid())
	assert.True(t, oidc.ChallengeS256.Valid())
	assert.False(t, oidc.CodeChallengeMethod("").Valid())
	assert.False(t, oidc.CodeChallengeMethod("s256").Valid(), "case-sensitive")
}

// TestVerifyPKCE covers the asymmetric semantics: a no-op only when neither side
// is present; both directions of the missing-half case are invalid_grant; a
// plain and an S256 round-trip pass; a mismatch is the invalid_pkce case.
func TestVerifyPKCE(t *testing.T) {
	t.Parallel()

	s256 := &oidc.PKCEChallenge{Challenge: rfcChallenge, Method: oidc.ChallengeS256}
	plain := &oidc.PKCEChallenge{Challenge: "plain-secret", Method: oidc.ChallengePlain}

	tests := []struct {
		name      string
		challenge *oidc.PKCEChallenge
		verifier  string
		wantErr   bool
		wantCode  oidc.ErrorCode
		wantDesc  string
	}{
		{name: "no challenge no verifier", challenge: nil, verifier: "", wantErr: false},
		{name: "plain round-trip", challenge: plain, verifier: "plain-secret", wantErr: false},
		{name: "s256 round-trip", challenge: s256, verifier: rfcVerifier, wantErr: false},
		{
			name: "s256 mismatch", challenge: s256, verifier: "wrong-verifier",
			wantErr: true, wantCode: oidc.CodeInvalidGrant, wantDesc: "invalid_pkce",
		},
		{
			name: "challenge stored empty verifier", challenge: s256, verifier: "",
			wantErr: true, wantCode: oidc.CodeInvalidGrant, wantDesc: "code_verifier required",
		},
		{
			name: "verifier without challenge", challenge: nil, verifier: rfcVerifier,
			wantErr: true, wantCode: oidc.CodeInvalidGrant, wantDesc: "code_verifier was not expected",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := oidc.CodeRecord{Challenge: tc.challenge}
			err := rec.VerifyPKCE(tc.verifier)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			var perr *oidc.ProtocolError
			require.ErrorAs(t, err, &perr)
			assert.Equal(t, tc.wantCode, perr.Code)
			assert.Contains(t, perr.Description, tc.wantDesc)
		})
	}
}

// TestNewCodeRecordLogin confirms an empty login leaves Login nil (the direct-
// code path) while a login with a username is captured by value.
func TestNewCodeRecordLogin(t *testing.T) {
	t.Parallel()

	at := oidc.NewInstant(time.Unix(1_700_000_000, 0))
	snap := oidc.AuthorizeSnapshot{
		Issuer:       "default",
		RedirectURI:  "https://app/cb",
		ResponseMode: oidc.ResponseModeQuery,
	}

	rec := oidc.NewCodeRecord(snap, nil, nil, oidc.LoginSubmission{}, at)
	assert.Nil(t, rec.Login, "empty login is not stored")
	assert.Equal(t, "https://app/cb", rec.RedirectURI, "redirect_uri captured verbatim")

	login := oidc.LoginSubmission{Username: "alice"}
	rec = oidc.NewCodeRecord(snap, nil, nil, login, at)
	require.NotNil(t, rec.Login)
	assert.Equal(t, oidc.Subject("alice"), rec.Login.Username)
}

// TestCodeRecordCallbackInput confirms the subject is sourced from the CACHED
// login, not the token request, and the scope comes from the record.
func TestCodeRecordCallbackInput(t *testing.T) {
	t.Parallel()

	rec := oidc.CodeRecord{
		Scope: oidc.ParseScopes("openid api"),
		Login: &oidc.LoginSubmission{Username: "alice"},
	}
	req := oidc.NewTokenRequest("default", oidc.GrantAuthorizationCode, oidc.Client{ID: "app"})
	in := rec.CallbackInput(req)
	assert.Equal(t, oidc.GrantAuthorizationCode, in.Grant)
	assert.Equal(t, oidc.ClientID("app"), in.Client.ID)
	assert.Equal(t, oidc.Subject("alice"), in.Subject, "subject from cached login")
	assert.Equal(t, oidc.ParseScopes("openid api"), in.Scopes)

	noLogin := oidc.CodeRecord{}
	assert.Empty(t, noLogin.CallbackInput(req).Subject, "no login => empty subject")
}

// TestNewLoginSubmission covers the required-username validation.
func TestNewLoginSubmission(t *testing.T) {
	t.Parallel()

	for _, blank := range []string{"", "   ", "\t"} {
		_, err := oidc.NewLoginSubmission(blank, oidc.CustomClaims{})
		var perr *oidc.ProtocolError
		require.ErrorAs(t, err, &perr, "blank %q rejected", blank)
		assert.Equal(t, oidc.CodeInvalidRequest, perr.Code)
		assert.Contains(t, perr.Description, "username")
	}

	got, err := oidc.NewLoginSubmission("bob", oidc.CustomClaims{})
	require.NoError(t, err)
	assert.Equal(t, oidc.Subject("bob"), got.Username)
}

// TestPromptRequiresLogin covers the interactive-trigger predicate.
func TestPromptRequiresLogin(t *testing.T) {
	t.Parallel()

	for _, p := range []oidc.Prompt{oidc.PromptLogin, oidc.PromptConsent, oidc.PromptSelectAccount} {
		assert.Truef(t, p.RequiresLogin(), "%q forces login", p)
	}
	for _, p := range []oidc.Prompt{oidc.PromptNone, "", "unrecognized"} {
		assert.Falsef(t, p.RequiresLogin(), "%q does not force login", p)
	}
}

// TestResponseTypeAndModeValid covers the closed advertised sets.
func TestResponseTypeAndModeValid(t *testing.T) {
	t.Parallel()

	for _, rt := range []oidc.ResponseType{oidc.ResponseTypeCode, oidc.ResponseTypeNone, oidc.ResponseTypeIDToken, oidc.ResponseTypeToken} {
		assert.Truef(t, rt.Valid(), "%q advertised", rt)
	}
	assert.False(t, oidc.ResponseType("code id_token").Valid(), "hybrid combo not a member")

	for _, rm := range []oidc.ResponseMode{oidc.ResponseModeQuery, oidc.ResponseModeFragment, oidc.ResponseModeFormPost} {
		assert.Truef(t, rm.Valid(), "%q advertised", rm)
	}
	assert.False(t, oidc.ResponseMode("web_message").Valid())
}

// TestChooseRefreshFormat confirms the nonce-driven form selection.
func TestChooseRefreshFormat(t *testing.T) {
	t.Parallel()

	assert.Equal(t, oidc.RefreshBareUUID, oidc.ChooseRefreshFormat(nil))
	n := oidc.Nonce("abc")
	assert.Equal(t, oidc.RefreshPlainJWT, oidc.ChooseRefreshFormat(&n))
}

// TestClaimSetWithLoginClaims confirms the login-claims merge is add-only
// (putIfAbsent): a login claim never overwrites an existing custom (mapping)
// claim, never shadows a registered claim, and the receiver is untouched.
func TestClaimSetWithLoginClaims(t *testing.T) {
	t.Parallel()

	var base oidc.ClaimSet
	base.Subject = "alice"
	base.Custom.Set("role", "admin") // a mapping claim already in place

	var login oidc.CustomClaims
	login.Set("role", "guest")              // collides with the mapping claim → dropped
	login.Set("email", "alice@example.com") // new → added
	login.Set("sub", "evil")                // registered name → dropped

	out := base.WithLoginClaims(login)

	role, _ := out.Custom.Get("role")
	assert.Equal(t, "admin", role, "the mapping claim wins over the login claim")
	email, ok := out.Custom.Get("email")
	assert.True(t, ok)
	assert.Equal(t, "alice@example.com", email)
	_, hasSub := out.Custom.Get("sub")
	assert.False(t, hasSub, "a login claim named sub is dropped (registered wins)")
	assert.Equal(t, oidc.Subject("alice"), out.Subject)

	// Copy-on-write: the receiver gains no login claims.
	_, leaked := base.Custom.Get("email")
	assert.False(t, leaked, "the receiver's custom map is not mutated")
}

// TestClaimSetCopyOnWrite confirms WithNonce/WithAZP return fresh ClaimSets that
// never alias the receiver's Custom map, so the id-token and access-token copies
// derived from one defaultClaims value stay independent.
func TestClaimSetCopyOnWrite(t *testing.T) {
	t.Parallel()

	var base oidc.ClaimSet
	base.Custom.Set("shared", "original")
	base.Audience = oidc.Audience{"default"}

	nonce := oidc.Nonce("n1")
	idSet := base.WithNonce(&nonce).WithAZP("app")
	accSet := base.WithNonce(&nonce)

	// Mutating one copy's custom claims must not touch the other or the base.
	idSet.Custom.Set("shared", "id-mutated")
	idSet.Custom.Set("only-id", "x")

	orig, _ := base.Custom.Get("shared")
	assert.Equal(t, "original", orig, "base custom map not aliased")
	accShared, _ := accSet.Custom.Get("shared")
	assert.Equal(t, "original", accShared, "sibling copy not aliased")
	_, hasOnlyID := accSet.Custom.Get("only-id")
	assert.False(t, hasOnlyID, "added claim did not leak to sibling")

	require.NotNil(t, idSet.Azp)
	assert.Equal(t, oidc.ClientID("app"), *idSet.Azp)
	assert.Nil(t, accSet.Azp, "azp only on the id-token copy")
	assert.Nil(t, base.Nonce, "receiver unchanged by WithNonce")

	// Audience slice is cloned, not shared.
	idSet.Audience[0] = "mutated"
	assert.Equal(t, "default", base.Audience[0], "audience slice not aliased")
}
