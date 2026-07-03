//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// loginTemplatesConfig is the JSON config the login-template container tests
// boot with: interactive login FORCED on, so the headless login_hint path is
// proven to bypass the login page, not merely ride the non-interactive default.
const loginTemplatesConfig = `{
	"interactiveLogin": true,
	"loginTemplates": [
		{"name": "admin-alice", "subject": "alice", "claims": {"email": "alice@example.com", "roles": ["admin"]}},
		{"name": "basic-bob", "subject": "bob"}
	]
}`

// TestContainerLoginTemplates drives the login-template acceptance path against
// the shipped image: the login page offers the configured templates as a
// dropdown, a known login_hint completes the authorization-code flow headlessly
// (no browser, interactive login forced on) with the template's subject and
// claims in the minted id_token, and an unknown hint fails loudly as
// invalid_request.
func TestContainerLoginTemplates(t *testing.T) {
	ctx := context.Background()

	skipIfImageMissing(ctx, t)

	req := testcontainers.ContainerRequest{
		Image:           imageTag,
		ExposedPorts:    []string{apiPort},
		AlwaysPullImage: false,
		Env:             map[string]string{"JSON_CONFIG": loginTemplatesConfig},
		WaitingFor: wait.ForHTTP("/isalive").
			WithPort(apiPort).
			WithStartupTimeout(60 * time.Second),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "boot %s with loginTemplates config", imageTag)
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

	var disco struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	getJSON(ctx, t, base+"/default/.well-known/openid-configuration", &disco)
	keys := fetchJWKSKeys(ctx, t, disco.JWKSURI)
	require.NotEmpty(t, keys)

	const clientID = "test-suite"
	authQuery := url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"redirect_uri":  {"http://127.0.0.1/cb"},
		"state":         {"tpl-1"},
	}

	// 1. Without a hint, interactive login renders the page WITH the dropdown.
	pageStatus, pageBody := getRaw(ctx, t, disco.AuthorizationEndpoint+"?"+authQuery.Encode())
	require.Equal(t, http.StatusOK, pageStatus, "interactive login page renders")
	page := string(pageBody)
	assert.Contains(t, page, `id="tmpl-select"`, "template dropdown offered")
	assert.Contains(t, page, ">admin-alice</option>", "template listed by name")
	assert.Contains(t, page, ">basic-bob</option>")

	// 2. A known login_hint bypasses the page and issues the code headlessly.
	hinted := url.Values{}
	for k, v := range authQuery {
		hinted[k] = v
	}
	hinted.Set("login_hint", "admin-alice")
	code, state := authorizeForCode(ctx, t, disco.AuthorizationEndpoint+"?"+hinted.Encode())
	require.NotEmpty(t, code, "code issued headlessly despite interactiveLogin:true")
	assert.Equal(t, "tpl-1", state)

	// 3. Redeem the code and verify the template identity in the id_token.
	var tok struct {
		IDToken string `json:"id_token"`
	}
	status, body := postTokenForm(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type": {"authorization_code"},
		"code":       {code},
		"client_id":  {clientID},
	})
	require.Equalf(t, http.StatusOK, status, "token exchange: %s", body)
	require.NoError(t, json.Unmarshal(body, &tok))
	require.NotEmpty(t, tok.IDToken)

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
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	assert.Equal(t, "alice", idClaims["sub"], "template subject on the id_token")
	assert.Equal(t, "alice@example.com", idClaims["email"], "template claim on the id_token")
	assert.Equal(t, []any{"admin"}, idClaims["roles"], "structured template claim preserved")

	// 4. An unknown hint fails loudly: error redirected into redirect_uri.
	unknown := url.Values{}
	for k, v := range authQuery {
		unknown[k] = v
	}
	unknown.Set("login_hint", "nobody")
	errStatus, errLoc := authorizeNoRedirect(ctx, t, disco.AuthorizationEndpoint+"?"+unknown.Encode())
	require.Equal(t, http.StatusFound, errStatus)
	loc, err := url.Parse(errLoc)
	require.NoError(t, err)
	assert.Equal(t, "invalid_request", loc.Query().Get("error"), "unknown template is a hard error")
	assert.Contains(t, loc.Query().Get("error_description"), "nobody")
	assert.Empty(t, loc.Query().Get("code"))
}

// getRaw GETs a URL (following no redirects) and returns the status + body —
// for asserting on rendered HTML.
func getRaw(ctx context.Context, t *testing.T, rawURL string) (int, []byte) {
	t.Helper()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// authorizeNoRedirect GETs the authorize URL without following the 302 and
// returns the status + raw Location header, so error redirects are inspectable.
func authorizeNoRedirect(ctx context.Context, t *testing.T, authURL string) (int, string) {
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
	return resp.StatusCode, resp.Header.Get("Location")
}
