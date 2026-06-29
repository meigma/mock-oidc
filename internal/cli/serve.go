package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/meigma/template-go-api/internal/app"
	"github.com/meigma/template-go-api/internal/config"
	"github.com/meigma/template-go-api/internal/observability"
)

// newServeCommand builds the serve subcommand, which runs the HTTP API server.
func newServeCommand(options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServe(cmd, options)
		},
	}
}

// runServe loads configuration, builds the logger, and runs the server until
// the command's context is cancelled (for example, on SIGINT or SIGTERM).
func runServe(cmd *cobra.Command, options Options) error {
	cfg := config.Load(options.Viper)
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	logger := observability.NewLogger(options.Err, observability.ParseLevel(cfg.LogLevel), cfg.LogFormat)

	application, err := app.New(cmd.Context(), cfg, logger, options.Build.Version)
	if err != nil {
		return fmt.Errorf("build application: %w", err)
	}

	if err := application.Run(cmd.Context()); err != nil {
		return fmt.Errorf("run server: %w", err)
	}

	return nil
}
