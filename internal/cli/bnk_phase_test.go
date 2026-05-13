package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The bnk dispatch tests cover the refusal matrix from PRD 06
// §"Dispatch table". Tests stage on-disk state under a temp
// ROKSBNKCTL_HOME so config.DetectShape sees the same layout it would
// at runtime, then assert that RunE returns the expected refusal
// error.
//
// The non-refusal cells (Empty → cluster-bootstrap, ClusterOnly/Split
// → trial up) would dispatch to terraform-exec which the test
// environment doesn't exercise; we assert those paths by *absence* of
// the refusal error message. The unit boundary is "refusal logic in
// runBnkUp / runBnkDown is correct" — the underlying trial /
// cluster flows are tested via their own paths (and live e2e).

// stageWorkspaceShape places terraform.tfstate files under a temp
// ROKSBNKCTL_HOME so DetectShape classifies the workspace according to
// `shape`. Empty workspace gets neither file. Returns the workspace
// name.
func stageWorkspaceShape(t *testing.T, shape config.WorkspaceShape) string {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "bnk-test"

	switch shape {
	case config.ShapeEmpty:
		// No files.
	case config.ShapeClusterOnly:
		writeStateForTest(t, ws, "", "tfstate_cluster_only.json")
	case config.ShapeSplit:
		writeStateForTest(t, ws, "tfstate_split.json", "tfstate_cluster_only.json")
	case config.ShapeLegacySingle:
		writeStateForTest(t, ws, "tfstate_legacy_single.json", "")
	default:
		t.Fatalf("unsupported test shape %v", shape)
	}
	return ws
}

// writeStateForTest copies the named fixtures from
// internal/config/testdata into the workspace's trial / cluster state
// dirs. Empty fixture names skip writing that side.
func writeStateForTest(t *testing.T, workspace, trialFixture, clusterFixture string) {
	t.Helper()
	if trialFixture != "" {
		trialDir, err := config.WorkspaceStateDir(workspace)
		if err != nil {
			t.Fatal(err)
		}
		copyFixtureTo(t, trialFixture, filepath.Join(trialDir, "terraform.tfstate"))
	}
	if clusterFixture != "" {
		clusterDir, err := config.WorkspaceClusterStateDir(workspace)
		if err != nil {
			t.Fatal(err)
		}
		copyFixtureTo(t, clusterFixture, filepath.Join(clusterDir, "terraform.tfstate"))
	}
}

func copyFixtureTo(t *testing.T, fixture, dst string) {
	t.Helper()
	// internal/config/testdata is the canonical fixture home; cli tests
	// borrow it via a relative path so we don't duplicate them.
	src := filepath.Join("..", "config", "testdata", fixture)
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// pointWorkspaceFlag aims the global -w flag at the staged workspace
// for the duration of the test. We don't go through cobra parsing
// because the test calls RunE directly.
func pointWorkspaceFlag(t *testing.T, name string) {
	t.Helper()
	prev := flagWorkspace
	flagWorkspace = name
	t.Cleanup(func() { flagWorkspace = prev })
}

// newCmd produces a cobra.Command with a context so the RunE
// implementations can pull cmd.Context() without nil-deref. The actual
// command tree isn't relevant to refusal-logic assertions.
func newCmd() *cobra.Command {
	c := &cobra.Command{}
	c.SetContext(context.Background())
	return c
}

// TestRunBnkUp_LegacySingleRefuses — `bnk up` must refuse on legacy
// single-state workspaces because cluster + trial share one state file
// there, and the trial can't be applied in isolation.
func TestRunBnkUp_LegacySingleRefuses(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeLegacySingle)
	pointWorkspaceFlag(t, ws)

	err := runBnkUp(newCmd(), nil)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	want := "legacy single-state"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("refusal message missing %q\n  got: %v", want, err)
	}
}

// TestRunBnkDown_LegacySingleRefuses — same constraint as bnk up: no
// trial-only destroy on legacy single-state.
func TestRunBnkDown_LegacySingleRefuses(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeLegacySingle)
	pointWorkspaceFlag(t, ws)

	err := runBnkDown(newCmd(), nil)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	want := "legacy single-state"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("refusal message missing %q\n  got: %v", want, err)
	}
}

