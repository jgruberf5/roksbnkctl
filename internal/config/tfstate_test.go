package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Test fixtures live in testdata/ and are copied into a per-test
// ROKSBNKCTL_HOME so DetectShape sees the same on-disk layout it would
// see at runtime: <home>/<workspace>/state/terraform.tfstate and
// <home>/<workspace>/state-cluster/terraform.tfstate.

const testWorkspace = "test-ws"

// writeFixture copies a testdata file into the target path, creating
// parent directories as needed. Fails the test on any I/O error.
func writeFixture(t *testing.T, fixture, dst string) {
	t.Helper()
	src := filepath.Join("testdata", fixture)
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatalf("writing %s: %v", dst, err)
	}
}

// setupWorkspaceShape stages the on-disk state files that DetectShape
// reads. Empty strings skip writing that side (so we can build
// missing-state scenarios). Returns the workspace name for the caller
// to feed to DetectShape.
func setupWorkspaceShape(t *testing.T, trialFixture, clusterFixture string) string {
	t.Helper()
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	if trialFixture != "" {
		trialDir, err := WorkspaceStateDir(testWorkspace)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, trialFixture, filepath.Join(trialDir, "terraform.tfstate"))
	}
	if clusterFixture != "" {
		clusterDir, err := WorkspaceClusterStateDir(testWorkspace)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, clusterFixture, filepath.Join(clusterDir, "terraform.tfstate"))
	}
	return testWorkspace
}

// TestDetectShape_Table covers the four classifications PRD 06
// promises, the two error edges (missing state → empty, malformed JSON
// → error), plus the legacy single-state precedence ("both states have
// resources, but trial has cluster modules" → LegacySingle, not Split).
func TestDetectShape_Table(t *testing.T) {
	tests := []struct {
		name           string
		trialFixture   string
		clusterFixture string
		wantShape      WorkspaceShape
		wantErr        bool
	}{
		{
			name:      "neither phase applied",
			wantShape: ShapeEmpty,
		},
		{
			name:           "cluster only (cluster up succeeded, no trial yet)",
			clusterFixture: "tfstate_cluster_only.json",
			wantShape:      ShapeClusterOnly,
		},
		{
			name:           "split (both phases populated independently)",
			trialFixture:   "tfstate_split.json",
			clusterFixture: "tfstate_cluster_only.json",
			wantShape:      ShapeSplit,
		},
		{
			name:         "split with empty cluster (registered cluster, then trial applied)",
			trialFixture: "tfstate_split.json",
			// cluster state file missing entirely — still split because
			// the cluster identity lives in cluster-outputs.json on the
			// register path, not in state-cluster/.
			wantShape: ShapeSplit,
		},
		{
			name:         "legacy single-state (v1.0.x pre-split)",
			trialFixture: "tfstate_legacy_single.json",
			wantShape:    ShapeLegacySingle,
		},
		{
			name:           "legacy beats split when trial carries cluster modules",
			trialFixture:   "tfstate_legacy_single.json",
			clusterFixture: "tfstate_cluster_only.json",
			wantShape:      ShapeLegacySingle,
		},
		{
			name:           "empty trial + empty cluster state files (applied then fully destroyed)",
			trialFixture:   "tfstate_empty.json",
			clusterFixture: "tfstate_empty.json",
			wantShape:      ShapeEmpty,
		},
		{
			name:         "malformed trial JSON surfaces error",
			trialFixture: "tfstate_malformed.json",
			wantErr:      true,
		},
		{
			name:           "malformed cluster JSON surfaces error",
			clusterFixture: "tfstate_malformed.json",
			wantErr:        true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := setupWorkspaceShape(t, tc.trialFixture, tc.clusterFixture)
			got, err := DetectShape(ws)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got shape=%s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantShape {
				t.Errorf("DetectShape = %s, want %s", got, tc.wantShape)
			}
		})
	}
}

// TestDetectShape_MissingStateIsEmpty pins the "no state dirs at all
// means empty" contract — a workspace that was never applied must not
// surface as an error, since fresh-init workspaces are normal.
func TestDetectShape_MissingStateIsEmpty(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	// No state dirs created; DetectShape should classify as Empty
	// without error.
	got, err := DetectShape("fresh-workspace")
	if err != nil {
		t.Fatalf("DetectShape: %v", err)
	}
	if got != ShapeEmpty {
		t.Errorf("expected ShapeEmpty for a never-applied workspace, got %s", got)
	}
}

// TestTrialStateHasClusterModules_ExactMatch covers the
// `r.Module == prefix` branch — the cluster_phase modules can appear
// at the root of the module address (no trailing sub-path).
func TestTrialStateHasClusterModules_ExactMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfstate")
	const body = `{
		"resources": [
			{"module": "module.cert_manager"}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	has, err := trialStateHasClusterModules(path)
	if err != nil {
		t.Fatalf("trialStateHasClusterModules: %v", err)
	}
	if !has {
		t.Error("expected exact-match cluster module to be detected")
	}
}

// TestTrialStateHasClusterModules_NestedPrefix covers the
// `strings.HasPrefix(r.Module, prefix+".")` branch — nested sub-modules
// of the cluster phase must also be detected (matches the real
// canada-roks state shape: `module.roks_cluster.module.cluster`).
func TestTrialStateHasClusterModules_NestedPrefix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfstate")
	const body = `{
		"resources": [
			{"module": "module.roks_cluster.module.cluster"},
			{"module": "module.testing.module.jumphost"}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	has, err := trialStateHasClusterModules(path)
	if err != nil {
		t.Fatalf("trialStateHasClusterModules: %v", err)
	}
	if !has {
		t.Error("expected nested cluster module to be detected")
	}
}

// TestTrialStateHasClusterModules_DotGuard checks that the trailing-dot
// match prevents false positives — `module.roks_cluster_extras` must
// NOT match `module.roks_cluster`.
func TestTrialStateHasClusterModules_DotGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfstate")
	const body = `{
		"resources": [
			{"module": "module.roks_cluster_extras"},
			{"module": "module.cert_managerific"}
		]
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	has, err := trialStateHasClusterModules(path)
	if err != nil {
		t.Fatalf("trialStateHasClusterModules: %v", err)
	}
	if has {
		t.Error("expected non-cluster module names with cluster prefixes to NOT match")
	}
}

// TestTfstateHasResources_MissingFile pins the "missing file → no
// resources, no error" contract.
func TestTfstateHasResources_MissingFile(t *testing.T) {
	has, err := tfstateHasResources(filepath.Join(t.TempDir(), "does-not-exist.tfstate"))
	if err != nil {
		t.Fatalf("expected no error on missing file, got %v", err)
	}
	if has {
		t.Error("expected has=false for missing file")
	}
}

// TestTfstateHasResources_Malformed pins the "malformed JSON → error"
// contract so dispatch doesn't silently misclassify a corrupt state as
// empty.
func TestTfstateHasResources_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfstate")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tfstateHasResources(path); err == nil {
		t.Error("expected error on malformed JSON")
	}
}

// TestWorkspaceShape_String pins each enum's human-readable form. The
// strings end up in log lines and error messages, so a casual rename
// would be a user-facing regression.
func TestWorkspaceShape_String(t *testing.T) {
	cases := map[WorkspaceShape]string{
		ShapeUnknown:       "unknown",
		ShapeEmpty:         "empty",
		ShapeClusterOnly:   "cluster-only",
		ShapeSplit:         "split",
		ShapeLegacySingle:  "legacy-single-state",
		WorkspaceShape(99): "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("WorkspaceShape(%d).String() = %q, want %q", s, got, want)
		}
	}
}
