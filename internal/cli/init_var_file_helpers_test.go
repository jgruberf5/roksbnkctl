package cli

// Sprint 19 Issue 1 — additive hermetic tests for the seam-level
// helpers behind `roksbnkctl init --var-file <path>`:
// `loadInitVarFile`, `writeUserTFVarsCopies`, `absVarFilePath`.
//
// Sibling file to init_var_file_test.go (the validator-shipped cobra-
// driven file): that file drives `init` through its public flag
// surface, so its positive cases (a/b — happy-path copy + config
// seeding) skip without live IBMCLOUD_API_KEY because runInit calls
// ibm.Verify() before reaching the copy step. THIS file targets the
// seam helpers directly so the positive path is hermetic — the
// helpers are the var-file-specific surface, the rest of runInit is
// byte-unchanged from pre-Sprint-19 by construction (the seeds.Has*
// branches short-circuit the existing prompts).
//
// Additive only — no edits to any pre-existing _test.go. Sprint 18
// parity discipline carries forward.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// TestLoadInitVarFile_FullMapping — every interview-targeted field
// carried by the var-file lands in seeds with Has*=true and the right
// coerced value. Same field set as runInit consumes; if a future
// change drops a field, this test catches it before the operator does.
func TestLoadInitVarFile_FullMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "terraform.tfvars")
	body := `# Hermetic fixture mirroring the canonical terraform.tfvars.example shape.
ibmcloud_api_key            = "TEST_KEY_NOT_A_REAL_CREDENTIAL"
ibmcloud_cluster_region     = "us-south"
ibmcloud_resource_group     = "my-rg"
openshift_cluster_name      = "my-cluster"
openshift_cluster_version   = "4.18"
roks_workers_per_zone       = 3
create_roks_cluster         = false
# Unsupported shape — parser skips silently (matches
# config.ReadTFVarsAssignments's tolerance for non-line-oriented HCL):
cneinstance_network_attachments = ["ens3", "macvlan"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	seeds, err := loadInitVarFile(path)
	if err != nil {
		t.Fatalf("loadInitVarFile: %v", err)
	}
	checks := []struct {
		has  bool
		want any
		got  any
		name string
	}{
		{seeds.HasRegion, "us-south", seeds.Region, "Region"},
		{seeds.HasResourceGroup, "my-rg", seeds.ResourceGroup, "ResourceGroup"},
		{seeds.HasClusterName, "my-cluster", seeds.ClusterName, "ClusterName"},
		{seeds.HasOCPVersion, "4.18", seeds.OCPVersion, "OCPVersion"},
		{seeds.HasWorkersPerZone, 3, seeds.WorkersPerZone, "WorkersPerZone"},
		{seeds.HasCreateCluster, false, seeds.CreateCluster, "CreateCluster"},
	}
	for _, c := range checks {
		if !c.has {
			t.Errorf("%s: Has* should be true; the fixture carries it", c.name)
			continue
		}
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestLoadInitVarFile_PartialFile — fields the file does not carry
// leave Has* = false so runInit falls back to the interactive prompt
// (or default) for those fields. Pins the per-field independence that
// matters when an operator's `./terraform.tfvars` is incomplete.
func TestLoadInitVarFile_PartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.tfvars")
	body := `ibmcloud_cluster_region = "eu-de"
openshift_cluster_name  = "only-name"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	seeds, err := loadInitVarFile(path)
	if err != nil {
		t.Fatalf("loadInitVarFile: %v", err)
	}
	if !seeds.HasRegion || seeds.Region != "eu-de" {
		t.Errorf("Region: got (%q, has=%v); want (eu-de, true)", seeds.Region, seeds.HasRegion)
	}
	if !seeds.HasClusterName || seeds.ClusterName != "only-name" {
		t.Errorf("ClusterName: got (%q, has=%v); want (only-name, true)", seeds.ClusterName, seeds.HasClusterName)
	}
	for _, c := range []struct {
		name string
		has  bool
	}{
		{"HasResourceGroup", seeds.HasResourceGroup},
		{"HasOCPVersion", seeds.HasOCPVersion},
		{"HasWorkersPerZone", seeds.HasWorkersPerZone},
		{"HasCreateCluster", seeds.HasCreateCluster},
	} {
		if c.has {
			t.Errorf("%s must be false when the file does not carry the key", c.name)
		}
	}
}

// TestLoadInitVarFile_QuotedStringsUnquoted — config.ReadTFVarsAssignments
// keeps quoted string values verbatim (with surrounding quotes); the
// seed-extraction seam unquotes so SaveWorkspace lands a clean YAML
// value (otherwise the workspace config would carry escaped quotes
// the YAML reader has to undo on every load).
func TestLoadInitVarFile_QuotedStringsUnquoted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quoted.tfvars")
	body := `ibmcloud_cluster_region = "ca-tor"
openshift_cluster_name  = "no-extra-quotes"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	seeds, err := loadInitVarFile(path)
	if err != nil {
		t.Fatalf("loadInitVarFile: %v", err)
	}
	if strings.Contains(seeds.Region, `"`) {
		t.Errorf("Region must not retain surrounding quotes; got %q", seeds.Region)
	}
	if strings.Contains(seeds.ClusterName, `"`) {
		t.Errorf("ClusterName must not retain surrounding quotes; got %q", seeds.ClusterName)
	}
}

