package orchestration

// Sprint 28 three-phase split — the Testing phase (the jumphost
// infrastructure) as an independent phase against state-testing/.
//
// The Testing phase is pure IBM VPC (the `testing` module declares only
// ibm providers — no helm/kubectl/kubernetes), so it has NO dependency on
// the cluster's k8s API. It depends on the cluster ONLY for the network
// (the cluster VPC id + the transit gateway name), which it consumes from
// cluster-outputs.json via the testing-phase-override (second_phase_reuse.go).
//
// This file owns: openTestingTF (the state-testing/ workspace opener),
// RunTestingUp / RunTestingDown (mirrors RunTrialUp / RunTrialDown), the
// jumphost SSH-target seeding (moved here from the BNK/trial phase —
// tryAutoJumphost / tryAutoClusterJumphosts), the pre-Sprint-28 jumphost
// migration (evict module.testing.* from state/ into state-testing/), and
// the prefix-writer + errgroup helpers the parallel BNK ∥ Testing up/down
// in lifecycle.go uses.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// openTestingTF mirrors openTF but targets the testing state dir
// (state-testing/) instead of the BNK state dir. Like the cluster-phase
// opener, the testing phase has its own forced override
// (testing-phase-override.tfvars) — that is written by
// writeAndInitTestingPhase, not here, so its path can be appended to the
// var-file chain.
//
// The optional stderr writer lets the parallel up/down path hand the
// phase a line-prefixed writer ([testing] ...) so two concurrent applies
// interleave readably; pass nil for the serial path (→ os.Stderr).
func openTestingTF(ctx context.Context, in *LifecycleInputs, stderr *prefixWriter) (*config.Context, *tf.Workspace, error) {
	cctx, err := config.New(in.Workspace)
	if err != nil {
		return nil, nil, err
	}
	if cctx.Workspace == nil {
		return nil, nil, fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}

	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving API key: %w", err)
	}

	stateDir, err := config.WorkspaceTestingStateDir(cctx.WorkspaceName)
	if err != nil {
		return nil, nil, err
	}

	errOut := errWriter(stderr)
	tfws, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, stateDir, apiKey, os.Stdout, errOut)
	if err != nil {
		return nil, nil, err
	}
	return cctx, tfws, nil
}

// writeAndInitTestingPhase is the testing-phase preamble. It renders the
// normal terraform.tfvars and, when this workspace has a
// cluster-outputs.json (the cluster phase completed / a cluster is
// registered), writes the forced testing-phase-override.tfvars and returns
// its path so the caller appends it LAST to the var-file chain. With no
// cluster-outputs.json (fresh/legacy) extraVF is nil and the run is
// byte-identical to the create path.
func writeAndInitTestingPhase(ctx context.Context, tfws *tf.Workspace, ws *config.Workspace, workspace string, out *prefixWriter) ([]string, error) {
	w := errWriter(out)
	if err := tfws.WriteTFVars(ws); err != nil {
		return nil, fmt.Errorf("writing tfvars: %w", err)
	}
	if tfws.HasUserTFVars() {
		fmt.Fprintf(w, "→ Layering user tfvars from %s (overrides config.yaml-derived values)\n", tfws.UserTFVarsPath())
	}

	co, err := loadReuseClusterOutputs(workspace)
	if err != nil {
		return nil, err
	}

	var extra []string
	if co != nil && co.VPCID != "" {
		overridePath, werr := writeTestingPhaseOverride(tfws, co)
		if werr != nil {
			return nil, werr
		}
		extra = []string{overridePath}
		fmt.Fprintf(w,
			"→ Testing-phase handoff: cluster-outputs.json present — provisioning the jumphosts "+
				"against the existing cluster VPC %s + transit gateway (create_roks_cluster=false, "+
				"deploy_bnk=false). The testing phase manages ONLY the jumphost infrastructure.\n",
			co.VPCID)
	}

	fmt.Fprintln(w, "→ terraform init")
	if err := tfws.Init(ctx); err != nil {
		return nil, err
	}
	return extra, nil
}

// RunTestingUp is the serial entry point for `roksbnkctl testing up` (the
// CLI adapter). Prints to os.Stderr and is allowed to prompt.
func RunTestingUp(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("testing up"); err != nil {
		return err
	}
	return runTestingUp(ctx, in, nil)
}

// RunTestingDown is the serial entry point for `roksbnkctl testing down`.
func RunTestingDown(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("testing down"); err != nil {
		return err
	}
	return runTestingDown(ctx, in, nil)
}

