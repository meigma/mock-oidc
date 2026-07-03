//go:build integration

package integration

import (
	"bytes"
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

// TestContainerControlPlane is the Slice 5 R3 headline (C6): it boots the shipped
// mock-oidc:dev image with MOCK_OIDC_CONTROL_ENABLED=true and drives the whole
// test-time control plane on-container the way a Testcontainers suite would —
// reset, freeze the clock, prove multi-issuer key isolation, script the next
// token with a one-shot scenario, mint a token with no flow, read back the exact
// raw bytes of a prior request, travel the clock to expire a token, and reset
// while the signing keys survive. It skips loudly when the image is not present
// locally.
func TestContainerControlPlane(t *testing.T) {
	ctx := context.Background()
	base := startControlContainer(ctx, t, map[string]string{"MOCK_OIDC_CONTROL_ENABLED": "true"})

	// 1. Reset to a known baseline, then freeze the clock at a fixed instant so
	//    every subsequent iat/nbf/exp is deterministic.
	status, body := postControlJSON(ctx, t, base+"/_mock/reset", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "reset: %s", body)

	const frozenAt = "2020-06-01T00:00:00Z"
	frozenTime, err := time.Parse(time.RFC3339, frozenAt)
	require.NoError(t, err)
	status, body = putJSON(ctx, t, base+"/_mock/clock", map[string]any{"frozen": true, "instant": frozenAt})
	require.Equalf(t, http.StatusOK, status, "freeze clock: %s", body)

	// 2. Multi-issuer key isolation: two issuers serve distinct keys (kid == issuer
	//    id); a token issued under 'other' never verifies against 'default'.
	defKeys := fetchJWKSKeys(ctx, t, base+"/default/jwks")
	otherKeys := fetchJWKSKeys(ctx, t, base+"/other/jwks")
	require.Contains(t, defKeys, "default", "default's kid is its issuer id")
	require.Contains(t, otherKeys, "other", "other's kid is its issuer id")
	assert.NotEqual(t, defKeys["default"].N.String(), otherKeys["other"].N.String(),
		"issuers materialize independent keys")

	// 3. Enqueue a one-shot scenario shaping the NEXT default token (acr=Level4). An
	//    interleaved 'other' token (issuer-matched head) is untouched and does not
	//    consume it; a second 'default' token reverts (single-use).
	status, body = postControlJSON(ctx, t, base+"/_mock/scenarios", map[string]any{
		"issuer": "default",
		"claims": map[string]any{"acr": "Level4"},
	})
	require.Equalf(t, http.StatusOK, status, "enqueue scenario: %s", body)

	otherTok := clientCredentialsToken(ctx, t, base, "other")
	assert.NotContains(t, unverifiedClaims(t, otherTok), "acr",
		"a queued 'default' scenario does not touch 'other'")

	firstDef := clientCredentialsToken(ctx, t, base, "default")
	assert.Equal(t, "Level4", unverifiedClaims(t, firstDef)["acr"], "the scenario shapes the next default token")

	secondDef := clientCredentialsToken(ctx, t, base, "default")
	assert.NotContains(t, unverifiedClaims(t, secondDef), "acr", "the scenario is single-use")

	// 4. Direct mint (no flow): the minted token verifies against /default/jwks and
	//    is accepted at /default/userinfo — mint ≡ grant. issuerUrl pins iss to an
	//    explicit, stable base here so the clock-travel step below can reason about it;
	//    the bare-Host derivation (no override) is proven separately just after.
	status, body = postControlJSON(ctx, t, base+"/_mock/mint", map[string]any{
		"issuer":    "default",
		"issuerUrl": base,
		"subject":   "alice",
		"claims":    map[string]any{"roles": []string{"admin"}},
	})
	require.Equalf(t, http.StatusOK, status, "mint: %s", body)
	var mint struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	require.NoError(t, json.Unmarshal(body, &mint))
	require.NotEmpty(t, mint.Token)

	// Verify at the frozen instant: the token was minted under the frozen clock, so
	// the stock jwt library must validate exp/nbf against that same instant (not the
	// test host's real wall time) — the server's userinfo/introspect below use the
	// server-side frozen clock, so they need no such adjustment.
	mintClaims := verifiedClaimsAt(t, defKeys, base+"/default", mint.Token, frozenTime)
	assert.Equal(t, "alice", mintClaims["sub"], "the minted token verifies against the published JWKS")

	uiStatus, uiBody := getBearer(ctx, t, base+"/default/userinfo", mint.Token)
	require.Equalf(t, http.StatusOK, uiStatus, "userinfo (live minted token): %s", uiBody)

	// 4b. Bare mint (host-indicator DX fix): with NO issuerUrl and NO X-Forwarded-Host,
	//     iss is derived from the request's own Host — the container's mapped address —
	//     so a caller needs no explicit host indicator. The resolved iss must equal the
	//     base the request reached, and the token must be accepted at /userinfo.
	status, body = postControlJSON(ctx, t, base+"/_mock/mint", map[string]any{
		"issuer":  "default",
		"subject": "carol",
	})
	require.Equalf(t, http.StatusOK, status, "bare mint (no host override): %s", body)
	var bareMint struct {
		Token  string `json:"token"`
		Issuer string `json:"issuer"`
	}
	require.NoError(t, json.Unmarshal(body, &bareMint))
	require.NotEmpty(t, bareMint.Token)
	assert.Equal(t, base+"/default", bareMint.Issuer,
		"a bare mint derives iss from the request Host (the mapped container address)")

	bareUIStatus, bareUIBody := getBearer(ctx, t, base+"/default/userinfo", bareMint.Token)
	require.Equalf(t, http.StatusOK, bareUIStatus, "userinfo (bare-minted token): %s", bareUIBody)

	// 5. Capture → take: after driving a known /default/token POST, the destructive
	//    take returns its exact raw bytes (order preserved); /_mock is never logged.
	status, body = deleteControl(ctx, t, base+"/_mock/requests")
	require.Equalf(t, http.StatusOK, status, "clear requests: %s", body)

	const rawForm = "grant_type=client_credentials&client_id=web&scope=a+b"
	postRawToken(ctx, t, base+"/default/token", rawForm)

	status, body = postControlJSON(ctx, t, base+"/_mock/requests/take", map[string]any{
		"endpoint":  "token",
		"timeoutMs": 2000,
	})
	require.Equalf(t, http.StatusOK, status, "take request: %s", body)
	var taken struct {
		Method     string `json:"method"`
		Path       string `json:"path"`
		BodyBase64 string `json:"bodyBase64"`
	}
	require.NoError(t, json.Unmarshal(body, &taken))
	assert.Equal(t, http.MethodPost, taken.Method)
	assert.Equal(t, "/default/token", taken.Path)
	rawBytes, err := base64.StdEncoding.DecodeString(taken.BodyBase64)
	require.NoError(t, err)
	assert.Equal(t, rawForm, string(rawBytes), "the exact raw form bytes are captured, order preserved")

	// The control plane never records itself.
	status, body = getControl(ctx, t, base+"/_mock/requests")
	require.Equalf(t, http.StatusOK, status, "list requests: %s", body)
	assert.NotContains(t, string(body), "/_mock", "no /_mock request ever appears in the log")

	// 6. Clock travel: advance past the minted token's exp. One clock drives both
	//    issuance and verification, so introspect flips to inactive and userinfo 401s.
	status, body = postControlJSON(ctx, t, base+"/_mock/clock/advance", map[string]any{"duration": "1h1m"})
	require.Equalf(t, http.StatusOK, status, "advance clock: %s", body)

	status, body = postFormAuth(ctx, t, base+"/default/introspect",
		url.Values{"token": {mint.Token}}, basicClientAuth())
	require.Equalf(t, http.StatusOK, status, "introspect (expired): %s", body)
	var intro map[string]any
	require.NoError(t, json.Unmarshal(body, &intro))
	assert.Equal(t, false, intro["active"], "the clock-advanced token introspects inactive")

	uiStatus, uiBody = getBearer(ctx, t, base+"/default/userinfo", mint.Token)
	assert.Equalf(t, http.StatusUnauthorized, uiStatus, "userinfo rejects the expired token: %s", uiBody)

	// 7. Reset unfreezes the clock but MUST NOT drop the materialized signing keys.
	status, body = postControlJSON(ctx, t, base+"/_mock/reset", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "reset: %s", body)

	status, body = getControl(ctx, t, base+"/_mock/clock")
	require.Equalf(t, http.StatusOK, status, "get clock: %s", body)
	var clockState struct {
		Frozen bool `json:"frozen"`
	}
	require.NoError(t, json.Unmarshal(body, &clockState))
	assert.False(t, clockState.Frozen, "reset unfreezes the clock")

	afterKeys := fetchJWKSKeys(ctx, t, base+"/default/jwks")
	require.Contains(t, afterKeys, "default")
	assert.Equal(t, defKeys["default"].N.String(), afterKeys["default"].N.String(),
		"reset preserves the materialized signing keys (previously-fetched JWKS still verify)")
}

// TestContainerConfigCallbackAudiencePrecedence unlocks the proof deferred from
// Slice 4: a config `tokenCallbacks` entry with its OWN audience overrides the
// request `audience` param on a token-exchange. It boots the image with a
// JSON_CONFIG seeding a default-issuer callback whose audience is fixed, then
// exchanges a token requesting a DIFFERENT audience and proves the configured one
// wins. Skips loudly when the image is absent.
func TestContainerConfigCallbackAudiencePrecedence(t *testing.T) {
	ctx := context.Background()

	const jsonConfig = `{
		"tokenCallbacks": [
			{"issuer": "default", "audience": ["configured-audience"]}
		]
	}`
	base := startControlContainer(ctx, t, map[string]string{"JSON_CONFIG": jsonConfig})

	keys := fetchJWKSKeys(ctx, t, base+"/default/jwks")
	require.NotEmpty(t, keys)

	// Mint the subject token to exchange.
	var cc struct {
		AccessToken string `json:"access_token"`
	}
	postToken(ctx, t, base+"/default/token", url.Values{
		"grant_type": {"client_credentials"},
		"client_id":  {"svc"},
	}, &cc)
	require.NotEmpty(t, cc.AccessToken)

	// Exchange it requesting a DIFFERENT audience; the configured callback audience
	// must win (chain step 1 beats the request's audience param, step 2).
	status, body := postTokenForm(ctx, t, base+"/default/token", url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {cc.AccessToken},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {"requested-audience"},
		"client_id":          {"svc"},
		"client_secret":      {"anything-never-validated"},
	})
	require.Equalf(t, http.StatusOK, status, "token-exchange: %s", body)
	var exch struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(body, &exch))

	claims := verifiedClaims(t, keys, base+"/default", exch.AccessToken)
	assert.Equal(t, []any{"configured-audience"}, claims["aud"],
		"the configured callback audience overrides the requested audience param")
}