// TestLoadInitVarFile_CanonicalExample — the shipped
// terraform/terraform.tfvars.example IS the canonical shape operators
// are told to copy. loadInitVarFile must parse it cleanly and extract
// every interview-targeted field the example carries. Catches drift
// between the on-disk example and the field-mapping seam.
func TestLoadInitVarFile_CanonicalExample(t *testing.T) {
	example := filepath.Join("..", "..", "terraform", "terraform.tfvars.example")
	if _, err := os.Stat(example); err != nil {
		t.Skipf("canonical example not at %s: %v", example, err)
	}
	seeds, err := loadInitVarFile(example)
	if err != nil {
		t.Fatalf("loadInitVarFile(canonical example): %v", err)
	}
	if !seeds.HasRegion || seeds.Region != "ca-tor" {
		t.Errorf("Region: got (%q, has=%v); want (ca-tor, true)", seeds.Region, seeds.HasRegion)
	}
	if !seeds.HasClusterName {
		t.Error("HasClusterName must be true; the canonical example carries openshift_cluster_name")
	}
	if !seeds.HasOCPVersion {
		t.Error("HasOCPVersion must be true; the canonical example carries openshift_cluster_version")
	}
	if !seeds.HasWorkersPerZone {
		t.Error("HasWorkersPerZone must be true; the canonical example carries roks_workers_per_zone")
	}
	if !seeds.HasCreateCluster {
		t.Error("HasCreateCluster must be true; the canonical example carries create_roks_cluster")
	}
}

// TestWriteUserTFVarsCopies_BothPhaseDirs — AC #1 hermetic positive
// path: byte-identical copies land at both phase state dirs at mode
// 0600. Drives the helper directly so the test is hermetic (no IBM
// network call gating the assertion surface, unlike the cobra-driven
// happy-path case in init_var_file_test.go).
//
// Mode 0600 is asserted on platforms where the filesystem honours unix
// bits (Linux, WSL2 backed by the linux FS); Windows skips the mode
// assertion because NTFS doesn't carry it.
func TestWriteUserTFVarsCopies_BothPhaseDirs(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "vf-helper-ws"

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "input.tfvars")
	body := []byte("ibmcloud_cluster_region = \"us-south\"\n")
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	trialDest, clusterDest, err := writeUserTFVarsCopies(ws, src)
	if err != nil {
		t.Fatalf("writeUserTFVarsCopies: %v", err)
	}

	for _, dest := range []string{trialDest, clusterDest} {
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read %s: %v", dest, err)
		}
		if string(got) != string(body) {
			t.Errorf("%s: body mismatch — copy must be byte-identical to the source", dest)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(dest)
			if err != nil {
				t.Fatalf("stat %s: %v", dest, err)
			}
			if mode := info.Mode().Perm(); mode != 0o600 {
				t.Errorf("%s: mode %o; want 0600", dest, mode)
			}
		}
	}

	// Destinations must be the canonical paths the existing
	// tfws.HasUserTFVars() codepath looks for.
	trialDir, _ := config.WorkspaceStateDir(ws)
	clusterDir, _ := config.WorkspaceClusterStateDir(ws)
	wantTrial := filepath.Join(trialDir, "terraform.tfvars.user")
	wantCluster := filepath.Join(clusterDir, "terraform.tfvars.user")
	if trialDest != wantTrial {
		t.Errorf("trial dest: got %q; want %q", trialDest, wantTrial)
	}
	if clusterDest != wantCluster {
		t.Errorf("cluster dest: got %q; want %q", clusterDest, wantCluster)
	}
}

// TestWriteUserTFVarsCopies_OverwriteExisting — AC #7: a second
// invocation with a different source overwrites the prior copy at both
// destinations. Pins the atomic-rename pattern's replace-not-append
// semantics.
func TestWriteUserTFVarsCopies_OverwriteExisting(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "vf-overwrite-ws"

	srcDir := t.TempDir()
	src1 := filepath.Join(srcDir, "first.tfvars")
	src2 := filepath.Join(srcDir, "second.tfvars")
	body1 := []byte("ibmcloud_cluster_region = \"first\"\n")
	body2 := []byte("ibmcloud_cluster_region = \"second\"\n")
	if err := os.WriteFile(src1, body1, 0o600); err != nil {
		t.Fatalf("write src1: %v", err)
	}
	if err := os.WriteFile(src2, body2, 0o600); err != nil {
		t.Fatalf("write src2: %v", err)
	}

	if _, _, err := writeUserTFVarsCopies(ws, src1); err != nil {
		t.Fatalf("first writeUserTFVarsCopies: %v", err)
	}
	trialDest, clusterDest, err := writeUserTFVarsCopies(ws, src2)
	if err != nil {
		t.Fatalf("second writeUserTFVarsCopies: %v", err)
	}
	for _, dest := range []string{trialDest, clusterDest} {
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read %s: %v", dest, err)
		}
		if string(got) != string(body2) {
			t.Errorf("%s: got %q; want %q (the second source must overwrite the first)", dest, got, body2)
		}
	}
}

// TestAbsVarFilePath_RelativeResolved — relative inputs resolve
// against the invocation CWD so the `✓ Wrote <abs-path>` confirmation
// lines print absolute paths the operator can grep on disk. Absolute
// inputs round-trip unchanged.
func TestAbsVarFilePath_RelativeResolved(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "abs.tfvars")
	got, err := absVarFilePath(abs)
	if err != nil {
		t.Fatalf("absVarFilePath(absolute): %v", err)
	}
	if got != abs {
		t.Errorf("absolute input must round-trip; got %q want %q", got, abs)
	}

	tmp := t.TempDir()
	origCWD, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCWD) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	got, err = absVarFilePath("./rel.tfvars")
	if err != nil {
		t.Fatalf("absVarFilePath(relative): %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("relative input must become absolute; got %q", got)
	}
}
