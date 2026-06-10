package config

import (
	"encoding/json"
	"errors"
	"fmt"
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

	switch {
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
type Presence struct {
	Cluster bool // state-cluster/ has managed resources (the ROKS cluster)
	BNK     bool // state/ has managed BNK resources
	Testing bool // state-testing/ has managed resources (the jumphosts)
	Gateway bool // state-gateway/ has managed resources (the data-plane config)
}

// Any reports whether at least one phase has resources — i.e. the
// workspace is non-empty.
func (p Presence) Any() bool {
	return p.Cluster || p.BNK || p.Testing || p.Gateway
}

// DetectPresence inspects on-disk state for `workspace` and reports the
// per-phase presence (Sprint 28). Pure filesystem + JSON-decode of the
// three tfstate files; missing files are treated as "no resources".
// Malformed JSON surfaces as an error so dispatch doesn't misroute.
//
// Detection rules (architect §2c):
//   - Cluster = state-cluster/ has a managed ibm_container_vpc_cluster
//     under a clusterPhaseModules prefix (the authoritative cluster signal).
//   - BNK     = state/ has ≥1 managed resource.
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

	// BNK — any managed resource in state/.
	bnkHas, err := tfstateHasResources(bnkState)
	if err != nil {
		return p, err
	}
	p.BNK = bnkHas

	// Testing — any managed resource in state-testing/.
	testingHas, err := tfstateHasResources(testingState)
	if err != nil {
		return p, err
	}
	p.Testing = testingHas

	// Gateway — any managed resource in state-gateway/.
	gatewayDir, err := WorkspaceGatewayStateDir(workspace)
	if err != nil {
		return p, err
	}
	gatewayHas, err := tfstateHasResources(filepath.Join(gatewayDir, "terraform.tfstate"))
	if err != nil {
		return p, err
	}
	p.Gateway = gatewayHas

	return p, nil
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
// TestingJumphostSubnetCIDRs reads the deployed Testing phase's jumphost
// subnet CIDRs from state-testing/terraform.tfstate's outputs (PRD 12) so
// `gateway up` can auto-derive the gateway client-subnet LISTS from the
// real test rig — one local route per cluster-VPC jumphost subnet, one
// remote route for the client-VPC subnet. Pure filesystem + JSON. Returns
// (tgw, cluster, ok); ok is false when the state file, the outputs, or
// every CIDR is absent — the caller falls back rather than failing.
//
// The TGW jumphost's "TGW jumphost not created" sentinel (emitted when
// testing_create_tgw_jumphost = false) is normalised to "".
func TestingJumphostSubnetCIDRs(workspace string) (tgw string, cluster map[string]string, ok bool) {
	dir, err := WorkspaceTestingStateDir(workspace)
	if err != nil {
		return "", nil, false
	}
	return readJumphostSubnetCIDRs(filepath.Join(dir, "terraform.tfstate"))
}

func readJumphostSubnetCIDRs(path string) (string, map[string]string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, false
	}
	var s struct {
		Outputs struct {
			TGW struct {
				Value string `json:"value"`
			} `json:"testing_tgw_jumphost_subnet_cidr"`
			Cluster struct {
				Value map[string]string `json:"value"`
			} `json:"testing_cluster_jumphost_subnet_cidrs"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return "", nil, false
	}
	tgw := strings.TrimSpace(s.Outputs.TGW.Value)
	if tgw == "TGW jumphost not created" {
		tgw = ""
	}
	cluster := s.Outputs.Cluster.Value
	ok := tgw != "" || len(cluster) > 0
	return tgw, cluster, ok
}

// StateOutput is one terraform output read from a phase's terraform.tfstate.
type StateOutput struct {
	Value     any
	Sensitive bool
}

// ReadStateOutputs reads the `.outputs` map from <stateDir>/terraform.tfstate,
// returning each output's decoded value + sensitivity. The per-phase `status`
// commands use it to surface runtime state (jumphost IPs, cluster endpoints,
// gateway listeners, …) straight from state — no terraform init, no API key.
// Returns an fs.ErrNotExist-wrapping error when the phase has no state file
// (i.e. the phase is not deployed); callers test that with errors.Is.
func ReadStateOutputs(stateDir string) (map[string]StateOutput, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, "terraform.tfstate"))
	if err != nil {
		return nil, err // fs.ErrNotExist when the phase isn't deployed
	}
	var s struct {
		Outputs map[string]struct {
			Value     json.RawMessage `json:"value"`
			Sensitive bool            `json:"sensitive"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parsing terraform.tfstate outputs in %s: %w", stateDir, err)
	}
	out := make(map[string]StateOutput, len(s.Outputs))
	for name, o := range s.Outputs {
		var v any
		if len(o.Value) > 0 {
			_ = json.Unmarshal(o.Value, &v)
		}
		out[name] = StateOutput{Value: v, Sensitive: o.Sensitive}
	}
	return out, nil
}

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
			Mode      string            `json:"mode"`
			Module    string            `json:"module"`
			Type      string            `json:"type"`
			Instances []json.RawMessage `json:"instances"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return false, err
	}
	for _, r := range s.Resources {
		// Require ≥1 INSTANCE, not just the resource block. In a BNK-phase
		// state the cluster module is count=0 (create_roks_cluster=false), so
		// terraform records a managed ibm_container_vpc_cluster block with
		// ZERO instances — a husk, NOT a real cluster. Counting the husk as
		// "Legacy single-state" falsely poisoned every BNK/testing phase
		// (refusing the phase verbs and routing `up` into the monolith path,
		// which cross-contaminated the per-phase states).
		if r.Mode != "managed" || r.Type != "ibm_container_vpc_cluster" || len(r.Instances) == 0 {
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
