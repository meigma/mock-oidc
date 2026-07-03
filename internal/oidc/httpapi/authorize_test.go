package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
)

// postNoRedirect POSTs a url-encoded form without following the 302, so the test
// can inspect the Location header the browser would otherwise chase.
func postNoRedirect(t *testing.T, srv *httptest.Server, path, form string) (*http.Response, []byte) {
	t.Helper()
	client := &http.Client{
		Transport:     srv.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+path, strings.NewReader(form))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp, body
}

// getNoRedirect GETs a path without following the 302.
func getNoRedirect(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	client := &http.Client{
		Transport:     srv.Client().Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp
}

// loginForCode drives the interactive login POST and returns the authorization
// code and state parsed out of the 302 Location's query string.
func loginForCode(t *testing.T, srv *httptest.Server, query, form string) (string, string) {
	t.Helper()
	resp, _ := postNoRedirect(t, srv, "/default/authorize?"+query, form)
	require.Equal(t, http.StatusFound, resp.StatusCode, "login POST redirects with the code")
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	q := loc.Query()
	return q.Get("code"), q.Get("state")
}

// TestAuthorizeInteractiveLoginPage verifies GET /authorize with interactive on
// renders the login HTML: a username/claims form POSTing back to the same
// /authorize URL (query string preserved).
func TestAuthorizeInteractiveLoginPage(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true)
	resp, body := doGet(
		t,
		srv,
		"/default/authorize?response_type=code&client_id=app&redirect_uri=https://client.example/cb&state=xyz&scope=openid",
	)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))

	html := string(body)
	assert.Contains(t, html, `name="username"`, "username field")
	assert.Contains(t, html, `name="claims"`, "optional claims field")
	assert.Contains(t, html, `method="post"`, "posts the login")
	// The form action preserves the authorize query string so the POST replays it.
	assert.Contains(t, html, "client_id=app", "action preserves the query string")
}

// TestAuthorizeLoginRedirect verifies POST /authorize (login submit) mints a code
// and 302-redirects with code+state in the Location query.
func TestAuthorizeLoginRedirect(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true)
	code, state := loginForCode(t, srv,
		"response_type=code&client_id=app&redirect_uri=https://client.example/cb&state=st123",
		"username=alice")

	assert.NotEmpty(t, code, "code delivered in the redirect")
	assert.Equal(t, "st123", state, "state echoed")
}

// TestAuthorizeDirectRedirectNonInteractive verifies GET /authorize with
// interactive OFF issues a code directly (no login page) as a 302 with the code.
func TestAuthorizeDirectRedirectNonInteractive(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, false)
	resp := getNoRedirect(t, srv,
		"/default/authorize?response_type=code&client_id=app&redirect_uri=https://client.example/cb&state=direct")

	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	assert.NotEmpty(t, loc.Query().Get("code"), "code issued without a login page")
	assert.Equal(t, "direct", loc.Query().Get("state"))
}

// TestAuthorizeFormPost verifies response_mode=form_post renders a self-submitting
// HTML form POSTing ONLY code+state to the redirect_uri.
func TestAuthorizeFormPost(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true)
	resp, body := postNoRedirect(
		t,
		srv,
		"/default/authorize?response_type=code&client_id=app&redirect_uri=https://client.example/cb&state=fp1&response_mode=form_post",
		"username=alice",
	)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))

	html := string(body)
	assert.Contains(t, html, `action="https://client.example/cb"`, "posts to redirect_uri")
	assert.Contains(t, html, `name="code"`, "carries the code")
	assert.Contains(t, html, `name="state"`, "carries the state")
	assert.Contains(t, html, `value="fp1"`, "state value")
	// Only code+state are posted — no client_id/response_type leak into the form.
	assert.NotContains(t, html, `name="client_id"`)
	assert.NotContains(t, html, `name="response_type"`)
}

