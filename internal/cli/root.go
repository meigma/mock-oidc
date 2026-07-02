package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/meigma/mock-oidc/internal/config"
)

// BuildInfo describes linker-injected build metadata printed by --version.
type BuildInfo struct {
	// Version is the release version.
	Version string
	// Commit is the source commit used to build the binary.
	Commit string
	// Date is the build timestamp.
	Date string
}

// Options customizes root command construction.
type Options struct {
	// In receives interactive command input.
	In io.Reader
	// Out receives machine-readable command output.
	Out io.Writer
	// Err receives diagnostics and human-readable status.
	Err io.Writer
	// Build controls the version output.
	Build BuildInfo
	// Viper is the configuration instance used by the command tree.
	Viper *viper.Viper
}

// NewRootCommand creates the mock-oidc Cobra command tree. The root runs the
// HTTP server (the same as the serve subcommand) when invoked with no
// subcommand.
func NewRootCommand(options Options) *cobra.Command {
	if options.In == nil {
		options.In = strings.NewReader("")
	}
	if options.Out == nil {
		options.Out = io.Discard
	}
	if options.Err == nil {
		options.Err = io.Discard
	}
	if options.Viper == nil {
		options.Viper = viper.New()
	}
	options.Build = options.Build.withDefaults()

	root := &cobra.Command{
		Use:           "mock-oidc",
		Short:         "Mock OAuth2/OIDC authorization server for testing",
		Long:          "mock-oidc runs a for-testing-only OIDC server that mints real, signed JWTs.",
		Version:       options.Build.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return initializeConfig(cmd, options.Viper)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd, options)
		},
	}
	root.SetVersionTemplate(versionLine(options.Build) + "\n")
	root.SetIn(options.In)
	root.SetOut(options.Out)
	root.SetErr(options.Err)
	config.RegisterFlags(root.PersistentFlags())
	root.AddCommand(newServeCommand(options))
	root.AddCommand(newVersionCommand(options))
	root.AddCommand(newOpenAPICommand(options))
	// newMigrateCommand removed: mock-oidc has no database.

	return root
}

// versionLine formats the single-line build metadata used by both the
// --version flag and the version subcommand.
func versionLine(build BuildInfo) string {
	return fmt.Sprintf("mock-oidc %s (%s) built %s", build.Version, build.Commit, build.Date)
}

func (b BuildInfo) withDefaults() BuildInfo {
	if strings.TrimSpace(b.Version) == "" {
		b.Version = "dev"
	}
	if strings.TrimSpace(b.Commit) == "" {
		b.Commit = "none"
	}
	if strings.TrimSpace(b.Date) == "" {
		b.Date = "unknown"
	}

	return b
}

func initializeConfig(cmd *cobra.Command, vp *viper.Viper) error {
	vp.SetEnvPrefix("MOCK_OIDC")
	vp.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	vp.AutomaticEnv()

	// Upstream-parity env aliases (Contract §1). AutomaticEnv only matches the
	// prefixed MOCK_OIDC_* form, so each bare upstream name is bound explicitly.
	// BindEnv checks its names in order and the first set wins, so listing the
	// prefixed name first keeps the meigma-native variable authoritative while the
	// bare upstream name stays a working alias. The port precedence is therefore
	// MOCK_OIDC_SERVER_PORT > SERVER_PORT > PORT > 8080. The server-hostname/
	// server-port and json-config keys are consumed by the Slice 1 process-config
	// and OIDC-seed layers; binding them here keeps the alias surface stable.
	_ = vp.BindEnv("server-hostname", "MOCK_OIDC_SERVER_HOSTNAME", "SERVER_HOSTNAME")
	_ = vp.BindEnv("server-port", "MOCK_OIDC_SERVER_PORT", "SERVER_PORT", "PORT")
	_ = vp.BindEnv("log-level", "MOCK_OIDC_LOG_LEVEL", "LOG_LEVEL")
	_ = vp.BindEnv("json-config", "MOCK_OIDC_JSON_CONFIG", "JSON_CONFIG")
	_ = vp.BindEnv("json-config-path", "MOCK_OIDC_JSON_CONFIG_PATH", "JSON_CONFIG_PATH")
	// LOGBACK_CONFIG is a JVM/Logback concept with no Go analog: accepted and
	// ignored as a no-op alias. It is intentionally not bound.

	if err := vp.BindPFlags(cmd.Root().PersistentFlags()); err != nil {
		return fmt.Errorf("bind persistent flags: %w", err)
	}
	if err := vp.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("bind flags: %w", err)
	}

	return nil
}
