package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/oidc/httpapi"
)

// TestStaticHandlerServesAndGuards is the R2 traversal/MIME table for the raw
// /static/* tree: a real asset serves 200 with a mime-typed Content-Type, while
// every escape attempt (dotdot climb, absolute path, nested climb) is a flat 404
// "not found" — never a read of a file outside the configured root.
func TestStaticHandlerServesAndGuards(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "css"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "css", "app.css"), []byte("body{color:red}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>console"), 0o644))

	// A secret one directory above the static root; a working traversal would read it.
	secret := filepath.Join(filepath.Dir(root), "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))
	t.Cleanup(func() { _ = os.Remove(secret) })

	// A symlink planted INSIDE the root that points at the out-of-tree secret:
	// the lexical guard cannot see through it, so the handler must resolve the
	// link and refuse it as a flat 404 (the doc-comment guarantee).
	require.NoError(t, os.Symlink(secret, filepath.Join(root, "leak.txt")))

	h := httpapi.NewStaticHandler(root)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantCT     string
		wantBody   string
	}{
		{"nested asset served with mime", "/static/css/app.css", http.StatusOK, "text/css", "body{color:red}"},
		{"index.html served, not redirected", "/static/index.html", http.StatusOK, "text/html", "console"},
		{"dotdot climb refused", "/static/../secret.txt", http.StatusNotFound, "", "not found"},
		{"deep climb refused", "/static/../../etc/passwd", http.StatusNotFound, "", "not found"},
		{"absolute escape refused", "/static//etc/passwd", http.StatusNotFound, "", "not found"},
		{"inside symlink escape refused", "/static/leak.txt", http.StatusNotFound, "", "not found"},
		{"missing file 404", "/static/nope.js", http.StatusNotFound, "", "not found"},
		{"directory 404", "/static/css", http.StatusNotFound, "", "not found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			require.Equal(t, tc.wantStatus, rec.Code)
			if tc.wantCT != "" {
				assert.Contains(t, rec.Header().Get("Content-Type"), tc.wantCT)
			}
			assert.Contains(t, rec.Body.String(), tc.wantBody)
		})
	}
}
