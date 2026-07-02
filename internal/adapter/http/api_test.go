package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaProbeOutput is a concrete-struct JSON output — the exact shape Huma's
// default SchemaLinkTransformer would decorate with a $schema field and a Link
// response header.
type schemaProbeOutput struct {
	Body struct {
		Value string `json:"value"`
	}
}

// TestNewAPIStripsSchemaLinkTransformer verifies Decision D-5: NewAPI clears
// Huma's SchemaLinkTransformer so a registered concrete-struct JSON operation
// emits neither a $schema first field nor a Link: rel="describedBy" header. This
// keeps the OIDC protocol JSON (discovery/JWKS) clean for strict third-party
// clients and preserves the fixed discovery field order.
func TestNewAPIStripsSchemaLinkTransformer(t *testing.T) {
	t.Parallel()

	mux := chi.NewMux()
	api := NewAPI(mux, "test")
	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/probe",
	}, func(_ context.Context, _ *struct{}) (*schemaProbeOutput, error) {
		out := &schemaProbeOutput{}
		out.Body.Value = "ok"
		return out, nil
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/probe", nil)
	require.NoError(t, err)
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Link"), "SchemaLinkTransformer Link header must be stripped")

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(raw, &body))
	_, hasSchema := body["$schema"]
	assert.False(t, hasSchema, "SchemaLinkTransformer $schema field must be stripped")
	assert.Equal(t, "ok", body["value"])
}
