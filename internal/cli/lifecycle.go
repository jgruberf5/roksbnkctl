package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	awspkg "github.com/JLCode-tech/awsbnkctl/internal/aws"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/phases"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/config"
	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// Sprint 3 retarget. The Sprint 0 lifecycle stubs (`up` / `plan` /
// `apply` / `down`) now drive the full end-to-end terraform graph
// instead of returning a "not implemented" error. `awsbnkctl up
// --dry-run` runs `terraform plan` against the full root module
// graph (eks_cluster → cert_manager / s3_supply_chain / iam_irsa →
// flo → cne_instance → license / testing); live apply is still
// gated on the operator-run spike per PRD 07 § "Spike protocol".
//
// `awsbnkctl up cluster --dry-run` continues to plan just the
// cluster phase for parity with the Sprint 1 surface.

var (
	flagAuto         bool
	flagTFSource     string
	flagUpgradeTF    bool
	flagNoKubeconfig bool
	flagVarFiles     []string

	// ── Cobra shared-flag-variable anti-pattern (READ BEFORE ADDING FLAGS) ──
	//
	// cobra/pflag's `Flags().BoolVar(&x, "name", default, ...)` does TWO things:
	//   1. Registers the flag on the command's FlagSet with the given default
	//      (held as metadata on the flag definition itself).
	//   2. Writes `*p = default` to the backing variable AT init() TIME.
	//
	// If two commands BoolVar the SAME variable with DIFFERENT defaults, the
	// LAST init() call wins for the variable's runtime value. The flag's
	// metadata default is per-command — but cobra reads the value through the
	// flag's Value interface which dereferences the shared pointer, so
	// `cmd.Flags().GetBool("name")` returns whatever the last writer set.
	//
	// Slice-01 integration testing hit this:
	//   upCmd.Flags().BoolVar(&flagLifecycleDryRun,  "dry-run", false, ...)
	//   downCmd.Flags().BoolVar(&flagLifecycleDryRun, "dry-run", false, ...)
	//   planCmd.Flags().BoolVar(&flagLifecycleDryRun, "dry-run", TRUE,  ...)
	// — `awsbnkctl up` (no --dry-run) silently ran in dry-run mode because
	// planCmd's BoolVar set the shared var to `true` at init.
	//
	// Rule: a shared package-level var is safe ONLY when every command's
	// BoolVar/StringVar/etc. uses the SAME default. If you need different
	// defaults, give each command its own variable (see flagUpDryRun and
	// flagDownDryRun below).
	//
	// Currently-shared vars with matching defaults (safe today, fragile if
	// defaults diverge):
	//   flagAuto         — upCmd, applyCmd, downCmd     (all false)
	//   flagNoKubeconfig — upCmd, applyCmd              (all false)
	//   flagTFSource     — initCmd, upCmd               (all "")
	//   flagConfig       — upCmd, downCmd               (all "") — legitimate
	//   flagVarFiles     — upCmd, planCmd, applyCmd, downCmd (all nil)
	//   flagClusterDryRun (cluster.go) — upClusterCmd, downClusterCmd (false)
	// If you change ANY of those defaults, split the variable per command.

	// flagLifecycleDryRun is bound ONLY to planCmd (default true). Was once
	// shared with upCmd/downCmd; the split happened in two stages:
	//   slice-01: upCmd → flagUpDryRun     (commit e3d70a8)
	//   audit:    downCmd → flagDownDryRun (this commit)
	flagLifecycleDryRun bool

	// flagUpDryRun is bound ONLY to upCmd's --dry-run.
	flagUpDryRun bool

	// flagDownDryRun is bound ONLY to downCmd's --dry-run. Same anti-pattern
	// fix as flagUpDryRun: planCmd's planCmd.BoolVar(&flagLifecycleDryRun,
	// "dry-run", true, ...) would otherwise poison downCmd's legacy TF
	// dry-run check — a destroy-semantics bug worse than the up case.
	flagDownDryRun bool

	// flagRegisterWithForge wires the P2 auto-handoff: after a
	// successful `awsbnkctl up`, register the resulting EKS cluster
	// with a running bnk-forge instance over MCP. No-op in dry-run.
	// Equivalent to running `awsbnkctl forge register` post-apply,
	// but bundled so operators don't have to remember the second
	// command.
	flagRegisterWithForge bool

	// flagConfig activates the new Go-SDK phased path when set.
	// When empty, up/down fall through to the existing TF path unchanged.
	// This is the dispatch gate for the post-Terraform direction
	// (docs/POST_TERRAFORM_DIRECTION.md).
	flagConfig string

	// flagYes skips the interactive "type 'destroy' to proceed" prompt
	// in `awsbnkctl down --config <file>`. Equivalent to the --yes/-y
	// flag in aws-gpu-setup/down.sh.
	flagYes bool

	// flagKeepForgeLink is bound ONLY to downCmd (single-owner per the
	// cobra shared-flag-variable anti-pattern rules above). When true,
	// Phase09ForgeRegisterDown skips forge unregister and preserves
	// forge-link.json so the operator can manage the forge project manually.
	// Default false (unregister on down).
	flagKeepForgeLink bool

	// flagKeepIRSA is bound ONLY to downCmd. When true, Phase18IrsaOidcDown
	// skips deletion of the OIDC provider and retains the IRSA role ARN in
	// state so a subsequent `up` can reuse the same OIDC provider. The IRSA
	// role itself is always deleted (it must be recreated with the new cluster's
	// federated trust policy). Default false (delete OIDC provider on down).
	flagKeepIRSA bool

	// flagSkipActivationPoll is bound ONLY to upCmd. When true, Phase25 returns
	// immediately after logging intent, skipping the 20-min CNEInstance+License
	// poll. Designed for reviewers who need to re-run up without re-burning a
	// real F5 license activation each round. Default false (always poll).
	flagSkipActivationPoll bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive AWS setup; collects region + VPC + subnets + FAR archive + JWT, writes the workspace config (PRD 08).",
	Long: `awsbnkctl init walks through the AWS-shaped prompts (region, VPC, subnets,
cluster name, FAR archive path, subscription JWT path, FLO namespace) and writes
the workspace config.yaml under ~/.awsbnkctl/<workspace>/. The supply-chain
artefacts are uploaded to S3 by 'awsbnkctl up' via aws_s3_object resources, not
by init directly — see PRD 08 § "Open questions" for the rationale.

Use --dry-run to walk the wizard offline (no AWS API calls; useful for
populating a workspace for terraform plan inspection ahead of a real apply).`,
	RunE: runInit,
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Provision the EKS cluster + cert-manager + IRSA + FLO + CNEInstance + license (full lifecycle, Sprint 3 dry-run only)",
	Long: `awsbnkctl up drives the full Sprint 3 end-to-end terraform graph:

  eks_cluster ──► cert_manager
              └─► s3_supply_chain + iam_irsa
                    └─► flo
                           └─► cne_instance
                                  └─► license
                                  └─► testing

Sprint 3 supports --dry-run only (terraform plan against the full root
module). Live apply against AWS is gated on the operator-run spike per
PRD 07 § "Spike protocol"; the v0.2 tag unlocks the non-dry-run path.

Subcommand 'awsbnkctl up cluster --dry-run' plans only the cluster
phase per PRD 06 — useful for fast iteration during the spike.`,
	RunE: runUp,
}

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Read-only; show what awsbnkctl up would change (Sprint 3 dry-run alias)",
	RunE:  runPlan,
}

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply Terraform without re-prompting (gated on PRD 07 spike)",
	RunE:  runApply,
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy everything in the workspace — terraform destroy (Sprint 3 dry-run only)",
	RunE:  runDown,
}