// TestAuthorizeFormPostOmitsAbsentState verifies form_post omits the state input
// entirely when no state was supplied (no upstream missing-state 500).
func TestAuthorizeFormPostOmitsAbsentState(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true)
	resp, body := postNoRedirect(
		t,
		srv,
		"/default/authorize?response_type=code&client_id=app&redirect_uri=https://client.example/cb&response_mode=form_post",
		"username=alice",
	)

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotContains(t, string(body), `name="state"`, "no state input when state absent")
}

// TestFavicon verifies GET /favicon.ico returns an empty 200.
func TestFavicon(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, false)
	resp, body := doGet(t, srv, "/favicon.ico")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, body, "empty favicon body")
}

// TestAuthCodeExchange verifies the full authorization_code exchange: a code from
// an interactive login mints id_token (aud=[client_id], azp, nonce+sub from the
// CACHED request), access_token (4-step audience), and refresh_token — both JWTs
// verifying against the published JWKS.
func TestAuthCodeExchange(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true)
	code, _ := loginForCode(t, srv,
		"response_type=code&client_id=app&redirect_uri=https://client.example/cb&scope=openid&state=st&nonce=nn456",
		"username=alice")
	require.NotEmpty(t, code)

	resp, body := postForm(
		t,
		srv,
		"grant_type=authorization_code&code="+code+"&client_id=app&redirect_uri=https://client.example/cb",
	)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var tok struct {
		TokenType    string `json:"token_type"`
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, json.Unmarshal(body, &tok))
	assert.Equal(t, "Bearer", tok.TokenType)
	require.NotEmpty(t, tok.IDToken, "id_token present")
	require.NotEmpty(t, tok.AccessToken, "access_token present")
	require.NotEmpty(t, tok.RefreshToken, "refresh_token present (nonce ⇒ alg=none PlainJWT)")

	pub := fetchPublicKey(t, srv)

	idClaims := verifyRS256(t, tok.IDToken, pub)
	assert.Equal(t, []any{"app"}, idClaims["aud"], "id_token aud is [client_id]")
	assert.Equal(t, "app", idClaims["azp"], "azp on the id_token")
	assert.Equal(t, "nn456", idClaims["nonce"], "nonce from the cached authorize request")
	assert.Equal(t, "alice", idClaims["sub"], "sub is the cached login username")

	accClaims := verifyRS256(t, tok.AccessToken, pub)
	assert.Equal(t, "nn456", accClaims["nonce"], "same nonce on the access token")
	assert.Equal(t, "alice", accClaims["sub"])
	_, hasAzp := accClaims["azp"]
	assert.False(t, hasAzp, "azp only on the id_token")
}

// TestAuthCodeLoginClaims verifies interactive-login claims are folded into the
// minted tokens add-only (they never shadow a registered claim like sub).
func TestAuthCodeLoginClaims(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true)
	form := url.Values{}
	form.Set("username", "alice")
	form.Set("claims", `{"email":"alice@example.com","sub":"attacker"}`)
	code, _ := loginForCode(t, srv,
		"response_type=code&client_id=app&redirect_uri=https://client.example/cb", form.Encode())
	require.NotEmpty(t, code)

	resp, body := postForm(t, srv, "grant_type=authorization_code&code="+code+"&client_id=app")
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var tok struct {
		IDToken string `json:"id_token"`
	}
	require.NoError(t, json.Unmarshal(body, &tok))
	claims := verifyRS256(t, tok.IDToken, fetchPublicKey(t, srv))
	assert.Equal(t, "alice@example.com", claims["email"], "login claim added")
	assert.Equal(t, "alice", claims["sub"], "login claim cannot shadow the registered sub")
}

// TestAuthCodePKCESuccess verifies a valid S256 code_verifier exchanges
// successfully.
func TestAuthCodePKCESuccess(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true)
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := s256(verifier)

	code, _ := loginForCode(
		t,
		srv,
		"response_type=code&client_id=app&redirect_uri=https://client.example/cb&code_challenge="+challenge+"&code_challenge_method=S256",
		"username=alice",
	)
	require.NotEmpty(t, code)

	resp, body := postForm(
		t,
		srv,
		"grant_type=authorization_code&code="+code+"&code_verifier="+verifier+"&client_id=app",
	)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)
}

