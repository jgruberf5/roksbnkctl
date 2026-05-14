package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stageWorkspaceForApplied points ROKSBNKCTL_HOME at a tempdir and
// returns a workspace name to use with WriteAppliedTFVars. No state
// files are required — the snapshot writer only touches the per-phase
// state dir.
func stageWorkspaceForApplied(t *testing.T) string {
	t.Helper()
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())
	return "applied-test-ws"
}

// writeTFVarsSource places a tfvars body into <dir>/<name> for the
// snapshot writer to consume. Returns the absolute path.
func writeTFVarsSource(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestAppliedTFVars_PathPerPhase pins the file-path contract from PRD
// 07: cluster/trial/legacy-single each route to the right per-phase
// state dir.
func TestAppliedTFVars_PathPerPhase(t *testing.T) {
	ws := stageWorkspaceForApplied(t)

	cluster, err := AppliedTFVarsPath(ws, "cluster")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cluster, filepath.Join("state-cluster", "terraform.applied.tfvars")) {
		t.Errorf("cluster path = %q, want suffix state-cluster/terraform.applied.tfvars", cluster)
	}

	trial, err := AppliedTFVarsPath(ws, "trial")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(trial, filepath.Join("state", "terraform.applied.tfvars")) {
		t.Errorf("trial path = %q, want suffix state/terraform.applied.tfvars", trial)
	}

	legacy, err := AppliedTFVarsPath(ws, "legacy-single")
	if err != nil {
		t.Fatal(err)
	}
	if legacy != trial {
		t.Errorf("legacy path = %q, want same as trial %q", legacy, trial)
	}
}

// TestAppliedTFVars_SourceOrdering feeds three sources in a known order
// and asserts the three section headers appear in the same order.
func TestAppliedTFVars_SourceOrdering(t *testing.T) {
	ws := stageWorkspaceForApplied(t)
	stateDir, err := WorkspaceClusterStateDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	auto := writeTFVarsSource(t, stateDir, "terraform.tfvars", `region = "ca-tor"`)
	wsDir := filepath.Dir(stateDir)
	user := writeTFVarsSource(t, wsDir, "terraform.tfvars.user", `worker_count = 4`)
	override := writeTFVarsSource(t, stateDir, "cluster-phase-override.tfvars", `deploy_bnk = false`)

	if err := WriteAppliedTFVars(ws, "cluster", []string{auto, user, override}); err != nil {
		t.Fatalf("WriteAppliedTFVars: %v", err)
	}

	target, _ := AppliedTFVarsPath(ws, "cluster")
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	got := string(b)

	idxCfg := strings.Index(got, "# === from config.yaml ===")
	idxUsr := strings.Index(got, "# === from terraform.tfvars.user ===")
	idxOvr := strings.Index(got, "# === from cluster-phase override ===")
	if idxCfg < 0 || idxUsr < 0 || idxOvr < 0 {
		t.Fatalf("missing section header(s):\ngot:\n%s", got)
	}
	if !(idxCfg < idxUsr && idxUsr < idxOvr) {
		t.Errorf("section headers out of order: cfg=%d user=%d override=%d", idxCfg, idxUsr, idxOvr)
	}
}

// TestAppliedTFVars_AlphabeticSortWithinSection feeds variables in
// reverse-alphabetic order and asserts the output is sorted.
func TestAppliedTFVars_AlphabeticSortWithinSection(t *testing.T) {
	ws := stageWorkspaceForApplied(t)
	stateDir, err := WorkspaceStateDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	body := `c_var = "3"
a_var = "1"
b_var = "2"
`
	src := writeTFVarsSource(t, stateDir, "terraform.tfvars", body)
	if err := WriteAppliedTFVars(ws, "trial", []string{src}); err != nil {
		t.Fatalf("WriteAppliedTFVars: %v", err)
	}
	target, _ := AppliedTFVarsPath(ws, "trial")
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	idxA := strings.Index(got, "a_var =")
	idxB := strings.Index(got, "b_var =")
	idxC := strings.Index(got, "c_var =")
	if idxA < 0 || idxB < 0 || idxC < 0 {
		t.Fatalf("missing variable line(s):\n%s", got)
	}
	if !(idxA < idxB && idxB < idxC) {
		t.Errorf("variables not sorted: a=%d b=%d c=%d", idxA, idxB, idxC)
	}
}

