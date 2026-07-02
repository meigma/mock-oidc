package httpapi_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// newDelegationServer wires the real Registrar over the real signing + memory
// adapters, seeding the IssuerRegistry with the given records so a test can pin a
// configured callback audience in-process (config-file seed parsing for callbacks
// is a Slice 5 deliverable, so the audience-precedence case is expressed at the Go
// level here). With no seed it is the zero-config default issuer.
func newDelegationServer(t *testing.T, seed ...oidc.IssuerRecord) *httptest.Server {
	t.Helper()

	signer, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	registry := memory.NewIssuerRegistry(seed...)
	clock := memory.NewClock()
	codes := memory.NewCodeStore()
	refresh := memory.NewRefreshTokenStore()
	provider := oidc.NewProviderService(registry, signer)
	tokens := oidc.NewTokenService(registry, signer, signer, clock,
		oidc.WithCodeStore(codes), oidc.WithRefreshStore(refresh))
	authorize := oidc.NewAuthorizeService(codes, clock, false)
	session := oidc.NewSessionService(signer, refresh, clock)
	deps := httpapi.Deps{Provider: provider, Tokens: tokens, Authorize: authorize, Session: session}

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

// fixedAudienceCallback is a configured TokenCallback that Matches every input and
// resolves a pinned audience — the "callback audience configured" precedence row,
// seeded in-process because config-seed parsing of callbacks lands in Slice 5.
type fixedAudienceCallback struct {
	issuer oidc.IssuerID
	aud    oidc.Audience
}

func (c fixedAudienceCallback) IssuerID() oidc.IssuerID                    { return c.issuer }
func (c fixedAudienceCallback) Subject(in oidc.CallbackInput) oidc.Subject { return in.Subject }
func (c fixedAudienceCallback) Audience(oidc.CallbackInput) oidc.Audience  { return c.aud }
func (c fixedAudienceCallback) TypeHeader(oidc.CallbackInput) oidc.JOSEType {
	return oidc.DefaultJOSEType
}
func (c fixedAudienceCallback) ExtraClaims(oidc.CallbackInput) oidc.ClaimSet { return oidc.ClaimSet{} }
func (c fixedAudienceCallback) Expiry() time.Duration                        { return time.Hour }
func (c fixedAudienceCallback) Matches(oidc.CallbackInput) bool              { return true }

// mintAccessToken drives client_credentials against srv and returns the signed
// access token, used as a real (server-minted) subject_token source.
func mintAccessToken(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {"app"}, "scope": {"api:read"}}
	resp, body := postForm(t, srv, form.Encode())
	require.Equal(t, http.StatusOK, resp.StatusCode, "mint subject token: %s", body)
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(body, &tok))
	require.NotEmpty(t, tok.AccessToken)
	return tok.AccessToken
}

// makeUnverifiedJWS builds a compact JWS whose signature segment is deliberate
// garbage: the delegation path PARSES but never verifies these, so a bogus
// signature must still be accepted. Only the payload is meaningful.
func makeUnverifiedJWS(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	body := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + body + ".not-a-real-signature"
}

// TestTokenExchangeOverHTTP is the RFC 8693 R2 case: a real server-minted
// subject_token is exchanged over HTTP. The response copies the subject-token
// claims, stamps issued_token_type, OMITS scope, and — on the zero-config default
// issuer (no configured callback audience) — sets aud to the request audience
// param. The exchanged access token verifies against the published JWKS.
func TestTokenExchangeOverHTTP(t *testing.T) {
	t.Parallel()

	srv := newDelegationServer(t)
	subject := mintAccessToken(t, srv)

	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subject},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {"https://rs.example"},
		// client_secret_post — token-exchange requires SOME client authentication.
		"client_id":     {"app"},
		"client_secret": {"anything"},
	}
	resp, body := postForm(t, srv, form.Encode())
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	assert.Equal(t, "Bearer", raw["token_type"])
	assert.Equal(t, "urn:ietf:params:oauth:token-type:access_token", raw["issued_token_type"])
	assert.NotEmpty(t, raw["access_token"])
	assert.NotContains(t, raw, "id_token", "token-exchange issues no id_token")
	assert.NotContains(t, raw, "refresh_token", "token-exchange issues no refresh_token")
	assert.NotContains(t, raw, "scope", "token-exchange never echoes scope")

	access, _ := raw["access_token"].(string)
	claims := verifyRS256(t, access, fetchPublicKey(t, srv))
	assert.Equal(t, "app", claims["sub"], "subject-token sub copied verbatim")
	assert.Equal(t, srv.URL+"/default", claims["iss"], "iss re-stamped to the resolved issuer")
	assert.Equal(t, []any{"https://rs.example"}, claims["aud"], "request audience wins (no configured audience)")
}

