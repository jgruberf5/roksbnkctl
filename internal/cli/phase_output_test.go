package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	roksbnkctl "github.com/jgruberf5/roksbnkctl"
	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// stageState writes a terraform.tfstate carrying outputs into the given phase
// state dir. outs maps name -> {value, sensitive}.
func stageState(t *testing.T, dir string, outs map[string]map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, _ := json.Marshal(map[string]any{"outputs": outs})
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), st, 0o644); err != nil {
		t.Fatal(err)
	}
}

func resetOutputFlags(t *testing.T) {
	t.Helper()
	oldWS, prevJSON, prevShow := flagWorkspace, flagOutputJSON, flagOutputShowSensitive
	flagWorkspace = ""
	t.Cleanup(func() {
		flagWorkspace, flagOutputJSON, flagOutputShowSensitive = oldWS, prevJSON, prevShow
	})
}

// TestPhaseOutputOwnershipFiltering pins that `<phase> output` shows ONLY the
// outputs that phase manages — the shared-schema placeholders for other phases'
// resources are dropped — and that naming another phase's output errors with a
// pointer to the right command.
func TestPhaseOutputOwnershipFiltering(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("ws"); err != nil {
		t.Fatal(err)
	}
	resetOutputFlags(t)

	dir, _ := config.WorkspaceClusterStateDir("ws")
	// cluster state carries the full shared schema: its own roks_* plus a
	// placeholder for a testing-owned output.
	stageState(t, dir, map[string]map[string]any{
		"roks_cluster_id":         {"value": "abc123", "sensitive": false},
		"roks_cluster_name":       {"value": "my-roks", "sensitive": false},
		"testing_tgw_jumphost_ip": {"value": "TGW jumphost not created", "sensitive": false},
		"flo_namespace":           {"value": "f5-bnk", "sensitive": false},
	})

	run := func(args ...string) (string, error) {
		cmd := &cobra.Command{}
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		err := runPhaseOutput(cmd, "cluster", config.WorkspaceClusterStateDir, args)
		return buf.String(), err
	}

	// Full set: only cluster-owned keys; testing_/flo_ filtered out.
	flagOutputJSON = true
	out, err := run()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["roks_cluster_id"]; !ok {
		t.Errorf("cluster output should contain roks_cluster_id: %v", m)
	}
	if _, ok := m["testing_tgw_jumphost_ip"]; ok {
		t.Errorf("cluster output must NOT contain the testing-owned placeholder: %v", m)
	}
	if _, ok := m["flo_namespace"]; ok {
		t.Errorf("cluster output must NOT contain the bnk-owned flo_namespace: %v", m)
	}

	// Named owned output → raw value.
	flagOutputJSON = false
	if got, err := run("roks_cluster_id"); err != nil || strings.TrimSpace(got) != "abc123" {
		t.Errorf("cluster output roks_cluster_id = %q, err=%v; want abc123", got, err)
	}

	// Naming a testing-owned output from the cluster command errors helpfully.
	if _, err := run("testing_tgw_jumphost_ip"); err == nil || !strings.Contains(err.Error(), "testing phase") {
		t.Errorf("naming a testing-owned output from `cluster output` must point at the testing phase; got %v", err)
	}

	// Unknown output errors.
	if _, err := run("does_not_exist"); err == nil {
		t.Error("unknown output name must error")
	}
}

