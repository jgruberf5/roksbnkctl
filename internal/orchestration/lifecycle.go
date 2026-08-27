package orchestration

// Sprint 16 consolidation phase-1b: the lifecycle RunE orchestration
// (up / trial-up / plan / apply / down / trial-down + their terraform /
// docker / retry / post-apply-hook helpers) relocated verbatim out of
// internal/cli/lifecycle.go into this service layer. internal/cli is now
// a thin cobra adapter: it binds flags, builds a LifecycleInputs once
// per command entry, and delegates here. Behavior is byte-for-byte
// preserved — this is a move, not a rewrite.
//
// The cobra/cli-resident collaborators the moved code calls (the
// confirmation prompt, the --on rejection, the per-AZ jumphost output
// extractors, the cluster-phase composites) are injected as function
// fields on LifecycleInputs rather than imported — the orchestration →
// cli boundary stays one-directional (asserted by the validator's
// import audit and the chokepoint guard test).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"golang.org/x/sync/errgroup"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// Apply retry tuning. ROKS master endpoints take 1–5 minutes to fully
// propagate after creation; the cneinstance/license/cert-manager
// modules race that propagation by curl-ing the master directly. When
// terraform-exec surfaces a transient-shaped failure, sleep and retry
// rather than making the user type `roksbnkctl up` again.
const (
	// 5×90s (vs the original 3×60s): a cold BNK deploy applies the F5SPKVlan
	// CRs and the License CR while the FLO validating-webhook pod and the
	// ResourceQuota controller are still coming up, so the readiness races
	// ("failed calling webhook", "status unknown for quota") can outlast a
	// 3-minute budget on a first apply. Only looksTransient failures retry,
	// so genuine errors still fail fast.
	applyMaxAttempts = 5
	applyRetryWait   = 90 * time.Second

	// Teardown budget. terraform destroy is idempotent — a resource already
	// gone is refreshed out of state on the next run — so when a destroy aborts
	// on a transient IBM provider delete-race (e.g. "Public Gateway not found"
	// because terraform's own parallel destroy removed it first), retrying lets
	// one `down` finish tearing down the resources it hadn't reached yet,
	// instead of leaking them. Shorter wait than apply: nothing needs to settle,
	// we just need terraform to re-refresh and continue.
	destroyMaxAttempts = 4
	destroyRetryWait   = 30 * time.Second
)

// LifecycleInputs is the resolved-invocation context the cobra adapter
// hands the lifecycle orchestration, replacing the package-level `flag*`
// globals the code read while it lived in internal/cli. Path-valued
// flags (VarFiles, TFSource) are already chokepoint-normalized by the
// root PersistentPreRunE before this struct is built — the orchestration
// never re-derives a path. The function fields inject the cli-resident
// collaborators so this package never imports internal/cli.
type LifecycleInputs struct {
	// Workspace is the resolved --workspace value (flagWorkspace).
	Workspace string
	// Backend is the resolved --backend value (flagBackend).
	Backend string
	// Auto is --auto (skip the confirm prompt).
	Auto bool
	// NoKubeconfig is --no-kubeconfig (skip the post-apply fetch).
	NoKubeconfig bool
	// VarFiles is the chokepoint-normalized --var-file slice
	// (absolute, os.Stat-checked) — the former flagVarFiles global.
	VarFiles []string

	// PlanOut is `plan --out <file>`: save the plan to a binary plan file (plus a
	// human-readable <file>.txt) so `apply --plan <file>` applies exactly it.
	PlanOut string
	// PlanFile is `apply --plan <file>`: apply that saved plan verbatim instead of
	// re-planning — closing the review-then-apply-exactly gap.
	PlanFile string

	// Stderr, when non-nil, is the writer the BNK/trial-phase leaf helpers
	// (and the underlying terraform handle) print to instead of os.Stderr.
	// Sprint 28 parallel up/down sets it to a line-prefixed ([bnk] )
	// writer so the concurrent BNK ∥ Testing output interleaves readably;
	// nil everywhere else → os.Stderr, byte-identical to prior behavior.
	Stderr io.Writer

	// PromptYesNo is the cli-resident TTY confirmation prompt
	// (cli.promptYesNo) — injected so a non-TTY run keeps returning the
	// default exactly as before.
	PromptYesNo func(label string, def bool) bool
	// RejectOnFlag is cli.rejectOnFlag — refuses `--on` on the lifecycle
	// verbs (unchanged error text).
	RejectOnFlag func(cmdName string) error
	// RunClusterUp / RunClusterDown are the cli-resident cluster-phase
	// composites (cluster_phase.go stays in cli per the scope). The
	// shape-aware `up`/`down` dispatchers call into them unchanged.
	RunClusterUp   func(ctx context.Context) error
	RunClusterDown func(ctx context.Context) error
	// StringOutput / MapOutput are the cli-resident terraform-output
	// decoders (cluster_phase.go) the post-apply jumphost hooks use.
	StringOutput func(outputs map[string]tfexec.OutputMeta, key string) string
	MapOutput    func(outputs map[string]tfexec.OutputMeta, key string) map[string]string
}

// errOut returns the writer the BNK/trial-phase helpers print to —
// in.Stderr when the parallel path set a prefixed writer, else os.Stderr.
func (in *LifecycleInputs) errOut() io.Writer {
	if in != nil && in.Stderr != nil {
		return in.Stderr
	}
	return os.Stderr
}

// ── lifecycle implementations ───────────────────────────────────────

// RunUp is the presence-aware composite dispatcher for the top-level
// `roksbnkctl up` (Sprint 28 three-phase split). It detects per-phase
// presence and routes per the architect's §2d dispatch table:
//
//   - Legacy single-state → monolithic trial up (preserves v1.0.x
//     byte-for-byte: one terraform apply against the trial state, which
//     still carries the cluster modules in pre-split workspaces).
//   - Otherwise → Cluster serial-first (created when absent; a no-op
//     refresh when present or when reusing a registered cluster), then
//     BNK ∥ Testing concurrently (errgroup), bringing up only the phases
//     that need it.
//
// "Reuse an existing cluster" (cluster-outputs.json present, no
// state-cluster/) is treated as "Cluster present": the cluster phase is
// skipped and BNK ∥ Testing deploy against the registered cluster.
//
// The composite is a pure dispatcher — all terraform / docker / retry
// behavior lives in the leaf helpers.
func RunUp(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("up"); err != nil {
		return err
	}
	// --var-file is already normalized to absolute paths against the
	// invocation CWD by the single chokepoint (root PersistentPreRunE →
	// resolveInvocationContext). No per-RunE re-derivation (Sprint 12
	// Issue 1, retired as a class in Sprint 15).
	cctx, err := config.New(in.Workspace)
	if err != nil {
		return err
	}
	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	// Cluster serial-first (both downstreams need it). Skip it when a
	// cluster is already present in state-cluster/ OR when reusing a
	// registered cluster (cluster-outputs.json present, no state-cluster/).
	reuse := false
	if !pres.Cluster {
		if _, rerr := config.ReadClusterOutputs(cctx.WorkspaceName); rerr == nil {
			reuse = true
		}
	}
	if !reuse {
		if err := in.RunClusterUp(ctx); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(os.Stderr, "→ Reusing the registered cluster (cluster-outputs.json) — skipping the cluster phase.")
	}

	// BNK ∥ Testing concurrently. Both depend only on the now-present
	// cluster, not on each other.
	return runBNKAndTestingParallel(ctx, in)
}

// runBNKAndTestingParallel brings up the BNK and Testing phases after the
// cluster phase has completed (architect §3a/§3b). Two concurrent
// `terraform apply`s can't each own an interactive approval prompt on one
// TTY, so the flow is:
//
//  1. PLAN both phases sequentially (plan is cheap relative to apply, and
//     sequential output is cleanly attributable for the review);
//  2. CONFIRM each phase that has changes, independently — the operator can
//     approve BNK and skip Testing (or vice-versa), bringing up just one
//     phase. --auto applies every changed phase without prompting;
//  3. APPLY the approved phases CONCURRENTLY (the expensive step), each
//     phase's stderr line-prefixed ([bnk] / [testing]) under a shared
//     mutex. The errgroup's first non-nil error cancels the sibling's apply
//     context; the error is surfaced to the caller.
func runBNKAndTestingParallel(ctx context.Context, in *LifecycleInputs) error {
	bnkErr, testErr := newPrefixWriters(os.Stderr)
	bnkIn := *in
	bnkIn.Stderr = bnkErr

	// ── 1. Plan both phases (sequential → clean, attributable diffs). ──
	fmt.Fprintln(os.Stderr, "→ Planning BNK phase…")
	bnkChanges, bnkApply, err := prepareBNKUp(ctx, &bnkIn)
	bnkErr.flush()
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "→ Planning Testing phase…")
	testChanges, testApply, err := prepareTestingUp(ctx, in, testErr)
	testErr.flush()
	if err != nil {
		return err
	}

	// ── 2. Confirm each changed phase independently (real TTY, serial). ──
	confirmBNK, confirmTest := false, false
	if !in.Auto {
		if bnkChanges {
			confirmBNK = in.PromptYesNo("Apply BNK plan?", false)
		}
		if testChanges {
			confirmTest = in.PromptYesNo("Apply Testing plan?", false)
		}
	}
	runBNK, runTest := applyDecision(bnkChanges, testChanges, in.Auto, confirmBNK, confirmTest)
	if bnkChanges && !runBNK {
		fmt.Fprintln(os.Stderr, "✓ BNK skipped")
	}
	if testChanges && !runTest {
		fmt.Fprintln(os.Stderr, "✓ testing skipped")
	}
	if !runBNK && !runTest {
		// Nothing to apply: success if both phases were no-ops, "aborted"
		// if the operator declined every phase that had changes.
		if bnkChanges || testChanges {
			return errors.New("aborted")
		}
		return nil
	}

	// ── 3. Apply the approved phases concurrently (the expensive step). ──
	g, gctx := errgroup.WithContext(ctx)
	if runBNK {
		g.Go(func() error {
			defer bnkErr.flush()
			return bnkApply(gctx)
		})
	}
	if runTest {
		g.Go(func() error {
			defer testErr.flush()
			return testApply(gctx)
		})
	}
	return g.Wait()
}

