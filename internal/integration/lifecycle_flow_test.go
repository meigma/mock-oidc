//go:build integration

package integration

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestContainerTokenLifecycle is the Slice 3 R3 parity test: it boots the shipped
// mock-oidc:dev image with ZERO config, mints an access+refresh pair via the
// authorization_code flow, then exercises the full post-token lifecycle the way a
// relying app would — refresh redemption, introspection, userinfo, revocation,
// and RP-initiated logout — verifying every re-minted signature against ONLY the
// served JWKS. It skips loudly when the image is not present locally.
func TestContainerTokenLifecycle(t *testing.T) {
	ctx := context.Background()

	skipIfImageMissing(ctx, t)

	req := testcontainers.ContainerRequest{
		Image:           imageTag,
		ExposedPorts:    []string{apiPort},
		AlwaysPullImage: false,
		WaitingFor: wait.ForHTTP("/isalive").
			WithPort(apiPort).
			WithStartupTimeout(60 * time.Second),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "boot %s with zero config", imageTag)
	t.Cleanup(func() {
		if terr := ctr.Terminate(context.Background()); terr != nil {
			t.Logf("terminate container: %v", terr)
		}
	})

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	mapped, err := ctr.MappedPort(ctx, "8080")
	require.NoError(t, err)
	base := "http://" + host + ":" + mapped.Port()

	// 1. Self-configure from discovery + JWKS, then mint an access+refresh pair via
	// the authorization_code flow (zero-config authorize issues a code directly).
	var disco struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	getJSON(ctx, t, base+"/default/.well-known/openid-configuration", &disco)
	require.NotEmpty(t, disco.TokenEndpoint)

	keys := fetchJWKSKeys(ctx, t, disco.JWKSURI)
	require.NotEmpty(t, keys, "JWKS carries at least one key")

	const clientID = "stock-client"
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	authQuery := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"http://127.0.0.1/cb"},
		"state":                 {"s-123"},
		"nonce":                 {"n-0S6_WzA2Mj"},
		"code_challenge":        {pkceS256(verifier)},
		"code_challenge_method": {"S256"},
	}
	code, _ := authorizeForCode(ctx, t, disco.AuthorizationEndpoint+"?"+authQuery.Encode())
	require.NotEmpty(t, code)

	var minted struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	status, body := postTokenForm(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"client_id":     {clientID},
		"redirect_uri":  {"http://127.0.0.1/cb"},
	})
	require.Equalf(t, http.StatusOK, status, "token exchange: %s", body)
	require.NoError(t, json.Unmarshal(body, &minted))
	require.NotEmpty(t, minted.AccessToken, "access_token minted")
	require.NotEmpty(t, minted.RefreshToken, "refresh_token minted")

	origSub, origJTI := verifiedClaim(t, keys, disco.Issuer, minted.AccessToken)

	// 2. Refresh: a new signed access token verifies against the SAME published
	// JWKS, carries the SAME subject, but a FRESH jti (re-mint, not a replay).
	var refreshed struct {
		TokenType   string `json:"token_type"`
		AccessToken string `json:"access_token"`
	}
	status, body = postTokenForm(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {minted.RefreshToken},
		"client_id":     {clientID},
	})
	require.Equalf(t, http.StatusOK, status, "refresh exchange: %s", body)
	require.NoError(t, json.Unmarshal(body, &refreshed))
	assert.Equal(t, "Bearer", refreshed.TokenType)
	require.NotEmpty(t, refreshed.AccessToken, "refresh re-mints an access token")

	newSub, newJTI := verifiedClaim(t, keys, disco.Issuer, refreshed.AccessToken)
	assert.Equal(t, origSub, newSub, "refreshed access token keeps the same subject")
	assert.NotEqual(t, origJTI, newJTI, "refreshed access token carries a fresh jti")

	// 3. Introspect the live access token (with client auth): 200 {active:true},
	// single-valued aud collapsed to a string, token_type defaulting to Bearer.
	status, body = postFormAuth(ctx, t, base+"/default/introspect",
		url.Values{"token": {refreshed.AccessToken}}, basicClientAuth())
	require.Equalf(t, http.StatusOK, status, "introspect: %s", body)
	var intro map[string]any
	require.NoError(t, json.Unmarshal(body, &intro))
	assert.Equal(t, true, intro["active"], "live token introspects active")
	assert.IsType(t, "", intro["aud"], "single-valued aud collapses to a string")
	assert.Equal(t, "Bearer", intro["token_type"], "token_type defaults to Bearer")

	// 4. UserInfo with the bearer access token returns the full claim set verbatim.
	status, body = getBearer(ctx, t, base+"/default/userinfo", refreshed.AccessToken)
	require.Equalf(t, http.StatusOK, status, "userinfo: %s", body)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(body, &claims))
	assert.Equal(t, newJTI, claims["jti"], "userinfo returns the bearer token's claim set verbatim")
	for _, name := range []string{"iat", "nbf", "exp", "jti", "iss", "aud"} {
		assert.Contains(t, claims, name, "userinfo carries claim %q verbatim", name)
	}

	// 5. Revoke the refresh token → 200; reusing the revoked token → invalid_grant.
	status, body = postFormAuth(ctx, t, base+"/default/revoke",
		url.Values{"token": {minted.RefreshToken}, "token_type_hint": {"refresh_token"}}, "")
	require.Equalf(t, http.StatusOK, status, "revoke: %s", body)

	status, body = postTokenForm(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {minted.RefreshToken},
		"client_id":     {clientID},
	})
	assert.Equal(t, http.StatusBadRequest, status, "revoked refresh token no longer redeems")
	assert.Equal(t, "invalid_grant", oauthError(t, body), "reusing a revoked refresh token → invalid_grant")

	// 6. RP-initiated logout: GET /endsession with a redirect URI → 302 appending
	// ?state=… when present.
	resp := getNoRedirect(ctx, t,
		base+"/default/endsession?post_logout_redirect_uri=https://client.example/after&state=xyz")
	require.Equal(t, http.StatusFound, resp.StatusCode, "endsession redirects")
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "https://client.example/after", loc.Scheme+"://"+loc.Host+loc.Path)
	assert.Equal(t, "xyz", loc.Query().Get("state"), "state appended to the logout redirect")
}

