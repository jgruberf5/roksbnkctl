package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
)

// `roksbnkctl testing ...` is the Testing-phase command group (Sprint 28
// three-phase split): the jumphost infrastructure (TGW jumphost, per-AZ
// cluster jumphosts, client VPC) as a phase parallel to `cluster`/`bnk`,
// running in its own state (state-testing/), pure IBM VPC (no k8s).
//
// NOTE — `testing` (this group) provisions the jumphosts; `test` /
// `test hosts` RUN connectivity/DNS/throughput probes against an already-
// deployed environment and provision nothing. See the architect's
// Sprint 28 §4 disambiguation.
var testingCmd = &cobra.Command{
	Use:   "testing",
	Short: "Testing-phase (jumphost) lifecycle — provisions the test rig",
	Long: `Manage the testing jumphost infrastructure (TGW jumphost, per-AZ
cluster jumphosts, client VPC) as an independent phase that sits beside the
BNK phase on top of a shared cluster. Pure IBM VPC — no Kubernetes.

Commands:
  roksbnkctl testing up       Provision the jumphosts (needs a cluster: VPC + transit gateway)
  roksbnkctl testing down     Destroy the jumphosts, leaving the cluster and BNK intact
  roksbnkctl testing status   Live jumphost IPs / SSH commands + reachability

This is the provisioning phase. To RUN connectivity/DNS/throughput probes
against a deployed environment, use ` + "`roksbnkctl test`" + ` (and
` + "`roksbnkctl test hosts`" + ` to manage the probe target list) — those
provision nothing.`,
}

var testingUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Provision the testing jumphosts (cluster must exist)",
	Long: `Provisions the testing jumphosts against the existing cluster's VPC +
transit gateway (read from cluster-outputs.json). Runs in its own state
(state-testing/), independent of the BNK phase — ` + "`bnk down`" + ` leaves
the jumphosts and ` + "`testing down`" + ` leaves BNK.`,
	Args: cobra.NoArgs,
	RunE: runTestingUp,
}

var testingDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy the testing jumphosts, leaving the cluster and BNK",
	Long: `Destroys only the testing jumphost resources (state-testing/), leaving
the cluster phase and the BNK phase intact. The inverse of ` + "`bnk down`" + `:
each phase tears down independently. Refuses when there's no testing state.`,
	Args: cobra.NoArgs,
	RunE: runTestingDown,
}

func init() {
	testingUpCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt before apply")
	testingUpCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")
	testingDownCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")
	testingDownCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")

	testingCmd.AddCommand(testingUpCmd, testingDownCmd)
	rootCmd.AddCommand(testingCmd)
}

// runTestingUp deploys the testing jumphosts against state-testing/.
func runTestingUp(cmd *cobra.Command, _ []string) error {
	return orchestration.RunTestingUp(ctxWithCmd(cmd), lifecycleInputs())
}

// runTestingDown destroys the testing jumphosts (state-testing/), leaving
// the cluster + BNK phases intact.
func runTestingDown(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	if !pres.Testing {
		return errors.New("no testing jumphost state to destroy in this workspace")
	}
	if err := orchestration.RunTestingDown(ctxWithCmd(cmd), lifecycleInputs()); err != nil {
		return err
	}
	clusterDir, derr := config.WorkspaceClusterStateDir(cctx.WorkspaceName)
	if derr == nil {
		fmt.Fprintf(os.Stderr, "\n✓ Testing jumphosts destroyed. Cluster phase %s/ and the BNK phase are intact.\n", clusterDir)
	}
	return nil
}
