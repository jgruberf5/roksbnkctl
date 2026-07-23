package cli

// Sprint 16 consolidation phase-1b: the lifecycle RunE orchestration
// (~the runUp/runTrialUp/runPlan/runApply/runDown/runTrialDown family +
// their terraform/docker/retry/post-apply-hook helpers) moved verbatim
// into internal/orchestration. What remains here is the thin cobra
// adapter: the command definitions, flag binding, the resolveVarFiles
// chokepoint wrapper, and RunE shims that build an
// orchestration.LifecycleInputs (flag globals + the cli-resident
// collaborators injected as function fields — no orchestration → cli
// import) and delegate. Behavior is byte-for-byte preserved.

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// Shared across init/up/apply/down — only one runs per invocation, so a
// single backing var per logically-distinct flag is fine.
var (
	flagAuto         bool
	flagTFSource     string
	flagUpgradeTF    bool
	flagNoKubeconfig bool
	flagLegacyBnk    bool     // --legacy-bnk: render bnk_cr_mode = "legacy_curl" (Sprint 27)
	flagVarFiles     []string // -var-file (repeatable; matches terraform's flag)
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive setup; writes the workspace config.yaml",
	Long: `roksbnkctl init walks through the prompts (region, resource group, cluster,
BNK version) and writes ~/.roksbnkctl/<workspace>/config.yaml.

On first run with no -w flag, creates and uses the 'default' workspace.
Re-run with --upgrade-tf to bump the pinned Terraform source to its latest
release.`,
	Args: cobra.NoArgs,
	RunE: runInit,
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Provision (or attach) and deploy BNK — terraform plan + apply",
	Long: `roksbnkctl up validates credentials, resolves the pinned Terraform source,
runs plan, and (after confirmation, unless --auto) applies. Idempotent and
resumable: a partial failure is recovered by re-running 'roksbnkctl up'.`,
	Args: cobra.NoArgs,
	RunE: runUp,
}

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Read-only; show what roksbnkctl up would change",
	Args:  cobra.NoArgs,
	RunE:  runPlan,
}

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply Terraform without re-prompting (assumes config.yaml exists)",
	Args:  cobra.NoArgs,
	RunE:  runApply,
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy everything in the workspace — terraform destroy",
	Long: `Tears down the workspace's phases in reverse-dependency order: BNK and
Testing in parallel, then the cluster. Two behaviours worth knowing:

  - If the cluster was attached to an EXISTING (shared) Transit Gateway, down
    auto-detaches that connection first — removing only this cluster's connection;
    the shared gateway and every other cluster's connection are left intact.
  - If this workspace CREATED a VPC that another cluster's subnets still live in
    (the shared-VPC topology), down refuses: the VPC owner must be torn down LAST,
    so tear the workspaces sharing the VPC down first.

The Gateway and FLP phases are separate and optional; if present, tear them down
first (down reports which command to run).`,
	Args: cobra.NoArgs,
	RunE: runDown,
}

func init() {
	initCmd.Flags().BoolVar(&flagUpgradeTF, "upgrade-tf", false, "resolve and pin the latest TF release into config.yaml")
	initCmd.Flags().StringVar(&flagTFSource, "tf-source", "", "override TF source (path or URL); relative local paths are resolved to absolute before being pinned into config.yaml")

	upCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt before apply")
	upCmd.Flags().StringVar(&flagTFSource, "tf-source", "", "override TF source for this run only (path or URL; relative local paths resolved against the invocation CWD)")
	upCmd.Flags().BoolVar(&flagNoKubeconfig, "no-kubeconfig", false, "skip the post-apply admin kubeconfig fetch")

	applyCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt")
	applyCmd.Flags().BoolVar(&flagNoKubeconfig, "no-kubeconfig", false, "skip the post-apply admin kubeconfig fetch")
	downCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")

	// --var-file matches terraform's own flag: repeatable, later wins.
	// Layered after the roksbnkctl-generated tfvars and the workspace's
	// optional terraform.tfvars.user override.
	for _, c := range []*cobra.Command{upCmd, planCmd, applyCmd, downCmd} {
		c.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")
	}

	rootCmd.AddCommand(initCmd, upCmd, planCmd, applyCmd, downCmd)
}

// resolveVarFiles is the cli-layer thin wrapper over the single
// path-normalization chokepoint (orchestration.NormalizeVarFiles). It
// exists so the in-package tests that pin the --var-file relative-path
// behavior (Sprint 12 Issue 1) call a stable local symbol; the
// canonical normalization lives in orchestration and is applied exactly
// once at command entry (the root PersistentPreRunE →
// resolveInvocationContext), NOT re-derived per RunE.
func resolveVarFiles(vfs []string) ([]string, error) {
	return orchestration.NormalizeVarFiles(vfs)
}