// TestAuthCodePKCETamperedBurnsCode verifies a tampered verifier is rejected as
// invalid_grant (invalid_pkce), AND that the code is burned even on that failed
// PKCE attempt — a second exchange reports the unknown/used-code error.
func TestAuthCodePKCETamperedBurnsCode(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true)
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := s256(verifier)

	code, _ := loginForCode(
		t,
		srv,
		"response_type=code&client_id=app&redirect_uri=https://client.example/cb&code_challenge="+challenge+"&code_challenge_method=S256",
		"username=alice",
	)
	require.NotEmpty(t, code)

	// Tampered verifier → invalid_grant (invalid_pkce).
	resp, body := postForm(
		t,
		srv,
		"grant_type=authorization_code&code="+code+"&code_verifier=wrong-verifier&client_id=app",
	)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var e struct {
		Code string `json:"error"`
		Desc string `json:"error_description"`
	}
	require.NoError(t, json.Unmarshal(body, &e))
	assert.Equal(t, "invalid_grant", e.Code)
	assert.Contains(t, e.Desc, "invalid_pkce")

	// The code was burned before the PKCE check: a retry (even with the right
	// verifier) is now an unknown/used code.
	resp2, body2 := postForm(
		t,
		srv,
		"grant_type=authorization_code&code="+code+"&code_verifier="+verifier+"&client_id=app",
	)
	require.Equal(t, http.StatusBadRequest, resp2.StatusCode)
	var e2 struct {
		Code string `json:"error"`
		Desc string `json:"error_description"`
	}
	require.NoError(t, json.Unmarshal(body2, &e2))
	assert.Equal(t, "invalid_grant", e2.Code)
	assert.Equal(t, "unknown or already-used authorization code", e2.Desc)
}

// TestAuthCodeSingleUse verifies a code is single-use: a second successful-path
// exchange of the same code fails as unknown/used.
func TestAuthCodeSingleUse(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true)
	code, _ := loginForCode(t, srv,
		"response_type=code&client_id=app&redirect_uri=https://client.example/cb", "username=alice")
	require.NotEmpty(t, code)

	resp, _ := postForm(t, srv, "grant_type=authorization_code&code="+code+"&client_id=app")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp2, body2 := postForm(t, srv, "grant_type=authorization_code&code="+code+"&client_id=app")
	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)
	var e struct {
		Code string `json:"error"`
		Desc string `json:"error_description"`
	}
	require.NoError(t, json.Unmarshal(body2, &e))
	assert.Equal(t, "invalid_grant", e.Code)
	assert.Equal(t, "unknown or already-used authorization code", e.Desc)
}

