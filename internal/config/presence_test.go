package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Sprint 28 — additive hermetic coverage for the per-phase presence model
// (DetectPresence) + the pre-Sprint-28 jumphost migration condition
// (TestingMigrationNeeded). Fixtures are staged into a per-test
// ROKSBNKCTL_HOME exactly as the runtime would see them:
//
//	<home>/<ws>/state-cluster/terraform.tfstate   (Cluster)
//	<home>/<ws>/state/terraform.tfstate            (BNK or Legacy monolith)
//	<home>/<ws>/state-testing/terraform.tfstate    (Testing jumphosts)
//
// No terraform / cloud calls — pure filesystem + JSON-decode, the same
// contract DetectShape already pins. These tests mirror the style of the
// existing tfstate_test.go (writeFixture + a per-test home) and assert the
// architect's §2c detection rules + the §2d dispatch table's presence
// inputs.

// setupWorkspacePresence stages the three per-phase state files DetectPresence
// reads. Empty fixture strings skip writing that side (so missing-state
// scenarios are expressible). Returns the workspace name.
func setupWorkspacePresence(t *testing.T, clusterFixture, bnkFixture, testingFixture string) string {
	t.Helper()
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	if clusterFixture != "" {
		dir, err := WorkspaceClusterStateDir(testWorkspace)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, clusterFixture, filepath.Join(dir, "terraform.tfstate"))
	}
	if bnkFixture != "" {
		dir, err := WorkspaceStateDir(testWorkspace)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, bnkFixture, filepath.Join(dir, "terraform.tfstate"))
	}
	if testingFixture != "" {
		dir, err := WorkspaceTestingStateDir(testWorkspace)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, testingFixture, filepath.Join(dir, "terraform.tfstate"))
	}
	return testWorkspace
}