// lifecycleInputs builds the resolved-invocation context the moved
// orchestration consumes, replacing the package-global `flag*` reads the
// code did while it lived here. The cli-resident collaborators (the TTY
// prompt, the --on rejection, the cluster-phase composites, the
// terraform-output decoders) are injected as function fields so
// internal/orchestration never imports internal/cli.
// resolvedWorkspaceName returns the workspace name the -w flag resolves to: the
// flag value if given, else the current-workspace pointer. The orchestration
// uses LifecycleInputs.Workspace directly as the workspace IDENTIFIER — the
// cluster-outputs.json lookup that drives the create_roks_cluster=false reuse
// override, plus the applied-tfvars snapshot paths. Passing the RAW (possibly
// empty) -w flag meant a no-`-w` run looked up cluster-outputs.json under the
// empty workspace, found nothing, skipped the override, and the BNK/Testing
// legs tried to re-create the cluster VPC/transit-gateway ("Provided Name … is
// not unique"). Empty when nothing is selected — the orchestration then
// surfaces ErrNoWorkspaceSelected. Falls back to the raw flag on a config error.
func resolvedWorkspaceName() string {
	if cctx, err := config.New(flagWorkspace); err == nil {
		return cctx.WorkspaceName
	}
	return flagWorkspace
}

func lifecycleInputs() *orchestration.LifecycleInputs {
	var in *orchestration.LifecycleInputs
	in = &orchestration.LifecycleInputs{
		Workspace:    resolvedWorkspaceName(),
		Backend:      flagBackend,
		Auto:         flagAuto,
		NoKubeconfig: flagNoKubeconfig,
		LegacyBNK:    flagLegacyBnk,
		VarFiles:     flagVarFiles,

		PromptYesNo:  promptYesNo,
		RejectOnFlag: rejectOnFlag,
		RunClusterUp: func(ctx context.Context) error {
			return runClusterUp(cmdFromCtx(ctx), nil)
		},
		// orchestration.RunDown's Split path flips in.Auto=true after
		// the combined confirmation so the leaves don't re-prompt.
		// runClusterDown still reads cli-package flagAuto (Sprint 16
		// kept cluster_phase.go in cli, byte-unchanged), so mirror
		// in.Auto onto flagAuto for the call's duration.
		RunClusterDown: func(ctx context.Context) error {
			if in.Auto && !flagAuto {
				prev := flagAuto
				flagAuto = true
				defer func() { flagAuto = prev }()
			}
			return runClusterDown(cmdFromCtx(ctx), nil)
		},
		StringOutput: stringOutput,
		MapOutput:    mapOutput,
	}
	return in
}

// ── cli-side helper wrappers for the (frozen) cluster-phase adapter ──
//
// cluster_phase.go (out of phase-1b scope — must stay byte-unchanged)
// still calls these lifecycle preamble/apply/kubeconfig helpers by their
// original cli-package names + signatures. They now delegate to the
// moved orchestration seams; behavior is identical.

func writeAndInit(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace) error {
	return orchestration.WriteAndInit(ctx, tfws, ws)
}

func applyWithRetry(ctx context.Context, tfws *tf.Workspace, varFiles []string) error {
	return orchestration.ApplyWithRetry(ctx, tfws, varFiles)
}

func tryAutoKubeconfig(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) {
	orchestration.TryAutoKubeconfig(ctx, lifecycleInputs(), cctx, tfws)
}

// tryAutoClusterJumphosts retains its original cli-package name +
// 3-arg signature so the frozen auto_cluster_jumphosts_test.go (and the
// nil-guard contract it pins) compiles and passes byte-unchanged. It
// delegates to the moved orchestration implementation.
func tryAutoClusterJumphosts(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) {
	orchestration.TryAutoClusterJumphosts(ctx, lifecycleInputs(), cctx, tfws)
}

// ── thin cobra RunE shims ───────────────────────────────────────────
//
// These keep the exact `func(*cobra.Command, []string) error` signature
// the cobra commands and the (out-of-scope, unchanged) bnk_phase.go +
// the pre-existing tests bind to; they bind a *cobra.Command onto the
// context (so the injected cluster-phase composites still receive the
// command they need) and delegate to internal/orchestration.

// cmdCtxKey carries the *cobra.Command across the cli→orchestration
// boundary so the injected runClusterUp/runClusterDown collaborators —
// which still take a *cobra.Command (cluster_phase.go stays in cli per
// the phase-1b scope) — receive the same command the composite was
// invoked with, exactly as before the move.
type cmdCtxKey struct{}

func ctxWithCmd(cmd *cobra.Command) context.Context {
	return context.WithValue(cmd.Context(), cmdCtxKey{}, cmd)
}

func cmdFromCtx(ctx context.Context) *cobra.Command {
	if c, ok := ctx.Value(cmdCtxKey{}).(*cobra.Command); ok {
		return c
	}
	return nil
}

func runUp(cmd *cobra.Command, _ []string) error {
	return orchestration.RunUp(ctxWithCmd(cmd), lifecycleInputs())
}

func runTrialUp(cmd *cobra.Command, _ []string) error {
	return orchestration.RunTrialUp(ctxWithCmd(cmd), lifecycleInputs())
}

func runPlan(cmd *cobra.Command, _ []string) error {
	return orchestration.RunPlan(ctxWithCmd(cmd), lifecycleInputs())
}

func runApply(cmd *cobra.Command, _ []string) error {
	return orchestration.RunApply(ctxWithCmd(cmd), lifecycleInputs())
}

func runDown(cmd *cobra.Command, _ []string) error {
	return orchestration.RunDown(ctxWithCmd(cmd), lifecycleInputs())
}

func runTrialDown(cmd *cobra.Command, _ []string) error {
	return orchestration.RunTrialDown(ctxWithCmd(cmd), lifecycleInputs())
}
