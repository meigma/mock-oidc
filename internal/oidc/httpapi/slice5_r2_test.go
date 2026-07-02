package httpapi_test

// This file holds the Slice 5 R2 functional tests: they wire the httpapi
// registrar, the controlapi /_mock group, and the request-recording middleware
// together over REAL signing + memory adapters — the composition app.go will
// perform — and drive the whole surface end to end through httptest. The
// controlapi import here is a TEST-ONLY composition dependency; production
// httpapi code never imports controlapi (the layering the arch guards protect).

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adapterhttp "github.com/meigma/mock-oidc/internal/adapter/http"
	"github.com/meigma/mock-oidc/internal/observability"
	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/controlapi"
	"github.com/meigma/mock-oidc/internal/oidc/httpapi"
	"github.com/meigma/mock-oidc/internal/oidc/memory"
	"github.com/meigma/mock-oidc/internal/oidc/signing"
)

// controlPlane bundles the httptest server with the shared adapters, so a test
// can both drive the protocol surface and inspect/steer via the same instances.
type controlPlane struct {
	srv   *httptest.Server
	queue *memory.CallbackQueue
	rec   *memory.RequestRecorder
	clock *memory.Clock
}

// newControlPlane wires the full Slice 5 surface the way app.go will: httpapi +
// controlapi on one Huma API, the RecordRequests middleware wrapping the mux, and
// one shared clock/queue/recorder. Optional seed records pre-populate configured
// issuer callbacks (the config tokenCallbacks path).
func newControlPlane(t *testing.T, seed ...oidc.IssuerRecord) controlPlane {
	t.Helper()

	signer, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	registry := memory.NewIssuerRegistry(seed...)
	queue := memory.NewCallbackQueue()
	rec := memory.NewRequestRecorder()
	clock := memory.NewClock()
	codes := memory.NewCodeStore()
	refresh := memory.NewRefreshTokenStore()

	provider := oidc.NewProviderService(registry, signer)
	tokens := oidc.NewTokenService(registry, signer, signer, clock,
		oidc.WithCodeStore(codes),
		oidc.WithRefreshStore(refresh),
		oidc.WithCallbackQueue(queue),
	)
	authorize := oidc.NewAuthorizeService(codes, clock, false) // non-interactive: GET /authorize issues a code
	session := oidc.NewSessionService(signer, refresh, clock)

	httpDeps := httpapi.Deps{Provider: provider, Tokens: tokens, Authorize: authorize, Session: session}
	ctrlDeps := controlapi.Deps{Tokens: tokens, Scenarios: queue, Requests: rec, Clock: clock}

	discard := observability.NewLogger(io.Discard, 0, "json")
	router := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Logger:         discard,
		Metrics:        observability.NewMetrics(),
		Version:        "test",
		RequestTimeout: testTimeout,
		Register: func(api huma.API) {
			httpapi.Register(api, httpDeps)
			controlapi.Register(api, ctrlDeps)
		},
		FallbackWriter: func(w http.ResponseWriter, r *http.Request) bool {
			if !httpapi.IsProtocolPath(r.URL.Path) {
				return false
			}
			httpapi.WriteOAuth2Error(w, http.StatusMethodNotAllowed,
				"invalid_request", "the method is not allowed for this resource")
			return true
		},
	})

	// Mux-level recording: wrap the whole handler so every inbound protocol request
	// is captured (path-guarded), exactly as oidcMux.Use(RecordRequests) will.
	srv := httptest.NewServer(httpapi.RecordRequests(rec)(router))
	t.Cleanup(srv.Close)
	return controlPlane{srv: srv, queue: queue, rec: rec, clock: clock}
}

// helpers --------------------------------------------------------------------

// postTokenForm POSTs a url-encoded body to /{issuer}/token and returns the
// decoded token response.
func postTokenForm(t *testing.T, srv *httptest.Server, issuer, form string) tokenResp {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/"+issuer+"/token", strings.NewReader(form))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	var tr tokenResp
	require.NoError(t, json.Unmarshal(body, &tr), "token body: %s", body)
	return tr
}

