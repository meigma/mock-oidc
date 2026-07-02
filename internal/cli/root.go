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

// NewRootCommand creates the template-go-api Cobra command tree. The root runs
// the HTTP server (the same as the serve subcommand) when invoked with no
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
		Use:           "template-go-api",
		Short:         "Meigma Go web API server template",
		Long:          "template-go-api runs a small HTTP API server built on chi and Huma.",
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
	root.AddCommand(newMigrateCommand(options))

	return root
}

// versionLine formats the single-line build metadata used by both the
// --version flag and the version subcommand.
func versionLine(build BuildInfo) string {
	return fmt.Sprintf("template-go-api %s (%s) built %s", build.Version, build.Commit, build.Date)
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
	vp.SetEnvPrefix("TEMPLATE_GO_API")
	vp.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	vp.AutomaticEnv()

	if err := vp.BindPFlags(cmd.Root().PersistentFlags()); err != nil {
		return fmt.Errorf("bind persistent flags: %w", err)
	}
	if err := vp.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("bind flags: %w", err)
	}

	return nil
}
