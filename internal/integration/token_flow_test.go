//go:build integration

package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"crypto/rsa"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestContainerTokenFlow is the Slice 1 R3 tracer-bullet: it boots the shipped
// mock-oidc:dev image with ZERO config, then drives the full C1 flow the way a
// stock OIDC/JWT client would — fetch discovery, fetch JWKS, POST a
// client_credentials request, and verify the returned access token's signature
// and claims using ONLY the served JWKS (via github.com/golang-jwt/jwt/v5). It
// skips loudly when the image is not present locally.
func TestContainerTokenFlow(t *testing.T) {
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

	// 1. Fetch the discovery document (self-configuration).
	var disco struct {
		Issuer        string `json:"issuer"`
		TokenEndpoint string `json:"token_endpoint"`
		JWKSURI       string `json:"jwks_uri"`
	}
	getJSON(ctx, t, base+"/default/.well-known/openid-configuration", &disco)
	require.True(t, strings.HasSuffix(disco.Issuer, "/default"), "issuer %q ends with /default", disco.Issuer)
	require.NotEmpty(t, disco.TokenEndpoint)
	require.NotEmpty(t, disco.JWKSURI)

	// 2. Fetch the JWKS advertised by discovery and build the verification keys.
	keys := fetchJWKSKeys(ctx, t, disco.JWKSURI)
	require.NotEmpty(t, keys, "JWKS carries at least one key")

	// 3. POST a client_credentials request to the advertised token endpoint.
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	postToken(ctx, t, disco.TokenEndpoint, url.Values{
		"grant_type": {"client_credentials"},
		"client_id":  {"svc"},
	}, &tokenResp)
	assert.Equal(t, "Bearer", tokenResp.TokenType)
	require.NotEmpty(t, tokenResp.AccessToken)
	assert.Positive(t, tokenResp.ExpiresIn)

	// 4. Verify the token as a stock client would: signature against the served
	// JWKS, plus issuer/audience validation — no special configuration.
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(tokenResp.AccessToken, claims,
		func(tok *jwt.Token) (any, error) {
			kid, _ := tok.Header["kid"].(string)
			key, ok := keys[kid]
			if !ok {
				return nil, jwt.ErrTokenUnverifiable
			}
			return key, nil
		},
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(disco.Issuer),
		jwt.WithAudience("default"),
	)
	require.NoError(t, err, "stock JWT client verifies the token against the published JWKS")
	require.True(t, parsed.Valid)

	assert.Equal(t, "svc", claims["sub"])
	assert.Equal(t, "default", claims["tid"])
	for _, name := range []string{"iat", "nbf", "exp", "jti"} {
		assert.Contains(t, claims, name, "registered claim %q present", name)
	}
}

// fetchJWKSKeys fetches a JWK set and reconstructs the RSA public keys, keyed by
// kid, exactly as a stock verifier would to check a token signature.
func fetchJWKSKeys(ctx context.Context, t *testing.T, jwksURL string) map[string]*rsa.PublicKey {
	t.Helper()

	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	getJSON(ctx, t, jwksURL, &set)

	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		require.Equal(t, "RSA", k.Kty)
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		require.NoError(t, err)
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		require.NoError(t, err)
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: int(new(big.Int).SetBytes(eBytes).Int64()),
		}
	}
	return keys
}

// getJSON issues a context-bound GET and decodes the JSON body into out.
func getJSON(ctx context.Context, t *testing.T, url string, out any) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "GET %s", url)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
}

// postToken issues a url-encoded token request and decodes the JSON body.
func postToken(ctx context.Context, t *testing.T, endpoint string, form url.Values, out any) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "POST %s: %s", endpoint, body)
	require.NoError(t, json.Unmarshal(body, out))
}