// TestTokenExchangeConfiguredAudienceWins pins catalog line 98 over HTTP: with a
// configured callback audience, the exchanged aud is the CONFIGURED value even
// when a differing audience param is sent. The callback is seeded in-process.
func TestTokenExchangeConfiguredAudienceWins(t *testing.T) {
	t.Parallel()

	seeded := oidc.IssuerRecord{
		ID: "default",
		Callbacks: []oidc.TokenCallback{
			fixedAudienceCallback{issuer: "default", aud: oidc.Audience{"https://configured.example"}},
		},
	}
	srv := newDelegationServer(t, seeded)
	subject := mintAccessToken(t, srv)

	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subject},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {"https://param.example"},
		"client_id":          {"app"},
		"client_secret":      {"anything"},
	}
	resp, body := postForm(t, srv, form.Encode())
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal(body, &tok))
	claims := verifyRS256(t, tok.AccessToken, fetchPublicKey(t, srv))
	assert.Equal(t, []any{"https://configured.example"}, claims["aud"],
		"configured callback audience wins over the request param")
}

// TestTokenExchangeNoClientAuth asserts the requirePrivateKeyJwt presence rule:
// token-exchange with no client authentication at all is invalid_request with the
// exact upstream text.
func TestTokenExchangeNoClientAuth(t *testing.T) {
	t.Parallel()

	srv := newDelegationServer(t)
	subject := mintAccessToken(t, srv)

	form := url.Values{
		"grant_type":         {"urn:ietf:params:oauth:grant-type:token-exchange"},
		"subject_token":      {subject},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"},
		"audience":           {"https://rs.example"},
	}
	resp, body := postForm(t, srv, form.Encode())
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	code, desc := errorBody(t, body)
	assert.Equal(t, "invalid_request", code)
	assert.Equal(t, "request must contain some form of ClientAuthentication.", desc)
}

// TestJWTBearerMissingAssertion asserts the parse-edge missing-parameter case: a
// jwt-bearer request with no assertion is 400 invalid_request with the exact text.
func TestJWTBearerMissingAssertion(t *testing.T) {
	t.Parallel()

	srv := newDelegationServer(t)
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}}
	resp, body := postForm(t, srv, form.Encode())

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	code, desc := errorBody(t, body)
	assert.Equal(t, "invalid_request", code)
	assert.Equal(t, "missing required parameter assertion", desc)
}

// TestJWTBearerAssertionScope asserts the OBO scope fallback over HTTP: with no
// request scope the assertion's own scope claim is used, an access token only is
// issued, and the assertion subject is copied into the minted token.
func TestJWTBearerAssertionScope(t *testing.T) {
	t.Parallel()

	srv := newDelegationServer(t)
	assertion := makeUnverifiedJWS(t, map[string]any{"sub": "obo-user", "scope": "api:read"})

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	resp, body := postForm(t, srv, form.Encode())
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	assert.NotEmpty(t, raw["access_token"])
	assert.NotContains(t, raw, "id_token")
	assert.NotContains(t, raw, "refresh_token")
	assert.NotContains(t, raw, "issued_token_type", "jwt-bearer omits issued_token_type")
	assert.Equal(t, "api:read", raw["scope"], "scope resolved from the assertion claim")

	access, _ := raw["access_token"].(string)
	claims := verifyRS256(t, access, fetchPublicKey(t, srv))
	assert.Equal(t, "obo-user", claims["sub"], "assertion sub copied verbatim")
	assert.Equal(t, srv.URL+"/default", claims["iss"], "iss re-stamped")
}