// applyDecision selects which of the two parallel phases to apply. A phase
// runs iff its plan had changes AND (we're in --auto OR the operator
// confirmed it). Separate per-phase confirms are what let the operator
// bring up just one phase (approve BNK, skip Testing).
func applyDecision(bnkChanges, testChanges, auto, confirmBNK, confirmTest bool) (runBNK, runTest bool) {
	runBNK = bnkChanges && (auto || confirmBNK)
	runTest = testChanges && (auto || confirmTest)
	return
}

// RunTrialUp = plan + confirm + apply + (optional) kubeconfig fetch
// against the trial state dir. The leaf "trial phase up" used by both
// the composite `RunUp` (on Empty/Split/ClusterOnly) and `bnk up`. For
// legacy single-state workspaces this is the v1.0.x monolithic apply —
// the trial state still carries the cluster modules in that shape.
//
// Preserves the v1.0.x docker-backend short-circuit at the top: a
// non-local terraform backend dispatches through
// runTerraformLifecycleDocker before any state-dir prep.
func RunTrialUp(ctx context.Context, in *LifecycleInputs) error {
	// VarFiles is already chokepoint-normalized (PersistentPreRunE).
	if spec, ok := terraformBackendSpec(in); ok && spec != "local" {
		return runTerraformLifecycleDocker(ctx, in, spec, "up")
	}
	changes, apply, err := prepareBNKUp(ctx, in)
	if err != nil {
		return err
	}
	if !changes {
		return nil
	}
	if !in.Auto && !in.PromptYesNo("Apply this plan?", false) {
		return errors.New("aborted")
	}
	return apply(ctx)
}

// prepareBNKUp does the open + render + init + plan for the BNK (trial)
// phase and reports whether the plan has changes. When it does, it returns
// an apply closure (terraform apply + kubeconfig/jumphost post-steps) that
// the caller runs after confirming; the closure takes its own context so
// the parallel orchestrator can cancel a sibling's apply. When there are no
// changes the best-effort kubeconfig/jumphost post-steps have already run
// and the returned apply is nil.
//
// Splitting plan from apply is what lets runBNKAndTestingParallel plan both
// phases, confirm each, then apply only the approved ones — while the
// serial `bnk up` (RunTrialUp) keeps its single "Apply this plan?" gate.
func prepareBNKUp(ctx context.Context, in *LifecycleInputs) (bool, func(context.Context) error, error) {
	cctx, tfws, err := openTF(ctx, in, true)
	if err != nil {
		return false, nil, err
	}
	// Sprint 29 air-gap guard: when the workspace config opts into a registry
	// mirror (registry: block) but the mirror has not been populated yet
	// (no/incomplete registry-mirror.json), fail before plan rather than
	// deploying BNK against far_repo_url — which an air-gapped cluster cannot
	// reach. Off the mirror path (ws.Registry == nil) this is a no-op.
	if err := guardRegistryMirror(cctx.WorkspaceName, cctx.Workspace); err != nil {
		return false, nil, err
	}
	// Second-phase preamble: renders tfvars and, when this workspace
	// already has a cluster-outputs.json (the cluster phase created the
	// entire cluster-shared network), writes a forced bnk-phase override
	// that turns ALL cluster-shared creation OFF and returns its path so
	// it is appended to the plan/apply var-file chain. No
	// cluster-outputs.json → extraVF is nil and the run is byte-identical
	// to the create path (fresh/legacy single-state unchanged) — Issue 2
	// round 2, symmetric with cluster-phase-override.tfvars.
	extraVF, err := writeAndInitSecondPhase(ctx, tfws, cctx.Workspace, in.Workspace, false, in.errOut())
	if err != nil {
		return false, nil, err
	}
	varFiles := append(append([]string{}, in.VarFiles...), extraVF...)

	w := in.errOut()
	// Refuse BEFORE planning if the cluster already carries a BNK install this
	// workspace has no state for. Planning first would report ~60 resources to add
	// over an install that already exists, then spend ~13 minutes failing to create
	// them (issue #53). Narrow by construction: a workspace WITH state converges as
	// before and never reaches this.

	// Both halves of the version question are known here and nowhere earlier: the
	// BNK line from bnk.manifest_version, and what the cluster actually IS from its
	// own record. Refusing an unsupported pairing now costs a second; discovering it
	// during the apply costs a cluster in a state neither half expects.
	if err := guardSupportedCombination(cctx, w); err != nil {
		return false, nil, err
	}
	// A create-time setting that contradicts the built cluster means a REPLACEMENT,
	// not a change. Refuse the enforceable ones, warn on the rest.
	if err := guardCreateTimeSettings(cctx, w); err != nil {
		return false, nil, err
	}
	if err := guardUnownedBNKInstall(ctx, cctx, tfws, w); err != nil {
		return false, nil, err
	}
	fmt.Fprintln(w, "→ terraform plan")
	changes, err := tfws.Plan(ctx, varFiles...)
	if err != nil {
		return false, nil, err
	}
	if !changes {
		fmt.Fprintln(w, "✓ no changes")
		// Even with no infra changes, fetching the kubeconfig is useful
		// (cluster may already exist; user wants creds locally).
		tryAutoKubeconfig(ctx, in, cctx, tfws)
		// Jumphost SSH-target seeding moved to the Testing phase (Sprint
		// 28). These calls remain as best-effort no-ops here for the
		// legacy single-state path (where the jumphosts still live in the
		// trial state); on a split BNK state there are no jumphost outputs
		// so they skip silently.
		tryAutoJumphost(ctx, in, cctx, tfws)
		tryAutoClusterJumphosts(ctx, in, cctx, tfws)
		return false, nil, nil
	}
	apply := func(actx context.Context) error {
		// Cheapest precondition first: a mirror we could never authenticate to
		// is knowable without touching the cluster, and failing here costs a
		// second instead of fifteen minutes and a half-installed BNK.
		if err := checkMirrorCredentials(cctx, w); err != nil {
			return err
		}
		// Air-gap precondition: install the private registry's CA on every
		// node before the apply's charts pull images, or the first pull fails
		// x509 "unknown authority" and BNK stalls in ImagePullBackOff. No-op
		// off the mirror path (or when the mirror carries no CA).
		if err := ensureRegistryCATrust(actx, cctx, tfws, w); err != nil {
			return err
		}
		fmt.Fprintln(w, "→ terraform apply")
		if err := applyBNKWithAdmissionSweep(actx, cctx, tfws, varFiles); err != nil {
			return err
		}
		tryAutoKubeconfig(actx, in, cctx, tfws)
		tryAutoJumphost(actx, in, cctx, tfws)
		tryAutoClusterJumphosts(actx, in, cctx, tfws)
		return nil
	}
	return true, apply, nil
}

// RunPlan = plan only. Read-only — never prompts.
func RunPlan(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("plan"); err != nil {
		return err
	}
	// VarFiles is already chokepoint-normalized (PersistentPreRunE).
	if spec, ok := terraformBackendSpec(in); ok && spec != "local" {
		if in.PlanOut != "" {
			return fmt.Errorf("plan --out requires the local backend (this workspace uses %q)", spec)
		}
		return runTerraformLifecycleDocker(ctx, in, spec, "plan")
	}
	cctx, tfws, err := openTF(ctx, in, true)
	if err != nil {
		return err
	}
	if err := writeAndInit(ctx, tfws, cctx.Workspace); err != nil {
		return err
	}
	// Auto-layer the trial phase's applied-tfvars replay (validator
	// Issue 3, round-3) as the lowest-precedence var-file so bare
	// `plan -w <ws>` succeeds against a workspace that has been applied.
	// Returns nil when no snapshot exists → byte-identical to prior
	// behaviour.
	appliedVF := LayerAppliedTFVars(in.Workspace, "trial")
	// Pre-empt terraform's bare "No value for required variable" with an
	// actionable roksbnkctl-level message when neither a snapshot, a
	// --var-file, nor an init --var-file-seeded terraform.tfvars.user is
	// available (validator Issue 3 option (b) + Sprint 19 init --var-file).
	if err := RequireSnapshotOrVarFile(appliedVF, in.VarFiles, tfws.HasUserTFVars(), cctx.Workspace.Prefix != "", "trial", "plan"); err != nil {
		return err
	}
	varFiles := append(append([]string{}, appliedVF...), in.VarFiles...)
	fmt.Fprintln(os.Stderr, "→ terraform plan")
	if in.PlanOut != "" {
		return planToFile(ctx, tfws, in, varFiles)
	}
	_, err = tfws.Plan(ctx, varFiles...)
	return err
}

