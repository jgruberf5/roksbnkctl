package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceShape captures the on-disk provisioning shape of a workspace
// so the lifecycle / phase commands can route to the correct dispatch
// path. Derived purely from artifacts under
// ~/.roksbnkctl/<workspace>/state[-cluster]/terraform.tfstate — no
// terraform calls, no IBM Cloud calls.
//
// See docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md §"Design" for the
// authoritative classification logic.
type WorkspaceShape int

const (
	// ShapeUnknown — could not classify (workspace not initialised,
	// state files unreadable for non-missing reasons, etc.). Callers
	// must surface the underlying error rather than dispatching on
	// this value.
	ShapeUnknown WorkspaceShape = iota

	// ShapeEmpty — neither phase has resources in its state file.
	// First run of a brand-new workspace, or a workspace that's been
	// fully torn down.
	ShapeEmpty

	// ShapeClusterOnly — the cluster phase state has resources, the
	// trial phase state is empty. Happens after `cluster up` succeeds
	// but before any `bnk up` / unscoped `up` has run.
	ShapeClusterOnly

	// ShapeSplit — both phases have resources in their own state
	// files. The "new normal" once `cluster up` + `bnk up` (or a
	// post-register `up`) have both run.
	ShapeSplit

	// ShapeLegacySingle — the trial-phase state contains cluster-phase
	// modules from a pre-split v1.0.x `roksbnkctl up` run. The cluster
	// phase state directory is typically empty / absent in this shape.
	// Triggers refusals on `cluster up/down` and `bnk up/down` — the
	// only safe command surface is the monolithic `up`/`down`.
	ShapeLegacySingle
)

// String returns the human-readable shape name used in logs, errors,
// and book examples. Kept lowercase + hyphenated to match the
// CLI-output convention elsewhere in the codebase.
func (s WorkspaceShape) String() string {
	switch s {
	case ShapeEmpty:
		return "empty"
	case ShapeClusterOnly:
		return "cluster-only"
	case ShapeSplit:
		return "split"
	case ShapeLegacySingle:
		return "legacy-single-state"
	default:
		return "unknown"
	}
}

// clusterPhaseModules — TF module addresses owned by the cluster phase
// (the set of modules `cluster_phase.go` provisions when
// deploy_bnk=false is forced).
//
// Per PRD 06 §"Design": "module.roks_cluster", "module.cert_manager",
// "module.testing". Matching uses exact-equality OR HasPrefix(name+".")
// so we catch root-of-module addresses and any nested sub-addresses
// (e.g. `module.roks_cluster.module.cluster`).
//
// Note: the cluster-phase signal in trial state is narrower than
// "any resource under one of these prefixes" — see
// `trialStateHasClusterModules` for the actual managed-cluster
// criterion. Data-source refreshes legitimately populate trial state
// with reads under `module.cert_manager` etc. and must not trip the
// legacy classification.
var clusterPhaseModules = []string{
	"module.roks_cluster",
	"module.cert_manager",
	"module.testing",
}

// DetectShape inspects on-disk state for `workspace` and reports which
// shape it's in. No terraform / cloud calls — pure filesystem +
// JSON-decode of the two tfstate files.
//
// Missing tfstate files are treated as "no resources" (workspaces
// aren't necessarily applied yet). Malformed JSON surfaces as an error
// so dispatch doesn't silently misroute (PRD 06 §"Design").
func DetectShape(workspace string) (WorkspaceShape, error) {
	trialDir, err := WorkspaceStateDir(workspace)
	if err != nil {
		return ShapeUnknown, err
	}
	clusterDir, err := WorkspaceClusterStateDir(workspace)
	if err != nil {
		return ShapeUnknown, err
	}
	trialState := filepath.Join(trialDir, "terraform.tfstate")
	clusterState := filepath.Join(clusterDir, "terraform.tfstate")

	trialHas, err := tfstateHasResources(trialState)
	if err != nil {
		return ShapeUnknown, err
	}
	clusterHas, err := tfstateHasResources(clusterState)
	if err != nil {
		return ShapeUnknown, err
	}

	// Only check for the legacy-single-state signature when the trial
	// state actually has something in it — saves a re-read for the
	// common "empty trial" case and keeps the legacy classification
	// from triggering on a truly empty workspace.
	trialHasCluster := false
	if trialHas {
		trialHasCluster, err = trialStateHasClusterModules(trialState)
		if err != nil {
			return ShapeUnknown, err
		}
	}

	switch {
	case trialHasCluster:
		// Cluster + trial share one state file. Legacy takes
		// precedence over Split even when the cluster state happens
		// to also have something in it — the shared-state shape is
		// the meaningful constraint for dispatch.
		return ShapeLegacySingle, nil
	case trialHas && clusterHas:
		return ShapeSplit, nil
	case trialHas:
		// Trial state has resources but no cluster-phase modules —
		// classic "registered cluster + trial apply" split workspace
		// whose cluster identity lives in cluster-outputs.json rather
		// than its own tfstate.
		return ShapeSplit, nil
	case clusterHas:
		return ShapeClusterOnly, nil
	default:
		return ShapeEmpty, nil
	}
}