// TestPasswordOverHTTP asserts the ROPC matrix over HTTP: id_token AND
// access_token (no refresh), sub=username, id_token aud=[client_id], any password
// accepted, both verifiable against the published JWKS.
func TestPasswordOverHTTP(t *testing.T) {
	t.Parallel()

	srv := newDelegationServer(t)
	form := url.Values{
		"grant_type": {"password"},
		"username":   {"alice"},
		"password":   {"not-checked-at-all"},
		"client_id":  {"app"},
		"scope":      {"openid api:read"},
	}
	resp, body := postForm(t, srv, form.Encode())
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(body, &raw))
	assert.NotEmpty(t, raw["id_token"])
	assert.NotEmpty(t, raw["access_token"])
	assert.NotContains(t, raw, "refresh_token", "password issues no refresh token")
	assert.Equal(t, "openid api:read", raw["scope"], "scope echoed")

	pub := fetchPublicKey(t, srv)
	idToken, _ := raw["id_token"].(string)
	idClaims := verifyRS256(t, idToken, pub)
	assert.Equal(t, "alice", idClaims["sub"], "sub = username")
	assert.Equal(t, []any{"app"}, idClaims["aud"], "id_token aud = [client_id]")
	assert.NotContains(t, idClaims, "nonce", "password id_token carries no nonce")
	assert.NotContains(t, idClaims, "azp", "only authorization_code adds azp")

	accessToken, _ := raw["access_token"].(string)
	accClaims := verifyRS256(t, accessToken, pub)
	assert.Equal(t, "alice", accClaims["sub"])
}

// TestPrivateKeyJWTExchange proves the structural-only client-assertion validation
// on the token-exchange path: each structurally-bad assertion is 400
// invalid_request, while a structurally-valid one — carrying a garbage signature —
// SUCCEEDS, proving the signature is never verified.
func TestPrivateKeyJWTExchange(t *testing.T) {
	t.Parallel()

	srv := newDelegationServer(t)
	subject := mintAccessToken(t, srv)
	tokenEndpointURL := srv.URL + "/default/token"

	// base is the structurally-sound assertion; each row mutates a copy.
	base := func() map[string]any {
		return map[string]any{
			"iss": "app",
			"sub": "app",
			"aud": tokenEndpointURL,
			"iat": 1_700_000_000,
			"exp": 1_700_000_060, // 60s lifetime — within the 120s cap
		}
	}

	tests := []struct {
		name   string
		mutate func(m map[string]any)
		wantOK bool
	}{
		{"structurally valid (garbage signature)", func(map[string]any) {}, true},
		{"lifetime exceeds 120s", func(m map[string]any) { m["exp"] = 1_700_000_121 }, false},
		{"iss != client_id", func(m map[string]any) { m["iss"] = "someone-else" }, false},
		{"sub != client_id", func(m map[string]any) { m["sub"] = "someone-else" }, false},
		{"empty audience", func(m map[string]any) { delete(m, "aud") }, false},
		{
			"audience size > 1",
			func(m map[string]any) { m["aud"] = []string{tokenEndpointURL, "https://extra.example"} },
			false,
		},
		{"audience not accepted", func(m map[string]any) { m["aud"] = "https://evil.example" }, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			claims := base()
			tc.mutate(claims)
			assertion := makeUnverifiedJWS(t, claims)

			form := url.Values{
				"grant_type":            {"urn:ietf:params:oauth:grant-type:token-exchange"},
				"subject_token":         {subject},
				"subject_token_type":    {"urn:ietf:params:oauth:token-type:access_token"},
				"audience":              {"https://rs.example"},
				"client_id":             {"app"},
				"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
				"client_assertion":      {assertion},
			}
			resp, body := postForm(t, srv, form.Encode())

			if tc.wantOK {
				require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)
				var raw map[string]any
				require.NoError(t, json.Unmarshal(body, &raw))
				assert.NotEmpty(t, raw["access_token"])
				return
			}
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body: %s", body)
			code, _ := errorBody(t, body)
			assert.Equal(t, "invalid_request", code)
		})
	}
}

// errorBody decodes an RFC 6749 §5.2 error envelope into its code and description.
func errorBody(t *testing.T, body []byte) (string, string) {
	t.Helper()
	var e struct {
		Code string `json:"error"`
		Desc string `json:"error_description"`
	}
	require.NoError(t, json.Unmarshal(body, &e))
	return e.Code, e.Desc
}
