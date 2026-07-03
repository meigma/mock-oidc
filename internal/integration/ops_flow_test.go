//go:build integration

package integration

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
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

// External authority the proxy scenario advertises. X-Forwarded-Port 443 is the
// scheme default, so ResolveBaseURL drops it and the resolved base is host-root.
const (
	fwdProto     = "https"
	fwdHost      = "idp.example.com"
	fwdAuthority = "https://idp.example.com"
)

// TestContainerProxyForwardedIssuer is the Slice 6 R3 `host.docker.internal`
// flavor: it proves the mock's advertised identity tracks the address a client
// actually reaches it under. It boots the shipped image with zero config, then
// drives every request through the mapped host port carrying X-Forwarded-*
// headers that name an EXTERNAL authority (as a reverse proxy would). Discovery
// must advertise that external authority for `iss` and every URL, and a token
// minted through the reachable port must carry the external `iss` and still
// verify against the served JWKS — i.e. advertised identity == reachable
// address (TDD §8.10 R3).
func TestContainerProxyForwardedIssuer(t *testing.T) {
	ctx := context.Background()

	skipIfImageMissing(ctx, t)

	// Zero-config container: the external identity comes purely from the
	// forwarded headers, exactly as it would behind a proxy.
	base := startControlContainer(ctx, t, nil)

	hdr := map[string]string{
		"X-Forwarded-Proto": fwdProto,
		"X-Forwarded-Host":  fwdHost,
		"X-Forwarded-Port":  "443",
	}

	disco := map[string]any{}
	doJSON(ctx, t, http.DefaultClient, http.MethodGet,
		base+"/default/.well-known/openid-configuration", hdr, &disco)

	assert.Equal(t, fwdAuthority+"/default", disco["issuer"],
		"iss reflects the forwarded external authority, host-root")
	assertAllURLsHavePrefix(t, disco, fwdAuthority+"/")

	// The advertised endpoints point at the unreachable external authority; a
	// real client reaches them through the proxy. Here we rewrite them back to
	// the reachable mapped port to prove the SAME identity is served there.
	tokenEndpoint := mustString(t, disco, "token_endpoint")
	jwksURI := mustString(t, disco, "jwks_uri")
	reachableToken := rewriteAuthority(t, tokenEndpoint, base)
	reachableJWKS := rewriteAuthority(t, jwksURI, base)

	keys := jwksKeysVia(ctx, t, http.DefaultClient, reachableJWKS, hdr)
	require.NotEmpty(t, keys, "served JWKS carries at least one key")

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	doForm(ctx, t, http.DefaultClient, reachableToken, url.Values{
		"grant_type": {"client_credentials"},
		"client_id":  {"svc"},
	}, hdr, &tokenResp)
	assert.Equal(t, "Bearer", tokenResp.TokenType)

	// A stock verifier accepts the token under the EXTERNAL issuer using ONLY
	// the JWKS served at the reachable address: advertised iss == reachable key
	// material.
	verifyContainerToken(t, tokenResp.AccessToken, keys, fwdAuthority+"/default", "default")
}

// TestContainerTLSSelfSigned is the Slice 6 R3 `ssl:{}` flavor: it boots the
// shipped image with JSON_CONFIG={"httpServer":{"ssl":{}}}, which turns on HTTPS
// with an in-process self-signed localhost certificate. It then curls discovery
// over `https` (skipping cert verification, as `curl -k` would), asserts every
// advertised URL is `https`, and verifies a freshly minted token against the
// JWKS served over the same HTTPS listener (TDD §8.10 R3, §6).
func TestContainerTLSSelfSigned(t *testing.T) {
	ctx := context.Background()

	skipIfImageMissing(ctx, t)

	base, client := startTLSContainer(ctx, t)

	disco := map[string]any{}
	doJSON(ctx, t, client, http.MethodGet,
		base+"/default/.well-known/openid-configuration", nil, &disco)

	issuer := mustString(t, disco, "issuer")
	assert.True(t, strings.HasPrefix(issuer, "https://"),
		"iss is https under ssl:{}, got %q", issuer)
	assertAllURLsHavePrefix(t, disco, "https://")

	// Discovery advertises the reachable https authority directly (no proxy), so
	// the advertised jwks_uri/token_endpoint are hit as-is.
	jwksURI := mustString(t, disco, "jwks_uri")
	keys := jwksKeysVia(ctx, t, client, jwksURI, nil)
	require.NotEmpty(t, keys, "served JWKS carries at least one key")

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	doForm(ctx, t, client, mustString(t, disco, "token_endpoint"), url.Values{
		"grant_type": {"client_credentials"},
		"client_id":  {"svc"},
	}, nil, &tokenResp)
	assert.Equal(t, "Bearer", tokenResp.TokenType)

	verifyContainerToken(t, tokenResp.AccessToken, keys, issuer, "default")
}

