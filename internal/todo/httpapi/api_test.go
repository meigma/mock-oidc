package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	adapterhttp "github.com/meigma/template-go-api/internal/adapter/http"
	"github.com/meigma/template-go-api/internal/observability"
	"github.com/meigma/template-go-api/internal/todo"
	"github.com/meigma/template-go-api/internal/todo/httpapi"
	"github.com/meigma/template-go-api/internal/todo/todotest"
)

const testRequestTimeout = 5 * time.Second

type testResponse struct {
	status      int
	body        string
	contentType string
}

// newTestServer wires the real generic router to the real todo adapter and a
// stateful in-memory test repository, exercising the full request path end to end.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	discard := observability.NewLogger(io.Discard, slog.LevelError, "json")
	service := todo.NewService(todotest.NewRepository(), discard)
	handler := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Logger:         discard,
		Metrics:        observability.NewMetrics(),
		Version:        "test",
		RequestTimeout: testRequestTimeout,
		Readiness:      nil,
		Register:       func(api huma.API) { httpapi.Register(api, service) },
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

func doRequest(t *testing.T, srv *httptest.Server, method, path, body string) testResponse {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return testResponse{
		status:      resp.StatusCode,
		body:        string(data),
		contentType: resp.Header.Get("Content-Type"),
	}
}

func TestTodoAPIFunctional(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)

	resp := doRequest(t, srv, http.MethodPost, "/todos", `{"title":"buy milk"}`)
	require.Equal(t, http.StatusCreated, resp.status)

	var created httpapi.TodoDTO
	require.NoError(t, json.Unmarshal([]byte(resp.body), &created))
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "buy milk", created.Title)
	assert.Equal(t, "open", created.Status)

	resp = doRequest(t, srv, http.MethodGet, "/todos/"+created.ID, "")
	require.Equal(t, http.StatusOK, resp.status)

	resp = doRequest(t, srv, http.MethodGet, "/todos/does-not-exist", "")
	require.Equal(t, http.StatusNotFound, resp.status)
	assert.Contains(t, resp.contentType, "application/problem+json")

	resp = doRequest(t, srv, http.MethodPost, "/todos", `{"title":""}`)
	require.Equal(t, http.StatusUnprocessableEntity, resp.status)
	assert.Contains(t, resp.contentType, "application/problem+json")

	resp = doRequest(t, srv, http.MethodGet, "/todos", "")
	require.Equal(t, http.StatusOK, resp.status)

	var listed struct {
		Todos []httpapi.TodoDTO `json:"todos"`
	}
	require.NoError(t, json.Unmarshal([]byte(resp.body), &listed))
	assert.Len(t, listed.Todos, 1)

	resp = doRequest(t, srv, http.MethodPost, "/todos/"+created.ID+"/complete", "")
	require.Equal(t, http.StatusOK, resp.status)

	var completed httpapi.TodoDTO
	require.NoError(t, json.Unmarshal([]byte(resp.body), &completed))
	assert.Equal(t, "completed", completed.Status)
	assert.NotNil(t, completed.CompletedAt)

	resp = doRequest(t, srv, http.MethodPost, "/todos/missing/complete", "")
	require.Equal(t, http.StatusNotFound, resp.status)

	// A valid path with an unregistered method returns RFC 9457 problem+json.
	resp = doRequest(t, srv, http.MethodDelete, "/todos", "")
	require.Equal(t, http.StatusMethodNotAllowed, resp.status)
	assert.Contains(t, resp.contentType, "application/problem+json")
}

// listPage mirrors the list response body for decoding in tests.
type listPage struct {
	Todos      []httpapi.TodoDTO `json:"todos"`
	NextCursor string            `json:"nextCursor"`
}

// decodeList asserts a 200 and decodes the list response body.
func decodeList(t *testing.T, resp testResponse) listPage {
	t.Helper()

	require.Equal(t, http.StatusOK, resp.status)
	var page listPage
	require.NoError(t, json.Unmarshal([]byte(resp.body), &page))

	return page
}