// s256 computes the PKCE S256 challenge for a verifier.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// testLoginTemplates builds the two-template collection the login-template edge
// tests share: admin-alice with claims, basic-bob without.
func testLoginTemplates(t *testing.T) oidc.LoginTemplates {
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

// TestAuthorizeLoginPageRendersTemplateDropdown verifies the login page offers
// the configured templates as a pre-fill dropdown: an option per template
// carrying the subject as its value and the claims JSON (attribute-escaped) in
// data-claims, plus the pre-fill script — and that the dropdown is absent when
// no templates are configured.
func TestAuthorizeLoginPageRendersTemplateDropdown(t *testing.T) {
	t.Parallel()

	authorizeQuery := "/default/authorize?response_type=code&client_id=app&redirect_uri=https://client.example/cb&state=xyz"

	t.Run("with templates", func(t *testing.T) {
		t.Parallel()

		srv := newAuthServer(t, true, testLoginTemplates(t))
		resp, body := doGet(t, srv, authorizeQuery)

		require.Equal(t, http.StatusOK, resp.StatusCode)
		html := string(body)
		assert.Contains(t, html, `id="tmpl-select"`, "template dropdown rendered")
		assert.Contains(t, html, `>admin-alice</option>`, "template name is the option text")
		assert.Contains(t, html, `value="alice"`, "subject rides the option value")
		assert.Contains(t, html, `value="bob"`)
		assert.Contains(t, html, `data-claims="{&#34;email&#34;:&#34;alice@example.com&#34;}"`,
			"claims JSON attribute-escaped on the option")
		assert.Contains(t, html, `data-claims=""`, "claimless template carries an empty pre-fill")
		assert.Contains(t, html, "tmpl-select')", "pre-fill script present")
	})

	t.Run("without templates", func(t *testing.T) {
		t.Parallel()

		srv := newAuthServer(t, true)
		resp, body := doGet(t, srv, authorizeQuery)

		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.NotContains(t, string(body), "tmpl-select", "no dropdown without templates")
	})
}

// TestAuthorizeLoginHintHeadlessIssuesCode verifies the headless path end to
// end: a known login_hint bypasses the login page even with interactive login
// forced, and the redeemed tokens carry the template's subject and claims.
func TestAuthorizeLoginHintHeadlessIssuesCode(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true, testLoginTemplates(t))
	resp := getNoRedirect(t, srv,
		"/default/authorize?response_type=code&client_id=app&redirect_uri=https://client.example/cb&state=hl1&login_hint=admin-alice")

	require.Equal(t, http.StatusFound, resp.StatusCode, "hint bypasses the login page")
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	code := loc.Query().Get("code")
	require.NotEmpty(t, code, "code issued headlessly")
	assert.Equal(t, "hl1", loc.Query().Get("state"))

	tokResp, body := postForm(t, srv, "grant_type=authorization_code&code="+code+"&client_id=app")
	require.Equal(t, http.StatusOK, tokResp.StatusCode, "body: %s", body)
	var tok struct {
		IDToken string `json:"id_token"`
	}
	require.NoError(t, json.Unmarshal(body, &tok))
	claims := verifyRS256(t, tok.IDToken, fetchPublicKey(t, srv))
	assert.Equal(t, "alice", claims["sub"], "template subject")
	assert.Equal(t, "alice@example.com", claims["email"], "template claim folded in")
}

// TestAuthorizeLoginHintUnknown verifies an unknown template name is a hard
// invalid_request on both error surfaces: redirected into a usable redirect_uri,
// and rendered as the direct HTML error page when redirect_uri is absent.
func TestAuthorizeLoginHintUnknown(t *testing.T) {
	t.Parallel()

	t.Run("redirects the error into redirect_uri", func(t *testing.T) {
		t.Parallel()

		srv := newAuthServer(t, false, testLoginTemplates(t))
		resp := getNoRedirect(t, srv,
			"/default/authorize?response_type=code&client_id=app&redirect_uri=https://client.example/cb&state=e1&login_hint=nobody")

		require.Equal(t, http.StatusFound, resp.StatusCode)
		loc, err := url.Parse(resp.Header.Get("Location"))
		require.NoError(t, err)
		assert.Equal(t, "invalid_request", loc.Query().Get("error"))
		assert.Contains(t, loc.Query().Get("error_description"), "nobody")
		assert.Empty(t, loc.Query().Get("code"), "no code on the error path")
	})

	t.Run("renders the direct error page without redirect_uri", func(t *testing.T) {
		t.Parallel()

		srv := newAuthServer(t, false, testLoginTemplates(t))
		resp, body := doGet(t, srv,
			"/default/authorize?response_type=code&client_id=app&login_hint=nobody")

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
		assert.Contains(t, string(body), "invalid_request")
	})
}

// TestAuthorizeLoginHintFormPost pins the hint + response_mode=form_post
// combination: the headless resolution still honors form_post and returns the
// auto-submit page instead of a 302.
func TestAuthorizeLoginHintFormPost(t *testing.T) {
	t.Parallel()

	srv := newAuthServer(t, true, testLoginTemplates(t))
	resp, body := doGet(t, srv,
		"/default/authorize?response_type=code&client_id=app&redirect_uri=https://client.example/cb&state=fp2&response_mode=form_post&login_hint=basic-bob")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	html := string(body)
	assert.Contains(t, html, `action="https://client.example/cb"`, "auto-submit posts to redirect_uri")
	assert.Contains(t, html, `name="code"`, "carries the code")
	assert.Contains(t, html, `value="fp2"`, "carries the state")
}
