package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
)

// `roksbnkctl flp ...` is the optional F5 License Proxy phase — the in-cluster
// FLP that brokers BNK licensing to F5. Runs in its own state (state-flp/),
// AFTER a cluster exists and BEFORE `bnk up` in f5licenseproxy mode. Standalone:
// the composite up/down never touches it.
var flpCmd = &cobra.Command{
	Use:   "flp",
	Short: "F5 License Proxy phase — optional in-cluster licensing proxy",
	Long: `Manage the F5 License Proxy (FLP) as an optional, independent phase on an
existing cluster: it deploys the f5-license-proxy chart (vault + postgresql +
proxy) pulling from the configured registry (Harbor mirror or FAR), and records
its root CA + service endpoint so a subsequent BNK install can license in FLP
mode. Runs in its own state (state-flp/), separate from cluster/BNK/testing/gateway.

Commands:
  roksbnkctl flp up     Install the F5 License Proxy (needs a cluster)
  roksbnkctl flp down   Remove the F5 License Proxy, leaving the other phases intact

Typical flow (FLP-licensed BNK):
  roksbnkctl cluster up          # or ` + "`cluster register`" + ` for an existing cluster
  roksbnkctl registry replicate  # mirror FAR into your registry (optional)
  roksbnkctl flp up              # install the proxy
  roksbnkctl bnk up              # with bnk.license_mode: f5licenseproxy

FLP is opt-in: without it, BNK licenses with a subscription JWT as before.`,
}

// flagFLPNodePort backs `flp up --add-node-port-access`. It is PERSISTED into the
// workspace config (bnk.flp.node_port_access), so a later `flp up` — or any
// re-apply — keeps the NodePort rather than silently destroying it because the flag
// was not repeated.
var flagFLPNodePort bool

// flagFLPNodePortCIDR backs `--node-port-source-cidr`, the consuming cluster's
// subnet, opened on the worker security group. Also persisted.
var flagFLPNodePortCIDRs []string

var flpUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Install the F5 License Proxy (a cluster must exist)",
	Long: `Deploys the F5 License Proxy into the existing cluster (read from
cluster-outputs.json) in its own state (state-flp/), and writes flp-outputs.json
(root CA + endpoint) for a later ` + "`bnk up`" + ` in f5licenseproxy mode.

Refuses when no cluster exists yet.

With --add-node-port-access the proxy is also reachable from OUTSIDE its own
cluster, so a BNK install in a DIFFERENT cluster (same VPC, or across a transit
gateway) can license through it — a "shared licensing cluster", where only the
cluster running the proxy needs egress to F5. That additionally puts the worker
node IPs in the proxy's server certificate (without them the remote cluster's
controller rejects the TLS handshake) and records an external endpoint in
flp-outputs.json. Feed that endpoint + CA to the other workspace via its
bnk.flp.external block.`,
	Args: cobra.NoArgs,
	RunE: runFLPUp,
}

var flpDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Remove the F5 License Proxy, leaving the other phases",
	Long: `Destroys only the FLP-phase resources (state-flp/), leaving cluster, BNK,
testing and gateway intact, and clears flp-outputs.json.

Exits 0 ("nothing to do") when there's no FLP state, so it's safe in a
reverse-order teardown of every phase.`,
	Args: cobra.NoArgs,
	RunE: runFLPDown,
}

func init() {
	flpUpCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt before apply")
	flpUpCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")
	flpUpCmd.Flags().BoolVar(&flagFLPNodePort, "add-node-port-access", false,
		"expose the proxy outside its cluster (NodePort + node-IP cert SANs) so a BNK install in another cluster can license through it")
	flpUpCmd.Flags().StringSliceVar(&flagFLPNodePortCIDRs, "node-port-source-cidr", nil,
		"with --add-node-port-access: open the NodePort on the worker security group to this CIDR. REPEATABLE — a multi-zone VPC has one address prefix PER ZONE, and a consuming pod scheduled in an unlisted zone is silently dropped. Pass every zone's CIDR (or a supernet covering them).")
	flpDownCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")
	flpDownCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")

	flpCmd.AddCommand(flpUpCmd, flpDownCmd)
	rootCmd.AddCommand(flpCmd)
}

// runFLPUp installs the FLP against state-flp/. Refuses when no cluster exists.
func runFLPUp(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	// Presence.Cluster only sees a cluster roksbnkctl CREATED (a managed
	// ibm_container_vpc_cluster in state-cluster/). A REGISTERED cluster has no
	// terraform state at all — just cluster-outputs.json — so checking presence
	// alone refused to install the FLP onto an adopted cluster, which is the
	// main reason to run the FLP phase in the first place. Same fallback the
	// composite `up` uses (orchestration/lifecycle.go).
	if !pres.Cluster {
		co, cerr := config.ReadClusterOutputs(cctx.WorkspaceName)
		if cerr != nil || co == nil || co.ClusterID == "" {
			return errors.New("no cluster found — run `roksbnkctl cluster up` (or `roksbnkctl cluster register` for an existing cluster) first, then `roksbnkctl flp up`")
		}
	}

	// Persist the NodePort intent, so it survives a re-apply that omits the flag.
	// Without this, a plain `flp up` after an `flp up --add-node-port-access` would
	// render node_port_access=false and terraform would DESTROY the NodePort exposure
	// (and re-issue the cert without the node IPs), silently breaking every remote
	// cluster licensing through this proxy.
	if cmd.Flags().Changed("add-node-port-access") || cmd.Flags().Changed("node-port-source-cidr") {
		if cctx.Workspace.BNK.FLP == nil {
			cctx.Workspace.BNK.FLP = &config.BNKFLPCfg{}
		}
		if cmd.Flags().Changed("add-node-port-access") {
			cctx.Workspace.BNK.FLP.NodePortAccess = flagFLPNodePort
		}
		if cmd.Flags().Changed("node-port-source-cidr") {
			cctx.Workspace.BNK.FLP.NodePortSourceCIDRs = flagFLPNodePortCIDRs
		}
		if err := config.SaveWorkspace(cctx.WorkspaceName, cctx.Workspace); err != nil {
			return fmt.Errorf("persisting FLP node-port settings: %w", err)
		}
		fmt.Fprintf(os.Stderr, "✓ FLP node-port access: %v (saved to config.yaml)\n", cctx.Workspace.BNK.FLP.NodePortAccess)
	}

	return orchestration.RunFLPUp(ctxWithCmd(cmd), lifecycleInputs())
}

// runFLPDown destroys the FLP phase (state-flp/), leaving the other phases intact.
func runFLPDown(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	if !pres.FLP {
		// No-op success, not an error: the FLP phase is opt-in (the composite
		// up/down never runs it), and bnk-forge's reverse-order teardown calls
		// every phase's `down`. "Nothing to do" is the correct outcome here.
		fmt.Fprintln(os.Stderr, "✓ No FLP phase state to destroy in this workspace — nothing to do.")
		return nil
	}
	if err := orchestration.RunFLPDown(ctxWithCmd(cmd), lifecycleInputs()); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "\n✓ F5 License Proxy destroyed. Cluster, BNK, testing and gateway phases are intact.")
	return nil
}
