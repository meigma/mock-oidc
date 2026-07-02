package httpapi_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
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

const testTimeout = 5 * time.Second

// newTestServer wires the real Registrar with interactive login OFF (the
// zero-config default), suiting the Slice 1 discovery/JWKS/token tests.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newAuthServer(t, false)
}

// newAuthServer wires the real Registrar over the real signing + memory adapters
// (no mocks) behind the kept transport router, including the composition-root
// FallbackWriter strategy so wrong-method protocol routes render the OAuth2 shape.
// The CodeStore is shared between the AuthorizeService and the TokenService so a
// code minted at /authorize is redeemable at /token. interactive forces the login
// page on GET /authorize.
func newAuthServer(t *testing.T, interactive bool) *httptest.Server {
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
	authorize := oidc.NewAuthorizeService(codes, clock, interactive)
	deps := httpapi.Deps{Provider: provider, Tokens: tokens, Authorize: authorize}

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
	return srv
}

func doGet(t *testing.T, srv *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp, body
}

// postForm POSTs a url-encoded body to the /default/token endpoint (following
// redirects; the token endpoint never issues one).
func postForm(t *testing.T, srv *httptest.Server, form string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		srv.URL+"/default/token",
		strings.NewReader(form),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp, body
}

// TestDiscoveryFixedFieldOrderNoSchemaLink verifies the discovery document serves
// the fixed field order (token_endpoint 5th), the request-derived issuer, and —
// per Decision D-5 — no $schema field and no Link header.
func TestDiscoveryFixedFieldOrderNoSchemaLink(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	resp, body := doGet(t, srv, "/default/.well-known/openid-configuration")

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Link"), "no SchemaLink header (D-5)")

	wantOrder := []string{
		"issuer", "authorization_endpoint", "end_session_endpoint", "revocation_endpoint",
		"token_endpoint", "userinfo_endpoint", "jwks_uri", "introspection_endpoint",
		"response_types_supported", "response_modes_supported", "subject_types_supported",
		"id_token_signing_alg_values_supported", "code_challenge_methods_supported",
	}
	assert.Equal(t, wantOrder, topLevelKeys(t, body), "fixed serialization order")

	var doc map[string]any
	require.NoError(t, json.Unmarshal(body, &doc))
	_, hasSchema := doc["$schema"]
	assert.False(t, hasSchema, "no $schema field (D-5)")

	issuer, _ := doc["issuer"].(string)
	assert.True(t, strings.HasSuffix(issuer, "/default"), "issuer ends with /default, got %q", issuer)
	assert.Equal(t, issuer+"/token", doc["token_endpoint"])

	// Advertised algs match the single-sourced domain constant.
	wantAlgs := make([]any, 0)
	for _, a := range oidc.SupportedSigningAlgorithms() {
		wantAlgs = append(wantAlgs, string(a))
	}
	assert.Equal(t, wantAlgs, doc["id_token_signing_alg_values_supported"])
}

// TestDiscoveryAliasIdenticalBody verifies the RFC 8414 alias returns the same
// body as the OIDC path.
func TestDiscoveryAliasIdenticalBody(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	_, oidcBody := doGet(t, srv, "/default/.well-known/openid-configuration")
	_, aliasBody := doGet(t, srv, "/default/.well-known/oauth-authorization-server")
	assert.JSONEq(t, string(oidcBody), string(aliasBody))
}

// TestJWKS verifies the JWK set carries one RSA key with kid=default, use=sig,
// alg=RS256, and public n/e (no private material).
func TestJWKS(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	resp, body := doGet(t, srv, "/default/jwks")

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var set struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(body, &set))
	require.Len(t, set.Keys, 1)
	k := set.Keys[0]
	assert.Equal(t, "RSA", k["kty"])
	assert.Equal(t, "sig", k["use"])
	assert.Equal(t, "default", k["kid"])
	assert.Equal(t, "RS256", k["alg"])
	assert.NotEmpty(t, k["n"])
	assert.NotEmpty(t, k["e"])
	_, hasD := k["d"]
	assert.False(t, hasD, "no private material in JWKS")
}