// TestContainerAtJwtScenario unlocks the at+jwt proof: a one-shot scenario whose
// callback overrides the JWS typ to at+jwt shapes the next default token, which is
// still a genuine token — accepted at /userinfo and introspected active. Skips
// loudly when the image is absent.
func TestContainerAtJwtScenario(t *testing.T) {
	ctx := context.Background()
	base := startControlContainer(ctx, t, map[string]string{"MOCK_OIDC_CONTROL_ENABLED": "true"})

	status, body := postControlJSON(ctx, t, base+"/_mock/scenarios", map[string]any{
		"issuer": "default",
		"typ":    "at+jwt",
	})
	require.Equalf(t, http.StatusOK, status, "enqueue at+jwt scenario: %s", body)

	tok := clientCredentialsToken(ctx, t, base, "default")
	assert.Equal(t, "at+jwt", jwtHeader(t, tok)["typ"], "the scenario overrides the JWS typ header")

	// The at+jwt access token is still genuine: accepted at userinfo, active at introspect.
	uiStatus, uiBody := getBearer(ctx, t, base+"/default/userinfo", tok)
	require.Equalf(t, http.StatusOK, uiStatus, "userinfo (at+jwt): %s", uiBody)

	status, body = postFormAuth(ctx, t, base+"/default/introspect",
		url.Values{"token": {tok}}, basicClientAuth())
	require.Equalf(t, http.StatusOK, status, "introspect (at+jwt): %s", body)
	var intro map[string]any
	require.NoError(t, json.Unmarshal(body, &intro))
	assert.Equal(t, true, intro["active"], "the at+jwt token introspects active")
}