// planToFile saves the plan to a binary plan file (for `apply --plan`) plus a
// human-readable <file>.txt copy (Mame's "get the plan's result output in a file").
// The path is resolved to absolute so the artifacts land where the operator runs
// from, not inside the embedded terraform source dir.
func planToFile(ctx context.Context, tfws *tf.Workspace, in *LifecycleInputs, varFiles []string) error {
	planPath, err := filepath.Abs(in.PlanOut)
	if err != nil {
		return err
	}
	changes, err := tfws.PlanTo(ctx, planPath, varFiles...)
	if err != nil {
		return err
	}
	if raw, serr := tfws.ShowPlan(ctx, planPath); serr == nil {
		txt := planPath + ".txt"
		if werr := os.WriteFile(txt, []byte(raw), 0o644); werr == nil {
			fmt.Fprintf(os.Stderr, "✓ Saved plan: %s (reviewable copy: %s)\n", planPath, txt)
		}
	}
	if changes {
		fmt.Fprintf(os.Stderr, "  Review it, then apply EXACTLY this plan:\n    roksbnkctl apply -w %s --plan %s\n", in.Workspace, planPath)
	} else {
		fmt.Fprintln(os.Stderr, "  No changes — nothing to apply.")
	}
	return nil
}

// applyReviewedPlan applies a saved plan file EXACTLY (from `plan --out`), closing the
// gap where a bare apply re-plans and could apply something other than what was
// reviewed. No re-plan, no var-files (the plan captured them), no retry — a saved plan
// that no longer matches state/config is refused by terraform, which is the safety the
// review flow wants. The gateway-api admission-policy sweep still runs for the
// crd-installer window, exactly as the normal apply path.
func applyReviewedPlan(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, in *LifecycleInputs, recordVarFiles []string) error {
	planPath, err := filepath.Abs(in.PlanFile)
	if err != nil {
		return err
	}
	stop := startAdmissionPolicySweep(ctx, cctx, tfws)
	defer stop()
	fmt.Fprintf(os.Stderr, "→ terraform apply %s (reviewed plan)\n", planPath)
	return tfws.ApplyPlan(ctx, planPath, recordVarFiles...)
}

// RunApply = direct apply, no plan-and-confirm gate. For users who know
// what they're doing (CI, scripted flows, post-`roksbnkctl plan`).
func RunApply(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("apply"); err != nil {
		return err
	}
	// VarFiles is already chokepoint-normalized (PersistentPreRunE).
	if spec, ok := terraformBackendSpec(in); ok && spec != "local" {
		if in.PlanFile != "" {
			return fmt.Errorf("apply --plan requires the local backend (this workspace uses %q)", spec)
		}
		return runTerraformLifecycleDocker(ctx, in, spec, "apply")
	}
	cctx, tfws, err := openTF(ctx, in, true)
	if err != nil {
		return err
	}
	// Second-phase preamble (Issue 2 round 2 — phase handoff). See
	// RunTrialUp. extraVF is nil (byte-identical to the create path)
	// when there is no cluster-outputs.json.
	extraVF, err := writeAndInitSecondPhase(ctx, tfws, cctx.Workspace, in.Workspace, false, in.errOut())
	if err != nil {
		return err
	}
	// Auto-layer the trial-phase applied-tfvars replay (validator
	// Issue 3, round-3) as the lowest-precedence var-file so bare
	// `apply -w <ws>` re-applies with the var-file environment the
	// prior apply consumed. The Issue 2 round-2 override (extraVF) is
	// appended LAST so phase-architectural values (create_roks_cluster=
	// false, …) still win over the replay.
	appliedVF := LayerAppliedTFVars(in.Workspace, "trial")
	// Option (b): when neither a snapshot, a --var-file, nor an
	// init --var-file-seeded terraform.tfvars.user is available,
	// pre-empt terraform's raw missing-required-var error with the
	// actionable roksbnkctl-level message. extraVF (bnk-phase-override)
	// only exists on the *second* phase of a `up` and contains no
	// secrets / user inputs, so it doesn't count as "the user supplied
	// the inputs" for this gate.
	// A saved plan (`--plan`) already captured every variable, so the missing-var
	// gate doesn't apply — skip it for that path.
	if in.PlanFile == "" {
		if err := RequireSnapshotOrVarFile(appliedVF, in.VarFiles, tfws.HasUserTFVars(), cctx.Workspace.Prefix != "", "trial", "apply"); err != nil {
			return err
		}
	}
	varFiles := append(append(append([]string{}, appliedVF...), in.VarFiles...), extraVF...)
	if in.PlanFile != "" {
		if err := applyReviewedPlan(ctx, cctx, tfws, in, varFiles); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(os.Stderr, "→ terraform apply")
		if err := applyBNKWithAdmissionSweep(ctx, cctx, tfws, varFiles); err != nil {
			return err
		}
	}
	tryAutoKubeconfig(ctx, in, cctx, tfws)
	tryAutoJumphost(ctx, in, cctx, tfws)
	tryAutoClusterJumphosts(ctx, in, cctx, tfws)
	return nil
}

// RunDown is the presence-aware composite dispatcher for top-level
// `roksbnkctl down` (Sprint 28 three-phase split). It detects per-phase
// presence and routes per the architect's §2d/§3c teardown ordering:
//
//   - Legacy single-state → monolithic trial down (one terraform destroy
//     against the trial state; same v1.0.x behavior).
//   - nothing present     → error "nothing to destroy".
//   - otherwise           → ONE composite confirmation naming the present
//     phases, then destroy BNK ∥ Testing in parallel (independent of each
//     other), then Cluster after both (reverse-dependency order — both
//     reference the cluster VPC/TGW).
//
// Pure dispatcher; all destroy logic lives in the leaf helpers. The
// single-confirm-then-in.Auto=true pattern means the parallel legs + the
// cluster leg don't each re-prompt.
func RunDown(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("down"); err != nil {
		return err
	}
	// VarFiles is already chokepoint-normalized (PersistentPreRunE).
	cctx, err := config.New(in.Workspace)
	if err != nil {
		return err
	}
	// Same distinction as `cluster down`: an uninitialised workspace is an
	// error, an existing-but-empty one is success (#89).
	if cctx.Workspace == nil {
		return config.WorkspaceNotReady(cctx.WorkspaceName)
	}
	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	// ClusterResidual covers a workspace whose cluster resource is already gone
	// but whose network (VPC/TGW) lingers from a partially-failed teardown —
	// `down` must still resume and finish it, not report "nothing to destroy".
	// Same rule as the per-phase downs: nothing to do is SUCCESS. `down` is the
	// composite an automated teardown reaches for, and pres.Any() is false only
	// when no phase has any state at all — so there is nothing to step over.
	// Erroring here made a clean workspace look like a failed teardown (#89).
	if !pres.Any() && !pres.ClusterResidual {
		fmt.Fprintln(os.Stderr, "✓ Nothing to destroy in this workspace — no phase has any state.")
		return nil
	}
	// The Gateway phase's CRs (F5BnkGateway, Egress, SnatPool, StaticRoutes)
	// live in the BNK namespace, so the BNK leg of this composite destroy would
	// hang on their finalizers. The composite `down` covers Cluster/BNK/Testing;
	// the Gateway phase is separate and optional — tear it down explicitly,
	// first. Mirrors the `bnk down` and `cluster down` guards.
	if pres.Gateway {
		return errors.New("the Gateway phase has resources — its CRs live in the BNK namespace and would block the BNK teardown. Run `roksbnkctl gateway down` first, then `roksbnkctl down`")
	}
	// The FLP is likewise a separate, optional phase the composite does not cover.
	// Destroying the cluster out from under it would strand state-flp/ pointing at
	// resources that no longer exist (its helm release + secrets live in the
	// cluster), so a later `flp down` could never reconcile. Tear it down first —
	// while the cluster is still up. Mirrors the Gateway guard and `cluster down`.
	if pres.FLP {
		return errors.New("the F5 License Proxy phase has resources — the composite `down` does not cover it, and destroying the cluster would orphan its state. Run `roksbnkctl flp down` first, then `roksbnkctl down`")
	}

	// Compose the confirmation copy from the present phases.
	var phases []string
	if pres.BNK {
		phases = append(phases, "BNK trial")
	}
	if pres.Testing {
		phases = append(phases, "testing jumphosts")
	}
	if pres.TGW {
		phases = append(phases, "transit gateway connection (detach only)")
	}
	if pres.Cluster || pres.ClusterResidual {
		phases = append(phases, "cluster (ROKS + transit gateway + registry COS)")
	}
	if !in.Auto {
		fmt.Fprintf(os.Stderr,
			"This will destroy the following phases for workspace %q: %s.\n",
			cctx.WorkspaceName, strings.Join(phases, ", "))
		if !in.PromptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
		in.Auto = true
	}

	// BNK ∥ Testing in parallel (independent), then Cluster after both.
	if pres.BNK || pres.Testing {
		bnkErr, testErr := newPrefixWriters(os.Stderr)
		g, gctx := errgroup.WithContext(ctx)
		if pres.BNK {
			bnkIn := *in
			bnkIn.Stderr = bnkErr
			g.Go(func() error {
				defer bnkErr.flush()
				return RunTrialDown(gctx, &bnkIn)
			})
		}
		if pres.Testing {
			testIn := *in
			g.Go(func() error {
				defer testErr.flush()
				return runTestingDown(gctx, &testIn, testErr)
			})
		}
		if err := g.Wait(); err != nil {
			return err
		}
	}

	// The Transit Gateway connection (state-tgw/) attaches the cluster VPC to an
	// EXISTING shared gateway — its own phase the composite otherwise wouldn't
	// touch. But the connection references the cluster VPC's CRN, so the
	// cluster-phase VPC delete FAILS while it exists ("VPC still has an attached
	// transit gateway connection"). Auto-disconnect it here, after BNK/Testing and
	// BEFORE the cluster, removing ONLY this cluster's connection (the gateway and
	// every other cluster's connection stay). Unlike the Gateway/FLP phases —
	// guarded because their teardown has cluster-namespace finalizer ordering the
	// composite won't automate — a TGW connection is a pure IBM resource with a
	// deterministic ordering, so automating it is safe. in.Auto is already true
	// here (set after the combined confirmation, or via --auto), so RunTGWDisconnect
	// won't re-prompt.
	if pres.TGW {
		if err := RunTGWDisconnect(ctx, in); err != nil {
			return fmt.Errorf("detaching the transit gateway before cluster teardown: %w", err)
		}
	}

	if pres.Cluster || pres.ClusterResidual {
		return in.RunClusterDown(ctx)
	}
	return nil
}