// runTestingUp = plan + confirm + apply against state-testing/, with the
// testing-phase override + the cluster-outputs handoff, then the jumphost
// SSH-target seeding. The leaf "testing phase up" used by both the
// composite RunUp (parallel BNK ∥ Testing) and `testing up`.
//
// out is non-nil only on the parallel path, where it carries the
// [testing] line prefix + the shared output mutex; nil → serial path
// (os.Stderr, prompts allowed).
func runTestingUp(ctx context.Context, in *LifecycleInputs, out *prefixWriter) error {
	changes, apply, err := prepareTestingUp(ctx, in, out)
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

// prepareTestingUp does the open + render + init + plan for the Testing
// phase and reports whether the plan has changes, returning an apply
// closure (terraform apply + jumphost SSH-target seeding) when it does. The
// closure takes its own context so the parallel orchestrator can cancel a
// sibling's apply. Symmetric with prepareBNKUp — see its doc for why plan
// and apply are split (parallel plan-both → confirm-each → apply-approved).
func prepareTestingUp(ctx context.Context, in *LifecycleInputs, out *prefixWriter) (bool, func(context.Context) error, error) {
	w := errWriter(out)
	cctx, tfws, err := openTestingTF(ctx, in, out)
	if err != nil {
		return false, nil, err
	}
	extraVF, err := writeAndInitTestingPhase(ctx, tfws, cctx.Workspace, in.Workspace, out)
	if err != nil {
		return false, nil, err
	}
	varFiles := append(append([]string{}, in.VarFiles...), extraVF...)

	fmt.Fprintln(w, "→ terraform plan (testing phase)")
	changes, err := tfws.Plan(ctx, varFiles...)
	if err != nil {
		return false, nil, err
	}
	if !changes {
		fmt.Fprintln(w, "✓ no changes")
		tryAutoJumphost(ctx, in, cctx, tfws)
		tryAutoClusterJumphosts(ctx, in, cctx, tfws)
		return false, nil, nil
	}
	apply := func(actx context.Context) error {
		fmt.Fprintln(w, "→ terraform apply (testing phase)")
		if err := applyWithRetry(actx, tfws, varFiles); err != nil {
			return err
		}
		// SSH-target seeding now lives with the Testing phase (it owns the
		// jumphosts). Pre-Sprint-28 these ran after the trial apply.
		tryAutoJumphost(actx, in, cctx, tfws)
		tryAutoClusterJumphosts(actx, in, cctx, tfws)
		return nil
	}
	return true, apply, nil
}

// runTestingDown = destroy against state-testing/, leaving the cluster +
// BNK untouched. Leaf used by `testing down` and the composite RunDown
// (parallel BNK ∥ Testing). On the parallel path the composite has
// already taken the single confirmation and flipped in.Auto=true.
func runTestingDown(ctx context.Context, in *LifecycleInputs, out *prefixWriter) error {
	w := errWriter(out)
	cctx, tfws, err := openTestingTF(ctx, in, out)
	if err != nil {
		return err
	}
	if !in.Auto {
		fmt.Fprintf(w, "This will destroy the testing jumphosts for workspace %q (cluster + BNK untouched).\n", cctx.WorkspaceName)
		if !in.PromptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
	}
	extraVF, err := writeAndInitTestingPhase(ctx, tfws, cctx.Workspace, in.Workspace, out)
	if err != nil {
		return err
	}
	appliedVF := LayerAppliedTFVars(in.Workspace, "testing")
	if err := RequireSnapshotOrVarFile(appliedVF, in.VarFiles, tfws.HasUserTFVars(), cctx.Workspace.Prefix != "", "testing", "testing down"); err != nil {
		return err
	}
	varFiles := append(append(append([]string{}, appliedVF...), in.VarFiles...), extraVF...)
	fmt.Fprintln(w, "→ terraform destroy (testing phase)")
	return tfws.Destroy(ctx, varFiles...)
}

// MigrateJumphostsToTesting evicts the jumphosts (module.testing.*) from
// the BNK state (state/) into the testing state (state-testing/) with no
// cloud churn — the Sprint 28 pre-Sprint-28-split migration (architect
// §1d). It is a one-shot `terraform state mv module.testing module.testing`
// with -state=state/ -state-out=state-testing/; the shared SSH key moves
// with the rest of module.testing.
//
// Idempotent-ish: callers gate on config.TestingMigrationNeeded (the
// jumphosts are still in state/ AND state-testing/ is empty). After the
// move, the BNK phase should be re-applied so its now-jumphost-free state
// reconciles (the bnk-phase override already forces testing_create_*=false,
// so that plan is clean — no orphan re-create).
func MigrateJumphostsToTesting(ctx context.Context, in *LifecycleInputs) error {
	cctx, err := config.New(in.Workspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}
	needed, err := config.TestingMigrationNeeded(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("checking migration state: %w", err)
	}
	if !needed {
		fmt.Fprintln(os.Stderr, "✓ Nothing to migrate — no module.testing.* resources in the BNK state, or state-testing/ already populated.")
		return nil
	}

	// Open the BNK (source) workspace — it owns the module.testing.*
	// addresses today and is the terraform that performs the move.
	bnkStateDir, err := config.WorkspaceStateDir(cctx.WorkspaceName)
	if err != nil {
		return err
	}
	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return fmt.Errorf("resolving API key: %w", err)
	}
	bnkTF, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, bnkStateDir, apiKey, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if err := bnkTF.WriteTFVars(cctx.Workspace); err != nil {
		return fmt.Errorf("writing tfvars: %w", err)
	}
	fmt.Fprintln(os.Stderr, "→ terraform init (BNK state, for the jumphost migration)")
	if err := bnkTF.Init(ctx); err != nil {
		return err
	}

	// Ensure the destination state dir exists (Open MkdirAll's it, but we
	// don't want to init a full workspace there — `state mv -state-out`
	// creates the destination state file directly).
	testingStateDir, err := config.WorkspaceTestingStateDir(cctx.WorkspaceName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(testingStateDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", testingStateDir, err)
	}
	destStatePath := testingStateDir + string(os.PathSeparator) + "terraform.tfstate"

	fmt.Fprintf(os.Stderr,
		"→ Migrating jumphosts: terraform state mv module.testing → %s (no cloud churn; the live jumphosts are preserved)\n",
		destStatePath)
	if err := bnkTF.StateMvTo(ctx, "module.testing", destStatePath); err != nil {
		return fmt.Errorf("moving module.testing into the testing state: %w", err)
	}
	fmt.Fprintln(os.Stderr, "✓ Jumphosts moved into state-testing/.")
	fmt.Fprintln(os.Stderr, "  Re-run `roksbnkctl bnk up` to reconcile the BNK state (now jumphost-free),")
	fmt.Fprintln(os.Stderr, "  and `roksbnkctl testing up` to adopt the moved jumphosts (plan shows no-op).")
	return nil
}

// ── concurrent-stderr plumbing (architect §3b) ──────────────────────────

// prefixWriter wraps a destination writer and prefixes every line with a
// per-phase tag ([bnk] / [testing] ) so two concurrent applies' stderr
// interleave readably. A shared mutex guards the destination so a single
// Write (one logical line) from one goroutine isn't split across the
// other's output. The cluster phase (serial) does not use this — only the
// parallel BNK ∥ Testing leg is prefixed.
type prefixWriter struct {
	mu     *sync.Mutex
	dest   io.Writer
	prefix []byte
	buf    []byte // carry an unterminated partial line between Writes
}

// newPrefixWriters builds the [bnk] and [testing] prefixed writers
// sharing one mutex + one destination (os.Stderr).
func newPrefixWriters(dest io.Writer) (bnk, testing *prefixWriter) {
	mu := &sync.Mutex{}
	return &prefixWriter{mu: mu, dest: dest, prefix: []byte("[bnk] ")},
		&prefixWriter{mu: mu, dest: dest, prefix: []byte("[testing] ")}
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(b)
	p.buf = append(p.buf, b...)
	for {
		i := bytes.IndexByte(p.buf, '\n')
		if i < 0 {
			break
		}
		line := p.buf[:i+1]
		if _, err := p.dest.Write(append(append([]byte{}, p.prefix...), line...)); err != nil {
			return n, err
		}
		p.buf = p.buf[i+1:]
	}
	return n, nil
}

// flush emits any trailing partial line (no terminating newline) with the
// prefix. Called after a phase goroutine returns so a final unterminated
// line isn't lost.
func (p *prefixWriter) flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.buf) == 0 {
		return
	}
	_, _ = p.dest.Write(append(append([]byte{}, p.prefix...), p.buf...))
	p.buf = nil
}

// errWriter returns the io.Writer the phase helpers print to: the
// prefixed writer on the parallel path, or os.Stderr on the serial path
// (nil prefixWriter).
func errWriter(p *prefixWriter) io.Writer {
	if p == nil {
		return os.Stderr
	}
	return p
}
