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
		fmt.Fprintf(f, "%s = %s\n", k, assigns[k])
	}
	return nil
}