// RunTrialDown = destroy against the trial state dir with a
// confirmation gate (skipped on --auto). Leaf "trial phase down" used
// by the composite `RunDown` (on LegacySingle and Split) and `bnk
// down`.
//
// Preserves the v1.0.x docker-backend short-circuit — non-local
// backends dispatch through runTerraformLifecycleDocker.
func RunTrialDown(ctx context.Context, in *LifecycleInputs) error {
	// VarFiles is already chokepoint-normalized (PersistentPreRunE).
	if spec, ok := terraformBackendSpec(in); ok && spec != "local" {
		// The webhook deadlock (#208) is a property of the CLUSTER, not of which
		// process runs terraform, so the containerised path needs the same sweep
		// and it must happen BEFORE the destroy starts. tfws is nil here;
		// resolveClusterIdentity falls back to cluster-outputs.json, which is
		// what a second-phase workspace has.
		if dcctx, derr := config.New(in.Workspace); derr == nil {
			// Webhook first, then the drain — see the ordering note on the local
			// path below (#235). Same reasoning for the drain itself (#217): the
			// destroy ordering that orphans the CNEInstance is in the terraform
			// graph, which is identical whichever process runs it.
			sweepTeardownWebhooks(ctx, dcctx, nil, in.errOut())
			sweepBNKCustomResources(ctx, dcctx, nil, in.errOut())
		}
		return runTerraformLifecycleDocker(ctx, in, spec, "destroy")
	}
	cctx, tfws, err := openTF(ctx, in, true)
	if err != nil {
		return err
	}
	w := in.errOut()
	if !in.Auto {
		fmt.Fprintf(w, "This will destroy workspace %q's resources.\n", cctx.WorkspaceName)
		if !in.PromptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
	}
	// Re-assert the BNK-phase override (create_roks_cluster=false + the reused
	// cluster identity), layered LAST so it wins — exactly as `bnk up` and the
	// testing/gateway downs do. Without it, destroy renders
	// create_roks_cluster=true from config.yaml, the cert-manager / CNE /
	// license kubernetes+helm+kubectl providers resolve an empty host, and
	// terraform dials http://localhost. A replayed applied-tfvars snapshot is
	// NOT enough: it carries create_roks_cluster twice (true from config.yaml,
	// false from the override), and a stale or cross-phase-contaminated
	// snapshot can leave the `true` winning — a clean override file as the
	// final var-file is the deterministic fix. On a legacy single-state
	// workspace (no cluster-outputs.json) this is a no-op, so the behaviour is
	// unchanged there. Symmetric with RunTestingDown / RunGatewayDown.
	extraVF, err := writeAndInitSecondPhase(ctx, tfws, cctx.Workspace, in.Workspace, true, w)
	if err != nil {
		return err
	}
	// Remove the admission webhook served from the BNK namespace before the
	// destroy reaches it, or the namespace hangs in Terminating forever (#208).
	//
	// AND BEFORE THE DRAIN (#235). #217 put the drain first, arguing that "these
	// deletes go through f5validate-f5-bnk, and #208's sweep is what removes it".
	// That is backwards. Removing the ValidatingWebhookConfiguration means the
	// API server has NOTHING to call -- that is the point of removing it.
	//
	// The webhook is served BY the install being torn down: f5-validation-svc
	// selects app=f5-cne-controller, and its failurePolicy is Fail. So the moment
	// the controller is unavailable, the API server refuses every DELETE of a
	// k8s.f5.com / k8s.f5net.com object:
	//
	//	failed calling webhook "f5validate.f5net.com": no endpoints available
	//	for service "f5-validation-svc"
	//
	// which timed the drain out twice (4m per namespace), left the finalizers in
	// place, and produced the very namespace stall #217 existed to prevent.
	sweepTeardownWebhooks(ctx, cctx, tfws, w)

	// Then drain BNK's custom resources while FLO is still alive to finalize
	// them (#217). terraform deletes the CNEInstance without waiting and removes
	// FLO three seconds later, so the finalizers outlive their controller and the
	// namespace delete then blocks until the provider's timeout.
	sweepBNKCustomResources(ctx, cctx, tfws, w)

	// Auto-layer the trial phase's applied-tfvars replay as a low-precedence
	// var-file so bare `down -w <ws>` (no --var-file) destroys cleanly.
	// Returns nil when no snapshot exists.
	appliedVF := LayerAppliedTFVars(in.Workspace, "trial")
	// No snapshot, no --var-file, AND no init --var-file-seeded
	// terraform.tfvars.user → actionable error before terraform sees a stack
	// of bare missing-required-var lines.
	if err := RequireSnapshotOrVarFile(appliedVF, in.VarFiles, tfws.HasUserTFVars(), cctx.Workspace.Prefix != "", "trial", "down"); err != nil {
		return err
	}
	varFiles := append(append(append([]string{}, appliedVF...), in.VarFiles...), extraVF...)
	fmt.Fprintln(w, "→ terraform destroy")
	if err := destroyWithRetry(ctx, tfws, varFiles); err != nil {
		// A BNK namespace stuck Terminating is the thing that makes this destroy
		// fail: both namespaces are terraform-managed and the kubernetes provider
		// blocks on namespace deletion, so the delete times out and never
		// finishes. Nothing in the cluster can clear those finalizers any more —
		// their operator is already gone — so retrying alone loops forever.
		//
		// Free them, then retry ONCE. The repair reports whether it actually
		// drained anything; if it did not, the original error stands, because a
		// retry that changes nothing is just a slower failure.
		//
		// This is why the repair cannot live on the success path, where a first
		// version of it sat: on that path the failure it repairs has already
		// returned.
		if !freeStuckBNKNamespace(ctx, cctx, tfws, w) {
			return err
		}
		fmt.Fprintln(w, "→ terraform destroy (retry, after freeing the stuck namespace)")
		if rerr := destroyWithRetry(ctx, tfws, varFiles); rerr != nil {
			return rerr
		}
	}
	// The CWC licence secrets survive the destroy — terraform never created them,
	// so it has nothing to remove (#172). They break the NEXT install rather than
	// this teardown: licensing reads them, finds a previous activation, and does
	// not re-activate, which surfaces much later as a licence gate timing out on a
	// cluster that looks clean.
	//
	// Best-effort and after a SUCCESSFUL destroy only. If the destroy failed the
	// install is still there and its secrets are still live; deleting them then
	// would break a running system to tidy up after a failure.
	sweepLicenseSecrets(ctx, cctx, tfws, w)

	// Also on the success path: a destroy can succeed while leaving a namespace
	// draining behind it, and that still breaks the NEXT install rather than
	// this teardown. Cheap when there is nothing to do — it returns immediately
	// unless a namespace is actually Terminating.
	freeStuckBNKNamespace(ctx, cctx, tfws, w)
	return nil
}

