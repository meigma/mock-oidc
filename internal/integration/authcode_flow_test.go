//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
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

// TestContainerAuthCodeFlow is the Slice 2 R3 parity test: it boots the shipped
// mock-oidc:dev image with ZERO config (so authorize is non-interactive and
// issues a code directly), then drives the full authorization_code + PKCE flow
// the way a stock client would — GET /authorize to obtain a code via the 302
// redirect, POST /token to exchange it, and verify the id_token's signature and
// claims using ONLY the served JWKS. It also asserts the two negative parity
// invariants: a tampered PKCE verifier is rejected, and a reused code is
// rejected. It skips loudly when the image is not present locally.
func TestContainerAuthCodeFlow(t *testing.T) {
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

	// 1. Fetch discovery + JWKS the way a stock client self-configures.
	var disco struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	getJSON(ctx, t, base+"/default/.well-known/openid-configuration", &disco)
	require.NotEmpty(t, disco.AuthorizationEndpoint, "discovery advertises the authorize endpoint")
	require.NotEmpty(t, disco.TokenEndpoint)
	require.NotEmpty(t, disco.JWKSURI)

	keys := fetchJWKSKeys(ctx, t, disco.JWKSURI)
	require.NotEmpty(t, keys, "JWKS carries at least one key")

	// 2. GET /authorize with a PKCE challenge. Zero-config is non-interactive, so
	// the server issues a code directly via a 302 redirect (no login page).
	const clientID = "stock-client"
	const nonce = "n-0S6_WzA2Mj"
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := pkceS256(verifier)

	authQuery := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {"http://127.0.0.1/cb"},
		"state":                 {"s-123"},
		"nonce":                 {nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	code, state := authorizeForCode(ctx, t, disco.AuthorizationEndpoint+"?"+authQuery.Encode())
	require.NotEmpty(t, code, "code issued in the 302 redirect")
	assert.Equal(t, "s-123", state, "state echoed on the redirect")

	// 3. POST /token exchanging the code + correct verifier.
	var tok struct {
		TokenType    string `json:"token_type"`
		IDToken      string `json:"id_token"`
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
	require.NoError(t, json.Unmarshal(body, &tok))
	assert.Equal(t, "Bearer", tok.TokenType)
	require.NotEmpty(t, tok.IDToken, "id_token present")
	require.NotEmpty(t, tok.AccessToken, "access_token present")
	require.NotEmpty(t, tok.RefreshToken, "refresh_token present (nonce ⇒ alg=none PlainJWT)")

	// 4. Verify the id_token as a stock client would: signature against the served
	// JWKS + issuer/audience, with the nonce and azp asserted from the claims.
	idClaims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(tok.IDToken, idClaims,
		func(t *jwt.Token) (any, error) {
			kid, _ := t.Header["kid"].(string)
			key, ok := keys[kid]
			if !ok {
				return nil, jwt.ErrTokenUnverifiable
			}
			return key, nil
		},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(disco.Issuer),
		jwt.WithAudience(clientID),
	)
	require.NoError(t, err, "stock JWT client verifies the id_token against the published JWKS")
	require.True(t, parsed.Valid)
	assert.Equal(t, []any{clientID}, idClaims["aud"], "id_token aud is [client_id]")
	assert.Equal(t, clientID, idClaims["azp"], "azp on the id_token")
	assert.Equal(t, nonce, idClaims["nonce"], "nonce from the cached authorize request")

	// 5. Negative parity: a reused code is rejected (single-use, invalid_grant).
	reuseStatus, reuseBody := postTokenForm(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"client_id":     {clientID},
		"redirect_uri":  {"http://127.0.0.1/cb"},
	})
	assert.Equal(t, http.StatusBadRequest, reuseStatus, "reused code rejected")
	assert.Equal(t, "invalid_grant", oauthError(t, reuseBody), "reused code → invalid_grant")

	// 6. Negative parity: a fresh code with a TAMPERED verifier is rejected.
	code2, _ := authorizeForCode(ctx, t, disco.AuthorizationEndpoint+"?"+authQuery.Encode())
	require.NotEmpty(t, code2)
	tamperStatus, tamperBody := postTokenForm(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code2},
		"code_verifier": {"tampered-verifier"},
		"client_id":     {clientID},
		"redirect_uri":  {"http://127.0.0.1/cb"},
	})
	assert.Equal(t, http.StatusBadRequest, tamperStatus, "tampered verifier rejected")
	assert.Equal(t, "invalid_grant", oauthError(t, tamperBody), "tampered PKCE → invalid_grant")
}

// authorizeForCode issues a non-redirect-following GET to the authorize endpoint
// and extracts the authorization code + state from the 302 Location query.
func authorizeForCode(ctx context.Context, t *testing.T, authURL string) (string, string) {
	t.Helper()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	require.Equal(t, http.StatusFound, resp.StatusCode, "non-interactive authorize issues a 302 with the code")
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	q := loc.Query()
	return q.Get("code"), q.Get("state")
}

// postTokenForm issues a url-encoded token request and returns the status code
// and raw body, so both success and OAuth2-error responses can be inspected.
func postTokenForm(ctx context.Context, t *testing.T, endpoint string, form url.Values) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// oauthError decodes the `error` field from an OAuth2 error body.
func oauthError(t *testing.T, body []byte) string {
	t.Helper()

	var e struct {
		Code string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &e), "decode OAuth2 error: %s", body)
	return e.Code
}

// pkceS256 computes the PKCE S256 challenge for a verifier.
func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
