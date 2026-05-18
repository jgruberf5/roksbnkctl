package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// `roksbnkctl bnk ...` is the BNK-trial-phase command group: lifecycle
// for the short-lived BNK trial resources (flo, cne_instance, license)
// that sit on top of a durable cluster. Pairs with `roksbnkctl cluster
// ...` for the cluster underneath. See cluster_phase.go for the cluster
// side and PRD 06 for the design.
var bnkCmd = &cobra.Command{
	Use:   "bnk",
	Short: "BNK trial lifecycle (sits on top of a cluster)",
	Long: `Manage the BNK trial resources (flo, cne_instance, license) as a
short-lived layer on top of a shared, durable cluster.

Commands:
  roksbnkctl bnk up    Deploy the BNK trial (auto-provisions the cluster if missing)
  roksbnkctl bnk down  Destroy the BNK trial, leaving the cluster in place

Use ` + "`roksbnkctl cluster ...`" + ` to manage the cluster phase itself.`,
}

var bnkUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Deploy the BNK trial; provisions the cluster first if missing",
	Long: `Provisions the BNK trial against the existing cluster phase. If
the workspace has no cluster registered yet, ` + "`bnk up`" + ` bootstraps the
cluster phase first (with a confirmation prompt — the cluster
provision takes ~30 min) before the trial apply.

Refuses on legacy single-state workspaces (those provisioned with
v1.0.x ` + "`roksbnkctl up`" + `): cluster + trial share one state file there,
so the trial can't be applied in isolation without state migration.
Use ` + "`roksbnkctl up`" + ` on those workspaces.`,
	RunE: runBnkUp,
}

var bnkDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy the BNK trial, leaving the cluster in place",
	Long: `Destroys only the BNK trial resources, leaving the cluster phase
intact for the next ` + "`bnk up`" + ` to attach to. The common iteration loop:

  roksbnkctl bnk down && roksbnkctl bnk up   # ~5 min trial reset
  roksbnkctl down                            # full teardown, ~30 min

Refuses when there's no trial state to destroy, and on legacy
single-state workspaces (use ` + "`roksbnkctl down`" + ` there).`,
	RunE: runBnkDown,
}

func init() {
	// bnk up/down share the same flag knobs as the unscoped lifecycle
	// commands and the cluster phase so users only carry one mental
	// model across the three command surfaces.
	bnkUpCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip confirmation prompts (cluster-bootstrap + apply)")
	bnkUpCmd.Flags().BoolVar(&flagNoKubeconfig, "no-kubeconfig", false, "skip the post-apply admin kubeconfig fetch")
	bnkUpCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")
	bnkDownCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")
	bnkDownCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")

	bnkCmd.AddCommand(bnkUpCmd, bnkDownCmd)
	rootCmd.AddCommand(bnkCmd)
}

// runBnkUp deploys the BNK trial against the trial state dir. Dispatch
// per PRD 06 §"Dispatch table":
//
//   - LegacySingle → refuse (cluster + trial share one state).
//   - Empty        → bootstrap the cluster phase first (prompt unless
//     --auto), then apply the trial.
//   - ClusterOnly  → trial up directly (cluster already provisioned).
//   - Split        → trial up directly (refresh; cluster phase is
//     already where it should be).
func runBnkUp(cmd *cobra.Command, _ []string) error {
	if err := rejectOnFlag("bnk up"); err != nil {
		return err
	}
	// Resolve --var-file against the invocation CWD before we hand off
	// to runClusterUp / runTrialUp (each of which also resolves
	// defensively — idempotent on already-absolute inputs).
	resolved, err := resolveVarFiles(flagVarFiles)
	if err != nil {
		return err
	}
	flagVarFiles = resolved

	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	shape, err := config.DetectShape(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace shape: %w", err)
	}
	if shape == config.ShapeLegacySingle {
		return errors.New("this workspace is legacy single-state; `bnk up` can't isolate the trial phase. Use `roksbnkctl up` for in-place behavior, or migrate the state first")
	}

	if shape == config.ShapeEmpty {
		fmt.Fprintln(os.Stderr, "No cluster registered for this workspace.")
		fmt.Fprintln(os.Stderr, "→ Provisioning the cluster phase first (ROKS cluster + transit gateway + registry COS + cert-manager + jumphost; ~30 min) before the BNK trial.")
		if !flagAuto && !promptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
		if err := runClusterUp(cmd, nil); err != nil {
			return err
		}
	}

	return runTrialUp(cmd, nil)
}

// runBnkDown destroys the BNK trial against the trial state dir,
// leaving the cluster phase in place. Dispatch per PRD 06 §"Dispatch
// table":
//
//   - LegacySingle      → refuse (shared state; use `roksbnkctl down`).
//   - Empty/ClusterOnly → refuse (no trial state to destroy).
//   - Split             → trial down.
func runBnkDown(cmd *cobra.Command, _ []string) error {
	if err := rejectOnFlag("bnk down"); err != nil {
		return err
	}
	resolved, err := resolveVarFiles(flagVarFiles)
	if err != nil {
		return err
	}
	flagVarFiles = resolved

	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	shape, err := config.DetectShape(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace shape: %w", err)
	}
	switch shape {
	case config.ShapeLegacySingle:
		return errors.New("this workspace is legacy single-state; `bnk down` can't isolate the trial phase. Use `roksbnkctl down` to tear down both, or migrate the state first")
	case config.ShapeEmpty, config.ShapeClusterOnly:
		return errors.New("no BNK trial state to destroy in this workspace")
	}
	if err := runTrialDown(cmd, nil); err != nil {
		return err
	}
	// Reassurance footer — chapter 10's `bnk down` sample documents this
	// exact shape. The user's main worry on a `bnk down` is "did I just
	// lose my cluster too?" — naming the persisting state-cluster path
	// answers that explicitly and points at the next-step verb.
	clusterDir, err := config.WorkspaceClusterStateDir(cctx.WorkspaceName)
	if err == nil {
		fmt.Fprintf(os.Stderr, "\n✓ Trial phase destroyed. Cluster phase %s/ is intact.\n", clusterDir)
		fmt.Fprintln(os.Stderr, "  Run `roksbnkctl bnk up` to deploy another trial against the same cluster.")
	}
	return nil
}