// ── exported helper seams (consumed by the cli cluster-phase adapter) ─
//
// cluster_phase.go stays in internal/cli per the phase-1b scope and
// still calls the lifecycle preamble/apply/kubeconfig helpers. These
// exported wrappers keep that collaboration working across the new
// package boundary without cluster_phase.go changing, and without
// internal/orchestration importing internal/cli.

// WriteAndInit is the exported seam over writeAndInit (the
// tfvars-render + terraform-init preamble) for the cli cluster-phase
// adapter. Behavior identical to the pre-move package-private helper.
func WriteAndInit(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace) error {
	return writeAndInit(ctx, tfws, ws)
}

// ApplyWithRetry is the exported seam over applyWithRetry (bounded
// transient-failure retry around tfws.Apply) for the cli cluster-phase
// adapter. Behavior identical to the pre-move package-private helper.
func ApplyWithRetry(ctx context.Context, tfws *tf.Workspace, varFiles []string) error {
	return applyWithRetry(ctx, tfws, varFiles)
}

// DestroyWithRetry is the exported seam over destroyWithRetry (bounded
// transient-failure retry around tfws.Destroy) for the cli cluster-phase
// adapter's `cluster down`.
func DestroyWithRetry(ctx context.Context, tfws *tf.Workspace, varFiles []string) error {
	return destroyWithRetry(ctx, tfws, varFiles)
}

// TryAutoKubeconfig is the exported seam over tryAutoKubeconfig (the
// best-effort post-apply admin-kubeconfig fetch) for the cli
// cluster-phase adapter. Behavior identical to the pre-move
// package-private helper.
func TryAutoKubeconfig(ctx context.Context, in *LifecycleInputs, cctx *config.Context, tfws *tf.Workspace) {
	tryAutoKubeconfig(ctx, in, cctx, tfws)
}

// TryAutoClusterJumphosts is the exported seam over
// tryAutoClusterJumphosts (the best-effort per-AZ jumphost-target
// writer) for the cli adapter — the frozen
// auto_cluster_jumphosts_test.go pins its nil-guard contract via the
// thin cli shim. Behavior identical to the pre-move package-private
// helper.
func TryAutoClusterJumphosts(ctx context.Context, in *LifecycleInputs, cctx *config.Context, tfws *tf.Workspace) {
	tryAutoClusterJumphosts(ctx, in, cctx, tfws)
}

// ── shared helpers ──────────────────────────────────────────────────

// openTF loads the workspace config, resolves the API key (if needed),
// and opens a terraform workspace ready for init/plan/apply/destroy.
//
// needAPIKey controls whether ResolveAPIKey is called. plan technically
// reads the API key path-to-validation but real cluster fetches happen
// at apply time, so this is mostly a flag for documentation; we set it
// true everywhere right now.
func openTF(ctx context.Context, in *LifecycleInputs, needAPIKey bool) (*config.Context, *tf.Workspace, error) {
	cctx, err := config.New(in.Workspace)
	if err != nil {
		return nil, nil, err
	}
	if cctx.Workspace == nil {
		return nil, nil, fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}

	var apiKey string
	if needAPIKey {
		resolver := &cred.Resolver{
			Workspace: cctx.WorkspaceName,
			Source:    cctx.Workspace.IBMCloud.APIKeySource,
		}
		apiKey, err = resolver.IBMCloudAPIKey(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving API key: %w", err)
		}
	}

	stateDir, err := config.WorkspaceStateDir(cctx.WorkspaceName)
	if err != nil {
		return nil, nil, err
	}

	tfws, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, stateDir, apiKey, os.Stdout, in.errOut())
	if err != nil {
		return nil, nil, err
	}
	return cctx, tfws, nil
}

// guardRegistryMirror enforces the Sprint-29 air-gap precondition: a
// workspace that opts into a registry mirror (config.yaml registry: block)
// MUST have a populated mirror record (registry-mirror.json with both the
// chart and image hosts) before the BNK phase deploys. Otherwise BNK would be
// rendered against far_repo_url, which an air-gapped cluster cannot reach.
//
// Off the mirror path (ws.Registry == nil) it returns nil immediately —
// behavior is unchanged for every non-air-gap workspace.
func guardRegistryMirror(workspaceName string, ws *config.Workspace) error {
	if ws == nil || ws.Registry == nil {
		return nil
	}
	m, err := config.ReadRegistryMirror(workspaceName)
	if err != nil {
		if errors.Is(err, config.ErrNoRegistryMirror) {
			return fmt.Errorf("a registry mirror is configured for this workspace but this workspace has no record of it.\n" +
				"  If the mirror still needs filling:      roksbnkctl registry replicate   (needs the FAR source)\n" +
				"  If it is already populated elsewhere:   roksbnkctl registry adopt       (no source access needed)")
		}
		return fmt.Errorf("reading registry-mirror record: %w", err)
	}
	if m.ChartHost == "" || m.ImageHost == "" {
		return fmt.Errorf("the registry-mirror record is incomplete (missing %s) — re-run `roksbnkctl registry replicate`, or `roksbnkctl registry adopt` if the mirror is already populated",
			missingMirrorHosts(m))
	}
	// Nor is "has both hosts" the same as "holds everything". A partial
	// replicate still writes a record — that is what lets a re-run resume — so
	// the record can name the right mirror, carry both hosts, and still be
	// missing artifacts the install will ask for (#150).
	//
	// Checked HERE rather than at tfvars render time, which was the first
	// attempt: WriteTFVarsForWorkspace is the shared preamble for up AND down,
	// so refusing there blocked `bnk down`, `cluster down`, `flp down`,
	// `gateway down`, `testing down` and `tgw disconnect` — stranding a ROKS
	// cluster, VPC, TGW and jumphosts because a registry mirror was missing
	// three images. Teardown does not read from the mirror and must never be
	// gated on it.
	if err := config.MirrorRecordIncompleteError(workspaceName, m); err != nil {
		return err
	}

	// Present and complete is not the same as current. The record describes the
	// mirror as it was last replicated; the config can have been repointed at a
	// different registry or repository since, and nothing re-probes on read. A
	// record for another mirror would redirect the whole install at it.
	return config.MirrorRecordMismatchError(workspaceName, ws, m)
}

// missingMirrorHosts names which mirror host(s) are absent, for the guard's
// error message.
func missingMirrorHosts(m *config.RegistryMirror) string {
	switch {
	case m.ChartHost == "" && m.ImageHost == "":
		return "chart_host and image_host"
	case m.ChartHost == "":
		return "chart_host"
	default:
		return "image_host"
	}
}

// writeAndInit renders tfvars and runs terraform init. Common preamble
// for plan/apply/up/down. Notes when a user-supplied tfvars override
// is going to be layered on top — visible cue so users aren't
// surprised when their values land.
func writeAndInit(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace) error {
	if err := tfws.WriteTFVars(ws); err != nil {
		return fmt.Errorf("writing tfvars: %w", err)
	}
	if tfws.HasUserTFVars() {
		fmt.Fprintf(os.Stderr, "→ Layering user tfvars from %s (overrides config.yaml-derived values)\n", tfws.UserTFVarsPath())
	}
	fmt.Fprintln(os.Stderr, "→ terraform init")
	return tfws.Init(ctx)
}

// tryAutoKubeconfig fetches the admin kubeconfig from IBM Cloud and
// writes it to $KUBECONFIG (or ~/.kube/config). Best-effort: any error
// is logged as a warning rather than failing the parent command —
// `roksbnkctl up` succeeded if terraform succeeded; the kubeconfig is a
// convenience the user can still grab via `roksbnkctl kubeconfig --download`.
//
// Skipped entirely with --no-kubeconfig.
func tryAutoKubeconfig(ctx context.Context, in *LifecycleInputs, cctx *config.Context, tfws *tf.Workspace) {
	if in.NoKubeconfig {
		return
	}
	if cctx == nil || cctx.Workspace == nil {
		return
	}
	cluster, _ := resolveClusterIdentity(ctx, cctx, tfws)
	if cluster == "" {
		return
	}
	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: skipping kubeconfig fetch (api key): %v\n", err)
		return
	}
	ic, err := ibm.New(apiKey, cctx.Workspace.IBMCloud.Region)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: skipping kubeconfig fetch: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "→ Fetching admin kubeconfig for %q\n", cluster)
	body, err := ic.FetchClusterConfig(ctx, cluster)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: kubeconfig fetch failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "         (run `roksbnkctl kubeconfig --download` to retry)")
		return
	}
	// KubeconfigWritePath returns a writable target even when nothing
	// exists yet (tf.Open has pointed $KUBECONFIG at the writable
	// <base>/.kube/config), so this no longer warns "mkdir <HOME>:
	// permission denied" in a runner whose $HOME isn't writable.
	target := k8s.KubeconfigWritePath()
	if target == "" {
		fmt.Fprintln(os.Stderr, "warning: could not resolve a kubeconfig write path")
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: creating %s: %v\n", filepath.Dir(target), err)
		return
	}
	if err := os.WriteFile(target, body, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: writing %s: %v\n", target, err)
		return
	}
	fmt.Fprintf(os.Stderr, "✓ Wrote kubeconfig to %s\n", target)

	// Also emit a token-based, fully self-contained kubeconfig for BNK Forge
	// registration + the cheap IAM-token refresh gate (Deliverable A). This
	// is an ADDITION — the admin cert-based config above is untouched.
	writeForgeKubeconfig(ic, cluster, body)
}