func init() {
	initCmd.Flags().BoolVar(&flagUpgradeTF, "upgrade-tf", false, "resolve and pin the latest TF release into config.yaml")
	initCmd.Flags().StringVar(&flagTFSource, "tf-source", "", "override TF source (path or URL); pinned into config.yaml")

	upCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt before apply")
	upCmd.Flags().StringVar(&flagTFSource, "tf-source", "", "override TF source for this run only")
	upCmd.Flags().BoolVar(&flagNoKubeconfig, "no-kubeconfig", false, "skip the post-apply admin kubeconfig fetch")
	// upCmd has its OWN dry-run variable; planCmd binds to flagLifecycleDryRun
	// with default=true and that shared-var design poisoned upCmd's default.
	upCmd.Flags().BoolVar(&flagUpDryRun, "dry-run", false, "for --config: print the phased plan and exit 0 with no AWS mutations; for legacy TF path: terraform plan only")
	upCmd.Flags().BoolVar(&flagRegisterWithForge, "register-with-forge", false, "after a successful apply, register the EKS cluster with bnk-forge over MCP (no-op in --dry-run)")
	upCmd.Flags().StringVar(&flagConfig, "config", "", "path to cluster.yaml; activates Go-SDK phased path (bypasses TF)")
	upCmd.Flags().BoolVar(&flagSkipActivationPoll, "skip-activation-poll", false, "skip the 20-min CNEInstance+License activation poll (for reviewer re-runs that must not re-burn the F5 license)")

	applyCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt")
	applyCmd.Flags().BoolVar(&flagNoKubeconfig, "no-kubeconfig", false, "skip the post-apply admin kubeconfig fetch")
	downCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")
	downCmd.Flags().BoolVar(&flagDownDryRun, "dry-run", false, "terraform plan -destroy only — never destroy against AWS")
	downCmd.Flags().StringVar(&flagConfig, "config", "", "path to cluster.yaml; activates Go-SDK phased path (bypasses TF)")
	downCmd.Flags().BoolVar(&flagYes, "yes", false, "skip the interactive destroy confirmation (required with --config)")
	downCmd.Flags().BoolVar(&flagKeepForgeLink, "keep-forge-link", false, "preserve forge-link.json on down (skips Phase 09 forge unregister)")
	downCmd.Flags().BoolVar(&flagKeepIRSA, "keep-irsa", false, "retain the OIDC provider and IRSA role on down (both are kept for reuse across cluster iterations)")
	planCmd.Flags().BoolVar(&flagLifecycleDryRun, "dry-run", true, "alias for `awsbnkctl up --dry-run` (always plans, never applies)")

	for _, c := range []*cobra.Command{upCmd, planCmd, applyCmd, downCmd} {
		c.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")
	}

	rootCmd.AddCommand(initCmd, upCmd, planCmd, applyCmd, downCmd)
}

