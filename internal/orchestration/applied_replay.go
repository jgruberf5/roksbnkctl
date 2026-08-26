package orchestration

// Applied-tfvars replay (validator Issue 3, round-3).
//
// Background. PRD 07 / Sprint 11 introduced a per-phase snapshot
// `terraform.applied.tfvars` written after a successful apply
// (`internal/config/applied_tfvars.go`, called from
// `internal/tf/terraform.go`'s post-apply hook). The snapshot captures
// the var-file inputs terraform actually consumed across multiple
// `--var-file` sources — and that is exactly the shape that bit the
// round-2 Issue 3 attempt: the snapshot has one `# === from <src> ===`
// section per consumed file with the same key potentially appearing in
// several sections (e.g. `testing_create_tgw_jumphost` set in both the
// user `terraform.tfvars` *and* the round-2 `bnk-phase-override.tfvars`).
// Terraform rejects intra-file duplicate keys, so a naive direct replay
// errors with `Each argument may be set only once` — observed live in
// run-id `20260519-220236` A5.
//
// Round-3 design. `LayerAppliedTFVars` no longer points terraform at
// the canonical snapshot. Instead it (1) parses the snapshot via the
// existing single-file parser (a single map naturally yields
// later-source-wins because the snapshot writer emits sources in apply
// order), (2) drops redacted secret keys (`ibmcloud_api_key` —
// terraform var-file precedence beats `TF_VAR_*` env, so a `"<redacted>"`
// in the replay would break IAM auth — the secret must keep coming from
// env / explicit `--var-file`), and (3) writes a deduped, secret-free
// `<phase state dir>/.applied-replay.tfvars` that terraform consumes as
// a single var-file. The canonical snapshot on disk stays unchanged —
// PRD 07's audit shape is preserved.
//
// Layering precedence stays lowest. Callers prepend the result to their
// var-file chain so explicit `--var-file` flags still override, and the
// Sprint 16 round-2 `bnk-phase-override.tfvars` (architectural force)
// still wins on top of both. A fresh / never-applied workspace has no
// snapshot → returns nil → byte-identical to prior behaviour.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// appliedReplayFile is the basename of the derived, deduped,
// secret-stripped replay var-file that terraform actually consumes.
// Leading-dot keeps it out of casual `ls` output without hiding it from
// terraform.
const appliedReplayFile = ".applied-replay.tfvars"

// RequireSnapshotOrVarFile pre-empts terraform's bare "No value for
// required variable" failure with a roksbnkctl-level actionable error
// when the caller has neither an applied-tfvars snapshot to replay,
// an explicit `--var-file`, nor a workspace-persistent
// `terraform.tfvars.user` written by `init --var-file` (Sprint 19).
// The check belongs in the verbs the operator expects to work against
// `-w <ws>` alone — `plan`, `apply`, `down`, `cluster down` — and is a
// no-op in every other case:
//
//   - replayed has a path  → a snapshot exists; round-3 replay covers it.
//   - userVarFiles has any → the user supplied the inputs explicitly.
//   - hasUserTFVars        → `init --var-file <path>` seeded the
//     workspace's `terraform.tfvars.user`; the lifecycle layers it
//     automatically (see `tf.Workspace.varFiles`).
//
// Only when *all three* are empty does the call resolve to terraform
// plan/apply with the small `config.yaml`-derived `state/terraform.tfvars`
// as the sole var-file, which doesn't carry `ibmcloud_api_key` /
// `testing_*` / `roks_*` / `f5_*` — so terraform errors with a stack
// of unrelated-looking "No value for required variable" lines that
// don't name a remedy. This helper names the remedy.
//
// `phase` ("cluster" / "trial" / "legacy-single") and `verb`
// (`"down"` / `"plan"` / `"apply"` / `"cluster down"`) shape the
// message so the user can copy/paste it.
//
// Escape hatches:
//   - pass `--var-file <any-file>` (even an empty one) to bypass — the
//     caller's `userVarFiles` slice is non-empty so this helper returns nil.
//   - run `roksbnkctl init --var-file <path> -w <ws>` once to seed the
//     workspace-persistent override — `hasUserTFVars` is true on every
//     subsequent verb against that workspace.
//
// configRendersComplete (Sprint 27) is true when the workspace's config.yaml
// carries a `prefix:` — i.e. it is a Sprint 26 prefix-driven workspace, whose
// `internal/tf/vars.go:RenderTFVars` emits a COMPLETE tfvars (region, resource
// group, every prefix-derived resource name, and the create/deploy toggles).
// Combined with `ibmcloud_api_key` (always supplied via the TF_VAR env var,
// never a tfvars file) and the upstream module defaults for everything else,
// that auto-rendered `state*/terraform.tfvars` is self-sufficient — so a bare
// `-w <ws>` plan/apply/down works WITHOUT a snapshot or `--var-file`. Without
// it (legacy empty-prefix workspaces render the old sparse tfvars), the gate
// still fires. This closes the gap where a prefix workspace whose first `up`
// failed (no applied snapshot written) and that was init'd without
// `--var-file` (no terraform.tfvars.user) could not be `down`'d at all.
func RequireSnapshotOrVarFile(replayed []string, userVarFiles []string, hasUserTFVars, configRendersComplete bool, phase, verb string) error {
	if len(replayed) > 0 || len(userVarFiles) > 0 || hasUserTFVars || configRendersComplete {
		return nil
	}
	return fmt.Errorf(
		"this workspace has no terraform.applied.tfvars snapshot for the %s phase yet,\n"+
			"  and no workspace-persistent terraform.tfvars.user from `init --var-file`,\n"+
			"  so `roksbnkctl %s -w <workspace>` alone can't supply the required terraform\n"+
			"  variables (ibmcloud_api_key, testing_*/roks_*/f5_* — none have defaults).\n"+
			"  Pick one:\n"+
			"    - re-run with `--var-file <path/to/your/terraform.tfvars>`; on the first\n"+
			"      successful `roksbnkctl up --var-file <path>` the snapshot is written\n"+
			"      automatically and subsequent `-w <ws>` lifecycle ops will pick it up, OR\n"+
			"    - re-init with `roksbnkctl init --var-file <path> -w <workspace>` to\n"+
			"      seed a workspace-persistent terraform.tfvars.user, then every bare\n"+
			"      `-w <ws>` verb layers it automatically",
		phase, verb)
}

