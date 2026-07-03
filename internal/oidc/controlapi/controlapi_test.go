package controlapi_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc"
	"github.com/meigma/mock-oidc/internal/oidc/controlapi"
	"github.com/meigma/mock-oidc/internal/oidc/memory"
	"github.com/meigma/mock-oidc/internal/oidc/signing"
)

// harness wires the control API over the REAL signing + memory adapters (no mocks),
// mirroring the composition root so the handler tests exercise true behavior.
type harness struct {
	api   humatest.TestAPI
	queue *memory.CallbackQueue
	rec   *memory.RequestRecorder
	clock *memory.Clock
}

func newHarness(t *testing.T) harness {
	t.Helper()

	signer, err := signing.NewProvider(oidc.RS256, nil)
	require.NoError(t, err)

	registry := memory.NewIssuerRegistry()
	queue := memory.NewCallbackQueue()
	rec := memory.NewRequestRecorder()
	clock := memory.NewClock()
	tokens := oidc.NewTokenService(registry, signer, signer, clock, oidc.WithCallbackQueue(queue))

	_, api := humatest.New(t)
	controlapi.Register(api, controlapi.Deps{
		Tokens:    tokens,
		Scenarios: queue,
		Requests:  rec,
		Clock:     clock,
	})
	return harness{api: api, queue: queue, rec: rec, clock: clock}
}

// decodeBody unmarshals a JSON response body into out.
func decodeBody(t *testing.T, data []byte, out any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(data, out))
}

// assertProblem asserts an RFC 9457 problem+json error at the expected status —
// NEVER the OAuth2 shape (no "error"/"error_description" fields).
func assertProblem(t *testing.T, resp interface {
	Result() *http.Response
}, code int, body []byte) {
	t.Helper()
	assert.Equal(t, code, resp.Result().StatusCode)
	assert.Equal(t, "application/problem+json", resp.Result().Header.Get("Content-Type"))
	var problem map[string]any
	decodeBody(t, body, &problem)
	assert.Contains(t, problem, "status")
	assert.Contains(t, problem, "title")
	assert.NotContains(t, problem, "error_description", "control errors must not use the OAuth2 shape")
}

func TestMintSuccess(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.api.Post("/_mock/mint", map[string]any{
		"issuer":    "default",
		"issuerUrl": "http://localhost:8080",
		"subject":   "alice",
		"audience":  []string{"my-api"},
		"claims":    map[string]any{"roles": []string{"admin"}},
	})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var out struct {
		Token     string         `json:"token"`
		Kid       string         `json:"kid"`
		Algorithm string         `json:"algorithm"`
		Issuer    string         `json:"issuer"`
		ExpiresAt time.Time      `json:"expiresAt"`
		Claims    map[string]any `json:"claims"`
	}
	decodeBody(t, resp.Body.Bytes(), &out)

	assert.NotEmpty(t, out.Token)
	assert.Equal(t, "default", out.Kid)
	assert.Equal(t, "RS256", out.Algorithm)
	assert.Equal(t, "http://localhost:8080/default", out.Issuer)
	assert.Equal(t, "alice", out.Claims["sub"])
	assert.Equal(t, "http://localhost:8080/default", out.Claims["iss"])
	assert.Equal(t, []any{"admin"}, out.Claims["roles"])
	assert.False(t, out.ExpiresAt.IsZero())
}

func TestMintProxyAwareIssuer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// No issuerUrl override -> iss resolves from the forwarded headers.
	resp := h.api.Post("/_mock/mint",
		"X-Forwarded-Proto: https",
		"X-Forwarded-Host: idp.example.com",
		map[string]any{"issuer": "default", "subject": "bob"},
	)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var out struct {
		Issuer string `json:"issuer"`
	}
	decodeBody(t, resp.Body.Bytes(), &out)
	assert.Equal(t, "https://idp.example.com/default", out.Issuer)
}

func TestMintBareHostDerivesIssuer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// No issuerUrl and no X-Forwarded-Host: iss must still resolve from the request's
	// own Host, which MintTokenInput.Resolve backfills from the request (Go keeps the
	// Host on r.Host, invisible to Huma's header binding). This is the DX fix: a bare
	// mint with no explicit host indicator succeeds instead of 422ing.
	resp := h.api.Post("/_mock/mint",
		"Host: idp.internal",
		map[string]any{"issuer": "default", "subject": "bob"},
	)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var out struct {
		Issuer string `json:"issuer"`
	}
	decodeBody(t, resp.Body.Bytes(), &out)
	assert.Equal(t, "http://idp.internal/default", out.Issuer,
		"a bare mint derives iss from the request Host")
}

func TestMintForwardedHostBeatsRequestHost(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Both are present; X-Forwarded-Host must win over the request Host.
	resp := h.api.Post("/_mock/mint",
		"Host: internal.local",
		"X-Forwarded-Host: forwarded.example",
		map[string]any{"issuer": "default", "subject": "bob"},
	)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var out struct {
		Issuer string `json:"issuer"`
	}
	decodeBody(t, resp.Body.Bytes(), &out)
	assert.Equal(t, "http://forwarded.example/default", out.Issuer,
		"X-Forwarded-Host takes precedence over the request Host")
}

