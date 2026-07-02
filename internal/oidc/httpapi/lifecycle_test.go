package httpapi_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adapterhttp "github.com/meigma/mock-oidc/internal/adapter/http"
	"github.com/meigma/mock-oidc/internal/observability"
	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/httpapi"
	"github.com/meigma/mock-oidc/internal/oidc/memory"
	"github.com/meigma/mock-oidc/internal/oidc/signing"
)

// lifecycleEnv exposes the running server plus the SAME signing provider it uses,
// so a test can mint tokens with a chosen JOSE typ (at+jwt / foo+jwt) that the
// server's verifier then accepts or rejects against its own key.
type lifecycleEnv struct {
	srv    *httptest.Server
	signer *signing.Provider
}

// newLifecycleServer wires the real Registrar (interactive login OFF) over real
// signing + memory adapters, returning the server and the signer so lifecycle
// tests can both drive the HTTP surface and hand-mint self-issued tokens.
func newLifecycleServer(t *testing.T) lifecycleEnv {
	t.Helper()

	signer, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	registry := memory.NewIssuerRegistry()
	clock := memory.NewClock()
	codes := memory.NewCodeStore()
	refresh := memory.NewRefreshTokenStore()
	provider := oidc.NewProviderService(registry, signer)
	tokens := oidc.NewTokenService(registry, signer, signer, clock,
		oidc.WithCodeStore(codes), oidc.WithRefreshStore(refresh))
	authorize := oidc.NewAuthorizeService(codes, clock, false)
	session := oidc.NewSessionService(signer, refresh, clock)
	deps := httpapi.Deps{
		Provider:  provider,
		Tokens:    tokens,
		Authorize: authorize,
		Session:   session,
	}

	discard := observability.NewLogger(io.Discard, slog.LevelError, "json")
	handler := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Logger:         discard,
		Metrics:        observability.NewMetrics(),
		Version:        "test",
		RequestTimeout: testTimeout,
		Register:       httpapi.Registrar(deps),
		FallbackWriter: func(w http.ResponseWriter, r *http.Request) bool {
			if !httpapi.IsProtocolPath(r.URL.Path) {
				return false
			}
			httpapi.WriteOAuth2Error(w, http.StatusMethodNotAllowed,
				"invalid_request", "the method is not allowed for this resource")
			return true
		},
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return lifecycleEnv{srv: srv, signer: signer}
}

// mintToken hand-signs a self-issued access token with the given JOSE typ against
// the env's own key for the default issuer. iss carries the default trailing
// segment and the times sit around wall-now so the server's verifier accepts a
// well-formed token (the only variable under test is typ).
func (e lifecycleEnv) mintToken(t *testing.T, typ, sub string) string {
	t.Helper()
	now := oidc.NewInstant(time.Now())
	claims := oidc.ClaimSet{
		Subject:   oidc.Subject(sub),
		Audience:  oidc.Audience{"default"},
		Issuer:    e.srv.URL + "/default",
		IssuedAt:  now,
		NotBefore: now,
		Expiry:    now.Add(time.Hour),
		JWTID:     "jti-lifecycle",
	}
	tok := oidc.NewToken(oidc.IssuerID("default"), oidc.RS256, oidc.JOSEType(typ), claims)
	signed, err := e.signer.Sign(context.Background(), oidc.IssuerID("default"), tok)
	require.NoError(t, err)
	return string(signed)
}

// getUserInfo GETs /default/userinfo with the given Authorization header,
// returning the response and body.
func getUserInfo(t *testing.T, srv *httptest.Server, authz string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/default/userinfo", nil)
	require.NoError(t, err)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp, body
}

// postPath POSTs a url-encoded form to an arbitrary path with optional headers.
func postPath(
	t *testing.T,
	srv *httptest.Server,
	path, form string,
	headers map[string]string,
) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+path, strings.NewReader(form))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp, body
}

// clientCredentialsToken drives a client_credentials exchange and returns the
// minted access token (a real, server-signed typ=JWT token).
func clientCredentialsToken(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp, body := postForm(t, srv, "grant_type=client_credentials&client_id=app")
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(body, &tok))
	require.NotEmpty(t, tok.AccessToken)
	return tok.AccessToken
}

// basicAuth builds a presence-only client Authorization header value.
func basicAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("app:secret"))
}

