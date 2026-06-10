package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestReadStateOutputs(t *testing.T) {
	dir := t.TempDir()
	state := `{
	  "outputs": {
	    "testing_tgw_jumphost_ip":      {"value": "150.0.0.5", "type": "string"},
	    "testing_cluster_jumphost_ips": {"value": ["10.0.1.4", "10.0.2.4"], "type": ["list","string"]},
	    "jumphost_shared_key":          {"value": "-----BEGIN KEY-----", "type": "string", "sensitive": true}
	  }
	}`
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := ReadStateOutputs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := out["testing_tgw_jumphost_ip"].Value; got != "150.0.0.5" {
		t.Errorf("tgw ip = %v, want 150.0.0.5", got)
	}
	if ips, ok := out["testing_cluster_jumphost_ips"].Value.([]any); !ok || len(ips) != 2 {
		t.Errorf("cluster ips = %v, want a 2-element list", out["testing_cluster_jumphost_ips"].Value)
	}
	if !out["jumphost_shared_key"].Sensitive {
		t.Error("jumphost_shared_key should be Sensitive")
	}

	// Not deployed (no state file) → fs.ErrNotExist so the status command can
	// report "not deployed" rather than erroring.
	if _, err := ReadStateOutputs(t.TempDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing state: want fs.ErrNotExist, got %v", err)
	}
}
