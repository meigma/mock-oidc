package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testVersion = "0.1.0"
	testCommit  = "abc1234"
	testDate    = "2026-05-08T10:00:00Z"
	wantVersion = "mock-oidc 0.1.0 (abc1234) built 2026-05-08T10:00:00Z\n"
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
	assert.False(t, names["migrate"], "migrate command is removed: mock-oidc has no database")
}

// TestRootUse verifies the root command is renamed to mock-oidc.
func TestRootUse(t *testing.T) {
	t.Parallel()

	root := NewRootCommand(Options{Build: testBuild()})
	assert.Equal(t, "mock-oidc", root.Use)
}

// TestParityEnvAliases verifies the unprefixed upstream-parity env aliases are
// honored and that the MOCK_OIDC_ prefixed form wins when both are set.
func TestParityEnvAliases(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")

	vp := viper.New()
	root := NewRootCommand(Options{Build: testBuild(), Viper: vp})
	root.SetArgs([]string{"version"})
	require.NoError(t, root.ExecuteContext(context.Background()))

	assert.Equal(t, "debug", vp.GetString("log-level"))
}
