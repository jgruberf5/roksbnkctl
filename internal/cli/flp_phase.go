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

var flpUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Install the F5 License Proxy (a cluster must exist)",
	Long: `Deploys the F5 License Proxy into the existing cluster (read from
cluster-outputs.json) in its own state (state-flp/), and writes flp-outputs.json
(root CA + endpoint) for a later ` + "`bnk up`" + ` in f5licenseproxy mode.

Refuses when no cluster exists yet.`,
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
	if !pres.Cluster {
		return errors.New("no cluster found — run `roksbnkctl cluster up` (or `roksbnkctl cluster register` for an existing cluster) first, then `roksbnkctl flp up`")
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