// TestUserInfoVerbatimClaimSet verifies /userinfo returns the ENTIRE claim set of
// a valid bearer access token verbatim (registered claims present, sub/iss/aud
// intact).
func TestUserInfoVerbatimClaimSet(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	token := clientCredentialsToken(t, env.srv)

	resp, body := getUserInfo(t, env.srv, "Bearer "+token)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var claims map[string]any
	require.NoError(t, json.Unmarshal(body, &claims))
	assert.Equal(t, "app", claims["sub"], "sub is the client_id")
	assert.Equal(t, []any{"default"}, claims["aud"], "aud echoed verbatim as an array")
	assert.Equal(t, "default", claims["tid"])
	iss, _ := claims["iss"].(string)
	assert.True(t, strings.HasSuffix(iss, "/default"), "iss ends with /default, got %q", iss)
	for _, name := range []string{"iat", "nbf", "exp", "jti"} {
		assert.Contains(t, claims, name, "claim %q present verbatim", name)
	}
}

// TestUserInfoGarbageTokenUnauthorized verifies a garbage bearer token yields 401
// invalid_token with the RFC 6750 WWW-Authenticate challenge.
func TestUserInfoGarbageTokenUnauthorized(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	resp, body := getUserInfo(t, env.srv, "Bearer not-a-jwt")

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, `Bearer error="invalid_token"`, resp.Header.Get("WWW-Authenticate"))
	var e struct {
		Code string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &e))
	assert.Equal(t, "invalid_token", e.Code)
}

// TestAtJWTSelfVerificationSucceeds verifies Decision D-4: a server-minted
// at+jwt-typ access token SUCCEEDS at /userinfo (200, full claims) and
// /introspect ({active:true}). It must NOT be rejected.
func TestAtJWTSelfVerificationSucceeds(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	token := env.mintToken(t, "at+jwt", "alice")

	resp, body := getUserInfo(t, env.srv, "Bearer "+token)
	require.Equal(t, http.StatusOK, resp.StatusCode, "at+jwt userinfo body: %s", body)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(body, &claims))
	assert.Equal(t, "alice", claims["sub"])

	iresp, ibody := postPath(t, env.srv, "/default/introspect",
		"token="+token, map[string]string{"Authorization": basicAuth()})
	require.Equal(t, http.StatusOK, iresp.StatusCode, "body: %s", ibody)
	var intro map[string]any
	require.NoError(t, json.Unmarshal(ibody, &intro))
	assert.Equal(t, true, intro["active"], "at+jwt introspects active")
}

// TestForeignTypRejected verifies a genuinely foreign typ (foo+jwt) fails
// self-verification: /userinfo → 401 invalid_token and /introspect → 200
// {active:false}.
func TestForeignTypRejected(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	token := env.mintToken(t, "foo+jwt", "alice")

	resp, _ := getUserInfo(t, env.srv, "Bearer "+token)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "foreign typ rejected at userinfo")

	iresp, ibody := postPath(t, env.srv, "/default/introspect",
		"token="+token, map[string]string{"Authorization": basicAuth()})
	require.Equal(t, http.StatusOK, iresp.StatusCode, "introspect is always 200")
	var intro map[string]any
	require.NoError(t, json.Unmarshal(ibody, &intro))
	assert.Equal(t, false, intro["active"], "foreign typ introspects inactive")
	assert.Len(t, intro, 1, "inactive body is exactly {active:false}")
}

// TestIntrospectActiveShape verifies an active introspection: single-element aud
// collapses to a scalar string, token_type defaults to Bearer, and active is
// true — all at 200.
func TestIntrospectActiveShape(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	token := clientCredentialsToken(t, env.srv)

	resp, body := postPath(t, env.srv, "/default/introspect",
		"token="+token, map[string]string{"Authorization": basicAuth()})
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var intro map[string]any
	require.NoError(t, json.Unmarshal(body, &intro))
	assert.Equal(t, true, intro["active"])
	assert.Equal(t, "default", intro["aud"], "single-element aud collapses to a string")
	assert.Equal(t, "Bearer", intro["token_type"], "token_type defaults to Bearer")
}

// TestIntrospectMissingAuthInvalidClient verifies presence-only client auth: a
// missing Authorization header is invalid_client at 400.
func TestIntrospectMissingAuthInvalidClient(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	resp, body := postPath(t, env.srv, "/default/introspect", "token=whatever", nil)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var e struct {
		Code string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &e))
	assert.Equal(t, "invalid_client", e.Code)
}

// TestEndSessionRedirectWithState verifies GET /endsession with a
// post_logout_redirect_uri → 302 to that URI with ?state=… appended.
func TestEndSessionRedirectWithState(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	resp := getNoRedirect(t, env.srv,
		"/default/endsession?post_logout_redirect_uri=https://client.example/after&state=xyz")

	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "https://client.example/after", loc.Scheme+"://"+loc.Host+loc.Path)
	assert.Equal(t, "xyz", loc.Query().Get("state"))
}