// verifiedClaim verifies an access token's signature against ONLY the published
// JWKS (plus issuer), then returns its sub and jti — proving a stock client both
// trusts the re-minted signature and can read the registered claims.
func verifiedClaim(t *testing.T, keys map[string]*rsa.PublicKey, issuer, token string) (string, string) {
	t.Helper()

	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims,
		func(tok *jwt.Token) (any, error) {
			kid, _ := tok.Header["kid"].(string)
			key, ok := keys[kid]
			if !ok {
				return nil, jwt.ErrTokenUnverifiable
			}
			return key, nil
		},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(issuer),
	)
	require.NoError(t, err, "stock JWT client verifies the access token against the published JWKS")
	require.True(t, parsed.Valid)

	sub, _ := claims["sub"].(string)
	jti, _ := claims["jti"].(string)
	require.NotEmpty(t, jti, "access token carries a jti")
	return sub, jti
}

// basicClientAuth returns a presence-only Basic client Authorization header value
// (the introspect edge enforces presence, not credential correctness).
func basicClientAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("stock-client:secret"))
}

// postFormAuth POSTs a url-encoded form with an optional Authorization header and
// returns the status code and raw body.
func postFormAuth(ctx context.Context, t *testing.T, endpoint string, form url.Values, authz string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, respBody
}

// getBearer GETs an endpoint with an Authorization: Bearer header and returns the
// status code and raw body.
func getBearer(ctx context.Context, t *testing.T, endpoint, token string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, respBody
}

// getNoRedirect issues a GET that does NOT follow redirects, so the 302 Location
// of the RP-initiated logout can be inspected directly.
func getNoRedirect(ctx context.Context, t *testing.T, endpoint string) *http.Response {
	t.Helper()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp
}
