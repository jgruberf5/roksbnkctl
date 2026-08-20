package tf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #112. The tfvars render is where a mirror record becomes the install: its
// presence rewrites every image and chart reference onto the recorded host.
// Believing a record for a mirror the workspace is no longer configured for
// points the entire install somewhere nobody asked for, and terraform applies
// it without complaint.
func TestWriteTFVarsRefusesAMirrorRecordForAnotherMirror(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		Target:    "generic",
		Namespace: "bnk-mirror",
		ChartHost: "artifactory.example.com/bnk-mirror",
		ImageHost: "artifactory.example.com/bnk-mirror",
	}); err != nil {
		t.Fatal(err)
	}
	ws := &config.Workspace{Registry: &config.RegistryCfg{
		Target:            "generic",
		GenericHost:       "artifactory.example.com",
		GenericRepoPrefix: "docker-local",
	}}

	path := filepath.Join(t.TempDir(), "terraform.tfvars")
	// A previous render the operator is entitled to keep if this one is refused.
	const existing = "# rendered earlier\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	err := WriteTFVarsForWorkspace(path, "ws", ws, "", "")
	if err == nil {
		t.Fatal("a record describing a different mirror must not be rendered into tfvars")
	}
	if !strings.Contains(err.Error(), "bnk-mirror") || !strings.Contains(err.Error(), "docker-local") {
		t.Errorf("the refusal should name both repositories, got:\n%s", err)
	}

	// The check runs before os.Create, so a refusal leaves the previous render
	// intact instead of truncating it to nothing.
	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatalf("the existing tfvars was removed: %v", rerr)
	}
	if string(got) != existing {
		t.Errorf("a refused render must not touch the existing tfvars; it now holds %q", got)
	}
}

// The matching case must still render the redirect — the whole air-gap path
// depends on it.
func TestWriteTFVarsRendersTheConfiguredMirror(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		Target:    "generic",
		Namespace: "docker-local",
		ChartHost: "artifactory.example.com/docker-local",
		ImageHost: "artifactory.example.com/docker-local",
	}); err != nil {
		t.Fatal(err)
	}
	ws := &config.Workspace{Registry: &config.RegistryCfg{
		Target:            "generic",
		GenericHost:       "artifactory.example.com",
		GenericRepoPrefix: "docker-local",
	}}

	path := filepath.Join(t.TempDir(), "terraform.tfvars")
	if err := WriteTFVarsForWorkspace(path, "ws", ws, "", ""); err != nil {
		t.Fatalf("the configured mirror must render: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "artifactory.example.com/docker-local") {
		t.Error("the redirect onto the configured mirror is missing from the render")
	}
}
