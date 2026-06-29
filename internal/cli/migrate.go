package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/meigma/template-go-api/internal/adapter/postgres"
	"github.com/meigma/template-go-api/internal/config"
)

// newMigrateCommand builds the migrate subcommand and its up/down/status verbs,
// which apply, roll back, or report the embedded goose migrations against the
// configured --database-url. Migrations are explicit and never run by serve.
func newMigrateCommand(options Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply, roll back, or report database migrations",
		Long: "migrate runs the embedded goose migrations against the PostgreSQL " +
			"database named by --database-url. It is not run automatically by serve.",
	}

	cmd.AddCommand(
		newMigrateVerbCommand(options, "up", "Apply all pending migrations"),
		newMigrateVerbCommand(options, "down", "Roll back the most recent migration"),
		newMigrateVerbCommand(options, "status", "Print the migration status of each version"),
	)

	return cmd
}

// newMigrateVerbCommand builds a single goose verb subcommand that loads the
// database URL from configuration and runs the migration command.
func newMigrateVerbCommand(options Options, verb, short string) *cobra.Command {
	return &cobra.Command{
		Use:   verb,
		Short: short,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrate(cmd, options, verb)
		},
	}
}

// runMigrate resolves the database URL from configuration and executes the goose
// command against the embedded migrations.
func runMigrate(cmd *cobra.Command, options Options, verb string) error {
	cfg := config.Load(options.Viper)
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return fmt.Errorf("database-url is required for migrate %s", verb)
	}

	if err := postgres.Migrate(cmd.Context(), cfg.DatabaseURL, verb); err != nil {
		return fmt.Errorf("migrate %s: %w", verb, err)
	}

	return nil
}