// TestEndSessionLoggedOutPage verifies GET /endsession with no redirect URI → 200
// "logged out" HTML page.
func TestEndSessionLoggedOutPage(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	resp, body := doGet(t, env.srv, "/default/endsession")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Contains(t, strings.ToLower(string(body)), "logged out")
}

// TestEndSessionOmitsAbsentState verifies the 302 omits state entirely when none
// was supplied (no bare state=, no NPE).
func TestEndSessionOmitsAbsentState(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	resp := getNoRedirect(t, env.srv,
		"/default/endsession?post_logout_redirect_uri=https://client.example/after")

	require.Equal(t, http.StatusFound, resp.StatusCode)
	assert.Equal(t, "https://client.example/after", resp.Header.Get("Location"), "no state appended")
}

// TestRefreshTokenFlow verifies the refresh grant redeems over the real HTTP
// surface: the token endpoint decodes the refresh_token form parameter, re-mints
// a fresh access token (200, Bearer), and — with rotation off — the same refresh
// token keeps redeeming. This guards the decodeTokenRequest refresh_token branch
// that TestRevokeFlow's negative assertion could otherwise pass without.
func TestRefreshTokenFlow(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	refresh := authCodeRefreshToken(t, env.srv)

	resp, body := postForm(t, env.srv,
		"grant_type=refresh_token&refresh_token="+url.QueryEscape(refresh)+"&client_id=app")
	require.Equalf(t, http.StatusOK, resp.StatusCode, "refresh redemption body: %s", body)
	var first struct {
		TokenType   string `json:"token_type"`
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(body, &first))
	assert.Equal(t, "Bearer", first.TokenType)
	require.NotEmpty(t, first.AccessToken, "refresh re-mints an access token")

	// Rotation is off by default: the same refresh token redeems a second time.
	resp2, body2 := postForm(t, env.srv,
		"grant_type=refresh_token&refresh_token="+url.QueryEscape(refresh)+"&client_id=app")
	require.Equalf(t, http.StatusOK, resp2.StatusCode, "second refresh body: %s", body2)
}

// TestRevokeFlow verifies the revoke lifecycle end to end: a refresh token minted
// via the authorization_code flow is revoked (200), a second revoke is idempotent
// (200), and the revoked token no longer redeems at /token (invalid_grant).
func TestRevokeFlow(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	refresh := authCodeRefreshToken(t, env.srv)

	// Revoke with the required hint → 200.
	resp, _ := postPath(t, env.srv, "/default/revoke",
		"token="+url.QueryEscape(refresh)+"&token_type_hint=refresh_token", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Idempotent second revoke → 200.
	resp2, _ := postPath(t, env.srv, "/default/revoke",
		"token="+url.QueryEscape(refresh)+"&token_type_hint=refresh_token", nil)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	// The revoked token no longer redeems.
	resp3, body3 := postForm(t, env.srv,
		"grant_type=refresh_token&refresh_token="+url.QueryEscape(refresh)+"&client_id=app")
	assert.Equal(t, http.StatusBadRequest, resp3.StatusCode)
	var e struct {
		Code string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body3, &e))
	assert.Equal(t, "invalid_grant", e.Code)
}

// TestRevokeBadHint verifies a hint other than refresh_token → 400
// unsupported_token_type.
func TestRevokeBadHint(t *testing.T) {
	t.Parallel()

	env := newLifecycleServer(t)
	resp, body := postPath(t, env.srv, "/default/revoke",
		"token=whatever&token_type_hint=access_token", nil)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var e struct {
		Code string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &e))
	assert.Equal(t, "unsupported_token_type", e.Code)
}

// authCodeRefreshToken drives a non-interactive authorization_code exchange and
// returns the minted refresh token.
func authCodeRefreshToken(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp := getNoRedirect(t, srv,
		"/default/authorize?response_type=code&client_id=app&redirect_uri=https://client.example/cb&state=s")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	code := loc.Query().Get("code")
	require.NotEmpty(t, code)

	tresp, tbody := postForm(t, srv,
		"grant_type=authorization_code&code="+code+"&client_id=app&redirect_uri=https://client.example/cb")
	require.Equal(t, http.StatusOK, tresp.StatusCode, "body: %s", tbody)
	var tok struct {
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, json.Unmarshal(tbody, &tok))
	require.NotEmpty(t, tok.RefreshToken)
	return tok.RefreshToken
}