// TestDetectPresence_Table walks every presence combination the §2d dispatch
// table keys off, plus the data-source-only no-false-positive edges and the
// legacy precedence rule (Legacy forces BNK/Testing false).
func TestDetectPresence_Table(t *testing.T) {
	tests := []struct {
		name           string
		clusterFixture string
		bnkFixture     string
		testingFixture string
		want           Presence
		wantErr        bool
	}{
		{
			name: "none — fresh workspace, nothing applied",
			want: Presence{},
		},
		{
			name:           "cluster only (cluster up, no BNK/Testing yet)",
			clusterFixture: "tfstate_cluster_only.json",
			want:           Presence{Cluster: true},
		},
		{
			name:       "BNK only (registered cluster + bnk up; cluster identity in cluster-outputs.json)",
			bnkFixture: "tfstate_split.json",
			want:       Presence{BNK: true},
		},
		{
			name:           "testing only (registered cluster + testing up)",
			testingFixture: "tfstate_testing.json",
			want:           Presence{Testing: true},
		},
		{
			name:           "cluster + BNK (cluster up + bnk up; no jumphosts yet)",
			clusterFixture: "tfstate_cluster_only.json",
			bnkFixture:     "tfstate_split.json",
			want:           Presence{Cluster: true, BNK: true},
		},
		{
			name:           "cluster + testing (cluster up + testing up; no BNK yet)",
			clusterFixture: "tfstate_cluster_only.json",
			testingFixture: "tfstate_testing.json",
			want:           Presence{Cluster: true, Testing: true},
		},
		{
			name:           "all three present (the new steady state)",
			clusterFixture: "tfstate_cluster_only.json",
			bnkFixture:     "tfstate_split.json",
			testingFixture: "tfstate_testing.json",
			want:           Presence{Cluster: true, BNK: true, Testing: true},
		},
		{
			// The v1.0.x monolith: cluster modules live in state/. Legacy
			// must be true and BNK/Testing forced false (the monolith is its
			// own world; the phase verbs refuse on it).
			name:       "legacy single-state (cluster modules in state/) → Legacy, BNK/Testing forced false",
			bnkFixture: "tfstate_legacy_single.json",
			want:       Presence{Legacy: true},
		},
		{
			// Legacy keys off state/ alone; a stray state-testing/ does not
			// flip BNK true, but Testing is still its own independent signal.
			name:           "legacy state/ + populated state-testing/ → Legacy true, BNK false, Testing true",
			bnkFixture:     "tfstate_legacy_single.json",
			testingFixture: "tfstate_testing.json",
			want:           Presence{Legacy: true, Testing: true},
		},
		{
			// Sprint 22 regression, retargeted: a post-up BNK state carries
			// DATA-source refreshes of ibm_container_vpc_cluster under
			// cluster-phase module prefixes. That is NOT the managed-cluster
			// signal, so it must read as BNK (managed helm releases present),
			// never Legacy.
			name:       "BNK state with cluster-phase DATA sources (refresh shape) → BNK, not Legacy",
			bnkFixture: "tfstate_split_data_in_trial.json",
			want:       Presence{BNK: true},
		},
		{
			// The cluster-present check requires a MANAGED
			// ibm_container_vpc_cluster in state-cluster/. A state-cluster/
			// that only carries data-source reads (plus unrelated managed
			// resources) must NOT false-positive as Cluster.
			name:           "state-cluster/ with only DATA-source cluster reads → Cluster false (no false-positive)",
			clusterFixture: "tfstate_split_data_in_trial.json",
			want:           Presence{},
		},
		{
			name:           "empty state files on every phase (applied then fully destroyed)",
			clusterFixture: "tfstate_empty.json",
			bnkFixture:     "tfstate_empty.json",
			testingFixture: "tfstate_empty.json",
			want:           Presence{},
		},
		{
			name:           "malformed cluster state surfaces an error",
			clusterFixture: "tfstate_malformed.json",
			wantErr:        true,
		},
		{
			name:       "malformed BNK state surfaces an error",
			bnkFixture: "tfstate_malformed.json",
			wantErr:    true,
		},
		{
			name:           "malformed testing state surfaces an error",
			testingFixture: "tfstate_malformed.json",
			wantErr:        true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := setupWorkspacePresence(t, tc.clusterFixture, tc.bnkFixture, tc.testingFixture)
			got, err := DetectPresence(ws)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got presence=%+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("DetectPresence = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestPresence_Any pins the Any() helper RunDown uses to short-circuit
// "nothing to destroy".
func TestPresence_Any(t *testing.T) {
	cases := []struct {
		p    Presence
		want bool
	}{
		{Presence{}, false},
		{Presence{Cluster: true}, true},
		{Presence{BNK: true}, true},
		{Presence{Testing: true}, true},
		{Presence{Legacy: true}, true},
		{Presence{Cluster: true, BNK: true, Testing: true}, true},
	}
	for _, c := range cases {
		if got := c.p.Any(); got != c.want {
			t.Errorf("Presence%+v.Any() = %v, want %v", c.p, got, c.want)
		}
	}
}

// TestDetectPresence_MissingHomeIsEmpty pins the "never-applied workspace is
// Empty, not an error" contract for the three-phase detector.
func TestDetectPresence_MissingHomeIsEmpty(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	got, err := DetectPresence("fresh-workspace")
	if err != nil {
		t.Fatalf("DetectPresence: %v", err)
	}
	if (got != Presence{}) {
		t.Errorf("expected zero Presence for a never-applied workspace, got %+v", got)
	}
}

// TestTestingMigrationNeeded_Table pins the architect's §1d migration
// condition: jumphosts (module.testing.*) still live in the BNK state AND
// state-testing/ is empty. It must be true ONLY for that pre-Sprint-28 split
// shape — not for a fresh workspace, not once the jumphosts are split out, and
// not for a legacy monolith whose jumphosts are already adopted by a populated
// state-testing/.
func TestTestingMigrationNeeded_Table(t *testing.T) {
	tests := []struct {
		name           string
		bnkFixture     string
		testingFixture string
		want           bool
	}{
		{
			name: "fresh workspace — nothing to migrate",
			want: false,
		},
		{
			// The migration target shape: a pre-Sprint-28 split workspace
			// where state/ still owns module.testing.* and state-testing/ is
			// absent.
			name:       "jumphosts in BNK state, state-testing/ absent → migration needed",
			bnkFixture: "tfstate_bnk_with_jumphosts.json",
			want:       true,
		},
		{
			// Same BNK state, but state-testing/ already populated → the move
			// already happened (or is in progress); don't re-nudge.
			name:           "jumphosts in BNK state, state-testing/ populated → NOT needed",
			bnkFixture:     "tfstate_bnk_with_jumphosts.json",
			testingFixture: "tfstate_testing.json",
			want:           false,
		},
		{
			// A clean post-Sprint-28 BNK state (no module.testing.*) never
			// triggers the nudge, even with an empty state-testing/.
			name:       "BNK state without jumphosts → NOT needed",
			bnkFixture: "tfstate_split.json",
			want:       false,
		},
		{
			// Legacy monolith carries module.testing.* in state/ too; with no
			// state-testing/ this is technically the same "jumphosts in
			// state/" signal. TestingMigrationNeeded is structural (it does
			// not special-case legacy) — the dispatchers gate the nudge on
			// !Legacy separately, so assert the structural truth here.
			name:       "legacy monolith (jumphosts in state/, no state-testing/) → structurally needed",
			bnkFixture: "tfstate_legacy_single.json",
			want:       true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := setupWorkspacePresence(t, "", tc.bnkFixture, tc.testingFixture)
			got, err := TestingMigrationNeeded(ws)
			if err != nil {
				t.Fatalf("TestingMigrationNeeded: %v", err)
			}
			if got != tc.want {
				t.Errorf("TestingMigrationNeeded = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestStateHasManagedModule_DataVsManaged pins that the migration probe
// counts only MANAGED module.testing.* ownership — a data-source read under
// module.testing must not be mistaken for a jumphost the workspace owns.
func TestStateHasManagedModule_DataVsManaged(t *testing.T) {
	dir := t.TempDir()
	managedPath := filepath.Join(dir, "managed.tfstate")
	writeJSON(t, managedPath, `{"resources":[{"mode":"managed","module":"module.testing.module.jumphost"}]}`)
	dataPath := filepath.Join(dir, "data.tfstate")
	writeJSON(t, dataPath, `{"resources":[{"mode":"data","module":"module.testing"}]}`)

	if has, err := stateHasManagedModule(managedPath, "module.testing"); err != nil || !has {
		t.Errorf("managed module.testing.* should match: has=%v err=%v", has, err)
	}
	if has, err := stateHasManagedModule(dataPath, "module.testing"); err != nil || has {
		t.Errorf("data-source-only module.testing must NOT match: has=%v err=%v", has, err)
	}
	// Missing file → (false, nil), same contract as the other probes.
	if has, err := stateHasManagedModule(filepath.Join(dir, "nope.tfstate"), "module.testing"); err != nil || has {
		t.Errorf("missing file should be (false,nil): has=%v err=%v", has, err)
	}
}

// writeJSON drops a literal JSON body at path (parent dirs must already
// exist). Small helper for the inline state bodies above.
func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
