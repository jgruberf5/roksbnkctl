package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/topology"
)

var (
	flagTopologyConfig string
	flagTopologyFormat string
)

var topologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Render the cluster data-path topology (VPC, TMM VLANs, jumphost, gateways)",
	Long: `awsbnkctl topology renders the whole-cluster data-path topology in one view:

  VPC + subnets, node group + TMM node, TMM external/internal VLANs + self-IPs,
  CSRC egress overlay, jumphost + EICE, GatewayClass + VIP range.

Works OFFLINE from cluster.yaml (intent) + state.env — no AWS or Kubernetes
API calls required. Fields not yet provisioned show as "(not provisioned)".

Formats:
  ascii    (default) ASCII box-drawing diagram
  mermaid  Mermaid graph TD for embedding in docs or forge`,
	RunE: runTopologyCmd,
}

func init() {
	topologyCmd.Flags().StringVarP(&flagTopologyConfig, "config", "f", "", "path to cluster.yaml (required)")
	topologyCmd.Flags().StringVar(&flagTopologyFormat, "format", "ascii", "output format: ascii or mermaid")
	_ = topologyCmd.MarkFlagRequired("config")
	rootCmd.AddCommand(topologyCmd)
}

func runTopologyCmd(_ *cobra.Command, _ []string) error {
	switch flagTopologyFormat {
	case "ascii", "mermaid":
		// valid
	default:
		return fmt.Errorf("unknown --format %q: must be ascii or mermaid", flagTopologyFormat)
	}

	cl, err := intent.Load(flagTopologyConfig)
	if err != nil {
		return fmt.Errorf("loading cluster.yaml: %w", err)
	}

	// Load state — a missing or empty state dir is expected pre-provisioning.
	// Proceed with an empty state so topology renders from intent alone.
	st, err := state.Load(cl.StateDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "[topology] warning: could not load state.env (%v); rendering from intent only\n", err)
		st, _ = state.Load(os.TempDir()) // guaranteed empty state
	}

	m := topology.Build(cl, st)

	var out string
	switch flagTopologyFormat {
	case "mermaid":
		out = topology.RenderMermaid(m)
	default:
		out = topology.RenderASCII(m)
	}

	fmt.Println(out)
	return nil
}