type tokenResp struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// postJSON POSTs a JSON body and returns status + raw response bytes.
func postJSON(t *testing.T, client *http.Client, url string, body any) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(string(raw)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp.StatusCode, out
}

// publicKeyFor fetches /{issuer}/jwks and reconstructs the RSA public key.
func publicKeyFor(t *testing.T, srv *httptest.Server, issuer string) (*rsa.PublicKey, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/"+issuer+"/jwks", nil)
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
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
	}, set.Keys[0].Kid
}

// rsVerify verifies a compact RS256 JWS against pub, returning the error (nil on
// success) plus the decoded claims when the signature checks out.
func rsVerify(token string, pub *rsa.PublicKey) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errBadToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if verifyErr := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); verifyErr != nil {
		return nil, verifyErr
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}

var errBadToken = stringError("token is not a compact JWS")

type stringError string

func (e stringError) Error() string { return string(e) }

// Tests ----------------------------------------------------------------------

// TestR2MultiIssuerDistinctJWKS proves two issuers serve independent keys (kid ==
// issuer id) and a token issued under one fails verification against the other's.
func TestR2MultiIssuerDistinctJWKS(t *testing.T) {
	t.Parallel()
	cp := newControlPlane(t)

	defKey, defKid := publicKeyFor(t, cp.srv, "default")
	otherKey, otherKid := publicKeyFor(t, cp.srv, "other")

	assert.Equal(t, "default", defKid)
	assert.Equal(t, "other", otherKid)
	assert.NotEqual(t, defKey.N.String(), otherKey.N.String(), "issuers have distinct moduli")

	tr := postTokenForm(t, cp.srv, "other", "grant_type=client_credentials&client_id=web")
	require.NotEmpty(t, tr.AccessToken)

	_, err := rsVerify(tr.AccessToken, defKey)
	require.Error(t, err, "an 'other' token must NOT verify against 'default' keys")
	_, err = rsVerify(tr.AccessToken, otherKey)
	require.NoError(t, err, "the 'other' token verifies against its own key")
}

// TestR2EnqueuedScenarioShapesNextTokenForIssuerOnly proves an enqueued scenario
// is single-use, issuer-matched-head (a queued default scenario does not touch
// 'other'), and reverts after one consumption.
func TestR2EnqueuedScenarioShapesNextTokenForIssuerOnly(t *testing.T) {
	t.Parallel()
	cp := newControlPlane(t)
	defKey, _ := publicKeyFor(t, cp.srv, "default")
	otherKey, _ := publicKeyFor(t, cp.srv, "other")

	// Enqueue a default-issuer scenario stamping acr=Level4.
	status, body := postJSON(t, cp.srv.Client(), cp.srv.URL+"/_mock/scenarios", map[string]any{
		"issuer": "default",
		"claims": map[string]any{"acr": "Level4"},
	})
	require.Equal(t, http.StatusOK, status, "enqueue: %s", body)

	// An interleaved 'other' token arrives FIRST: the head targets 'default', so it
	// must be unaffected and must NOT consume the scenario.
	otherTok := postTokenForm(t, cp.srv, "other", "grant_type=client_credentials&client_id=web")
	otherClaims, err := rsVerify(otherTok.AccessToken, otherKey)
	require.NoError(t, err)
	assert.NotContains(t, otherClaims, "acr", "the 'other' token is untouched by a 'default' scenario")

	// The next 'default' token consumes the scenario -> acr=Level4.
	firstDef := postTokenForm(t, cp.srv, "default", "grant_type=client_credentials&client_id=web")
	firstClaims, err := rsVerify(firstDef.AccessToken, defKey)
	require.NoError(t, err)
	assert.Equal(t, "Level4", firstClaims["acr"])

	// A second 'default' token reverts (single-use) -> no acr.
	secondDef := postTokenForm(t, cp.srv, "default", "grant_type=client_credentials&client_id=web")
	secondClaims, err := rsVerify(secondDef.AccessToken, defKey)
	require.NoError(t, err)
	assert.NotContains(t, secondClaims, "acr", "scenario is single-use")
}