// writeForgeKubeconfig derives a portable, self-contained CERT-based kubeconfig
// from the just-fetched admin kubeconfig (server + CA-if-any + admin client
// cert/key) and writes it to config.ForgeKubeconfigPath(). This is the file
// BNK Forge registers the cluster from; the freshness gate classifies it as
// cert-based and keeps it current by re-fetching the admin kubeconfig.
//
// IBM ROKS is Red Hat OpenShift: its API server authenticates via OpenShift
// OAuth tokens or client certificates — NOT raw IBM IAM bearer tokens, which it
// rejects with 401. Earlier versions stamped an IAM token here; that produced a
// forge kubeconfig that BNK Forge registered but could not authenticate with.
// The admin client cert/key authenticate directly, so we carry those instead.
//
// Best-effort: a failure is a warning, never a failed `cluster up` — the admin
// kubeconfig is already written and the cluster is up.
func writeForgeKubeconfig(_ *ibm.Client, cluster string, adminKubeconfig []byte) {
	path, err := config.ForgeKubeconfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: resolving forge kubeconfig path: %v\n", err)
		return
	}
	certKC, err := k8s.BuildCertKubeconfig(adminKubeconfig, cluster+"-admin")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: building forge kubeconfig: %v\n", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: creating %s: %v\n", filepath.Dir(path), err)
		return
	}
	if err := writeFileAtomic(path, certKC, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: writing %s: %v\n", path, err)
		return
	}
	fmt.Fprintf(os.Stderr, "✓ Wrote cert kubeconfig to %s (for BNK Forge registration)\n", path)
}

// tryAutoJumphost is the post-apply jumphost-target writer. When the
// upstream HCL provisions a TGW jumphost (testing_tgw_jumphost_ip + the
// jumphost_shared_key PEM at root), persist a `jumphost` entry under
// `targets:` so subsequent commands can `--on jumphost`.
//
// Best-effort: any failure (no outputs, parse error, save error) is
// logged as a warning and the parent command still succeeds — `up`
// passed because terraform passed; the target is a convenience.
//
// Idempotent: re-running on a workspace that already has a `jumphost`
// target overwrites the entry. The IP / PEM may legitimately change
// across destroy+recreate cycles, and we want known_hosts to follow
// — caller's responsibility to clean ~/.roksbnkctl/known_hosts when
// the IP rotates (PRD 01 open question; not auto-handled in v0.7).
func tryAutoJumphost(ctx context.Context, in *LifecycleInputs, cctx *config.Context, tfws *tf.Workspace) {
	if cctx == nil || cctx.Workspace == nil || tfws == nil {
		return
	}
	outputs, err := tfws.Output(ctx)
	if err != nil {
		// Not fatal — the cluster may be partway up, or this is a
		// no-jumphost configuration.
		return
	}
	ip := in.StringOutput(outputs, "testing_tgw_jumphost_ip")
	keyPEM := in.StringOutput(outputs, "jumphost_shared_key")
	if ip == "" || ip == "TGW jumphost not created" || keyPEM == "" {
		return
	}
	cfg := config.TargetCfg{
		Host:      ip,
		User:      "ubuntu", // upstream HCL provisions Ubuntu cloud-init users
		KeySource: "tf-output:jumphost_shared_key",
	}
	if err := remote.SetTarget(cctx.WorkspaceName, "jumphost", cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: writing jumphost target: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "✓ Auto-registered target jumphost (%s); use `roksbnkctl --on jumphost ...`\n", ip)
}

// tryAutoClusterJumphosts is the per-AZ sibling of tryAutoJumphost
// (Sprint 13 Issue 3 / PRD 09). When the deploy provisions one cluster
// jumphost per cluster-VPC AZ (testing_create_cluster_jumphosts = true),
// it registers a `jumphost-<zone>` target per AZ from the
// {zone => fip} terraform output, reusing the same shared key the TGW
// jumphost uses (KeySource "tf-output:jumphost_shared_key" — no new
// output needed).
//
// Stale-target handling is UPSERT-ONLY: orphaned `jumphost-<oldzone>` entries
// (zone removed / testing_create_cluster_jumphosts flipped false) linger
// until manual `targets remove`. Reconciling them automatically would need a
// prefix sweep or an `auto:` schema marker to tell a generated target from a
// hand-added one; neither exists, so removal stays manual.
//
// Best-effort, mirroring tryAutoJumphost: any failure logs a single
// `warning:` to stderr and does NOT fail `up` (terraform succeeded;
// these targets are a convenience). No-op (no error, no warning noise)
// when testing_create_cluster_jumphosts = false / the output is absent
// or the `[]`-default empty map.
//
// Called immediately after tryAutoJumphost from the same post-`up`
// hook sites. SetTarget is idempotent/upsert, so a re-`up` after a FIP
// rotation refreshes the host values in place.
func tryAutoClusterJumphosts(ctx context.Context, in *LifecycleInputs, cctx *config.Context, tfws *tf.Workspace) {
	if cctx == nil || cctx.Workspace == nil || tfws == nil {
		return
	}
	outputs, err := tfws.Output(ctx)
	if err != nil {
		// Not fatal — cluster may be partway up, or this is a
		// no-cluster-jumphost configuration.
		return
	}
	// The root TF output that surfaces the per-zone FIP map is
	// `testing_cluster_jumphost_ips` (terraform/outputs.tf:82, value
	// `try(module.testing.testing_cluster_jumphost_public_ips, [])`).
	// The carried issue text names the *module* output
	// (`testing_cluster_jumphost_public_ips`); read the root name with
	// the module name as a defensive fallback (see closure note).
	fips := in.MapOutput(outputs, "testing_cluster_jumphost_ips")
	if len(fips) == 0 {
		fips = in.MapOutput(outputs, "testing_cluster_jumphost_public_ips")
	}
	if len(fips) == 0 {
		// No cluster jumphosts (testing_create_cluster_jumphosts=false,
		// output absent, or the `[]`-default empty map). Skip silently —
		// parity with the `ip == ""` guard in tryAutoJumphost.
		return
	}
	keyPEM := in.StringOutput(outputs, "jumphost_shared_key")
	if keyPEM == "" {
		// Same shared key as the TGW jumphost; if it's not present we
		// can't auth to these hosts — skip (no warning noise; the TGW
		// path already reported the same condition).
		return
	}

	// Stable order so the summary line + any warnings are deterministic.
	zones := make([]string, 0, len(fips))
	for z := range fips {
		zones = append(zones, z)
	}
	sort.Strings(zones)

	registered := make([]string, 0, len(zones))
	for _, zone := range zones {
		fip := fips[zone]
		if fip == "" {
			continue
		}
		name := "jumphost-" + zone
		cfg := config.TargetCfg{
			Host:      fip,
			User:      "ubuntu", // upstream HCL provisions Ubuntu cloud-init users
			KeySource: "tf-output:jumphost_shared_key",
		}
		if err := remote.SetTarget(cctx.WorkspaceName, name, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: writing %s target: %v\n", name, err)
			continue
		}
		registered = append(registered, name)
	}
	if len(registered) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr,
		"✓ Auto-registered %d per-AZ cluster jumphost target(s) (%s); use `roksbnkctl --on jumphost-<zone> ...`\n",
		len(registered), strings.Join(registered, ", "))
}

// resolveClusterIdentity figures out which cluster to fetch the kubeconfig for, and
// reports whether the identity is a cluster ID (unambiguous) or a NAME (which a
// duplicate-named cluster makes ambiguous). Order — IDs first, because a ROKS cluster
// name is NOT unique in an account, so a bare name can resolve to the wrong (even a
// dead orphan) cluster:
//
//  1. cluster-outputs.json `cluster_id` — authoritative, unambiguous, and written by
//     the CLUSTER phase, so it exists BEFORE the BNK phase (whose own outputs are not
//     yet written at sweep-start on a first apply). This is the identity that must be
//     preferred: it is immune to a duplicate cluster NAME.
//  2. This workspace's terraform output `roks_cluster_id` — also a real ID, but only
//     present post-apply.
//  3. `roks_cluster_name` output, then cctx.Workspace.Cluster.Name — NAME fallbacks,
//     usable but ambiguous if a duplicate-named cluster exists. byID=false so callers
//     that need a guaranteed-correct cluster (the admission sweep) can warn.
//
// Returns ("", false) if no source produced a usable identity.
func resolveClusterIdentity(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) (identity string, byID bool) {
	if cctx != nil {
		if out, err := config.ReadClusterOutputs(cctx.WorkspaceName); err == nil && out != nil && out.ClusterID != "" {
			return out.ClusterID, true
		}
	}
	if tfws != nil {
		if outputs, err := tfws.Output(ctx); err == nil {
			for _, k := range []struct {
				key  string
				isID bool
			}{{"roks_cluster_id", true}, {"roks_cluster_name", false}} {
				if om, ok := outputs[k.key]; ok && len(om.Value) > 0 {
					var s string
					if json.Unmarshal(om.Value, &s) == nil && s != "" {
						return s, k.isID
					}
				}
			}
		}
	}
	if cctx != nil && cctx.Workspace != nil && cctx.Workspace.Cluster.Name != "" {
		return cctx.Workspace.Cluster.Name, false
	}
	return "", false
}