// Presence is the Sprint 28 per-phase presence model that replaces the
// combinatorial WorkspaceShape enum for the three-phase dispatchers
// (Cluster / BNK / Testing). Each field is an independent boolean
// derived purely from the per-phase state dir — no terraform / cloud
// calls, same contract as DetectShape. See
// issues/issue_sprint28_architect.md §2c.
//
// Legacy is special: when a workspace is a v1.0.x single-state monolith
// (cluster modules live in state/), Legacy is true and BNK / Testing are
// reported false — the monolith is its own world and the phase verbs
// refuse on it. Cluster may still be reported true via state-cluster/ in
// the unusual case both exist, but in practice a legacy workspace has no
// state-cluster/.
type Presence struct {
	Cluster bool // state-cluster/ has managed resources (the ROKS cluster)
	BNK     bool // state/ has managed BNK resources (and is NOT legacy-monolith)
	Testing bool // state-testing/ has managed resources (the jumphosts)
	Legacy  bool // state/ carries cluster modules (v1.0.x single-state)
}

// Any reports whether at least one of the three phases (or the legacy
// monolith) has resources — i.e. the workspace is non-empty.
func (p Presence) Any() bool {
	return p.Cluster || p.BNK || p.Testing || p.Legacy
}

// DetectPresence inspects on-disk state for `workspace` and reports the
// per-phase presence (Sprint 28). Pure filesystem + JSON-decode of the
// three tfstate files; missing files are treated as "no resources".
// Malformed JSON surfaces as an error so dispatch doesn't misroute.
//
// Detection rules (architect §2c):
//   - Cluster = state-cluster/ has a managed ibm_container_vpc_cluster
//     under a clusterPhaseModules prefix (the authoritative cluster
//     signal, retargeted at the cluster state).
//   - Legacy  = state/ carries that same managed-cluster signal (the
//     v1.0.x monolith). When Legacy, BNK and Testing are forced false.
//   - BNK     = state/ has ≥1 managed resource AND not Legacy.
//   - Testing = state-testing/ has ≥1 managed resource.
func DetectPresence(workspace string) (Presence, error) {
	var p Presence

	bnkDir, err := WorkspaceStateDir(workspace)
	if err != nil {
		return p, err
	}
	clusterDir, err := WorkspaceClusterStateDir(workspace)
	if err != nil {
		return p, err
	}
	testingDir, err := WorkspaceTestingStateDir(workspace)
	if err != nil {
		return p, err
	}
	bnkState := filepath.Join(bnkDir, "terraform.tfstate")
	clusterState := filepath.Join(clusterDir, "terraform.tfstate")
	testingState := filepath.Join(testingDir, "terraform.tfstate")

	// Cluster — managed ibm_container_vpc_cluster in state-cluster/.
	clusterHasCluster, err := stateHasManagedClusterResource(clusterState)
	if err != nil {
		return p, err
	}
	p.Cluster = clusterHasCluster

	// Legacy / BNK — both keyed off state/.
	bnkHas, err := tfstateHasResources(bnkState)
	if err != nil {
		return p, err
	}
	if bnkHas {
		legacy, lerr := stateHasManagedClusterResource(bnkState)
		if lerr != nil {
			return p, lerr
		}
		if legacy {
			p.Legacy = true
		} else {
			p.BNK = true
		}
	}

	// Testing — any managed resource in state-testing/.
	testingHas, err := tfstateHasResources(testingState)
	if err != nil {
		return p, err
	}
	p.Testing = testingHas

	return p, nil
}