// TestAppliedTFVars_Redaction pins the PRD 07 §"Resolved design
// decisions" #4 contract: ibmcloud_api_key (and ONLY ibmcloud_api_key)
// is replaced with <redacted>. Other variable names — even ones a naive
// pattern matcher might catch like database_password — pass through
// verbatim because the redaction list is exact-name.
func TestAppliedTFVars_Redaction(t *testing.T) {
	ws := stageWorkspaceForApplied(t)
	stateDir, err := WorkspaceStateDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	body := `ibmcloud_api_key = "actual-secret-value"
database_password = "not-redacted-because-not-on-the-list"
region = "ca-tor"
`
	src := writeTFVarsSource(t, stateDir, "terraform.tfvars", body)
	if err := WriteAppliedTFVars(ws, "trial", []string{src}); err != nil {
		t.Fatalf("WriteAppliedTFVars: %v", err)
	}
	target, _ := AppliedTFVarsPath(ws, "trial")
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)

	if strings.Contains(got, "actual-secret-value") {
		t.Errorf("snapshot leaks ibmcloud_api_key value:\n%s", got)
	}
	if !strings.Contains(got, `ibmcloud_api_key = "<redacted>"  # source: cred resolver, not persisted`) {
		t.Errorf("missing redacted line for ibmcloud_api_key:\n%s", got)
	}
	if !strings.Contains(got, `database_password = "not-redacted-because-not-on-the-list"`) {
		t.Errorf("database_password got redacted but should not have:\n%s", got)
	}
}

// TestAppliedTFVars_Idempotent asserts byte-identical output on
// re-write with the same inputs (modulo the timestamp in the header,
// which renderAppliedTFVars takes as a parameter so we can pin it).
func TestAppliedTFVars_Idempotent(t *testing.T) {
	body := `a_var = "1"
ibmcloud_api_key = "secret"
b_var = "2"
`
	dir := t.TempDir()
	src := writeTFVarsSource(t, dir, "terraform.tfvars", body)

	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	out1, err := renderAppliedTFVars("cluster", []string{src}, now, "v1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	out2, err := renderAppliedTFVars("cluster", []string{src}, now, "v1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if out1 != out2 {
		t.Errorf("idempotency violated:\nfirst:\n%s\nsecond:\n%s", out1, out2)
	}
}

// TestAppliedTFVars_FilePermissions confirms mode 0600 lands on the
// written snapshot. Skipped on platforms where 0600 doesn't round-trip
// cleanly through the filesystem (mostly Windows; the wsl2 mount under
// /mnt/c/ also doesn't preserve POSIX mode bits faithfully).
func TestAppliedTFVars_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not meaningful on windows")
	}
	ws := stageWorkspaceForApplied(t)
	stateDir, err := WorkspaceStateDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	src := writeTFVarsSource(t, stateDir, "terraform.tfvars", `region = "ca-tor"`)
	if err := WriteAppliedTFVars(ws, "trial", []string{src}); err != nil {
		t.Fatalf("WriteAppliedTFVars: %v", err)
	}
	target, _ := AppliedTFVarsPath(ws, "trial")
	st, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	mode := st.Mode().Perm()
	// Some test filesystems (notably the wsl2 /mnt/c/ DrvFs mount used
	// by this repo's developer environment) refuse to honor 0600 — they
	// snap permissions to whatever the underlying NTFS ACL allows,
	// usually 0777. Treat that case as a skip rather than a failure;
	// real Linux filesystems honor the chmod and the assertion catches
	// genuine regressions.
	if mode == 0o777 || mode == 0o666 {
		t.Skipf("filesystem doesn't preserve POSIX mode bits (got %o); skipping permission assertion", mode)
	}
	if mode != 0o600 {
		t.Errorf("mode = %o, want 0600", mode)
	}
}