// TestAggregateOutput pins that `roksbnkctl output` merges each phase's OWNED
// outputs from that phase's OWN state — so the value is the populated one and the
// merged set never conflicts.
func TestAggregateOutput(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("ws"); err != nil {
		t.Fatal(err)
	}
	resetOutputFlags(t)

	clusterDir, _ := config.WorkspaceClusterStateDir("ws")
	testingDir, _ := config.WorkspaceTestingStateDir("ws")
	// Both states carry roks_cluster_id (shared schema) with DIFFERENT values,
	// and each its own placeholder for the other's resource.
	stageState(t, clusterDir, map[string]map[string]any{
		"roks_cluster_id":         {"value": "cluster-real", "sensitive": false},
		"testing_tgw_jumphost_ip": {"value": "TGW jumphost not created", "sensitive": false},
	})
	stageState(t, testingDir, map[string]map[string]any{
		"roks_cluster_id":         {"value": "testing-copy", "sensitive": false},
		"testing_tgw_jumphost_ip": {"value": "150.1.2.3", "sensitive": false},
	})

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	flagOutputJSON = true
	if err := runAggregateOutput(cmd, nil); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	// roks_cluster_id from the CLUSTER owner; testing_tgw_jumphost_ip from TESTING.
	if m["roks_cluster_id"] != "cluster-real" {
		t.Errorf("aggregate roks_cluster_id = %v; want cluster-real (from the owning phase's state)", m["roks_cluster_id"])
	}
	if m["testing_tgw_jumphost_ip"] != "150.1.2.3" {
		t.Errorf("aggregate testing_tgw_jumphost_ip = %v; want 150.1.2.3 (from the testing phase)", m["testing_tgw_jumphost_ip"])
	}
}

// TestPhaseOutputSensitiveRedaction pins redaction on the one sensitive output
// (jumphost_shared_key, testing-owned).
func TestPhaseOutputSensitiveRedaction(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("ws"); err != nil {
		t.Fatal(err)
	}
	resetOutputFlags(t)

	dir, _ := config.WorkspaceTestingStateDir("ws")
	stageState(t, dir, map[string]map[string]any{
		"jumphost_shared_key": {"value": "PEMDATA", "sensitive": true},
	})

	run := func() map[string]any {
		cmd := &cobra.Command{}
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		flagOutputJSON = true
		if err := runPhaseOutput(cmd, "testing", config.WorkspaceTestingStateDir, nil); err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	flagOutputShowSensitive = false
	if got := run()["jumphost_shared_key"]; got != "<sensitive>" {
		t.Errorf("sensitive output = %v; want <sensitive>", got)
	}
	flagOutputShowSensitive = true
	if got := run()["jumphost_shared_key"]; got != "PEMDATA" {
		t.Errorf("--show-sensitive should reveal the key, got %v", got)
	}
}

// TestPhaseOutputOwnershipPartitionsRootOutputs guards against drift: every
// output declared in the embedded terraform/outputs.tf is owned by exactly one
// phase, and the ownership map names no output that isn't in the root.
func TestPhaseOutputOwnershipPartitionsRootOutputs(t *testing.T) {
	body, err := fs.ReadFile(roksbnkctl.EmbeddedTerraform, "terraform/outputs.tf")
	if err != nil {
		t.Fatalf("reading embedded terraform/outputs.tf: %v", err)
	}
	re := regexp.MustCompile(`(?m)^output "([a-z0-9_]+)"`)
	rootOutputs := map[string]bool{}
	for _, mt := range re.FindAllStringSubmatch(string(body), -1) {
		rootOutputs[mt[1]] = true
	}
	if len(rootOutputs) == 0 {
		t.Fatal("parsed zero outputs from terraform/outputs.tf")
	}

	// Every root output owned by exactly one phase.
	for name := range rootOutputs {
		if outputOwner(name) == "" {
			t.Errorf("root output %q is not assigned to any phase in phaseOutputOwnership", name)
		}
	}
	// No phantom owned names (every owned name exists in the root).
	for _, name := range allOwnedOutputNames() {
		if !rootOutputs[name] {
			t.Errorf("phaseOutputOwnership names %q which is not declared in terraform/outputs.tf", name)
		}
	}
	// No name owned by two phases.
	seen := map[string]int{}
	for _, name := range allOwnedOutputNames() {
		seen[name]++
		if seen[name] > 1 {
			t.Errorf("output %q is owned by more than one phase", name)
		}
	}
}
