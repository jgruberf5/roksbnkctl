package cli

import (
	"fmt"
	"io/fs"

	roksbnkctl "github.com/jgruberf5/roksbnkctl"
	"github.com/spf13/cobra"
)

// exampleTFVarsPath is the bundled root example tfvars `init example` prints —
// the same annotated template that ships in the binary's embedded terraform.
const exampleTFVarsPath = "terraform/terraform.tfvars.example"

var initExampleCmd = &cobra.Command{
	Use:   "example",
	Short: "Print the example terraform.tfvars to stdout (a template, or for piping)",
	Long: `Writes the bundled terraform.tfvars.example to stdout — the annotated template
of the available terraform variables. It reads from the binary's embedded
terraform, so it works from any directory and matches the binary's version.

Create a starting tfvars or inspect the knobs with ordinary pipes:

  roksbnkctl init example > terraform.tfvars.user
  roksbnkctl init example | grep -E 'far_repo_url|manifest_version'`,
	Args: cobra.NoArgs,
	RunE: runInitExample,
}

func init() {
	initCmd.AddCommand(initExampleCmd)
}

func runInitExample(cmd *cobra.Command, _ []string) error {
	body, err := fs.ReadFile(roksbnkctl.EmbeddedTerraform, exampleTFVarsPath)
	if err != nil {
		return fmt.Errorf("reading embedded %s: %w", exampleTFVarsPath, err)
	}
	_, err = cmd.OutOrStdout().Write(body)
	return err
}