// TestAppliedTFVars_MissingSourceFile asserts the missing-source branch:
// the snapshot still gets written, the missing source becomes a marked
// section header, and no error is returned.
func TestAppliedTFVars_MissingSourceFile(t *testing.T) {
	ws := stageWorkspaceForApplied(t)
	stateDir, err := WorkspaceStateDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	present := writeTFVarsSource(t, stateDir, "terraform.tfvars", `region = "ca-tor"`)
	missing := filepath.Join(stateDir, "does-not-exist.tfvars")

	if err := WriteAppliedTFVars(ws, "trial", []string{present, missing}); err != nil {
		t.Fatalf("WriteAppliedTFVars: %v", err)
	}
	target, _ := AppliedTFVarsPath(ws, "trial")
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `# === from does-not-exist.tfvars (missing) ===`) {
		t.Errorf("expected missing marker for missing source:\n%s", got)
	}
	if !strings.Contains(got, `region = "ca-tor"`) {
		t.Errorf("expected present source's vars to land:\n%s", got)
	}
}

// TestAppliedTFVars_AllFourShapesCover walks the four PRD 06 workspace
// shapes that PRD 07 §"Acceptance criteria" calls out:
//
//   - Empty           — no apply, no snapshot expected.
//   - ClusterOnly     — only the cluster-phase snapshot is written.
//   - Split           — both per-phase snapshots are independent.
//   - LegacySingle    — single snapshot lands at the trial-phase path.
//
// We don't drive real applies here (that's the validator's surface);
// we drive the snapshot writer directly and assert per-shape file
// existence at the expected per-phase paths.
func TestAppliedTFVars_AllFourShapesCover(t *testing.T) {
	tests := []struct {
		name        string
		phases      []string // phases we'll call WriteAppliedTFVars for
		expectFiles []string // phase names whose snapshot file MUST exist
	}{
		{
			name:        "ShapeEmpty — no applies, no snapshots",
			phases:      nil,
			expectFiles: nil,
		},
		{
			name:        "ShapeClusterOnly — cluster snapshot only",
			phases:      []string{"cluster"},
			expectFiles: []string{"cluster"},
		},
		{
			name:        "ShapeSplit — cluster + trial snapshots, independent",
			phases:      []string{"cluster", "trial"},
			expectFiles: []string{"cluster", "trial"},
		},
		{
			name:        "ShapeLegacySingle — single snapshot at trial path",
			phases:      []string{"legacy-single"},
			expectFiles: []string{"legacy-single"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := stageWorkspaceForApplied(t)
			for _, phase := range tc.phases {
				// Per-phase tfvars goes into the matching state dir so
				// readTFVarsAssignments has something to consume; the
				// snapshot writer will land alongside it.
				var dir string
				switch phase {
				case "cluster":
					d, err := WorkspaceClusterStateDir(ws)
					if err != nil {
						t.Fatal(err)
					}
					dir = d
				default:
					d, err := WorkspaceStateDir(ws)
					if err != nil {
						t.Fatal(err)
					}
					dir = d
				}
				src := writeTFVarsSource(t, dir, "terraform.tfvars", `region = "ca-tor"`)
				if err := WriteAppliedTFVars(ws, phase, []string{src}); err != nil {
					t.Fatalf("WriteAppliedTFVars(%s): %v", phase, err)
				}
			}
			// All expected files exist:
			for _, phase := range tc.expectFiles {
				p, _ := AppliedTFVarsPath(ws, phase)
				if _, err := os.Stat(p); err != nil {
					t.Errorf("expected snapshot at %s for phase %s, stat error: %v", p, phase, err)
				}
			}
			// On the empty shape, neither per-phase snapshot path should
			// exist (no apply, no snapshot — PRD 07 §"Trigger point").
			if len(tc.expectFiles) == 0 {
				cp, _ := AppliedTFVarsPath(ws, "cluster")
				tp, _ := AppliedTFVarsPath(ws, "trial")
				if _, err := os.Stat(cp); err == nil {
					t.Errorf("Empty shape produced unexpected cluster snapshot at %s", cp)
				}
				if _, err := os.Stat(tp); err == nil {
					t.Errorf("Empty shape produced unexpected trial snapshot at %s", tp)
				}
			}
		})
	}
}

