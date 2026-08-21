package tf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Review of #150 (D1). The completeness check was first put in
// WriteTFVarsForWorkspace, which is the SHARED tfvars preamble for up AND down
// (internal/orchestration/lifecycle.go writeAndInit — "Common preamble for
// plan/apply/up/down"). Refusing there blocked every teardown path: `bnk down`,
// `cluster down`, `flp down`, `gateway down`, `testing down` and
// `tgw disconnect`.
//
// The consequence is far worse than the bug it was fixing: a ROKS cluster, VPC,
// TGW and jumphosts left running because a registry mirror was missing three
// images. Teardown does not read from the mirror and must never be gated on it.
//
// The check now lives in guardRegistryMirror, which is up-path-only. This test
// pins the render staying open, from the layer that was broken.
func TestRenderingIsNotGatedOnMirrorCompleteness(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRegistryMirror("ws", &config.RegistryMirror{
		Target:       "generic",
		Namespace:    "docker-local",
		ChartHost:    "artifactory.example.com/docker-local",
		ImageHost:    "artifactory.example.com/docker-local",
		MissingCount: 3,
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
		t.Fatalf("an incomplete mirror must NOT block the tfvars render — this is the "+
			"preamble for teardown as well as install, and refusing here strands cloud "+
			"resources:\n%v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "artifactory.example.com/docker-local") {
		t.Error("the render should still emit the mirror redirect")
	}
}

// The identity check (#112) stays here, because a record for the WRONG mirror
// is wrong on every path — a teardown pointed at someone else's registry is not
// safer than an install pointed at it.
func TestTheIdentityCheckStillGuardsTheRender(t *testing.T) {
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
	if err := WriteTFVarsForWorkspace(path, "ws", ws, "", ""); err == nil {
		t.Fatal("a record describing a different mirror must still be refused here")
	}
}
