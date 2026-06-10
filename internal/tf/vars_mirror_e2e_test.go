package tf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// These tests exercise the PUBLIC WriteTFVarsForWorkspace entry point, which is
// the only path that resolves the registry-mirror.json record off disk (via
// workspaceName + ws.Registry). The sibling vars_mirror_test.go drives the
// unexported renderTFVars with a hand-built record and so never covers the
// on-disk resolution branch (vars.go lines ~40-49). These close that gap.

func mirrorE2EWorkspace() *config.Workspace {
	return &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
		Cluster:  config.ClusterCfg{Name: "c"},
		BNK:      config.BNKCfg{FARRepoURL: "repo.f5.com"},
	}
}

// TestWriteTFVarsForWorkspace_MirrorRecordOnDisk: a workspace that opts into a
// mirror (Registry block set) AND has a populated registry-mirror.json renders
// the Sprint-29 redirect, resolving the hosts from the on-disk record.
func TestWriteTFVarsForWorkspace_MirrorRecordOnDisk(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	ws := mirrorE2EWorkspace()
	ws.Registry = &config.RegistryCfg{}
	if err := config.SaveWorkspace("ws", ws); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		ChartHost: "route.apps.x/bnk-mirror",
		ImageHost: "image-registry.svc:5000/bnk-mirror",
	}); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "terraform.tfvars")
	if err := WriteTFVarsForWorkspace(out, "ws", ws, "", ""); err != nil {
		t.Fatalf("WriteTFVarsForWorkspace: %v", err)
	}
	body := readFile(t, out)
	for _, w := range []string{
		`far_repo_url = "repo.f5.com"`,
		`far_chart_repo_url = "route.apps.x/bnk-mirror"`,
		`far_image_repo_url = "image-registry.svc:5000/bnk-mirror"`,
		"use_registry_mirror = true",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q in:\n%s", w, body)
		}
	}
}

// TestWriteTFVarsForWorkspace_RecordWithoutRegistryBlock_Redirects pins the
// Sprint-29 fix: `registry replicate` writes the record flag-driven (no registry:
// block in config.yaml required), so the record's PRESENCE alone triggers the
// install redirect — otherwise the mirror is built but the install still pulls
// from far_repo_url (the live failure: 76 pods from repo.f5.com).
func TestWriteTFVarsForWorkspace_RecordWithoutRegistryBlock_Redirects(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	ws := mirrorE2EWorkspace() // NO Registry block — replicate ran via --target
	if err := config.SaveWorkspace("ws", ws); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		ChartHost: "route.apps.x",
		ImageHost: "image-registry.svc:5000",
	}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "terraform.tfvars")
	if err := WriteTFVarsForWorkspace(out, "ws", ws, "", ""); err != nil {
		t.Fatalf("WriteTFVarsForWorkspace: %v", err)
	}
	body := readFile(t, out)
	for _, w := range []string{
		`far_chart_repo_url = "route.apps.x"`,
		`far_image_repo_url = "image-registry.svc:5000"`,
		"use_registry_mirror = true",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("record present but redirect var %q missing:\n%s", w, body)
		}
	}
}

// TestWriteTFVarsForWorkspace_RegistryBlockButNoRecord: opting into a mirror
// but not yet replicating (no registry-mirror.json) must NOT half-redirect —
// the render falls back to far_repo_url and emits no Sprint-29 vars. (The up
// guard, tested separately in orchestration, is what hard-errors this state.)
func TestWriteTFVarsForWorkspace_RegistryBlockButNoRecord(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	ws := mirrorE2EWorkspace()
	ws.Registry = &config.RegistryCfg{}
	if err := config.SaveWorkspace("ws", ws); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "terraform.tfvars")
	if err := WriteTFVarsForWorkspace(out, "ws", ws, "", ""); err != nil {
		t.Fatalf("WriteTFVarsForWorkspace: %v", err)
	}
	body := readFile(t, out)
	if !strings.Contains(body, `far_repo_url = "repo.f5.com"`) {
		t.Errorf("far_repo_url fallback missing:\n%s", body)
	}
	for _, forbidden := range []string{"far_chart_repo_url", "far_image_repo_url", "use_registry_mirror"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("unreplicated mirror must not emit %q:\n%s", forbidden, body)
		}
	}
}

// TestWriteTFVarsForWorkspace_NoRegistry_ByteIdenticalToWriteTFVars pins the
// off-path invariant at the public boundary: a workspace with no mirror record
// renders byte-for-byte the same through WriteTFVarsForWorkspace (even with a
// non-empty workspaceName) as through the legacy WriteTFVars. This guarantees
// Sprint 29 is inert for every workspace that has not replicated a mirror.
func TestWriteTFVarsForWorkspace_NoRegistry_ByteIdenticalToWriteTFVars(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	ws := mirrorE2EWorkspace() // no Registry block
	if err := config.SaveWorkspace("ws", ws); err != nil {
		t.Fatal(err)
	}

	legacy := filepath.Join(t.TempDir(), "legacy.tfvars")
	if err := WriteTFVars(legacy, ws, "", ""); err != nil {
		t.Fatalf("WriteTFVars: %v", err)
	}
	s29 := filepath.Join(t.TempDir(), "s29.tfvars")
	if err := WriteTFVarsForWorkspace(s29, "ws", ws, "", ""); err != nil {
		t.Fatalf("WriteTFVarsForWorkspace: %v", err)
	}
	if a, b := readFile(t, legacy), readFile(t, s29); a != b {
		t.Errorf("off-path render diverged from WriteTFVars:\nlegacy:\n%s\n--- s29:\n%s", a, b)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
