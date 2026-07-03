package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meigma/mock-oidc/internal/config"
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

// bindViaRoot runs the root command's config init (PersistentPreRunE →
// initializeConfig) so the env-parity BindEnv calls are applied, then returns the
// viper instance for Load.
func bindViaRoot(t *testing.T) *viper.Viper {
	t.Helper()
	vp := viper.New()
	root := NewRootCommand(Options{Build: testBuild(), Viper: vp})
	root.SetArgs([]string{"version"})
	require.NoError(t, root.ExecuteContext(context.Background()))
	return vp
}

// TestParityServerPortResolvesAddr verifies the SERVER_PORT > PORT > 8080
// precedence resolves the listen address through the bound env aliases.
func TestParityServerPortResolvesAddr(t *testing.T) {
	t.Setenv("SERVER_PORT", "9123")
	t.Setenv("PORT", "7000")

	cfg := config.Load(bindViaRoot(t))
	assert.Equal(t, ":9123", cfg.Addr, "SERVER_PORT wins over PORT and composes the listen addr")
}

// TestParityPortFallback verifies PORT resolves the listen address when
// SERVER_PORT is absent.
func TestParityPortFallback(t *testing.T) {
	t.Setenv("PORT", "7000")

	cfg := config.Load(bindViaRoot(t))
	assert.Equal(t, ":7000", cfg.Addr, "PORT composes the listen addr when SERVER_PORT is unset")
}

// TestParityDefaultPort verifies the zero-env default remains :8080.
func TestParityDefaultPort(t *testing.T) {
	cfg := config.Load(bindViaRoot(t))
	assert.Equal(t, ":8080", cfg.Addr, "no port env falls back to :8080")
}