// applyWithRetry wraps tfws.Apply with bounded retries on transient
// failures. Terraform's natural idempotence makes retry safe — already
// created resources are skipped on subsequent runs; only the failed
// null_resources / data sources re-execute.
//
// Triggers a retry on any of the heuristic patterns in looksTransient,
// up to applyMaxAttempts total. Sleeps applyRetryWait between attempts
// so the master endpoint or other timing-sensitive resources can settle.
func applyWithRetry(ctx context.Context, tfws *tf.Workspace, varFiles []string) error {
	var err error
	for attempt := 1; attempt <= applyMaxAttempts; attempt++ {
		err = tfws.Apply(ctx, varFiles...)
		if err == nil {
			return nil
		}
		if !looksTransient(err) {
			return err
		}
		if attempt == applyMaxAttempts {
			fmt.Fprintf(os.Stderr, "\n✗ apply still failing after %d attempts — giving up\n", applyMaxAttempts)
			return err
		}
		fmt.Fprintf(os.Stderr, "\n→ apply attempt %d hit a transient-looking failure; waiting %s and retrying...\n",
			attempt, applyRetryWait)
		select {
		case <-time.After(applyRetryWait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// destroyWithRetry wraps tfws.Destroy with bounded retries on transient
// failures — the shared looksTransient patterns PLUS the teardown-specific
// provider delete-races in looksTransientDestroy. Safe because destroy is
// idempotent: a resource already deleted is refreshed out of state on the
// next attempt, so the retry RESUMES the teardown from where the partial
// destroy stopped (e.g. continues on to the VPC after a public gateway that
// vanished mid-destroy) rather than leaking the un-reached resources.
func destroyWithRetry(ctx context.Context, tfws *tf.Workspace, varFiles []string) error {
	var err error
	for attempt := 1; attempt <= destroyMaxAttempts; attempt++ {
		err = tfws.Destroy(ctx, varFiles...)
		if err == nil {
			return nil
		}
		if !looksTransient(err) && !looksTransientDestroy(err) {
			return err
		}
		if attempt == destroyMaxAttempts {
			fmt.Fprintf(os.Stderr, "\n✗ destroy still failing after %d attempts — giving up\n", destroyMaxAttempts)
			return err
		}
		fmt.Fprintf(os.Stderr, "\n→ destroy attempt %d hit a transient-looking failure; waiting %s and retrying...\n",
			attempt, destroyRetryWait)
		select {
		case <-time.After(destroyRetryWait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// looksTransientDestroy adds teardown-specific retryable patterns on top of
// looksTransient. The IBM provider surfaces a delete operation whose target is
// ALREADY gone (typically because terraform's own parallel destroy removed it
// first, or eventual-consistency lag) as "<Operation>WithContext failed: …
// not found". On retry, terraform refreshes the missing resource out of state
// and proceeds — so for DESTROY (never apply) these are safe to retry. The
// "WithContext failed" + "not found" pairing keeps this narrow to real IBM
// provider operation races, not arbitrary "not found" text.
func looksTransientDestroy(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	if strings.Contains(s, "WithContext failed") && strings.Contains(s, "not found") {
		return true
	}
	for _, pat := range []string{
		"Public Gateway not found",
		"Cannot delete VPC",        // children still detaching — settles on retry
		"is still attached",        // subnet/gateway attachment lingering
		"please retry the request", // generic IBM throttle/eventual-consistency hint
	} {
		if strings.Contains(s, pat) {
			return true
		}
	}
	return false
}

// terraformBackendSpec resolves the execution backend for terraform.
// Mirrors `resolveBackendSpecWith` for the exec passthrough commands
// but lives here because the lifecycle commands don't go through the
// same dispatch shape (they use terraform-exec on the host by default,
// not exec.Backend).
//
// Returns the spec ("local" | "docker") and a bool reporting whether
// the user explicitly opted into a non-default backend (so the caller
// can short-circuit only when it matters).
//
// PRD 03 §"terraform" + PLAN.md Sprint 5 row 8: terraform supports
// `local` and `docker` in v0.9; `k8s` and `ssh` are deferred to v1.x
// (state-handling design is open). Errors clearly when the user picks
// a deferred backend.
func terraformBackendSpec(in *LifecycleInputs) (string, bool) {
	cctx, _ := config.New(in.Workspace)
	spec := in.Backend
	if spec == "" && cctx != nil && cctx.Workspace != nil {
		if entry, ok := cctx.Workspace.Exec["terraform"]; ok && entry.Backend != "" {
			spec = entry.Backend
		}
	}
	if spec == "" {
		spec = "local"
	}
	return spec, spec != "local"
}

// runTerraformLifecycleDocker runs the named lifecycle phase
// ("plan" | "apply" | "destroy" | "up") through the docker backend.
// `up` is a composite — it runs plan, prompts (unless --auto), then
// runs apply.
//
// The flow:
//
//  1. Open the terraform Workspace (fetches embedded source, writes
//     auto-rendered terraform.tfvars, writes the backend override
//     pointing at the per-workspace state file). This re-uses the
//     local-backend's preparation helpers — the docker backend only
//     overrides the *execution*, not the workspace prep.
//  2. Resolve the IBM Cloud API key via the Resolver, ensure
//     TF_VAR_ibmcloud_api_key is in the host process env (the
//     credential bare-name passthrough in docker.go propagates it
//     into the container).
//  3. Build the docker run argv: `terraform <subcmd> <flags>`. The
//     state dir is bind-mounted at /state read-write; WorkDir is
//     /state/tf-source/embedded-terraform; UID/GID is the host user
//     so the state file ends up host-user-owned.
//  4. Dispatch via exec.ResolveBackend("docker") + Run.
//
// PRD 03 §"terraform" + chapter 17 §"terraform docker subsection" +
// chapter 31 §"embedded-terraform layout".
func runTerraformLifecycleDocker(ctx context.Context, in *LifecycleInputs, spec, phase string) error {
	switch spec {
	case "docker":
		// supported
	case "k8s":
		return errors.New("terraform --backend k8s is deferred to v1.x; see PRD 03 §\"State concerns\". For now, use --backend local (host) or --backend docker (containerised)")
	default:
		if strings.HasPrefix(spec, "ssh:") {
			return errors.New("terraform --backend ssh:<target> is deferred to v1.x; see PRD 03 §\"State concerns\". For now, use --backend local (host) or --backend docker (containerised)")
		}
		return fmt.Errorf("unsupported --backend %q for terraform (want local | docker)", spec)
	}

	// Step 1+2: open the workspace (prep state dir, fetch source,
	// write tfvars + backend override) and resolve creds. This calls
	// `tf.Open` which performs the side-effect of os.Setenv'ing
	// TF_VAR_ibmcloud_api_key on the host process — that's the
	// channel the docker backend's bare-name env passthrough uses.
	cctx, tfws, err := openTF(ctx, in, true)
	if err != nil {
		return err
	}
	if err := writeAndInit(ctx, tfws, cctx.Workspace); err != nil {
		return fmt.Errorf("preparing terraform workspace: %w", err)
	}

	// Resolve the credential explicitly so the docker dispatch can
	// stamp it on RunOpts.Credentials (in addition to the os.Setenv
	// path tf.Open already did).
	resolver := &cred.Resolver{
		Workspace:      cctx.WorkspaceName,
		NonInteractive: true,
		Source:         cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return fmt.Errorf("resolving IBM Cloud API key: %w", err)
	}

	// Map the lifecycle phase to one or more terraform subcommands.
	// `up` is a composite (plan + confirm + apply); `plan`/`apply`/
	// `destroy` are single-shot.
	switch phase {
	case "plan":
		return dockerTerraform(ctx, in, cctx, tfws, apiKey, []string{"plan"})
	case "apply":
		return dockerTerraform(ctx, in, cctx, tfws, apiKey, []string{"apply", "-auto-approve"})
	case "destroy":
		if !in.Auto {
			fmt.Fprintf(os.Stderr, "This will destroy workspace %q's resources.\n", cctx.WorkspaceName)
			if !in.PromptYesNo("Continue?", false) {
				return errors.New("aborted")
			}
		}
		return dockerTerraform(ctx, in, cctx, tfws, apiKey, []string{"destroy", "-auto-approve"})
	case "up":
		fmt.Fprintln(os.Stderr, "→ terraform plan (docker)")
		if err := dockerTerraform(ctx, in, cctx, tfws, apiKey, []string{"plan"}); err != nil {
			return err
		}
		if !in.Auto && !in.PromptYesNo("Apply this plan?", false) {
			return errors.New("aborted")
		}
		fmt.Fprintln(os.Stderr, "→ terraform apply (docker)")
		if err := dockerTerraform(ctx, in, cctx, tfws, apiKey, []string{"apply", "-auto-approve"}); err != nil {
			return err
		}
		// Post-apply convenience hooks. Output() is read via host
		// terraform-exec; the state file landed at the same path
		// regardless of who wrote it, so this works the same as the
		// local path.
		tryAutoKubeconfig(ctx, in, cctx, tfws)
		tryAutoJumphost(ctx, in, cctx, tfws)
		tryAutoClusterJumphosts(ctx, in, cctx, tfws)
		return nil
	default:
		return fmt.Errorf("internal: unknown terraform phase %q", phase)
	}
}

// dockerTerraform dispatches one `terraform <subcmd>` invocation
// through the docker backend with the workspace state bind-mount and
// host-user UID/GID.
//
// The tfvars chain (auto-rendered + optional terraform.tfvars.user +
// --var-file) is layered identically to the local-backend path — the
// auto-rendered file is in stateDir (/state in the container) so we
// reference it via /state/terraform.tfvars.
func dockerTerraform(ctx context.Context, in *LifecycleInputs, cctx *config.Context, tfws *tf.Workspace, apiKey string, subcmd []string) error {
	be, err := execbackend.ResolveBackend("docker")
	if err != nil {
		return err
	}

	// Workspace state path layout (matches `tf.Open` + tf.Workspace):
	//
	//   stateDir/
	//     terraform.tfvars              (auto-rendered)
	//     tf-source/
	//       embedded-terraform/         (the .tf files)
	//         roksbnkctl_backend_override.tf
	//
	// `dockerTerraformExec` recomputes the container source dir from
	// the workspace; here we only need the var-file argv assembled.

	// Var-file argv, expressed as paths inside the container. Order
	// matches the local-backend's varFiles helper:
	//   1. auto-rendered terraform.tfvars (in state dir)
	//   2. terraform.tfvars.user (workspace-persistent override)
	//   3. extra --var-file flags
	args := append([]string(nil), subcmd...)
	args = append(args, "-var-file=/state/terraform.tfvars")
	if tfws.HasUserTFVars() {
		// terraform.tfvars.user lives outside stateDir (the workspace
		// dir), so we bind-mount its parent and reference it.
		args = append(args, "-var-file=/state/terraform.tfvars.user")
	}
	for _, vf := range in.VarFiles {
		// User-supplied --var-file paths are already on the host
		// filesystem; project them via the container fixture mount
		// (we'd need to bind-mount each parent, complicating things).
		// For v0.9 require absolute paths and surface a clearer error
		// — full pass-through arrives in a v1.x polish pass.
		if !filepath.IsAbs(vf) {
			return fmt.Errorf("--var-file %q must be absolute when --backend docker (paths are projected into the container at the same location); use absolute paths or run with --backend local", vf)
		}
		args = append(args, "-var-file="+vf)
	}

	// Subcommand-specific flag tweaks. `init` runs once at the start
	// of every dispatch (terraform requires .terraform/ to be set up
	// before plan/apply); we shell-pre-`init` here rather than ask
	// users to run two commands.
	//
	// Init is its own docker invocation — keeps the args simple.
	if err := dockerTerraformInit(ctx, be, cctx, tfws, apiKey); err != nil {
		return fmt.Errorf("terraform init: %w", err)
	}

	return dockerTerraformExec(ctx, in, be, cctx, tfws, apiKey, args)
}

// dockerTerraformInit runs `terraform init -reconfigure` via the
// docker backend. Split out because every plan/apply/destroy needs
// the .terraform/ directory provisioned first, and the init args
// don't take -var-file.
func dockerTerraformInit(ctx context.Context, be execbackend.Backend, cctx *config.Context, tfws *tf.Workspace, apiKey string) error {
	return dockerTerraformExec(ctx, nil, be, cctx, tfws, apiKey, []string{"init", "-reconfigure"})
}

// dockerTerraformExec is the low-level docker dispatch for a
// terraform subcommand. Mounts the workspace state dir at /state RW,
// pins the container UID/GID to the host user (so state files are
// host-owned), and ensures TF_VAR_ibmcloud_api_key is set in the
// process env for the cred bare-name passthrough.
func dockerTerraformExec(ctx context.Context, in *LifecycleInputs, be execbackend.Backend, cctx *config.Context, tfws *tf.Workspace, apiKey string, subargv []string) error {
	uid, gid := hostUIDGID()
	runAsUser := ""
	if uid != "" {
		runAsUser = uid
		if gid != "" {
			runAsUser += ":" + gid
		}
	}

	stateDir := tfws.StateDir()
	srcRel := strings.TrimPrefix(tfws.SourceDir(), stateDir)
	srcRel = strings.TrimPrefix(srcRel, string(os.PathSeparator))
	containerSrcDir := filepath.ToSlash(filepath.Join("/state", srcRel))

	hostMounts := []execbackend.HostMount{{
		HostPath:      stateDir,
		ContainerPath: "/state",
		ReadOnly:      false,
	}}
	// Project terraform.tfvars.user (lives in the workspace dir, one
	// level above stateDir) so the in-container -var-file path resolves.
	if tfws.HasUserTFVars() {
		userPath := tfws.UserTFVarsPath()
		hostMounts = append(hostMounts, execbackend.HostMount{
			HostPath:      userPath,
			ContainerPath: "/state/terraform.tfvars.user",
			ReadOnly:      true,
		})
	}
	// Pass any user-supplied --var-file as bind mounts at the same
	// absolute path inside the container so their existing absolute
	// paths in -var-file=<path> resolve unchanged. init dispatches with
	// no LifecycleInputs (no -var-file on init); guard the nil.
	if in != nil {
		for _, vf := range in.VarFiles {
			if !filepath.IsAbs(vf) {
				continue // dockerTerraform validated these earlier
			}
			hostMounts = append(hostMounts, execbackend.HostMount{
				HostPath:      vf,
				ContainerPath: vf,
				ReadOnly:      true,
			})
		}
	}

	creds := &execbackend.Credentials{
		IBMCloudAPIKey: apiKey,
	}

	argv := append([]string{"terraform"}, subargv...)
	rc, err := be.Run(ctx, argv, execbackend.RunOpts{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		WorkDir:     containerSrcDir,
		HostMounts:  hostMounts,
		RunAsUser:   runAsUser,
		Credentials: creds,
		Env: []string{
			"TF_DATA_DIR=/state/terraform",
			"TF_IN_AUTOMATION=1",
		},
	})
	if err != nil && rc == 0 {
		return err
	}
	if rc != 0 {
		return fmt.Errorf("terraform %s exited %d (docker backend)", subargv[0], rc)
	}
	return nil
}

// hostUIDGID returns the current process's UID + GID as strings, or
// ("","") on platforms where it isn't meaningful (Windows). The
// docker backend uses these to set the container's `--user`, so
// terraform-in-container writes the state file with host-user
// ownership. On Linux/macOS we expect both to be populated.
func hostUIDGID() (string, string) {
	u, err := user.Current()
	if err != nil {
		return "", ""
	}
	return u.Uid, u.Gid
}

// looksTransient reports whether an apply error matches one of the
// known apply-time race or transient-network patterns. Heuristic, not
// exhaustive — false negatives just mean the user retries manually
// like before, false positives are harmless because terraform's apply
// is naturally idempotent for resources already in state.
//
// Cases covered:
//   - "exit status 7" — curl couldn't connect (master endpoint not yet
//     propagated; the cneinstance SCC binding curls hit this)
//   - "Connection refused" / "i/o timeout" / "no route to host" /
//     "network is unreachable" / "TLS handshake timeout" — generic
//     transient-network class. WSL2 / VPN flapping / IBM IAM blips all
//     surface as one of these.
//   - "no such host" — DNS hiccup (transient, almost always self-heals)
//   - "failed to dial" — Go net stdlib transient
//   - "to download the config doesn't exist" — the IBM provider's
//     ibm_container_cluster_config target dir is missing (we pre-create
//     it now, but the safety net stays for older state)
func looksTransient(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, pat := range []string{
		"exit status 7",
		"Connection refused",
		"connection refused",
		"i/o timeout",
		"no route to host",
		"network is unreachable",
		"no such host",
		"TLS handshake timeout",
		"failed to dial",
		"to download the config doesn't exist",
		// The F5 validating webhook (f5validate.f5net.com / f5-validation-svc)
		// comes up as part of the CNEInstance reconcile; an F5SPKVlan (or other
		// F5 CR) applied before its pod is serving TLS fails with one of these.
		// It's a readiness race — a retry after the wait succeeds.
		"failed calling webhook",
		"server gave HTTP response to HTTPS client",
		// On a cold BNK deploy the ResourceQuota controller (f5-single-license-quota)
		// hasn't computed the quota's status before the License CR is applied, so the
		// admission check rejects it with "status unknown for quota". It resolves the
		// instant the quota controller observes the namespace — a retry succeeds.
		"status unknown for quota",
	} {
		if strings.Contains(s, pat) {
			return true
		}
	}
	return false
}
