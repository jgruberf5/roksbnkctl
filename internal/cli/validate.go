package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// validateCmd: cheap, AWS-free check that a cluster.yaml parses and
// passes our strict schema (unknown-field rejection, regex on name,
// non-empty AZs / subnets, etc.). Useful in a tight edit loop without
// burning an SSO session on `up --dry-run`.
//
// Pattern borrowed from mwiget/kindbnkctl (which has `validate poc.yaml`).
// Captured as ADR D-008 — validate-without-AWS subcommand.
var validateCmd = &cobra.Command{
	Use:   "validate <path>",
	Short: "Parse and validate a cluster.yaml (no AWS API calls)",
	Long: `awsbnkctl validate reads the given cluster.yaml, applies the
strict schema (unknown fields are errors, metadata.name must match the
EKS regex, network.azs and subnets must be non-empty, ...) and exits
non-zero on any validation failure.

It makes no AWS API calls — useful for tightening the edit loop on a
cluster.yaml without an SSO session, or as a CI gate for cluster.yaml
files committed under examples/.

Example:
  awsbnkctl validate examples/full-cluster/cluster.yaml`,
	Args: cobra.ExactArgs(1),
	RunE: runValidate,
}

func runValidate(_ *cobra.Command, args []string) error {
	path := args[0]
	c, err := intent.Load(path)
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ %s parses cleanly\n", path)
	fmt.Fprintf(os.Stderr, "  apiVersion=%s kind=%s name=%s region=%s\n",
		c.APIVersion, c.Kind, c.Metadata.Name, c.Metadata.Region)
	fmt.Fprintf(os.Stderr, "  network: vpc=%s azs=%d public-subnets=%d private-subnets=%d\n",
		c.Network.VPCCidr, len(c.Network.AZs),
		len(c.Network.Subnets.Public), len(c.Network.Subnets.Private))
	if c.Forge != nil {
		fmt.Fprintf(os.Stderr, "  forge: enabled=%t url=%s mcpUrl=%s\n",
			c.Forge.Enabled, c.Forge.URL, c.Forge.MCPURL)
	}
	if c.BigIPVE != nil {
		fmt.Fprintf(os.Stderr, "  bigipVE: enabled=%t instanceType=%s vip=%s tier=%s\n",
			c.BigIPVE.Enabled, c.BigIPVE.InstanceType, c.BigIPVE.VIP, c.BigIPVE.LicenseTier)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(validateCmd)
}