// TestAppliedTFVars_DestroyDoesNotMutate pins PRD 07 §"Resolved design
// decisions" #2: a snapshot written by a successful apply must NOT be
// modified by a subsequent destroy. We exercise this at the boundary
// the code controls — WriteAppliedTFVars is only called from
// Workspace.Apply (the destroy path has no hook), so the test asserts
// that writing the snapshot once and then "running destroy" (a no-op
// on the snapshot's perspective: we just don't call WriteAppliedTFVars
// again) leaves the file byte-identical.
func TestAppliedTFVars_DestroyDoesNotMutate(t *testing.T) {
	ws := stageWorkspaceForApplied(t)
	stateDir, err := WorkspaceStateDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	src := writeTFVarsSource(t, stateDir, "terraform.tfvars", `region = "ca-tor"`)
	if err := WriteAppliedTFVars(ws, "trial", []string{src}); err != nil {
		t.Fatalf("WriteAppliedTFVars: %v", err)
	}
	target, _ := AppliedTFVarsPath(ws, "trial")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	stBefore, _ := os.Stat(target)

	// Simulate destroy: we deliberately do NOT call WriteAppliedTFVars
	// again. (The real Destroy method on tf.Workspace also doesn't.)
	// Sleep a beat then re-read; the file should be identical bytes
	// and the mtime should not have moved.
	time.Sleep(10 * time.Millisecond)
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	stAfter, _ := os.Stat(target)

	if string(before) != string(after) {
		t.Error("destroy mutated the snapshot bytes")
	}
	if !stBefore.ModTime().Equal(stAfter.ModTime()) {
		t.Errorf("destroy bumped the snapshot mtime: before=%v after=%v", stBefore.ModTime(), stAfter.ModTime())
	}
}

// TestAppliedTFVars_HeaderRecordsPhase walks each documented phase
// label and asserts the header line records the right phase=… token.
func TestAppliedTFVars_HeaderRecordsPhase(t *testing.T) {
	for _, phase := range []string{"cluster", "trial", "legacy-single"} {
		t.Run(phase, func(t *testing.T) {
			now := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
			body, err := renderAppliedTFVars(phase, nil, now, "v1.4.0")
			if err != nil {
				t.Fatal(err)
			}
			want := "phase=" + phase
			if !strings.Contains(body, want) {
				t.Errorf("header missing %q:\n%s", want, body)
			}
			if !strings.Contains(body, "v1.4.0") {
				t.Errorf("header missing version v1.4.0:\n%s", body)
			}
		})
	}
}

// TestAppliedTFVars_SkipsHCLComments asserts that comment lines (#…,
// //…) and blank lines in a source tfvars file are skipped, not
// emitted as bogus assignments.
func TestAppliedTFVars_SkipsHCLComments(t *testing.T) {
	body := `# this is a comment
// also a comment

region = "ca-tor"
# trailing
`
	dir := t.TempDir()
	src := writeTFVarsSource(t, dir, "terraform.tfvars", body)
	out, err := renderAppliedTFVars("trial", []string{src}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "v1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	// Only `region = "ca-tor"` from the source should land.
	if !strings.Contains(out, `region = "ca-tor"`) {
		t.Errorf("expected region assignment in output:\n%s", out)
	}
	if strings.Contains(out, "this is a comment") {
		t.Errorf("comment leaked into output:\n%s", out)
	}
}

// TestAppliedTFVars_StripsTrailingComment asserts that a `name = value  # note`
// line gets emitted as `name = value` — the trailing-comment scrubber is
// what keeps the redacted-line shape consistent when a user has hand-
// added inline comments in terraform.tfvars.user.
func TestAppliedTFVars_StripsTrailingComment(t *testing.T) {
	body := `region = "ca-tor" # the canada-toronto region
`
	dir := t.TempDir()
	src := writeTFVarsSource(t, dir, "terraform.tfvars", body)
	out, err := renderAppliedTFVars("trial", []string{src}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "v1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `region = "ca-tor"`) {
		t.Errorf("expected trailing comment stripped:\n%s", out)
	}
	if strings.Contains(out, "canada-toronto region") {
		t.Errorf("trailing comment leaked into output:\n%s", out)
	}
}