// TestTodoListPagination exercises the keyset-paginated list endpoint end to
// end: a bounded page advertises a cursor, following it walks the rest of the
// collection without overlap, the last page omits the cursor, the limit is
// bounded (422 outside [1, MaxPageSize]), and a malformed cursor is a 422.
func TestTodoListPagination(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t)

	for _, title := range []string{"a", "b", "c"} {
		resp := doRequest(t, srv, http.MethodPost, "/todos", fmt.Sprintf(`{"title":%q}`, title))
		require.Equal(t, http.StatusCreated, resp.status)
	}

	// A full page of two carries the cursor to the next page.
	page1 := decodeList(t, doRequest(t, srv, http.MethodGet, "/todos?limit=2", ""))
	assert.Len(t, page1.Todos, 2)
	require.NotEmpty(t, page1.NextCursor, "a full page must advertise the next cursor")

	// Following the cursor returns the remaining todo and ends the walk.
	page2 := decodeList(t, doRequest(t, srv, http.MethodGet,
		"/todos?limit=2&cursor="+url.QueryEscape(page1.NextCursor), ""))
	assert.Len(t, page2.Todos, 1)
	assert.Empty(t, page2.NextCursor, "the last page omits the next cursor")

	// The two pages cover the whole collection with no overlap.
	ids := map[string]bool{}
	for _, td := range append(page1.Todos, page2.Todos...) {
		assert.False(t, ids[td.ID], "todo %s appeared on two pages", td.ID)
		ids[td.ID] = true
	}
	assert.Len(t, ids, 3)

	// The limit is bounded: below the minimum and above the maximum are 422s.
	// Deriving the over-max value from todo.MaxPageSize keeps this coupled to the
	// constant behind the static maximum tag.
	for _, q := range []string{"limit=0", fmt.Sprintf("limit=%d", todo.MaxPageSize+1)} {
		resp := doRequest(t, srv, http.MethodGet, "/todos?"+q, "")
		require.Equal(t, http.StatusUnprocessableEntity, resp.status, q)
		assert.Contains(t, resp.contentType, "application/problem+json")
	}

	// The maximum itself is accepted.
	resp := doRequest(t, srv, http.MethodGet, fmt.Sprintf("/todos?limit=%d", todo.MaxPageSize), "")
	require.Equal(t, http.StatusOK, resp.status)

	// A malformed cursor is a client error, not a 500.
	resp = doRequest(t, srv, http.MethodGet, "/todos?cursor="+url.QueryEscape("!not base64!"), "")
	require.Equal(t, http.StatusUnprocessableEntity, resp.status)
	assert.Contains(t, resp.contentType, "application/problem+json")
}

// TestServiceLogCarriesRequestID proves the request-scoped logger reaches the
// domain: the service's "todo created" line must carry the same request_id as
// the access-log line. The handler is driven synchronously (no live server) so
// the shared buffer is written and read on one goroutine, avoiding a data race.
func TestServiceLogCarriesRequestID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := observability.NewLogger(&buf, slog.LevelInfo, "json")
	service := todo.NewService(todotest.NewRepository(), logger)
	handler := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Logger:         logger,
		Metrics:        observability.NewMetrics(),
		Version:        "test",
		RequestTimeout: testRequestTimeout,
		Readiness:      nil,
		Register:       func(api huma.API) { httpapi.Register(api, service) },
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/todos", strings.NewReader(`{"title":"buy milk"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)

	created := findLogEntry(t, buf.String(), "todo created")
	access := findLogEntry(t, buf.String(), "http request")
	require.NotEmpty(t, created["request_id"])
	assert.Equal(t, access["request_id"], created["request_id"])
}

// findLogEntry returns the first JSON log line in logs whose msg equals want.
func findLogEntry(t *testing.T, logs, want string) map[string]any {
	t.Helper()

	for line := range strings.SplitSeq(strings.TrimSpace(logs), "\n") {
		if line == "" {
			continue
		}

		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["msg"] == want {
			return entry
		}
	}

	t.Fatalf("no log entry with msg %q in:\n%s", want, logs)

	return nil
}