// LayerAppliedTFVars returns the phase's deduped applied-tfvars replay
// file as a single-element var-file slice when a snapshot exists. The
// returned path is `<phase state dir>/.applied-replay.tfvars`, a freshly
// rewritten file derived from `terraform.applied.tfvars` (later-source-
// wins; redacted secrets stripped). Callers prepend the result to their
// var-file chain so it sits at the **lowest precedence**.
//
// `phase` is one of "cluster" / "trial" / "legacy-single" (matching
// `config.AppliedTFVarsPath` / `ReadAppliedTFVarsReplayAssignments`).
// Returns nil — the no-op result — on any of:
//   - the snapshot file does not exist (fresh / never-applied phase),
//   - the snapshot parses to an empty assignment map (nothing usable
//     after stripping redacted keys),
//   - resolving the state dir or writing the replay file fails (logged
//     to stderr; caller falls back to its prior behaviour).
//
// On success, emits a loud stderr line naming the file so the contract
// stays visible — no magic.
func LayerAppliedTFVars(workspace, phase string) []string {
	assigns, err := config.ReadAppliedTFVarsReplayAssignments(workspace, phase)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: applied-tfvars replay disabled (reading snapshot for phase=%s: %v)\n",
			phase, err)
		return nil
	}
	if len(assigns) == 0 {
		return nil
	}
	stateDir, err := phaseStateDir(workspace, phase)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: applied-tfvars replay disabled (resolving state dir for phase=%s: %v)\n",
			phase, err)
		return nil
	}
	replayPath := filepath.Join(stateDir, appliedReplayFile)
	if err := writeAppliedReplayFile(replayPath, phase, assigns); err != nil {
		fmt.Fprintf(os.Stderr,
			"warning: applied-tfvars replay disabled (writing %s: %v)\n",
			replayPath, err)
		return nil
	}
	fmt.Fprintf(os.Stderr,
		"→ Replaying applied tfvars from %s "+
			"(deduped from terraform.applied.tfvars, secret keys excluded; "+
			"pass --var-file to override)\n",
		replayPath)
	return []string{replayPath}
}

// phaseStateDir maps the phase string to the directory that holds the
// snapshot and the derived replay file. Matches `config.AppliedTFVarsPath`.
func phaseStateDir(workspace, phase string) (string, error) {
	switch phase {
	case "cluster":
		return config.WorkspaceClusterStateDir(workspace)
	case "trial", "legacy-single":
		return config.WorkspaceStateDir(workspace)
	case "testing":
		return config.WorkspaceTestingStateDir(workspace)
	case "gateway":
		return config.WorkspaceGatewayStateDir(workspace)
	default:
		// Conservative fallback: trial dir (matches the writer's default).
		return config.WorkspaceStateDir(workspace)
	}
}

// writeAppliedReplayFile renders the deduped assignments to a single
// terraform-consumable var-file. Keys are sorted for determinism (helps
// diffing + hermetic tests). The file is rewritten on every lifecycle
// op so a stale replay can never linger — it always reflects the
// snapshot at the moment of read.
func writeAppliedReplayFile(path, phase string, assigns map[string]string) error {
	keys := make([]string, 0, len(assigns))
	for k := range assigns {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprintf(f, "# Auto-generated by roksbnkctl from terraform.applied.tfvars (phase=%s).\n", phase)
	fmt.Fprintln(f, "# Deduped (later-source-wins) and stripped of redacted secret keys.")
	fmt.Fprintln(f, "# Re-generated on every down/plan/apply. Do not edit by hand — your changes will be overwritten.")
	fmt.Fprintln(f)
	for _, k := range keys {
		// NEVER EMIT A VAR-FILE TERRAFORM CANNOT PARSE (#219).
		//
		// This file is handed to terraform on plan, apply AND down, so one
		// malformed value does not degrade the run — it ends it, and on `down`
		// that means a workspace nobody can destroy. The parser upstream now
		// drops unterminated blocks, but this is the last thing between a
		// corrupt snapshot and terraform's own parse error, and it costs a
		// bracket count.
		//
		// The key is named in a comment rather than dropped silently: an
		// operator debugging a missing variable needs to see that it was
		// skipped and why.
		if config.HCLValueIsUnbalanced(assigns[k]) {
			fmt.Fprintf(f, "# %s = <skipped: unbalanced brackets in the applied snapshot>\n", k)
			fmt.Fprintf(os.Stderr,
				"warning: %s: skipping %q — its value in the applied snapshot has unbalanced "+
					"brackets and terraform could not parse it\n", path, k)
			continue
		}
		fmt.Fprintf(f, "%s = %s\n", k, assigns[k])
	}
	return nil
}