// verifiedClaimsAt verifies a token's signature against ONLY the published JWKS
// (plus issuer) with exp/nbf evaluated at the given instant, so a token minted
// under a frozen clock verifies against that same frozen instant rather than the
// test host's real wall time.
func verifiedClaimsAt(
	t *testing.T,
	keys map[string]*rsa.PublicKey,
	issuer, token string,
	at time.Time,
) jwt.MapClaims {
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
		jwt.WithTimeFunc(func() time.Time { return at }),
	)
	require.NoError(t, err, "stock JWT client verifies the minted token against the published JWKS")
	require.True(t, parsed.Valid)
	return claims
}

// startControlContainer boots the shipped mock-oidc:dev image with env and returns
// its base URL, skipping loudly when the image (or Docker) is absent.
func startControlContainer(ctx context.Context, t *testing.T, env map[string]string) string {
	t.Helper()

	skipIfImageMissing(ctx, t)

	req := testcontainers.ContainerRequest{
		Image:           imageTag,
		ExposedPorts:    []string{apiPort},
		AlwaysPullImage: false,
		Env:             env,
		WaitingFor: wait.ForHTTP("/isalive").
			WithPort(apiPort).
			WithStartupTimeout(60 * time.Second),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "boot %s with env %v", imageTag, env)
	t.Cleanup(func() {
		if terr := ctr.Terminate(context.Background()); terr != nil {
			t.Logf("terminate container: %v", terr)
		}
	})

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	mapped, err := ctr.MappedPort(ctx, "8080")
	require.NoError(t, err)
	return "http://" + host + ":" + mapped.Port()
}

