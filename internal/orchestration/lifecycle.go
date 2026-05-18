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
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"

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
	applyMaxAttempts = 3
	applyRetryWait   = 60 * time.Second
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

// ── lifecycle implementations ───────────────────────────────────────

// RunUp is the shape-aware composite dispatcher for the top-level
// `roksbnkctl up`. It detects the workspace's on-disk shape and routes
// to the right phase combination per PRD 06 §"Dispatch table":
//
//   - LegacySingle → monolithic trial up (preserves v1.0.x byte-for-byte:
//     one terraform apply against the trial state, which still carries
//     the cluster modules in pre-split workspaces).
//   - Empty / Split → cluster up first (no-op refresh on Split), then
//     trial up.
//   - ClusterOnly → trial up directly (cluster already provisioned).
//
// The composite is a pure dispatcher — no business logic of its own.
// All the terraform / docker / retry behavior lives in the leaf helpers.
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
	shape, err := config.DetectShape(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace shape: %w", err)
	}
	switch shape {
	case config.ShapeLegacySingle:
		// Cluster + trial share one state file — the monolithic path
		// applies the whole HCL tree in one terraform run, matching
		// v1.0.x semantics exactly.
		return RunTrialUp(ctx, in)
	case config.ShapeEmpty, config.ShapeSplit:
		// Empty: brand-new workspace; cluster up creates the cluster
		// phase, then trial up adds the BNK trial layer on top.
		// Split: cluster up is a no-op refresh (PRD 06 open Q on
		// `--skip-cluster-refresh`); trial up applies any drift /
		// tfvars changes.
		if err := in.RunClusterUp(ctx); err != nil {
			return err
		}
		return RunTrialUp(ctx, in)
	case config.ShapeClusterOnly:
		return RunTrialUp(ctx, in)
	default:
		return fmt.Errorf("unrecognised workspace shape %v", shape)
	}
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
	cctx, tfws, err := openTF(ctx, in, true)
	if err != nil {
		return err
	}
	if err := writeAndInit(ctx, tfws, cctx.Workspace); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "→ terraform plan")
	changes, err := tfws.Plan(ctx, in.VarFiles...)
	if err != nil {
		return err
	}
	if !changes {
		fmt.Fprintln(os.Stderr, "✓ no changes")
		// Even with no infra changes, fetching the kubeconfig is useful
		// (cluster may already exist; user wants creds locally).
		tryAutoKubeconfig(ctx, in, cctx, tfws)
		// Same for the jumphost target — if it was provisioned by an
		// earlier apply, populate the workspace's targets:jumphost so
		// `--on jumphost` works without manual config.
		tryAutoJumphost(ctx, in, cctx, tfws)
		tryAutoClusterJumphosts(ctx, in, cctx, tfws)
		return nil
	}
	if !in.Auto && !in.PromptYesNo("Apply this plan?", false) {
		return errors.New("aborted")
	}

	fmt.Fprintln(os.Stderr, "→ terraform apply")
	if err := applyWithRetry(ctx, tfws, in.VarFiles); err != nil {
		return err
	}
	tryAutoKubeconfig(ctx, in, cctx, tfws)
	tryAutoJumphost(ctx, in, cctx, tfws)
	tryAutoClusterJumphosts(ctx, in, cctx, tfws)
	return nil
}

// RunPlan = plan only. Read-only — never prompts.
func RunPlan(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("plan"); err != nil {
		return err
	}
	// VarFiles is already chokepoint-normalized (PersistentPreRunE).
	if spec, ok := terraformBackendSpec(in); ok && spec != "local" {
		return runTerraformLifecycleDocker(ctx, in, spec, "plan")
	}
	cctx, tfws, err := openTF(ctx, in, true)
	if err != nil {
		return err
	}
	if err := writeAndInit(ctx, tfws, cctx.Workspace); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "→ terraform plan")
	_, err = tfws.Plan(ctx, in.VarFiles...)
	return err
}

// RunApply = direct apply, no plan-and-confirm gate. For users who know
// what they're doing (CI, scripted flows, post-`roksbnkctl plan`).
func RunApply(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("apply"); err != nil {
		return err
	}
	// VarFiles is already chokepoint-normalized (PersistentPreRunE).
	if spec, ok := terraformBackendSpec(in); ok && spec != "local" {
		return runTerraformLifecycleDocker(ctx, in, spec, "apply")
	}
	cctx, tfws, err := openTF(ctx, in, true)
	if err != nil {
		return err
	}
	if err := writeAndInit(ctx, tfws, cctx.Workspace); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "→ terraform apply")
	if err := applyWithRetry(ctx, tfws, in.VarFiles); err != nil {
		return err
	}
	tryAutoKubeconfig(ctx, in, cctx, tfws)
	tryAutoJumphost(ctx, in, cctx, tfws)
	tryAutoClusterJumphosts(ctx, in, cctx, tfws)
	return nil
}

