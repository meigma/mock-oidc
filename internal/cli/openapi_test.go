package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAPICommandWritesFile(t *testing.T) {
	t.Parallel()

	out := filepath.Join(t.TempDir(), "openapi.yaml")
	root := NewRootCommand(Options{Build: BuildInfo{Version: "1.2.3"}})
	root.SetArgs([]string{"openapi", "--output", out})

	require.NoError(t, root.ExecuteContext(context.Background()))

	data, err := os.ReadFile(out)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "openapi: 3.0")
	assert.Contains(t, content, "/v1/todos")
	assert.Contains(t, content, "1.2.3")
}

func TestOpenAPICommandWritesStdout(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	root := NewRootCommand(Options{Out: &stdout, Build: BuildInfo{Version: "1.2.3"}})
	root.SetArgs([]string{"openapi"})

	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.Contains(t, stdout.String(), "openapi: 3.0")
}