// resolveVarFiles normalises --var-file entries to absolute paths
// against the invocation CWD. Terraform runs with CWD = the per-phase
// state directory (~/.awsbnkctl/<workspace>/state/tf-source/), so a
// user's `--var-file=./terraform.tfvars` would otherwise resolve there
// instead of in the shell directory they typed it from.
//
// Order:
//  1. `~` / `~/...` expansion via os.UserHomeDir.
//  2. Absolute paths pass through unchanged (just cleaned).
//  3. Relative paths join against os.Getwd().
//  4. os.Stat against the resolved absolute, so a typo or wrong-CWD
//     surfaces *before* terraform runs with a message that names both
//     the user-supplied input and the resolved absolute.
//
// Idempotent on already-absolute slices — safe to call once at the
// RunE entry of every lifecycle command.
//
// Ported from roksbnkctl@28ccc59 (sprint12 var-file relative-path fix).
// This codebase has the same bug *plus* a wiring gap: `flagVarFiles`
// was previously declared but never threaded to tfws.Plan/Apply/Destroy.
// Both are fixed in this commit.
func resolveVarFiles(vfs []string) ([]string, error) {
	if len(vfs) == 0 {
		return vfs, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve --var-file: %w", err)
	}
	out := make([]string, len(vfs))
	for i, vf := range vfs {
		expanded := vf
		if expanded == "~" || strings.HasPrefix(expanded, "~/") {
			if home, herr := os.UserHomeDir(); herr == nil {
				if expanded == "~" {
					expanded = home
				} else {
					expanded = filepath.Join(home, expanded[2:])
				}
			}
		}
		if filepath.IsAbs(expanded) {
			out[i] = filepath.Clean(expanded)
			continue
		}
		abs := filepath.Join(cwd, expanded)
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("--var-file %s (resolved to %s): %w", vf, abs, err)
		}
		out[i] = abs
	}
	return out, nil
}

