package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// All tests redirect $ROKSBNKCTL_HOME via t.Setenv so they never touch the
// real ~/.awsbnkctl. t.TempDir auto-cleans on failure.

func TestNew_DefaultWorkspace_NoState(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	ctx, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ctx.WorkspaceName != DefaultWorkspace {
		t.Errorf("WorkspaceName = %q, want %q", ctx.WorkspaceName, DefaultWorkspace)
	}
	if ctx.Workspace != nil {
		t.Errorf("Workspace = %+v, want nil for fresh state", ctx.Workspace)
	}
}

func TestNew_FlagOverridesGlobalCurrent(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	if err := SaveGlobal(&Global{CurrentWorkspace: "prod"}); err != nil {
		t.Fatal(err)
	}
	ctx, err := New("demo")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ctx.WorkspaceName != "demo" {
		t.Errorf("flag did not override global; got %q", ctx.WorkspaceName)
	}
}

func TestNew_GlobalCurrentUsedWhenNoFlag(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	if err := SaveGlobal(&Global{CurrentWorkspace: "prod"}); err != nil {
		t.Fatal(err)
	}
	ctx, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ctx.WorkspaceName != "prod" {
		t.Errorf("global current ignored; got %q want prod", ctx.WorkspaceName)
	}
}

func TestSaveAndLoadWorkspace_Roundtrip(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	in := &Workspace{
		AWS:     AWSCfg{Region: "us-east-1", Profile: "default"},
		Cluster: ClusterCfg{Create: true, Name: "bnk-demo", WorkersPerZone: 1},
	}
	if err := SaveWorkspace("demo", in); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}

	out, err := LoadWorkspace("demo")
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	if out.AWS.Region != "us-east-1" || out.Cluster.Name != "bnk-demo" || out.AWS.Profile != "default" {
		t.Errorf("roundtrip mismatch: %+v", out)
	}
}

func TestLoadWorkspace_NotFound(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	_, err := LoadWorkspace("nope")
	if err == nil {
		t.Fatal("expected ErrWorkspaceNotFound, got nil")
	}
	if !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("error = %v, want it to wrap ErrWorkspaceNotFound", err)
	}
}

// writeWorkspaceStateEnv stages a state.env with the given body under
// <workspace>/state/ for the given workspace name. Used by the delete-guard
// tests.
func writeWorkspaceStateEnv(t *testing.T, ws, body string) {
	t.Helper()
	dir, err := WorkspaceStateDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDeleteWorkspace_GuardOnPopulatedStateEnv covers the state.env-based
// delete guard: a state.env with a populated resource ID blocks deletion
// unless --force; an empty state.env (or none) deletes cleanly.
func TestDeleteWorkspace_GuardOnPopulatedStateEnv(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	// Workspace with a live VPC_ID — guard must block delete.
	if err := SaveWorkspace("live", &Workspace{AWS: AWSCfg{Region: "us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceStateEnv(t, "live", "VPC_ID=vpc-123\nEKS_CLUSTER_NAME=demo\n")
	if err := DeleteWorkspace("live", false); err == nil {
		t.Error("expected DeleteWorkspace to refuse a workspace with provisioned resources")
	}
	// --force overrides.
	if err := DeleteWorkspace("live", true); err != nil {
		t.Errorf("force delete should succeed: %v", err)
	}
	if WorkspaceExists("live") {
		t.Error("workspace should be gone after force delete")
	}

	// Workspace whose state.env has only empty values (post-down) — deletes.
	if err := SaveWorkspace("torndown", &Workspace{AWS: AWSCfg{Region: "us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceStateEnv(t, "torndown", "VPC_ID=\nEKS_CLUSTER_NAME=\n# comment\n")
	if err := DeleteWorkspace("torndown", false); err != nil {
		t.Errorf("torn-down workspace (empty IDs) should delete without --force: %v", err)
	}

	// Workspace with no state.env at all — deletes.
	if err := SaveWorkspace("fresh", &Workspace{AWS: AWSCfg{Region: "us-east-1"}}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteWorkspace("fresh", false); err != nil {
		t.Errorf("fresh workspace (no state.env) should delete without --force: %v", err)
	}
}

func TestStateEnvHasResources(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Missing file → false.
	if stateEnvHasResources(filepath.Join(dir, "absent.env")) {
		t.Error("missing file should report no resources")
	}

	cases := []struct {
		name string
		body string
		want bool
	}{
		{"empty values", "VPC_ID=\nTMM_INSTANCE_ID=\n", false},
		{"only non-resource keys", "AWS_REGION=ap-southeast-2\nINTERNAL_PCI=0000:00:07.0\n", false},
		{"populated VPC", "AWS_REGION=ap-southeast-2\nVPC_ID=vpc-abc\n", true},
		{"populated jumphost", "JUMPHOST_INSTANCE_ID=i-0123\n", true},
		{"comments + blanks only", "# header\n\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "state.env")
			if err := os.WriteFile(p, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := stateEnvHasResources(p); got != tc.want {
				t.Errorf("stateEnvHasResources(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestValidateName(t *testing.T) {
	good := []string{"default", "prod", "demo-1", "team_a", "ABC.123", "a"}
	bad := []string{
		"",
		"../escape",
		"foo/bar",
		strings.Repeat("a", 65),
		"-leading",
		".dot",
		"_underscore",
	}
	for _, n := range good {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q): unexpected error %v", n, err)
		}
	}
	for _, n := range bad {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q): expected error, got nil", n)
		}
	}
}

func TestRejectPlaintextSecrets(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	tmpHome, _ := BaseDir()
	cfg := filepath.Join(tmpHome, "tainted", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "aws:\n  region: us-east-1\n  api_key: hunter2\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadWorkspace("tainted")
	if err == nil {
		t.Fatal("expected plaintext-secret rejection, got nil")
	}
	if !strings.Contains(err.Error(), "plaintext secret") {
		t.Errorf("error = %v, want plaintext-secret rejection", err)
	}
}

func TestRejectPlaintextSecrets_AllowsCommentedExamples(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	tmpHome, _ := BaseDir()
	cfg := filepath.Join(tmpHome, "ok", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	// Commented + empty-value forms must not trip the rejection.
	body := `aws:
  region: us-east-1
  profile: default
  # api_key: this-would-be-bad-but-it-is-commented
cluster:
  create: true
  name: bnk-demo
tf_source:
  type: github
  repo: JLCode-tech/awsbnkctl-tf
  ref: v0.6.7
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkspace("ok"); err != nil {
		t.Errorf("expected commented api_key to be allowed; got %v", err)
	}
}

func TestListWorkspaces(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	for _, n := range []string{"alpha", "beta", "gamma"} {
		if err := SaveWorkspace(n, &Workspace{}); err != nil {
			t.Fatal(err)
		}
	}
	// A non-workspace dir (no config.yaml) must be skipped.
	tmpHome, _ := BaseDir()
	if err := os.MkdirAll(filepath.Join(tmpHome, "not-a-workspace"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSetCurrent_RejectsMissingWorkspace(t *testing.T) {
	t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())

	if err := SetCurrent("phantom"); err == nil {
		t.Fatal("expected SetCurrent to reject missing workspace")
	}
}