// TestR2ConfigMappingTemplatesAudFromParam proves a config tokenCallbacks
// RequestMapping templates aud from a submitted request param (via the
// multi-valued form path threaded at the edge).
func TestR2ConfigMappingTemplatesAudFromParam(t *testing.T) {
	t.Parallel()

	var audClaims oidc.CustomClaims
	audClaims.Set("aud", oidc.ClaimValue("${tenant}"))
	mapping, err := oidc.NewRequestMappingCallback("default", 0, []oidc.RequestMapping{
		{Param: "tenant", Match: "*", Claims: audClaims},
	})
	require.NoError(t, err)
	cp := newControlPlane(t, oidc.IssuerRecord{
		ID:        "default",
		Callbacks: []oidc.TokenCallback{mapping},
	})
	defKey, _ := publicKeyFor(t, cp.srv, "default")

	tr := postTokenForm(t, cp.srv, "default",
		"grant_type=client_credentials&client_id=web&tenant=my-api")
	claims, err := rsVerify(tr.AccessToken, defKey)
	require.NoError(t, err)
	assert.Equal(t, "my-api", audString(claims["aud"]), "aud templated from the tenant param")
}

// audString normalizes an aud claim (string or single-element array) to a string.
func audString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		if len(t) == 1 {
			if s, ok := t[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// TestR2MintVerifiesAndUserinfoAccepts proves POST /_mock/mint returns a token
// that verifies at /jwks and is accepted by /userinfo — mint ≡ grant, no flow.
func TestR2MintVerifiesAndUserinfoAccepts(t *testing.T) {
	t.Parallel()
	cp := newControlPlane(t)

	// issuerUrl pins iss to this server's own base URL so the minted token's iss
	// matches what /userinfo derives from its request origin (Go does not expose
	// the special Host header to Huma's header binding).
	status, body := postJSON(t, cp.srv.Client(), cp.srv.URL+"/_mock/mint", map[string]any{
		"issuer":    "default",
		"issuerUrl": cp.srv.URL,
		"subject":   "alice",
		"claims":    map[string]any{"roles": []string{"admin"}},
	})
	require.Equal(t, http.StatusOK, status, "mint: %s", body)

	var out struct {
		Token  string         `json:"token"`
		Claims map[string]any `json:"claims"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.NotEmpty(t, out.Token)
	assert.Equal(t, "alice", out.Claims["sub"])

	defKey, _ := publicKeyFor(t, cp.srv, "default")
	claims, err := rsVerify(out.Token, defKey)
	require.NoError(t, err, "minted token verifies at /jwks")
	assert.Equal(t, "alice", claims["sub"])

	// The token is accepted at /userinfo (same signer + clock).
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, cp.srv.URL+"/default/userinfo", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	resp, err := cp.srv.Client().Do(req)
	require.NoError(t, err)
	uiBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode, "userinfo: %s", uiBody)

	var ui map[string]any
	require.NoError(t, json.Unmarshal(uiBody, &ui))
	assert.Equal(t, "alice", ui["sub"])
}

// TestR2RequestsTakeReturnsRawTokenPostAndSkipsControl proves the recorder
// captures the exact raw bytes (order preserved) of a prior /token POST and that
// /_mock/* itself is never recorded.
func TestR2RequestsTakeReturnsRawTokenPostAndSkipsControl(t *testing.T) {
	t.Parallel()
	cp := newControlPlane(t)

	const form = "grant_type=client_credentials&client_id=web&scope=a+b"
	tr := postTokenForm(t, cp.srv, "default", form)
	require.NotEmpty(t, tr.AccessToken)

	status, body := postJSON(t, cp.srv.Client(), cp.srv.URL+"/_mock/requests/take", map[string]any{
		"endpoint":  "token",
		"timeoutMs": 1000,
	})
	require.Equal(t, http.StatusOK, status, "take: %s", body)

	var got struct {
		Method     string `json:"method"`
		Path       string `json:"path"`
		BodyBase64 string `json:"bodyBase64"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "POST", got.Method)
	assert.Equal(t, "/default/token", got.Path)
	raw, err := base64.StdEncoding.DecodeString(got.BodyBase64)
	require.NoError(t, err)
	assert.Equal(t, form, string(raw), "exact raw form bytes, order preserved")

	// The /_mock/requests/take call and other control traffic must never appear.
	for _, r := range cp.rec.List(oidc.CaptureFilter{}) {
		assert.False(t, strings.HasPrefix(r.Path, "/_mock"), "control plane must not record itself: %s", r.Path)
	}
}

// TestR2DebuggerFullRoundTrip drives the four-operation debugger flow with a
// cookie jar: GET renders the pre-filled form, POST 302-redirects into
// /authorize, and the callback performs the REAL back-channel /token exchange and
// renders the tokens plus raw exchange bytes.
func TestR2DebuggerFullRoundTrip(t *testing.T) {
	t.Parallel()
	cp := newControlPlane(t)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	var redirects []string
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			redirects = append(redirects, req.URL.String())
			if len(via) >= 10 {
				return stringError("too many redirects")
			}
			return nil
		},
	}

	// 1. GET the pre-filled form.
	getReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, cp.srv.URL+"/default/debugger", nil)
	require.NoError(t, err)
	getResp, err := client.Do(getReq)
	require.NoError(t, err)
	formBody, err := io.ReadAll(getResp.Body)
	require.NoError(t, err)
	require.NoError(t, getResp.Body.Close())
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	form := string(formBody)
	assert.Contains(t, form, `action="/default/debugger"`, "form posts back to the issuer debugger")
	assert.Contains(t, form, `name="client_id"`)
	assert.Contains(t, form, `value="debugger"`, "client_id is pre-filled")

	// 2. POST the form — the client follows the 302 into /authorize and on into the
	//    callback, which runs the real back-channel exchange.
	postReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		cp.srv.URL+"/default/debugger", strings.NewReader("client_id=web-app&scope=openid+profile"))
	require.NoError(t, err)
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postResp, err := client.Do(postReq)
	require.NoError(t, err)
	resultBody, err := io.ReadAll(postResp.Body)
	require.NoError(t, err)
	require.NoError(t, postResp.Body.Close())

	// The front-channel leg redirected into the issuer's /authorize.
	joined := strings.Join(redirects, "\n")
	assert.Contains(t, joined, "/default/authorize", "POST /debugger 302s into /authorize")
	assert.Contains(t, joined, "code_challenge_method=S256", "PKCE challenge rides the authorize redirect")

	// The browser lands on the callback, which rendered the exchange result.
	require.Equal(t, http.StatusOK, postResp.StatusCode, "callback: %s", resultBody)
	assert.Equal(t, "/default/debugger/callback", postResp.Request.URL.Path)
	result := string(resultBody)
	assert.Contains(t, result, "Token exchange complete")
	assert.Contains(t, result, "Back-channel request", "raw exchange bytes are shown")
	assert.Contains(t, result, "grant_type=authorization_code", "the real code exchange was performed")

	// The back-channel /token exchange really happened (recorded like any /token).
	var sawTokenPost bool
	for _, r := range cp.rec.List(oidc.CaptureFilter{Endpoint: "token"}) {
		if r.Method == http.MethodPost && strings.Contains(string(r.Body), "authorization_code") {
			sawTokenPost = true
		}
	}
	assert.True(t, sawTokenPost, "the callback issued a real POST /default/token code exchange")
}
