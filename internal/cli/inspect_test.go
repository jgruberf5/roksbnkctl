package cli

// Table tests for `runStatus`'s per-phase deployment lines. Sprint 29:
// the lines are driven by `config.DetectPresence` (four phases — Cluster
// / BNK / Testing / Gateway) rather than the old two-phase DetectShape.
// We stage tfstate fixtures from `internal/config/testdata/` into a temp
// `ROKSBNKCTL_HOME` so DetectPresence picks them up the way it does at
// runtime, then assert on the per-phase lines plus the legacy script-
// compat `Last apply` line.
//
// We DON'T assert exact timestamps (file mtimes drift); the token
// `deployed (last apply ` is enough to distinguish a deployed phase from
// "not deployed".

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// stageStatusWorkspace stages a workspace under a per-test
// ROKSBNKCTL_HOME so the status command finds:
//
//   - a minimal `config.yaml` (so `cctx.Workspace` is non-nil)
//   - the requested tfstate fixtures under `state/` and `state-cluster/`
//
// The fixtures are the Sprint 8 four-shape set living in
// `internal/config/testdata/`; we borrow them via a relative path so
// the test surface stays in sync without duplicating files.
func stageStatusWorkspace(t *testing.T, shape config.WorkspaceShape) string {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "status-test"

	// Minimal workspace config so config.New(ws).Workspace is non-nil.
	if err := config.SaveWorkspace(ws, &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
		Cluster:  config.ClusterCfg{Name: "test-cluster"},
		TFSource: config.TFSourceCfg{Type: "github", Repo: "test/repo", Ref: "main"},
	}); err != nil {
		t.Fatal(err)
	}

	switch shape {
	case config.ShapeEmpty:
		// No state files.
	case config.ShapeClusterOnly:
		writeStateForStatusTest(t, ws, "", "tfstate_cluster_only.json")
	case config.ShapeSplit:
		writeStateForStatusTest(t, ws, "tfstate_split.json", "tfstate_cluster_only.json")
	case config.ShapeLegacySingle:
		writeStateForStatusTest(t, ws, "tfstate_legacy_single.json", "")
	default:
		t.Fatalf("unsupported test shape %v", shape)
	}
	return ws
}

func writeStateForStatusTest(t *testing.T, workspace, trialFixture, clusterFixture string) {
	t.Helper()
	if trialFixture != "" {
		dir, err := config.WorkspaceStateDir(workspace)
		if err != nil {
			t.Fatal(err)
		}
		copyStatusFixture(t, trialFixture, filepath.Join(dir, "terraform.tfstate"))
	}
	if clusterFixture != "" {
		dir, err := config.WorkspaceClusterStateDir(workspace)
		if err != nil {
			t.Fatal(err)
		}
		copyStatusFixture(t, clusterFixture, filepath.Join(dir, "terraform.tfstate"))
	}
}

func copyStatusFixture(t *testing.T, fixture, dst string) {
	t.Helper()
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

// captureStdout redirects os.Stdout for the duration of fn, returning
// what was written. Required because `runStatus` writes through a
// tabwriter wrapping os.Stdout; the function takes no io.Writer.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = prev })

	doneCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		doneCh <- buf.String()
	}()

	fn()
	_ = w.Close()
	out := <-doneCh
	_ = r.Close()
	return out
}

// runStatusForTest invokes runStatus against the staged workspace and
// returns its stdout. We use a fresh cobra.Command so cmd.Context()
// doesn't nil-deref. The cluster-probe step at the tail of runStatus
// will fail gracefully ("no kubeconfig" path); we don't assert on it.
func runStatusForTest(t *testing.T, workspace string) string {
	t.Helper()

	prevWs := flagWorkspace
	flagWorkspace = workspace
	t.Cleanup(func() { flagWorkspace = prevWs })

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())

	return captureStdout(t, func() {
		if err := runStatus(cmd, nil); err != nil {
			t.Fatalf("runStatus returned error: %v", err)
		}
	})
}

// — table tests —

func TestRunStatus_ShapeEmpty_NoPhaseDeployments(t *testing.T) {
	ws := stageStatusWorkspace(t, config.ShapeEmpty)
	out := runStatusForTest(t, ws)

	wantContains := []string{
		"Cluster phase:",
		"not deployed",
		"BNK trial:",
	}
	for _, w := range wantContains {
		if !strings.Contains(out, w) {
			t.Errorf("expected output to contain %q, got:\n%s", w, out)
		}
	}
	// The v1.0.x Last apply line MUST NOT appear on non-Legacy shapes;
	// PRD 06 §"`status` command integration" replaces it.
	if strings.Contains(out, "Last apply:") {
		t.Errorf("ShapeEmpty: should not emit 'Last apply' line, got:\n%s", out)
	}
	// "deployed (last apply" is the per-phase-deployed token; should not
	// appear on a fully-empty workspace.
	if strings.Contains(out, "deployed (last apply") {
		t.Errorf("ShapeEmpty: should not emit a deployed-line, got:\n%s", out)
	}
}

