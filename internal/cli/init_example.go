package cli

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

// exampleConfigYAML is the annotated config.yaml template `init example` prints —
// the canonical declarative input, with the required fields filled and every
// optional axis documented. Embedded so it works from any directory and ships
// with the binary.
//
//go:embed config.example.yaml
var exampleConfigYAML []byte

var initExampleCmd = &cobra.Command{
	Use:   "example",
	Short: "Print an annotated example config.yaml to stdout (a template, or for piping)",
	Long: `Writes an annotated config.yaml to stdout — the canonical declarative input,
with the required fields filled and every optional axis documented (cluster
create-or-attach, BYO infrastructure reuse, BNK install, gateway, registry
mirror, remote state). config.yaml is the single input to a workspace; the
embedded terraform is internal.

Create a starting config or inspect the knobs with ordinary pipes:

  roksbnkctl init example > config.yaml
  roksbnkctl -w demo init --config-file config.yaml
  roksbnkctl init example | grep -E 'manifest_version|cluster_vpc'`,
	Args: cobra.NoArgs,
	RunE: runInitExample,
}

func init() {
	initCmd.AddCommand(initExampleCmd)
}

func runInitExample(cmd *cobra.Command, _ []string) error {
	if _, err := cmd.OutOrStdout().Write(exampleConfigYAML); err != nil {
		return fmt.Errorf("writing example config.yaml: %w", err)
	}
	return nil
}
