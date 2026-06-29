package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/meigma/template-go-api/internal/app"
)

// openAPIFilePerm is the permission used when writing the spec to a file.
const openAPIFilePerm = 0o600

// openAPICommandName is the name of the openapi subcommand.
const openAPICommandName = "openapi"

// newOpenAPICommand builds the openapi subcommand, which exports the OpenAPI
// specification without starting the server.
func newOpenAPICommand(options Options) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   openAPICommandName,
		Short: "Write the OpenAPI specification (YAML) to stdout or a file",
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := app.OpenAPIYAML(options.Build.Version)
			if err != nil {
				return fmt.Errorf("generate openapi spec: %w", err)
			}

			if output == "" || output == "-" {
				if _, err := cmd.OutOrStdout().Write(spec); err != nil {
					return fmt.Errorf("write openapi spec: %w", err)
				}

				return nil
			}

			if err := os.WriteFile(output, spec, openAPIFilePerm); err != nil {
				return fmt.Errorf("write openapi spec to %q: %w", output, err)
			}

			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "output file path; defaults to stdout")

	return cmd
}
