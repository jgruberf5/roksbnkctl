package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
)

// `roksbnkctl tgw ...` attaches the workspace's cluster VPC to an EXISTING
// Transit Gateway, so multiple clusters can share one gateway. Its own phase
// (state-tgw/), reusing the cluster from cluster-outputs.json — so it works for a
// cluster roksbnkctl created OR one you registered. Standalone: the composite
// up/down never touches it (though `cluster up` auto-connects when the config
// names an existing gateway).
var tgwCmd = &cobra.Command{
	Use:   "tgw",
	Short: "Attach the cluster to an existing Transit Gateway (share one across clusters)",
	Long: `Connect this workspace's cluster VPC to an EXISTING IBM Transit Gateway,
by name or id — at create time or after. Multiple workspaces can point at the
same gateway; each owns its own connection.

Commands:
  roksbnkctl tgw connect <name-or-id>   Attach the cluster VPC to the gateway
  roksbnkctl tgw disconnect             Detach (leaves the gateway + other clusters)
  roksbnkctl tgw status                 Gateway id/name + live connection state

Runs in its own state (state-tgw/), separate from cluster/BNK/testing/gateway/FLP,
and works against a created OR a registered cluster.`,
}

var tgwConnectCmd = &cobra.Command{
	Use:   "connect [transit-gateway-name-or-id]",
	Short: "Attach the cluster VPC to an existing Transit Gateway",
	Long: `Attaches this workspace's cluster VPC to an existing Transit Gateway.

The gateway may be given by NAME or by ID — either is resolved against your
account. Pass it as the argument (persisted to config for later re-applies), or
set resources.transit_gateway (create: false, existing: <name-or-id>) in
config.yaml / the init interview.

Idempotent and shareable: run it in several workspaces with the same gateway and
each cluster gets its own connection to the one gateway.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTGWConnect,
}

var tgwDisconnectCmd = &cobra.Command{
	Use:   "disconnect",
	Short: "Detach the cluster VPC from its Transit Gateway",
	Long: `Removes ONLY this cluster's connection to the Transit Gateway. The
gateway itself and every other cluster's connection to it are left intact.

Exits 0 ("nothing to do") when there's no connection.`,
	Args: cobra.NoArgs,
	RunE: runTGWDisconnect,
}

var tgwStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the Transit Gateway id/name and the live connection state",
	Args:  cobra.NoArgs,
	RunE:  runTGWStatus,
}

func init() {
	tgwConnectCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt before apply")
	tgwConnectCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")
	tgwDisconnectCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")
	tgwDisconnectCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")

	tgwCmd.AddCommand(tgwConnectCmd, tgwDisconnectCmd, tgwStatusCmd)
	rootCmd.AddCommand(tgwCmd)
}

func runTGWConnect(cmd *cobra.Command, args []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}

	// A cluster must exist — created (state-cluster/) OR registered
	// (cluster-outputs.json only). The connection needs the cluster VPC id,
	// which cluster-outputs.json records either way.
	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	if !pres.Cluster {
		co, cerr := config.ReadClusterOutputs(cctx.WorkspaceName)
		if cerr != nil || co == nil || co.VPCID == "" {
			return errors.New("no cluster found — run `roksbnkctl cluster up` (or `roksbnkctl cluster register`) first, then `roksbnkctl tgw connect <name-or-id>`")
		}
	}

	// Persist the target so a later re-apply (or `cluster up` auto-connect) keeps
	// it. A bare `connect` with no arg reuses whatever config already carries.
	if len(args) == 1 && args[0] != "" {
		if cctx.Workspace.Resources == nil {
			cctx.Workspace.Resources = config.DefaultResources()
		}
		cctx.Workspace.Resources.TransitGateway = config.ResourceToggle{Create: false, Existing: args[0]}
		if err := config.SaveWorkspace(cctx.WorkspaceName, cctx.Workspace); err != nil {
			return fmt.Errorf("persisting transit gateway target: %w", err)
		}
		fmt.Fprintf(os.Stderr, "✓ Transit Gateway target: %s (saved to config.yaml)\n", args[0])
	}

	return orchestration.RunTGWConnect(ctxWithCmd(cmd), lifecycleInputs())
}

func runTGWDisconnect(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	if !pres.TGW {
		fmt.Fprintln(os.Stderr, "✓ No Transit Gateway connection in this workspace — nothing to do.")
		return nil
	}
	return orchestration.RunTGWDisconnect(ctxWithCmd(cmd), lifecycleInputs())
}

func runTGWStatus(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	out, err := config.ReadTGWOutputs(cctx.WorkspaceName)
	if err != nil {
		if errors.Is(err, config.ErrTGWOutputsMissing) {
			fmt.Fprintln(os.Stderr, "transit gateway: not connected — run `roksbnkctl tgw connect <name-or-id>`")
			return nil
		}
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "transit_gateway_id:\t%s\n", out.GatewayID)
	fmt.Fprintf(tw, "transit_gateway_name:\t%s\n", out.GatewayName)
	fmt.Fprintf(tw, "connection_name:\t%s\n", out.ConnectionName)
	fmt.Fprintf(tw, "connection_id:\t%s\n", out.ConnectionID)
	fmt.Fprintf(tw, "cluster_vpc:\t%s\n", out.VPCID)

	// Live state — best-effort, so `tgw status` still shows the recorded identity
	// when the account is unreachable.
	state := liveTGWConnectionState(ctxWithCmd(cmd), out)
	fmt.Fprintf(tw, "connection_state:\t%s\n", state)
	return tw.Flush()
}

// liveTGWConnectionState queries IBM for the current attachment state of this
// cluster's VPC on the gateway, matching on the VPC CRN recorded at connect time.
// Returns a human string, never an error — a failed lookup degrades to
// "(unknown: <reason>)" so status still renders the recorded identity.
func liveTGWConnectionState(ctx context.Context, out *config.TGWOutputs) string {
	if out.GatewayID == "" || out.VPCCRN == "" {
		return "(unknown)"
	}
	// Deliberately does NOT interpolate the openIBMClient error: it comes from
	// credential resolution (which reads IBMCloud.APIKeySource), and echoing that
	// into a status line is exactly the clear-text-logging pattern to avoid.
	_, ic, err := openIBMClient()
	if err != nil {
		return "(unknown: no IBM credentials configured)"
	}
	state, err := ic.TGWConnectionState(ctx, out.GatewayID, out.VPCCRN)
	if err != nil {
		return "(unknown: could not reach the Transit Gateway API)"
	}
	if state == "" {
		return "detached"
	}
	return state
}
