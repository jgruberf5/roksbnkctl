package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
)

// `roksbnkctl gateway ...` is the optional Gateway-phase command group — the
// BNK data-plane ingress/egress config (Gateway API + SnatPool + Egress +
// static routes + the VXLAN security-group rule), applied AFTER a healthy BNK
// in its own state (state-gateway/). Standalone: the composite up/down never
// touches it.
var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "Gateway-phase (data-plane config) lifecycle — optional ingress/egress setup",
	Long: `Manage the BNK data-plane configuration as an optional, independent phase
on top of a healthy BNK deployment: the Gateway API objects (GatewayClass,
F5BnkGateway, Gateway, HTTPRoute), the egress SnatPool + Egress CRs, the
per-zone static routes, and the cluster security-group VXLAN rule. Runs in its
own state (state-gateway/), separate from cluster/BNK/testing.

Commands:
  roksbnkctl gateway up     Apply the data-plane config (needs a healthy BNK)
  roksbnkctl gateway down   Remove the data-plane config, leaving cluster/BNK/testing intact

Run this only after ` + "`roksbnkctl up`" + ` has brought up the cluster + BNK and
the BNK is healthy (TMM pods Ready). The composite ` + "`up`/`down`" + ` never runs
this phase — it is opt-in.`,
}

var gatewayUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Apply the BNK data-plane config (BNK must be healthy)",
	Long: `Applies the Gateway API + SnatPool + Egress + static-route CRs and the
VXLAN security-group rule against the existing cluster + BNK (read from
cluster-outputs.json). Runs in its own state (state-gateway/).

Refuses when no cluster/BNK exists yet.`,
	Args: cobra.NoArgs,
	RunE: runGatewayUp,
}

var gatewayDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Remove the BNK data-plane config, leaving cluster/BNK/testing",
	Long: `Destroys only the Gateway-phase resources (state-gateway/), leaving the
cluster, BNK and testing phases intact.

Exits 0 ("nothing to do") when there's no gateway state, so it's safe in a
reverse-order teardown of every phase.`,
	Args: cobra.NoArgs,
	RunE: runGatewayDown,
}

func init() {
	gatewayUpCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt before apply")
	gatewayUpCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")
	gatewayDownCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")
	gatewayDownCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")

	gatewayCmd.AddCommand(gatewayUpCmd, gatewayDownCmd)
	rootCmd.AddCommand(gatewayCmd)
}

// runGatewayUp applies the data-plane config against state-gateway/. Refuses
// when no cluster/BNK is present yet.
func runGatewayUp(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	if !pres.Cluster && !pres.BNK {
		return errors.New("no cluster/BNK found — run `roksbnkctl up` (the cluster + BNK phases) first, then `roksbnkctl gateway up`")
	}
	return orchestration.RunGatewayUp(ctxWithCmd(cmd), lifecycleInputs())
}

// runGatewayDown destroys the gateway phase (state-gateway/), leaving the
// cluster + BNK + testing phases intact.
func runGatewayDown(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	if !pres.Gateway {
		// No-op success, not an error: the gateway phase is opt-in (the
		// composite up/down never runs it), and bnk-forge's reverse-order
		// teardown calls every phase's `down`. A non-zero exit here would
		// block the cluster from being destroyed. "Nothing to do" is the
		// correct outcome for an absent phase.
		fmt.Fprintln(os.Stderr, "✓ No gateway phase state to destroy in this workspace — nothing to do.")
		return nil
	}
	if err := orchestration.RunGatewayDown(ctxWithCmd(cmd), lifecycleInputs()); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "\n✓ Gateway data-plane config destroyed. Cluster, BNK and testing phases are intact.")
	return nil
}
