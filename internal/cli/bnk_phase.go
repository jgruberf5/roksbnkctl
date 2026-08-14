package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
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
provision takes ~30 min) before the trial apply.`,
	Args: cobra.NoArgs,
	RunE: runBnkUp,
}

var bnkDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy the BNK trial, leaving the cluster in place",
	Long: `Destroys only the BNK trial resources, leaving the cluster phase
intact for the next ` + "`bnk up`" + ` to attach to. The common iteration loop:

  roksbnkctl bnk down && roksbnkctl bnk up   # ~5 min trial reset
  roksbnkctl down                            # full teardown, ~30 min

Exits 0 ("nothing to do") when there's no trial state (cluster-only or
empty), so it's safe in a reverse-order teardown of every phase. Refuses
on legacy single-state workspaces (use ` + "`roksbnkctl down`" + ` there).`,
	Args: cobra.NoArgs,
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
	// flagVarFiles is already chokepoint-normalized (root
	// PersistentPreRunE → resolveInvocationContext); no per-RunE
	// re-derivation (Sprint 12 Issue 1, retired as a class).
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	// Trust a co-located private mirror's CA for the terraform/helm chart pulls this
	// install runs — a no-op unless `registry replicate` recorded a private CA. Lets
	// `bnk up` work from a container operator with no OS trust for the mirror.
	ensureMirrorCATrust(cctx.WorkspaceName)
	shape, err := config.DetectShape(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace shape: %w", err)
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

// bnkTargetClusterGone reports whether the cluster this workspace targets is
// definitively absent, and its recorded name.
//
// Conservative by construction: it answers true ONLY for an explicit
// ErrClusterNotFound. No recorded cluster, an unreadable record, a client that
// will not build, or any other lookup error all answer false, so the destroy
// proceeds exactly as it did before. The cost of a false negative is the status
// quo; the cost of a false positive is a skipped uninstall.
func bnkTargetClusterGone(cmd *cobra.Command, cctx *config.Context) (bool, string) {
	out, err := config.ReadClusterOutputs(cctx.WorkspaceName)
	if err != nil || out == nil || out.ClusterID == "" {
		return false, ""
	}
	_, ic, err := openIBMClient()
	if err != nil {
		return false, ""
	}
	_, lookupErr := ic.GetCluster(cmd.Context(), out.ClusterID)
	return clusterGoneFromLookup(out, lookupErr)
}

// clusterGoneFromLookup is the decision, split out from the I/O so it can be
// tested directly rather than re-implemented in a test — which would pass no
// matter what the production path did.
//
// True ONLY for an explicit ErrClusterNotFound. Every other error, including a
// timeout or a refused connection, means "cannot tell", and cannot-tell must
// behave exactly as before: proceed with the destroy.
func clusterGoneFromLookup(out *config.ClusterOutputs, lookupErr error) (bool, string) {
	if out == nil || out.ClusterID == "" || lookupErr == nil {
		return false, ""
	}
	if !errors.Is(lookupErr, ibm.ErrClusterNotFound) {
		return false, ""
	}
	name := out.ClusterName
	if name == "" {
		name = out.ClusterID
	}
	return true, name
}

// runBnkDown destroys the BNK trial against the trial state dir,
// leaving the cluster phase in place. Dispatch per PRD 06 §"Dispatch
// table":
//
//   - Empty/ClusterOnly → refuse (no trial state to destroy).
//   - Split             → trial down.
func runBnkDown(cmd *cobra.Command, _ []string) error {
	if err := rejectOnFlag("bnk down"); err != nil {
		return err
	}
	// flagVarFiles is already chokepoint-normalized (PersistentPreRunE).
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	shape, err := config.DetectShape(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace shape: %w", err)
	}
	switch shape {
	case config.ShapeEmpty, config.ShapeClusterOnly:
		// No-op success, not an error: a workspace with no trial state
		// (cluster-only, or empty) has nothing for `bnk down` to do. The
		// cluster phase persists either way, and bnk-forge's reverse-order
		// teardown calls `bnk down` before `cluster down` — a non-zero exit
		// here would block the cluster destroy.
		fmt.Fprintln(os.Stderr, "✓ No BNK trial state to destroy in this workspace — nothing to do.")
		return nil
	}
	// A cluster that no longer exists cannot be uninstalled from, and there is no
	// state that could be left behind on it — so this is the same category as "no
	// trial state above": report it and exit 0 (#79).
	//
	// Reached when a cluster is deleted out from under a workspace that still
	// holds BNK state for it — one BNK Forge project destroying a cluster another
	// had adopted, in the reported case. Forge tears down in reverse dependency
	// order, so a non-zero exit here blocks `cluster-registry` behind it and the
	// project cannot reach zero. That is exactly when teardown most needs to work.
	//
	// KEYED ON AN EXPLICIT PROVIDER NOT-FOUND, never on a connection failure. A
	// transient outage must still fail, or a network blip would silently skip a
	// real uninstall and leave BNK running on a cluster the operator believes is
	// clean. ErrClusterNotFound is a 404 from the container service; a timeout,
	// an auth failure or a DNS error is none of those and falls through to the
	// normal destroy.
	if gone, name := bnkTargetClusterGone(cmd, cctx); gone {
		fmt.Fprintf(os.Stderr, "✓ Cluster %q no longer exists — nothing for `bnk down` to uninstall.\n", name)
		return nil
	}

	// The Gateway phase's CRs (F5BnkGateway, Egress, SnatPool, StaticRoutes)
	// live in the BNK namespace (f5-bnk). Destroying BNK deletes that namespace,
	// and those CRs' finalizers — which only the (now-removed) CNE controller
	// can clear — block the deletion and hang the destroy. Tear the Gateway
	// phase down first. Mirrors the symmetric guard on `cluster down`.
	if pres, perr := config.DetectPresence(cctx.WorkspaceName); perr == nil && pres.Gateway {
		return errors.New("the Gateway phase has resources, and its CRs live in the BNK namespace (f5-bnk) — destroying BNK now would hang on their finalizers. Run `roksbnkctl gateway down` first, then `roksbnkctl bnk down`")
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
		fmt.Fprintf(os.Stderr, "\n✓ BNK phase destroyed. Cluster phase %s/ is intact.\n", clusterDir)
		// Sprint 28: the testing jumphosts are their own phase now, so
		// `bnk down` leaves them untouched — reassure the user explicitly.
		fmt.Fprintln(os.Stderr, "  The testing jumphosts (if any) are also intact — `bnk down` no longer touches them.")
		fmt.Fprintln(os.Stderr, "  Run `roksbnkctl bnk up` to deploy another trial against the same cluster,")
		fmt.Fprintln(os.Stderr, "  or `roksbnkctl testing down` to tear down the jumphosts separately.")
	}
	return nil
}