// runUp wires `awsbnkctl up` (no subcommand) — full-lifecycle path.
//
// When --config is provided, dispatches to the new Go-SDK phased path
// (docs/POST_TERRAFORM_DIRECTION.md). Otherwise falls through to the
// existing Terraform path (unchanged).
func runUp(cmd *cobra.Command, _ []string) error {
	// --- New Go-SDK phased path ---
	if flagConfig != "" {
		// flagUpDryRun is upCmd-only so planCmd's shared-var poisoning of
		// flagLifecycleDryRun cannot affect us.
		return runPhasedUp(cmd.Context(), flagConfig, flagUpDryRun, flagSkipActivationPoll)
	}

	// --- Legacy Terraform path ---
	// flagUpDryRun is upCmd's own --dry-run var; flagLifecycleDryRun is
	// owned by planCmd and is poisoned to `true` at init time, so we must
	// not read it here. (The original code used flagLifecycleDryRun and
	// happened to work because upCmd's BoolVar reset it to false; that
	// binding is now gone because we split the var to fix --dry-run leakage
	// into the phased path.)
	if !flagUpDryRun {
		return errors.New("awsbnkctl up requires --dry-run in Sprint 3: live apply is gated on the operator-run PRD 07 spike (see docs/prd/07-EKS-CLUSTER-SRIOV.md § \"Spike protocol\"); v0.2 unlocks the non-dry-run path")
	}
	resolved, err := resolveVarFiles(flagVarFiles)
	if err != nil {
		return err
	}
	flagVarFiles = resolved
	if err := runFullLifecyclePlan(cmd.Context()); err != nil {
		return err
	}
	// P2: auto-register with forge over MCP after a successful apply.
	// Dry-run skips because forge.Register needs a real EKS cluster to
	// describe + generate a kubeconfig for — and there isn't one yet.
	// The flag still wires through so operators get a single command
	// once v0.2 unlocks live apply.
	if flagRegisterWithForge {
		if flagUpDryRun {
			fmt.Fprintln(os.Stderr, "→ --register-with-forge: dry-run, skipping forge registration (would run `forge register` after live apply)")
			return nil
		}
		return registerWithForgePostApply(cmd.Context())
	}
	return nil
}

// registerWithForgePostApply runs the same flow as `awsbnkctl forge
// register` — used by `awsbnkctl up --register-with-forge`. Pulled out
// of runUp so the dry-run / live-apply branching stays readable, and
// so it can be unit-tested independently of the lifecycle path.
func registerWithForgePostApply(ctx context.Context) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return fmt.Errorf("forge register after apply: %w", err)
	}
	wsDir, err := config.WorkspaceDir(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("forge register after apply: resolving workspace dir: %w", err)
	}

	clusterName := cctx.Workspace.Cluster.Name
	if clusterName == "" {
		return fmt.Errorf("forge register after apply: workspace cluster.name is empty")
	}
	region := cctx.Workspace.AWS.Region
	if region == "" {
		return fmt.Errorf("forge register after apply: workspace AWS.region is empty")
	}

	// Generate the kubeconfig in-process via the EKS presigned-URL
	// flow. Matches what `awsbnkctl forge register` does without a
	// --kubeconfig override.
	clients, err := awspkg.NewClients(ctx, awspkg.Options{
		Region:  region,
		Profile: cctx.Workspace.AWS.Profile,
	})
	if err != nil {
		return fmt.Errorf("forge register after apply: aws clients: %w", err)
	}
	ci, err := clients.DescribeCluster(ctx, clusterName)
	if err != nil {
		return fmt.Errorf("forge register after apply: eks describe-cluster %s: %w", clusterName, err)
	}
	yaml, err := clients.KubeconfigFromCluster(ci)
	if err != nil {
		return fmt.Errorf("forge register after apply: generate kubeconfig: %w", err)
	}

	fc := forge.NewClient("")
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "→ forge MCP: %s\n", fc.URL())
	}

	regCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := forge.Register(regCtx, fc, forge.RegisterRequest{
		WorkspaceName: cctx.WorkspaceName,
		WorkspaceDir:  wsDir,
		ClusterName:   clusterName,
		Region:        region,
		Kubeconfig:    []byte(yaml),
	})
	if err != nil {
		return fmt.Errorf("forge register after apply: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ registered with forge (project_id=%d cluster_id=%d)\n",
		res.Link.ProjectID, res.Link.ClusterID)
	return nil
}

func runPlan(cmd *cobra.Command, _ []string) error {
	// `awsbnkctl plan` is always a dry-run alias for `awsbnkctl up --dry-run`.
	resolved, err := resolveVarFiles(flagVarFiles)
	if err != nil {
		return err
	}
	flagVarFiles = resolved
	return runFullLifecyclePlan(cmd.Context())
}