// TestingMigrationNeeded reports the Sprint 28 migration condition: the
// jumphosts (module.testing.*) still live in the BNK state (state/) AND
// state-testing/ has no resources yet. This is the pre-Sprint-28 split
// workspace whose jumphosts need evicting from state/ into state-testing/
// (architect §1d). Pure filesystem; surfaced as a one-line nudge, never
// auto-run.
func TestingMigrationNeeded(workspace string) (bool, error) {
	bnkDir, err := WorkspaceStateDir(workspace)
	if err != nil {
		return false, err
	}
	testingDir, err := WorkspaceTestingStateDir(workspace)
	if err != nil {
		return false, err
	}
	bnkState := filepath.Join(bnkDir, "terraform.tfstate")
	testingState := filepath.Join(testingDir, "terraform.tfstate")

	bnkHasTesting, err := stateHasManagedModule(bnkState, "module.testing")
	if err != nil {
		return false, err
	}
	if !bnkHasTesting {
		return false, nil
	}
	testingHas, err := tfstateHasResources(testingState)
	if err != nil {
		return false, err
	}
	return !testingHas, nil
}

// stateHasManagedClusterResource is the generalized form of
// trialStateHasClusterModules, retargetable at any state file (Sprint 28
// reuses it for both the legacy-detection on state/ and the
// cluster-present check on state-cluster/). Same three filters: managed
// mode, type == ibm_container_vpc_cluster, module under a
// clusterPhaseModules prefix.
func stateHasManagedClusterResource(path string) (bool, error) {
	return trialStateHasClusterModules(path)
}

// stateHasManagedModule reports whether `path` carries at least one
// MANAGED resource whose module address equals `prefix` or begins with
// `prefix + "."`. Used by TestingMigrationNeeded to detect jumphosts
// (module.testing.*) lingering in the BNK state. Data-source reads
// (mode == "data") don't count — only ownership.
func stateHasManagedModule(path, prefix string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var s struct {
		Resources []struct {
			Mode   string `json:"mode"`
			Module string `json:"module"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return false, err
	}
	for _, r := range s.Resources {
		if r.Mode != "managed" {
			continue
		}
		if r.Module == prefix || strings.HasPrefix(r.Module, prefix+".") {
			return true, nil
		}
	}
	return false, nil
}

// tfstateHasResources reports whether `path` is a terraform.tfstate
// with at least one resource in `state.resources`. Missing file →
// (false, nil): not-yet-applied workspaces aren't an error. Malformed
// JSON → (false, error) so the caller surfaces the corruption rather
// than silently classifying as empty.
//
// Lives on this type (not under workspace.go) because PRD 06 makes
// shape detection a first-class concern — `DeleteWorkspace`'s existing
// call site (workspace.go) continues to work because we're in the same
// package.
func tfstateHasResources(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var s struct {
		Resources []any `json:"resources"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return false, err
	}
	return len(s.Resources) > 0, nil
}

// trialStateHasClusterModules reports whether the trial-phase tfstate
// at `path` carries the unambiguous v1.0.x single-state signal: a
// managed `ibm_container_vpc_cluster` resource (the ROKS cluster
// itself) provisioned under one of the `clusterPhaseModules` address
// prefixes.
//
// Three filters, all required:
//
//  1. `mode == "managed"` — data sources (`mode == "data"`) are
//     external lookups, not ownership. Trial-state plans legitimately
//     refresh data sources under `module.cert_manager` /
//     `module.roks_cluster` / `module.testing` (the BNK trial modules
//     read cluster info via those addresses) and pre-Sprint-22 that
//     refresh tripped the legacy classifier — see Sprint 22 issue.
//  2. `type == "ibm_container_vpc_cluster"` — the ROKS cluster
//     resource is the singular marker of "this state file owns the
//     cluster." Other managed resources can appear under cluster-phase
//     module addresses for benign reasons (stray `tls_private_key`,
//     `ibm_resource_instance`-of-COS, etc., observed during partial
//     applies) without indicating shared-state ownership.
//  3. The module address matches a `clusterPhaseModules` prefix
//     (exact match OR `HasPrefix(prefix + ".")` — the trailing-dot
//     guard prevents `module.roks_cluster_extras` from matching
//     `module.roks_cluster`).
//
// Missing file → (false, nil); malformed JSON → (false, error).
func trialStateHasClusterModules(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	var s struct {
		Resources []struct {
			Mode   string `json:"mode"`
			Module string `json:"module"`
			Type   string `json:"type"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return false, err
	}
	for _, r := range s.Resources {
		if r.Mode != "managed" || r.Type != "ibm_container_vpc_cluster" {
			continue
		}
		for _, prefix := range clusterPhaseModules {
			if r.Module == prefix || strings.HasPrefix(r.Module, prefix+".") {
				return true, nil
			}
		}
	}
	return false, nil
}