func TestMintIssuerURLBeatsHostAndForwarded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// issuerUrl must win over both the forwarded header and the request Host.
	resp := h.api.Post("/_mock/mint",
		"Host: internal.local:8080",
		"X-Forwarded-Host: forwarded.example",
		map[string]any{
			"issuer":    "default",
			"issuerUrl": "https://explicit.example.com",
			"subject":   "bob",
		},
	)
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var out struct {
		Issuer string `json:"issuer"`
	}
	decodeBody(t, resp.Body.Bytes(), &out)
	assert.Equal(t, "https://explicit.example.com/default", out.Issuer,
		"issuerUrl overrides both X-Forwarded-Host and the request Host")
}

func TestMintReservedIssuerIs404Problem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.api.Post("/_mock/mint", map[string]any{
		"issuer":    "_mock",
		"issuerUrl": "http://localhost:8080",
	})
	assertProblem(t, resp, http.StatusNotFound, resp.Body.Bytes())
}

func TestMintBadIssuerURLIs422(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.api.Post("/_mock/mint", map[string]any{
		"issuer":    "default",
		"issuerUrl": "not-a-url", // no scheme -> ErrInvalidBaseURL -> 422
	})
	assertProblem(t, resp, http.StatusUnprocessableEntity, resp.Body.Bytes())
}

func TestMintInvalidKindEnumIsValidationError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.api.Post("/_mock/mint", map[string]any{
		"issuer":    "default",
		"issuerUrl": "http://localhost:8080",
		"kind":      "bogus",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
}

func TestScenariosEnqueueListClear(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Enqueue a default-callback scenario.
	resp := h.api.Post("/_mock/scenarios", map[string]any{
		"issuer": "default",
		"claims": map[string]any{"acr": "Level4"},
	})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var enq struct {
		ScenarioID string `json:"scenarioId"`
		QueueDepth int    `json:"queueDepth"`
	}
	decodeBody(t, resp.Body.Bytes(), &enq)
	assert.NotEmpty(t, enq.ScenarioID)
	assert.Equal(t, 1, enq.QueueDepth)

	// Enqueue a request-mapping scenario.
	resp = h.api.Post("/_mock/scenarios", map[string]any{
		"issuer": "other",
		"requestMappings": []map[string]any{
			{"param": "client_id", "match": "*", "claims": map[string]any{"aud": "${client_id}"}},
		},
	})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	// List reports both, head-first, with decoded kinds.
	resp = h.api.Get("/_mock/scenarios")
	require.Equal(t, http.StatusOK, resp.Code)
	var list struct {
		QueueDepth int `json:"queueDepth"`
		Scenarios  []struct {
			Issuer string `json:"issuer"`
			Kind   string `json:"kind"`
		} `json:"scenarios"`
	}
	decodeBody(t, resp.Body.Bytes(), &list)
	require.Equal(t, 2, list.QueueDepth)
	assert.Equal(t, "default", list.Scenarios[0].Issuer)
	assert.Equal(t, "default", list.Scenarios[0].Kind)
	assert.Equal(t, "other", list.Scenarios[1].Issuer)
	assert.Equal(t, "requestMapping", list.Scenarios[1].Kind)

	// Clear flushes the queue.
	resp = h.api.Delete("/_mock/scenarios")
	require.Equal(t, http.StatusOK, resp.Code)
	var cleared struct {
		QueueDepth int `json:"queueDepth"`
	}
	decodeBody(t, resp.Body.Bytes(), &cleared)
	assert.Equal(t, 0, cleared.QueueDepth)
	assert.Empty(t, h.queue.List())
}

func TestEnqueueReservedIssuerIs404Problem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.api.Post("/_mock/scenarios", map[string]any{"issuer": "_mock"})
	assertProblem(t, resp, http.StatusNotFound, resp.Body.Bytes())
}

func TestTakeRequestTimeoutIs404(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.api.Post("/_mock/requests/take", map[string]any{"timeoutMs": 20})
	assertProblem(t, resp, http.StatusNotFound, resp.Body.Bytes())
}

func TestTakeRequestReturnsRawBytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	raw := []byte("grant_type=client_credentials&client_id=cashier&scope=a+b")
	require.NoError(t, h.rec.Record(context.Background(), oidc.NewCapturedRequest(
		http.MethodPost,
		"http://localhost:8080/default/token",
		map[string][]string{"Content-Type": {"application/x-www-form-urlencoded"}},
		nil,
		raw,
	)))

	resp := h.api.Post("/_mock/requests/take", map[string]any{"endpoint": "token", "timeoutMs": 100})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	var out struct {
		Method     string `json:"method"`
		Path       string `json:"path"`
		Issuer     string `json:"issuer"`
		BodyBase64 string `json:"bodyBase64"`
		Body       string `json:"body"`
	}
	decodeBody(t, resp.Body.Bytes(), &out)
	assert.Equal(t, http.MethodPost, out.Method)
	assert.Equal(t, "/default/token", out.Path)
	assert.Equal(t, "default", out.Issuer)
	assert.Equal(t, string(raw), out.Body)
	decoded, err := base64.StdEncoding.DecodeString(out.BodyBase64)
	require.NoError(t, err)
	assert.Equal(t, raw, decoded, "bodyBase64 must round-trip the exact bytes")

	// The take was destructive: a second take times out.
	resp = h.api.Post("/_mock/requests/take", map[string]any{"endpoint": "token", "timeoutMs": 20})
	assert.Equal(t, http.StatusNotFound, resp.Code)
}

func TestListAndClearRequests(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	for _, ep := range []string{"token", "userinfo"} {
		require.NoError(t, h.rec.Record(context.Background(), oidc.NewCapturedRequest(
			http.MethodPost, "http://localhost:8080/default/"+ep, nil, nil, []byte("x"))))
	}

	resp := h.api.Get("/_mock/requests")
	require.Equal(t, http.StatusOK, resp.Code)
	var list struct {
		Count    int `json:"count"`
		Requests []struct {
			Path string `json:"path"`
		} `json:"requests"`
	}
	decodeBody(t, resp.Body.Bytes(), &list)
	assert.Equal(t, 2, list.Count)

	// Filter by endpoint.
	resp = h.api.Get("/_mock/requests?endpoint=userinfo")
	require.Equal(t, http.StatusOK, resp.Code)
	decodeBody(t, resp.Body.Bytes(), &list)
	require.Equal(t, 1, list.Count)
	assert.Equal(t, "/default/userinfo", list.Requests[0].Path)

	resp = h.api.Delete("/_mock/requests")
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Empty(t, h.rec.List(oidc.CaptureFilter{}))
}

func TestClockTransitions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Initial: unfrozen.
	resp := h.api.Get("/_mock/clock")
	require.Equal(t, http.StatusOK, resp.Code)
	var st struct {
		Frozen bool      `json:"frozen"`
		Now    time.Time `json:"now"`
	}
	decodeBody(t, resp.Body.Bytes(), &st)
	assert.False(t, st.Frozen)

	// Freeze at a fixed instant.
	frozenAt := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	resp = h.api.Put("/_mock/clock", map[string]any{"frozen": true, "instant": frozenAt})
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	decodeBody(t, resp.Body.Bytes(), &st)
	assert.True(t, st.Frozen)
	assert.True(t, frozenAt.Equal(st.Now))

	// Advance by 1h1m.
	resp = h.api.Post("/_mock/clock/advance", map[string]any{"duration": "1h1m"})
	require.Equal(t, http.StatusOK, resp.Code)
	decodeBody(t, resp.Body.Bytes(), &st)
	assert.True(t, st.Frozen)
	assert.True(t, frozenAt.Add(time.Hour+time.Minute).Equal(st.Now))

	// Unfreeze.
	resp = h.api.Put("/_mock/clock", map[string]any{"frozen": false})
	require.Equal(t, http.StatusOK, resp.Code)
	decodeBody(t, resp.Body.Bytes(), &st)
	assert.False(t, st.Frozen)
}

func TestClockFreezeWithoutInstantIs422(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.api.Put("/_mock/clock", map[string]any{"frozen": true})
	assertProblem(t, resp, http.StatusUnprocessableEntity, resp.Body.Bytes())
}

func TestClockAdvanceBadDurationIs422(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	resp := h.api.Post("/_mock/clock/advance", map[string]any{"duration": "not-a-duration"})
	assertProblem(t, resp, http.StatusUnprocessableEntity, resp.Body.Bytes())
}

func TestResetClearsStateButKeepsClockUnfrozen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	// Seed queue, log, and a frozen clock.
	_, err := h.queue.Enqueue(mustScenario(t, "default"))
	require.NoError(t, err)
	require.NoError(t, h.rec.Record(context.Background(), oidc.NewCapturedRequest(
		http.MethodPost, "http://localhost:8080/default/token", nil, nil, []byte("x"))))
	h.clock.Freeze(oidc.NewInstant(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)))

	resp := h.api.Post("/_mock/reset")
	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())

	assert.Empty(t, h.queue.List(), "reset flushes the scenario queue")
	assert.Empty(t, h.rec.List(oidc.CaptureFilter{}), "reset clears the request log")
	assert.False(t, h.clock.State().Frozen, "reset unfreezes the clock")
}

// mustScenario builds a bare default-callback scenario for the given issuer.
func mustScenario(t *testing.T, issuer string) oidc.Scenario {
	t.Helper()
	sc, err := oidc.NewScenario(oidc.NewDefaultTokenCallback(oidc.IssuerID(issuer)))
	require.NoError(t, err)
	return sc
}