func runApply(_ *cobra.Command, _ []string) error {
	return errors.New("awsbnkctl apply is gated on the operator-run PRD 07 spike (docs/prd/07-EKS-CLUSTER-SRIOV.md § \"Spike protocol\"); use `awsbnkctl up --dry-run` until v0.2 unlocks live apply")
}

func runDown(cmd *cobra.Command, _ []string) error {
	// --- New Go-SDK phased path ---
	if flagConfig != "" {
		return runPhasedDown(cmd.Context(), flagConfig, flagYes)
	}

	// --- Legacy Terraform path ---
	// flagDownDryRun is downCmd's own --dry-run var; the previous code read
	// flagLifecycleDryRun which planCmd poisons to `true` at init, causing
	// `awsbnkctl down` (no --dry-run) to silently skip this guard and run
	// a live `terraform destroy`. Destroy-semantics-affecting bug, fixed in
	// the cobra-flag-poisoning audit.
	if !flagDownDryRun {
		return errors.New("awsbnkctl down requires --dry-run in Sprint 3: live destroy is gated on the operator-run PRD 07 spike; v0.2 unlocks the non-dry-run path")
	}
	resolved, err := resolveVarFiles(flagVarFiles)
	if err != nil {
		return err
	}
	flagVarFiles = resolved
	return runFullLifecyclePlan(cmd.Context())
}

