package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCommand builds the version subcommand, which prints the same build
// metadata as the --version flag.
func newVersionCommand(options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), versionLine(options.Build)); err != nil {
				return fmt.Errorf("write version: %w", err)
			}

			return nil
		},
	}
}