// TestTokenClientCredentials verifies a client_credentials token is minted, has
// the default claims, and verifies against the served JWKS.
func TestTokenClientCredentials(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)

	resp, body := postForm(t, srv, "grant_type=client_credentials&client_id=app")
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var tok struct {
		TokenType   string `json:"token_type"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	require.NoError(t, json.Unmarshal(body, &tok))
	assert.Equal(t, "Bearer", tok.TokenType)
	require.NotEmpty(t, tok.AccessToken)
	assert.Positive(t, tok.ExpiresIn)

	// Signature verifies against the published JWKS.
	pub := fetchPublicKey(t, srv)
	claims := verifyRS256(t, tok.AccessToken, pub)

	_, discoveryBody := doGet(t, srv, "/default/.well-known/openid-configuration")
	var doc map[string]any
	require.NoError(t, json.Unmarshal(discoveryBody, &doc))

	assert.Equal(t, doc["issuer"], claims["iss"])
	assert.Equal(t, "app", claims["sub"])
	assert.Equal(t, []any{"default"}, claims["aud"])
	assert.Equal(t, "default", claims["tid"])
	for _, name := range []string{"iat", "nbf", "exp", "jti"} {
		assert.Contains(t, claims, name, "claim %q present", name)
	}
}

// TestTokenErrorsCorrectCase verifies OAuth2 errors are correct-case (never
// lowercased) and carry the expected code/status.
func TestTokenErrorsCorrectCase(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)

	tests := []struct {
		name       string
		form       string
		wantStatus int
		wantCode   string
		wantDesc   string
	}{
		{
			name:       "unknown grant",
			form:       "grant_type=bogus",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_grant",
			wantDesc:   "grant_type bogus not supported.",
		},
		{
			name:       "missing grant",
			form:       "client_id=app",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
			wantDesc:   "missing required parameter grant_type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, body := postForm(t, srv, tt.form)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

			var e struct {
				Code string `json:"error"`
				Desc string `json:"error_description"`
			}
			require.NoError(t, json.Unmarshal(body, &e))
			assert.Equal(t, tt.wantCode, e.Code)
			assert.Equal(t, tt.wantDesc, e.Desc)
		})
	}
}

// TestGetTokenReturns405 verifies GET /{issuer}/token yields a 405 in the uniform
// OAuth2 shape (via the router FallbackWriter), never RFC 9457 problem+json.
func TestGetTokenReturns405(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)
	resp, body := doGet(t, srv, "/default/token")

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var e map[string]any
	require.NoError(t, json.Unmarshal(body, &e))
	assert.Contains(t, e, "error", "OAuth2-shaped body")
	assert.NotContains(t, e, "status", "not RFC 9457 problem+json")
}

// TestCORSSmoke verifies Decision D-3 end to end: a cross-origin GET reflects the
// Origin with credentials, and an OPTIONS preflight answers 204 with the fixed
// method set — all with zero configuration.
func TestCORSSmoke(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)

	// Cross-origin simple request.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/default/jwks", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://spa.example")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, "https://spa.example", resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))

	// Preflight on the token endpoint.
	preq, err := http.NewRequestWithContext(context.Background(), http.MethodOptions, srv.URL+"/default/token", nil)
	require.NoError(t, err)
	preq.Header.Set("Origin", "https://spa.example")
	preq.Header.Set("Access-Control-Request-Method", http.MethodPost)
	presp, err := srv.Client().Do(preq)
	require.NoError(t, err)
	require.NoError(t, presp.Body.Close())
	assert.Equal(t, http.StatusNoContent, presp.StatusCode)
	assert.Equal(t, "POST, GET, OPTIONS", presp.Header.Get("Access-Control-Allow-Methods"))
}

// --- helpers -------------------------------------------------------------

// topLevelKeys returns the top-level object keys of a JSON document in
// serialization order.
func topLevelKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), tok)

	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		require.NoError(t, err)
		key, ok := keyTok.(string)
		require.True(t, ok)
		keys = append(keys, key)
		skipValue(t, dec)
	}
	return keys
}

// skipValue consumes the next JSON value from dec, descending into any nested
// object/array so the decoder is positioned at the following key.
func skipValue(t *testing.T, dec *json.Decoder) {
	t.Helper()
	tok, err := dec.Token()
	require.NoError(t, err)
	if tok == json.Delim('[') || tok == json.Delim('{') {
		depth := 1
		for depth > 0 {
			tk, err := dec.Token()
			require.NoError(t, err)
			switch tk {
			case json.Delim('['), json.Delim('{'):
				depth++
			case json.Delim(']'), json.Delim('}'):
				depth--
			}
		}
	}
}

// fetchPublicKey fetches the served JWKS and reconstructs the RSA public key.
func fetchPublicKey(t *testing.T, srv *httptest.Server) *rsa.PublicKey {
	t.Helper()
	_, body := doGet(t, srv, "/default/jwks")
	var set struct {
		Keys []struct {
			N string `json:"n"`
			E string `json:"e"`
		} `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(body, &set))
	require.Len(t, set.Keys, 1)

	nBytes, err := base64.RawURLEncoding.DecodeString(set.Keys[0].N)
	require.NoError(t, err)
	eBytes, err := base64.RawURLEncoding.DecodeString(set.Keys[0].E)
	require.NoError(t, err)
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}
}

// verifyRS256 verifies a compact JWS against pub and returns its claims. It fails
// the test on any signature error — proving a stock client accepts the token.
func verifyRS256(t *testing.T, token string, pub *rsa.PublicKey) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "compact JWS has three segments")

	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)

	digest := sha256.Sum256([]byte(signingInput))
	require.NoError(t, rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig), "signature verifies against JWKS")

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}