func TestRunStatus_ShapeClusterOnly_ClusterDeployedTrialNotDeployed(t *testing.T) {
	ws := stageStatusWorkspace(t, config.ShapeClusterOnly)
	out := runStatusForTest(t, ws)

	// Cluster phase line should carry the deployed token; BNK trial
	// should be `not deployed`.
	if !strings.Contains(out, "Cluster phase:") {
		t.Errorf("missing 'Cluster phase:' line:\n%s", out)
	}
	if !strings.Contains(out, "deployed (last apply") {
		t.Errorf("expected cluster phase to show 'deployed (last apply …)', got:\n%s", out)
	}
	if !strings.Contains(out, "BNK trial:") {
		t.Errorf("missing 'BNK trial:' line:\n%s", out)
	}
	// Specifically: there should be exactly one "deployed (last apply"
	// substring in the cluster-only shape — the cluster line.
	if got := strings.Count(out, "deployed (last apply"); got != 1 {
		t.Errorf("ShapeClusterOnly: want 1 'deployed (last apply' occurrence, got %d in:\n%s", got, out)
	}
	if strings.Contains(out, "Last apply:") {
		t.Errorf("ShapeClusterOnly: should not emit v1.0.x 'Last apply' line:\n%s", out)
	}
}

func TestRunStatus_ShapeSplit_BothPhasesDeployed(t *testing.T) {
	ws := stageStatusWorkspace(t, config.ShapeSplit)
	out := runStatusForTest(t, ws)

	if !strings.Contains(out, "Cluster phase:") {
		t.Errorf("missing 'Cluster phase:' line:\n%s", out)
	}
	if !strings.Contains(out, "BNK trial:") {
		t.Errorf("missing 'BNK trial:' line:\n%s", out)
	}
	// Cluster + BNK deployed → two `deployed (last apply` substrings.
	if got := strings.Count(out, "deployed (last apply"); got != 2 {
		t.Errorf("ShapeSplit: want 2 'deployed (last apply' occurrences, got %d in:\n%s", got, out)
	}
	// Testing + Gateway aren't staged here, so both read `not deployed`.
	if !strings.Contains(out, "Testing phase:") || !strings.Contains(out, "Gateway phase:") {
		t.Errorf("ShapeSplit: missing Testing/Gateway phase lines:\n%s", out)
	}
	if got := strings.Count(out, "not deployed"); got != 2 {
		t.Errorf("ShapeSplit: want 2 'not deployed' (Testing + Gateway), got %d in:\n%s", got, out)
	}
	if strings.Contains(out, "Last apply:") {
		t.Errorf("ShapeSplit: should not emit v1.0.x 'Last apply' line:\n%s", out)
	}
}

// stagePhaseState copies a tfstate fixture into one phase's state dir.
// dirFn is the config helper yielding that phase's directory (e.g.
// config.WorkspaceTestingStateDir).
func stagePhaseState(t *testing.T, ws, fixture string, dirFn func(string) (string, error)) {
	t.Helper()
	dir, err := dirFn(ws)
	if err != nil {
		t.Fatal(err)
	}
	copyStatusFixture(t, fixture, filepath.Join(dir, "terraform.tfstate"))
}

func TestRunStatus_AllFourPhasesDeployed(t *testing.T) {
	// Cluster + BNK from the split shape; Testing + Gateway staged on top.
	ws := stageStatusWorkspace(t, config.ShapeSplit)
	stagePhaseState(t, ws, "tfstate_testing.json", config.WorkspaceTestingStateDir)
	stagePhaseState(t, ws, "tfstate_testing.json", config.WorkspaceGatewayStateDir)
	out := runStatusForTest(t, ws)

	for _, label := range []string{"Cluster phase:", "BNK trial:", "Testing phase:", "Gateway phase:"} {
		if !strings.Contains(out, label) {
			t.Errorf("missing %q line:\n%s", label, out)
		}
	}
	if got := strings.Count(out, "deployed (last apply"); got != 4 {
		t.Errorf("want 4 deployed phases, got %d in:\n%s", got, out)
	}
	if strings.Contains(out, "not deployed") {
		t.Errorf("all four phases should be deployed, got 'not deployed' in:\n%s", out)
	}
}

func TestRunStatus_ShapeLegacySingle_PreservesV10xLastApply(t *testing.T) {
	ws := stageStatusWorkspace(t, config.ShapeLegacySingle)
	out := runStatusForTest(t, ws)

	// Shape callout + v1.0.x Last apply line both present, per PRD 06
	// §"`status` command integration" (script-compat preservation).
	if !strings.Contains(out, "Shape:") || !strings.Contains(out, "legacy single-state") {
		t.Errorf("ShapeLegacySingle: missing 'Shape: … legacy single-state' callout:\n%s", out)
	}
	if !strings.Contains(out, "Last apply:") {
		t.Errorf("ShapeLegacySingle: v1.0.x 'Last apply' line must be preserved verbatim:\n%s", out)
	}
	// Per-phase lines should NOT appear under Legacy.
	if strings.Contains(out, "Cluster phase:") || strings.Contains(out, "BNK trial:") {
		t.Errorf("ShapeLegacySingle: should not emit per-phase lines (only the v1.0.x Last apply line + shape callout):\n%s", out)
	}
}