// clientCredentialsToken drives a client_credentials grant against issuer and
// returns the raw access token.
func clientCredentialsToken(ctx context.Context, t *testing.T, base, issuer string) string {
	t.Helper()

	var tr struct {
		AccessToken string `json:"access_token"`
	}
	postToken(ctx, t, base+"/"+issuer+"/token", url.Values{
		"grant_type": {"client_credentials"},
		"client_id":  {"web"},
	}, &tr)
	require.NotEmpty(t, tr.AccessToken, "%s token grant returns an access token", issuer)
	return tr.AccessToken
}

// postControlJSON POSTs a JSON body to a /_mock endpoint and returns status + bytes.
func postControlJSON(ctx context.Context, t *testing.T, endpoint string, payload any) (int, []byte) {
	t.Helper()
	return sendJSON(ctx, t, http.MethodPost, endpoint, payload)
}

// putJSON PUTs a JSON body and returns status + bytes.
func putJSON(ctx context.Context, t *testing.T, endpoint string, payload any) (int, []byte) {
	t.Helper()
	return sendJSON(ctx, t, http.MethodPut, endpoint, payload)
}

// sendJSON issues a JSON request with method and returns status + body bytes.
func sendJSON(ctx context.Context, t *testing.T, method, endpoint string, payload any) (int, []byte) {
	t.Helper()

	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// getControl GETs a /_mock endpoint and returns status + bytes.
func getControl(ctx context.Context, t *testing.T, endpoint string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// deleteControl DELETEs a /_mock endpoint and returns status + bytes.
func deleteControl(ctx context.Context, t *testing.T, endpoint string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// postRawToken POSTs a verbatim url-encoded body to a token endpoint (bytes are
// sent exactly as given so the recorder captures them unchanged).
func postRawToken(ctx context.Context, t *testing.T, endpoint, rawForm string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(rawForm))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// unverifiedClaims decodes a compact JWT's payload segment WITHOUT verifying the
// signature — used only to read a scripted claim (acr) whose exact value is the
// assertion; signature-bearing assertions use verifiedClaims instead.
func unverifiedClaims(t *testing.T, token string) map[string]any {
	t.Helper()
	return decodeSegment(t, token, 1)
}

// jwtHeader decodes a compact JWT's header segment (typ/alg/kid).
func jwtHeader(t *testing.T, token string) map[string]any {
	t.Helper()
	return decodeSegment(t, token, 0)
}

// decodeSegment base64url-decodes the nth dot-separated segment of a compact JWT
// into a JSON object.
func decodeSegment(t *testing.T, token string, n int) map[string]any {
	t.Helper()

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "token is a compact JWS")
	raw, err := base64.RawURLEncoding.DecodeString(parts[n])
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}