// runPhasedUp is the Go-SDK phased provisioning path activated by
// `awsbnkctl up --config <file>`. It reads the cluster.yaml intent,
// constructs AWS clients with the SSO sentinel middleware, then runs
// phases 00 → 25 in order.
//
// When dryRun is true the phase functions print planned actions but make
// zero AWS API mutations.
// When skipActivationPoll is true, Phase 25 returns immediately (for reviewers
// who must not re-burn a real F5 license each round).
func runPhasedUp(ctx context.Context, configPath string, dryRun bool, skipActivationPoll bool) error {
	cl, err := intent.Load(configPath)
	if err != nil {
		return fmt.Errorf("up: %w", err)
	}

	clients, err := phases.NewClients(ctx, cl.Metadata.Region, "")
	if err != nil {
		return fmt.Errorf("up: aws clients: %w", err)
	}
	// Attach forge client when forge is enabled in cluster.yaml.
	if cl.Forge != nil {
		clients.AttachForgeClient(cl.Forge.Enabled, cl.Forge.MCPURL)
	}

	stateDir := cl.StateDir()
	st, err := state.Load(stateDir)
	if err != nil {
		return fmt.Errorf("up: loading state: %w", err)
	}

	if dryRun {
		fmt.Fprintln(os.Stderr, "→ dry-run: printing plan, no AWS mutations will be made")
	}

	if err := phases.Phase00Preflight(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase02VPC(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase03Subnets(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase04IGW(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase05NAT(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase06RouteTables(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase07IAM(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase08EKSCluster(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase09ForgeRegister(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase10NodeGroup(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase11Kubeconfig(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}

	// Phase 16: label the TMM-target node + resolve EC2 instance ID.
	// Requires K8s client (attached below for dryRun=false); dry-run
	// path is handled inside Phase16TMMNodeLabel.
	if !dryRun {
		kubeconfigPath := st.Get("KUBECONFIG_PATH")
		if kubeconfigPath == "" {
			return fmt.Errorf("up: KUBECONFIG_PATH not in state after phase 11")
		}
		if err := clients.AttachK8s(kubeconfigPath); err != nil {
			return fmt.Errorf("up: attaching k8s clients: %w", err)
		}
	}
	if err := phases.Phase16TMMNodeLabel(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase17SecondaryENIs(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase18IRSAOIDC(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}

	if err := phases.Phase12K8sFoundation(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase14FLOHelm(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if err := phases.Phase15OTELCerts(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	// Phase 19: cloud-network-mapping ConfigMap (required by cne-controller pre-CNEInstance).
	if err := phases.Phase19CloudNetworkMapping(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	// Phase 20: host-device NADs in f5-cne-system + default (required by CNEInstance webhook).
	if err := phases.Phase20NADs(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	// Phase 21: IRSA ServiceAccount pre-creation with eks.amazonaws.com/role-arn annotation.
	if err := phases.Phase21IRSASA(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	// Phase 22: CNEInstance CR apply + reconcile-started gate (2 min).
	if err := phases.Phase22CNEInstance(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	// Phase 23: License CRD wait + License CR apply.
	if err := phases.Phase23License(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	// Phase 24: CWC DNS-warmup heal (best-effort; never returns error).
	if err := phases.Phase24CWCHeal(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	// Phase 25: Activation poll — CNEInstance + License status (up to 20 min).
	// skipActivationPoll is set by --skip-activation-poll for reviewer re-runs.
	if err := phases.Phase25ActivationPoll(ctx, cl, st, clients, dryRun, skipActivationPoll); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	// Phase 13 postflight runs LAST so it can verify FLO + OTEL + activation state.
	if err := phases.Phase13Postflight(ctx, cl, st, clients, dryRun); err != nil {
		return fmt.Errorf("up: %w", err)
	}

	if dryRun {
		fmt.Fprintln(os.Stderr, "→ dry-run complete")
	} else {
		fmt.Fprintf(os.Stderr, "✓ up complete: cluster=%s state=%s\n", cl.Metadata.Name, stateDir)
	}
	return nil
}

// runPhasedDown is the Go-SDK phased destroy path activated by
// `awsbnkctl down --config <file>`. It reads the cluster.yaml intent,
// loads the IDs cache (with tag-discovery fallback), then destroys
// resources in reverse phase order.
//
// When yes is false the operator is prompted to type 'destroy' to proceed.
func runPhasedDown(ctx context.Context, configPath string, yes bool) error {
	cl, err := intent.Load(configPath)
	if err != nil {
		return fmt.Errorf("down: %w", err)
	}

	clients, err := phases.NewClients(ctx, cl.Metadata.Region, "")
	if err != nil {
		return fmt.Errorf("down: aws clients: %w", err)
	}
	// Attach forge client when forge is enabled in cluster.yaml.
	if cl.Forge != nil {
		clients.AttachForgeClient(cl.Forge.Enabled, cl.Forge.MCPURL)
	}

	stateDir := cl.StateDir()
	st, err := state.Load(stateDir)
	if err != nil {
		return fmt.Errorf("down: loading state: %w", err)
	}

	if !yes {
		fmt.Fprintf(os.Stderr, "About to DESTROY cluster %q in %s.\n", cl.Metadata.Name, cl.Metadata.Region)
		fmt.Fprintln(os.Stderr, "Type 'destroy' to proceed:")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() && scanner.Text() != "destroy" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	// Reverse phase order: 15 → 14 → 12 → 11 → 10 → 09 → 08 → 07 → 06 → 05 → 04 → 03 → 02.
	// Phase 15/14/12 k8s teardown runs FIRST (while kubeconfig is still valid).
	// Attach k8s clients using the kubeconfig path from state before k8s down phases.
	kubeconfigPath := st.Get("KUBECONFIG_PATH")
	if kubeconfigPath != "" {
		if err := clients.AttachK8s(kubeconfigPath); err != nil {
			// Log and continue — kubeconfig may be absent if phase 11 never ran.
			fmt.Fprintf(os.Stderr, "down: warning: could not attach k8s clients (%v) — phase 12/14/15 down will log warning and skip\n", err)
		}
	}
	if err := phases.Phase15OTELCertsDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	// Phase 25/24/23/22: activation teardown (reverse of up order).
	if err := phases.Phase25ActivationPollDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase24CWCHealDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase23LicenseDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase22CNEInstanceDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	// Phase 21/20/19: k8s BNK prerequisites teardown (reverse of up order).
	if err := phases.Phase21IRSASADown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase20NADsDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase19CloudNetworkMappingDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase14FLOHelmDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase12K8sFoundationDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase11KubeconfigDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	// Phase 18/17/16: AWS-side BNK teardown (ENIs, OIDC/IRSA, node label).
	// Runs after kubeconfig down (TMM node already gone with node group teardown).
	if err := phases.Phase18IrsaOidcDown(ctx, cl, st, clients, flagKeepIRSA); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase17SecondaryENIsDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase16TMMNodeLabelDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase10NodeGroupDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase09ForgeRegisterDown(ctx, cl, st, clients, flagKeepForgeLink); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase08EKSClusterDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase07IAMDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase06RouteTablesDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase05NATDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase04IGWDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase03SubnetsDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase02VPCDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ down complete: cluster=%s\n", cl.Metadata.Name)
	return nil
}
