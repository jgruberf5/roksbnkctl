package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The applied-tfvars snapshot is not a log. It is prepended to the -var-file
// list on plan, apply AND down, so whatever it contains has to be a var-file
// terraform will parse. It is also re-rendered from the parsed assignment map
// rather than copied through, which means a value the parser truncates is a
// value the snapshot writes back truncated.
//
// That was harmless while every value roksbnkctl emitted fit on one line. It
// stopped being harmless when cneinstance_advanced_env started rendering a
// multi-line block: the parser recorded `cneinstance_advanced_env = {` and the
// snapshot wrote exactly that back, which terraform refuses to parse — leaving
// a workspace that had installed successfully unable to run `down`.
//
// So this asserts on terraform's own verdict rather than on the text.
func TestTheAppliedSnapshotRoundTripsMultiLineValues(t *testing.T) {
	src := filepath.Join(t.TempDir(), "terraform.tfvars")
	const body = `flo_namespace = "f5-bnk"
cneinstance_advanced_env = {
  "cneController" = {
    "USE_GATEWAY_SETTINGS" = "false"
  }
  "tmm" = {
    "TMM_DEFAULT_MTU" = "9000"
  }
}
cneinstance_network_zones = [
  {
    ext_vlan_cidr = "10.1.0.0/24"
  },
]
cneinstance_deployment_size = "Tiny"
`
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	rendered, err := renderAppliedTFVars("trial", []string{src}, time.Unix(0, 0).UTC(), "test")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// The value after the last key must not have been swallowed by the block.
	if !strings.Contains(rendered, `cneinstance_deployment_size = "Tiny"`) {
		t.Errorf("a scalar following the multi-line block was lost:\n%s", rendered)
	}
	for _, want := range []string{"USE_GATEWAY_SETTINGS", "TMM_DEFAULT_MTU", "ext_vlan_cidr"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("multi-line body lost %q:\n%s", want, rendered)
		}
	}

	// terraform's verdict. A var-file it cannot parse is the actual failure.
	tf, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform not on PATH; the assertions above still ran")
	}
	dir := t.TempDir()
	mod := `variable "flo_namespace" { type = string }
variable "cneinstance_advanced_env" { type = map(map(string)) }
variable "cneinstance_network_zones" { type = list(object({ ext_vlan_cidr = string })) }
variable "cneinstance_deployment_size" { type = string }
output "seen" { value = var.cneinstance_advanced_env }
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(mod), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	vf := filepath.Join(dir, "snapshot.tfvars")
	if err := os.WriteFile(vf, []byte(rendered), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	init := exec.Command(tf, "init", "-backend=false", "-input=false")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Skipf("terraform init unavailable: %v\n%s", err, out)
	}
	cmd := exec.Command(tf, "console", "-var-file", vf)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("var.cneinstance_advanced_env[\"cneController\"][\"USE_GATEWAY_SETTINGS\"]\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terraform rejected the snapshot as a var-file:\n%s\n--- snapshot ---\n%s", out, rendered)
	}
	if !strings.Contains(string(out), "false") {
		t.Errorf("snapshot did not round-trip the value: %s", out)
	}
}