// RunDown is the shape-aware composite dispatcher for top-level
// `roksbnkctl down`. Detects the workspace's on-disk shape and routes
// per PRD 06 §"Dispatch table":
//
//   - LegacySingle → monolithic trial down (one terraform destroy
//     against the trial state; same v1.0.x behavior).
//   - Empty        → error "nothing to destroy".
//   - Split        → trial down, then cluster down (tear down in the
//     reverse order they were created so trial doesn't get orphaned
//     against a missing cluster).
//   - ClusterOnly  → cluster down.
//
// Pure dispatcher; all destroy / confirmation logic lives in the leaf
// helpers.
func RunDown(ctx context.Context, in *LifecycleInputs) error {
	if err := in.RejectOnFlag("down"); err != nil {
		return err
	}
	// VarFiles is already chokepoint-normalized (PersistentPreRunE).
	cctx, err := config.New(in.Workspace)
	if err != nil {
		return err
	}
	shape, err := config.DetectShape(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace shape: %w", err)
	}
	switch shape {
	case config.ShapeLegacySingle:
		return RunTrialDown(ctx, in)
	case config.ShapeEmpty:
		return errors.New("nothing to destroy in this workspace")
	case config.ShapeSplit:
		if err := RunTrialDown(ctx, in); err != nil {
			return err
		}
		return in.RunClusterDown(ctx)
	case config.ShapeClusterOnly:
		return in.RunClusterDown(ctx)
	default:
		return fmt.Errorf("unrecognised workspace shape %v", shape)
	}
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
		return runTerraformLifecycleDocker(ctx, in, spec, "destroy")
	}
	cctx, tfws, err := openTF(ctx, in, true)
	if err != nil {
		return err
	}
	if !in.Auto {
		fmt.Fprintf(os.Stderr, "This will destroy workspace %q's resources.\n", cctx.WorkspaceName)
		if !in.PromptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
	}
	if err := writeAndInit(ctx, tfws, cctx.Workspace); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "→ terraform destroy")
	return tfws.Destroy(ctx, in.VarFiles...)
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

	tfws, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, stateDir, apiKey, os.Stdout, os.Stderr)
	if err != nil {
		return nil, nil, err
	}
	return cctx, tfws, nil
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
	cluster := resolveClusterIdentity(ctx, cctx, tfws)
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
	target := k8s.DefaultKubeconfigPath()
	if target == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			fmt.Fprintf(os.Stderr, "warning: resolving home dir: %v\n", herr)
			return
		}
		target = filepath.Join(home, ".kube", "config")
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
// Stale-target handling is OPTION (a) UPSERT-ONLY (integrator decision,
// prompts/sprint13/README.md): orphaned `jumphost-<oldzone>` entries
// (zone removed / testing_create_cluster_jumphosts flipped false) linger
// until manual `targets remove`. Option (b) reconcile/orphan-removal is
// a deliberate post-v1.5.0 follow-up and is intentionally NOT
// implemented here (no prefix-sweep, no `auto:` schema marker).
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

// resolveClusterIdentity figures out which cluster to fetch the
// kubeconfig for. Order:
//
//  1. Terraform output `roks_cluster_id` — post-apply truth, the actual
//     ID provisioned. Beats config.yaml when the user's --var-file
//     overrides cluster.name.
//  2. Terraform output `roks_cluster_name` — same idea, slightly less
//     stable if the cluster was renamed.
//  3. cctx.Workspace.Cluster.Name — config.yaml fallback (pre-apply or
//     if outputs aren't reachable).
//
// Returns "" if no source produced a usable identity — caller skips
// auto-fetch silently.
func resolveClusterIdentity(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) string {
	if tfws != nil {
		if outputs, err := tfws.Output(ctx); err == nil {
			for _, key := range []string{"roks_cluster_id", "roks_cluster_name"} {
				if om, ok := outputs[key]; ok && len(om.Value) > 0 {
					var s string
					if json.Unmarshal(om.Value, &s) == nil && s != "" {
						return s
					}
				}
			}
		}
	}
	if cctx != nil && cctx.Workspace != nil {
		return cctx.Workspace.Cluster.Name
	}
	return ""
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
	} {
		if strings.Contains(s, pat) {
			return true
		}
	}
	return false
}
