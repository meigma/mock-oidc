package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testVersion = "0.1.0"
	testCommit  = "abc1234"
	testDate    = "2026-05-08T10:00:00Z"
	wantVersion = "template-go-api 0.1.0 (abc1234) built 2026-05-08T10:00:00Z\n"
)

func testBuild() BuildInfo {
	return BuildInfo{Version: testVersion, Commit: testCommit, Date: testDate}
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	root := NewRootCommand(Options{Out: &stdout, Err: &stderr, Build: testBuild()})
	root.SetArgs([]string{"--version"})

	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.Equal(t, wantVersion, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVersionSubcommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	root := NewRootCommand(Options{Out: &stdout, Build: testBuild()})
	root.SetArgs([]string{"version"})

	require.NoError(t, root.ExecuteContext(context.Background()))
	assert.Equal(t, wantVersion, stdout.String())
}

func TestRootHasSubcommands(t *testing.T) {
	t.Parallel()

	root := NewRootCommand(Options{Build: testBuild()})

	names := make(map[string]bool)
	for _, cmd := range root.Commands() {
		names[cmd.Name()] = true
	}

	assert.True(t, names["serve"])
	assert.True(t, names["version"])
	assert.True(t, names["openapi"])
}
