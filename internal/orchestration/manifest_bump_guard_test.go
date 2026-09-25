package orchestration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #309 layer 2. CheckManifestVersionChange being correct is not enough — it has
// to be REACHED on the path `bnk up` takes. A guard that is never called is the
// failure mode #302's mutation run kept finding, so this drives
// guardCreateTimeSettings, the function prepareBNKUp actually invokes.

// stageBumpWS stages a workspace that HAS a BNK install (so the guard's
// bnkPresent branch is taken) and an applied-tfvars snapshot recording `prior`.
func stageBumpWS(t *testing.T, configured, prior string) *config.Context {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "bump-test-ws"

	// DetectPresence reads managed resources out of state/terraform.tfstate.
	dir, err := config.WorkspaceStateDir(ws)
	if err != nil {
		t.Fatalf("state dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const tfstate = `{"version":4,"resources":[{"mode":"managed","type":"kubectl_manifest","name":"cnemanifest","instances":[{"attributes":{"name":"bnk-2.4.0-ea"}}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(tfstate), 0o644); err != nil {
		t.Fatalf("write tfstate: %v", err)
	}

	snap, err := config.AppliedTFVarsPath(ws, "trial")
	if err != nil {
		t.Fatalf("AppliedTFVarsPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(snap), 0o755); err != nil {
		t.Fatalf("mkdir snap: %v", err)
	}
	body := "f5_bigip_k8s_manifest_version = \"" + prior + "\"\n"
	if err := os.WriteFile(snap, []byte(body), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	return &config.Context{
		WorkspaceName: ws,
		Workspace: &config.Workspace{
			BNK: config.BNKCfg{ManifestVersion: configured},
		},
	}
}

// The guard chain must refuse the bump, not just the helper in isolation.
func TestManifestBumpIsRefusedOnTheBNKUpPath(t *testing.T) {
	cctx := stageBumpWS(t, "2.4.0", "2.4.0-EA")
	var buf bytes.Buffer

	err := guardCreateTimeSettings(cctx, &buf)
	if err == nil {
		t.Fatal("guardCreateTimeSettings allowed a manifest bump; `bnk up` would start an apply that " +
			"cannot finish and can leave BNK down with no CNEManifest (#309)")
	}
	for _, want := range []string{"2.4.0-EA", "bnk down", "bnk up"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal reached the operator without %q:\n%v", want, err)
		}
	}
}

// And the same path must stay silent when nothing changed, or every re-apply
// on an existing workspace would refuse.
func TestUnchangedManifestPassesTheBNKUpPath(t *testing.T) {
	cctx := stageBumpWS(t, "2.4.0", "2.4.0")
	var buf bytes.Buffer

	if err := guardCreateTimeSettings(cctx, &buf); err != nil {
		t.Fatalf("an unchanged manifest was refused on the bnk up path, which would block every re-apply: %v", err)
	}
}