// startTLSContainer boots the shipped image with an ssl:{} JSON config so the
// API listener terminates TLS with the generated self-signed localhost cert. It
// returns the https base URL and an insecure client (the cert is self-signed).
func startTLSContainer(ctx context.Context, t *testing.T) (string, *http.Client) {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:           imageTag,
		ExposedPorts:    []string{apiPort},
		AlwaysPullImage: false,
		Env:             map[string]string{"JSON_CONFIG": `{"httpServer":{"ssl":{}}}`},
		WaitingFor: wait.ForHTTP("/isalive").
			WithPort(apiPort).
			WithTLS(true).
			WithAllowInsecure(true).
			WithStartupTimeout(90 * time.Second),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "boot %s with ssl:{}", imageTag)
	t.Cleanup(func() {
		if terr := ctr.Terminate(context.Background()); terr != nil {
			t.Logf("terminate container: %v", terr)
		}
	})

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	mapped, err := ctr.MappedPort(ctx, "8080")
	require.NoError(t, err)

	client := &http.Client{
		Transport: &http.Transport{
			// The container serves a self-signed localhost cert; a stock `curl -k`
			// client skips verification too.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	return "https://" + host + ":" + mapped.Port(), client
}

// assertAllURLsHavePrefix asserts every string value in the discovery document
// that looks like a URL starts with prefix.
func assertAllURLsHavePrefix(t *testing.T, disco map[string]any, prefix string) {
	t.Helper()

	for key, val := range disco {
		s, ok := val.(string)
		if !ok || !strings.Contains(s, "://") {
			continue
		}
		assert.Truef(t, strings.HasPrefix(s, prefix),
			"advertised %s=%q must start with %q", key, s, prefix)
	}
}

// mustString extracts a required string field from a decoded JSON object.
func mustString(t *testing.T, m map[string]any, key string) string {
	t.Helper()

	s, ok := m[key].(string)
	require.Truef(t, ok && s != "", "discovery field %q present and non-empty", key)
	return s
}

// rewriteAuthority swaps the scheme+host of raw for those of base, keeping the
// path and query. It turns an advertised external URL into one reachable through
// the mapped host port.
func rewriteAuthority(t *testing.T, raw, base string) string {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)
	b, err := url.Parse(base)
	require.NoError(t, err)
	u.Scheme = b.Scheme
	u.Host = b.Host
	return u.String()
}

// verifyContainerToken verifies a token exactly as a stock JWT client would:
// signature against the served JWKS keyed by kid, plus issuer/audience checks.
func verifyContainerToken(t *testing.T, raw string, keys map[string]*rsa.PublicKey, issuer, audience string) {
	t.Helper()

	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims,
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
		jwt.WithAudience(audience),
	)
	require.NoError(t, err, "stock JWT client verifies the token against the served JWKS")
	require.True(t, parsed.Valid)
}

// doJSON issues a context-bound GET with optional headers over the given client
// and decodes the JSON body into out.
func doJSON(ctx context.Context, t *testing.T, client *http.Client, method, u string, hdr map[string]string, out any) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	require.NoError(t, err)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "%s %s: %s", method, u, body)
	require.NoError(t, json.Unmarshal(body, out))
}

// doForm issues a url-encoded POST with optional headers over the given client
// and decodes the JSON body into out.
func doForm(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	endpoint string,
	form url.Values,
	hdr map[string]string,
	out any,
) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "POST %s: %s", endpoint, body)
	require.NoError(t, json.Unmarshal(body, out))
}

// jwksKeysVia fetches a JWK set over the given client and reconstructs the RSA
// public keys keyed by kid, as a stock verifier would.
func jwksKeysVia(
	ctx context.Context,
	t *testing.T,
	client *http.Client,
	jwksURL string,
	hdr map[string]string,
) map[string]*rsa.PublicKey {
	t.Helper()

	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	doJSON(ctx, t, client, http.MethodGet, jwksURL, hdr, &set)

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
