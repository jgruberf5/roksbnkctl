package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
)

// apikeyCmd prints the workspace's resolved IBM Cloud API key — the scripting
// seam so external tooling (e.g. scripts/deploy-artifactory.sh) can reuse the
// SAME credential roksbnkctl uses, without re-implementing the resolver chain.
var apikeyCmd = &cobra.Command{
	Use:   "apikey",
	Short: "Print the workspace's resolved IBM Cloud API key to stdout (a secret)",
	Long: `Resolve the workspace's IBM Cloud API key via the standard chain and print it
to stdout — the scripting seam for driving the ibmcloud CLI off a workspace:

  ibmcloud login --apikey "$(roksbnkctl -w <ws> apikey)"

Resolution order (the same roksbnkctl itself uses):
  1. environment — IBMCLOUD_API_KEY / IC_API_KEY / TF_VAR_* (including any
     loaded from $PWD/.env, which roksbnkctl reads at startup)
  2. OS keychain  (service "roksbnkctl", account "<workspace>/ibmcloud_api_key")
  3. workspace config.yaml — ibmcloud.api_key_b64 (base64-decoded)

Non-interactive: it never prompts — if no key is found it exits non-zero with an
actionable error.

WARNING: this writes the credential to stdout. Pipe or redirect it; never echo it
into logs or CI output.`,
	Args: cobra.NoArgs,
	RunE: runAPIKey,
}

func runAPIKey(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	resolver := &cred.Resolver{Workspace: cctx.WorkspaceName, NonInteractive: true}
	key, err := resolver.IBMCloudAPIKey(cmd.Context())
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), key)
	return nil
}

func init() {
	rootCmd.AddCommand(apikeyCmd)
}