// TestRunBnkDown_EmptyRefuses — `bnk down` must refuse on empty
// workspaces with "no BNK trial state to destroy" (PRD 06 §"Refusal
// messages").
func TestRunBnkDown_EmptyRefuses(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeEmpty)
	pointWorkspaceFlag(t, ws)

	err := runBnkDown(newCmd(), nil)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	want := "no BNK trial state to destroy"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("refusal message missing %q\n  got: %v", want, err)
	}
}

// TestRunBnkDown_ClusterOnlyRefuses — same "no trial" message on a
// cluster-only workspace; user landed `cluster up` but never
// `bnk up` / `up`.
func TestRunBnkDown_ClusterOnlyRefuses(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeClusterOnly)
	pointWorkspaceFlag(t, ws)

	err := runBnkDown(newCmd(), nil)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	want := "no BNK trial state to destroy"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("refusal message missing %q\n  got: %v", want, err)
	}
}

// TestRunBnkDown_SplitDispatches — Split is the only shape where
// `bnk down` is a non-refusal. We can't realistically run terraform in
// this test, so the assertion is "the error doesn't match any of the
// known refusal messages." Reaching the trial-down path is what we
// want; failure there is downstream of this unit's contract.
func TestRunBnkDown_SplitDispatches(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeSplit)
	pointWorkspaceFlag(t, ws)

	err := runBnkDown(newCmd(), nil)
	// We expect an error from the actual terraform path (no workspace
	// config.yaml, no API key, etc.). The contract here is that the
	// error is NOT one of the bnk-down refusal messages.
	if err == nil {
		// Surprising but acceptable — there's no terraform.tfstate.user
		// to make terraform-exec really fail; the test environment
		// passed all the way through. Nothing to assert beyond
		// non-refusal.
		return
	}
	for _, badMatch := range []string{
		"legacy single-state",
		"no BNK trial state to destroy",
	} {
		if strings.Contains(err.Error(), badMatch) {
			t.Errorf("Split workspace tripped refusal %q\n  got: %v", badMatch, err)
		}
	}
}

// TestClusterDown_LegacySingleRefuses pins the `cluster down` refusal
// on legacy single-state (PRD 06 §"Refusal messages"). cluster_phase
// has the same shape-detection wiring as bnk_phase, so we cover its
// happy-path-blocked behavior here.
func TestClusterDown_LegacySingleRefuses(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeLegacySingle)
	pointWorkspaceFlag(t, ws)

	err := runClusterDown(newCmd(), nil)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	want := "legacy single-state"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("refusal message missing %q\n  got: %v", want, err)
	}
}

// TestClusterDown_SplitRefuses — cluster down must refuse when trial
// state exists so users aren't surprised by orphans. Replaces the
// v1.0.x "warning, but-prompt" copy.
func TestClusterDown_SplitRefuses(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeSplit)
	pointWorkspaceFlag(t, ws)

	err := runClusterDown(newCmd(), nil)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	want := "run `roksbnkctl bnk down` first"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("refusal message missing %q\n  got: %v", want, err)
	}
}

// TestClusterDown_EmptyRefuses — nothing to destroy.
func TestClusterDown_EmptyRefuses(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeEmpty)
	pointWorkspaceFlag(t, ws)

	err := runClusterDown(newCmd(), nil)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	want := "nothing to destroy"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("refusal message missing %q\n  got: %v", want, err)
	}
}

// TestClusterUp_LegacySingleRefuses — cluster up on legacy must
// refuse: applying the cluster phase against an empty state-cluster/
// would create a duplicate cluster.
func TestClusterUp_LegacySingleRefuses(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeLegacySingle)
	pointWorkspaceFlag(t, ws)

	err := runClusterUp(newCmd(), nil)
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	want := "v1.0.x single-state"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("refusal message missing %q\n  got: %v", want, err)
	}
}

// TestRunDown_EmptyRefuses — composite `down` on empty errors with
// "nothing to destroy", per PRD 06 dispatch table. Pinned here because
// the composite is a new code path in Sprint 8.
func TestRunDown_EmptyRefuses(t *testing.T) {
	ws := stageWorkspaceShape(t, config.ShapeEmpty)
	pointWorkspaceFlag(t, ws)

	err := runDown(newCmd(), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	want := "nothing to destroy"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error message missing %q\n  got: %v", want, err)
	}
}
