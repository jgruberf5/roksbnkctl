package orchestration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func TestSortedClusterCIDRs(t *testing.T) {
	got := sortedClusterCIDRs(map[string]string{
		"jp-osa-3": "10.0.3.0/24",
		"jp-osa-1": "10.0.1.0/24",
		"jp-osa-2": "10.0.2.0/24",
	})
	want := []string{"10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sortedClusterCIDRs = %v, want %v (sorted by zone)", got, want)
	}
	// Blank CIDRs are dropped; empty map yields empty.
	if got := sortedClusterCIDRs(map[string]string{"z": "  "}); len(got) != 0 {
		t.Errorf("blank CIDR should be dropped, got %v", got)
	}
	if got := sortedClusterCIDRs(nil); len(got) != 0 {
		t.Errorf("nil map should yield empty, got %v", got)
	}
}

// stageTestingState writes a state-testing/terraform.tfstate carrying the
// subnet-CIDR outputs, under a per-test ROKSBNKCTL_HOME.
func stageTestingState(t *testing.T, tfstateJSON string) string {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "gw-autosubnet-test"
	dir, err := config.WorkspaceTestingStateDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(tfstateJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

const tfstateWithSubnetCIDRs = `{
  "outputs": {
    "testing_tgw_jumphost_subnet_cidr": { "value": "10.245.64.0/24", "type": "string" },
    "testing_cluster_jumphost_subnet_cidrs": {
      "value": { "jp-osa-2": "10.244.2.0/24", "jp-osa-1": "10.244.1.0/24" },
      "type": ["map", "string"]
    }
  }
}`

func TestTryAutoGatewayClientSubnets_FillsEmpty(t *testing.T) {
	ws := stageTestingState(t, tfstateWithSubnetCIDRs)
	wsCfg := &config.Workspace{} // both client subnets empty
	var out bytes.Buffer

	tryAutoGatewayClientSubnets(wsCfg, ws, &out)

	if got := wsCfg.Gateway.ClientSubnetRemote; len(got) != 1 || got[0] != "10.245.64.0/24" {
		t.Errorf("remote = %v, want [10.245.64.0/24] (the TGW jumphost subnet)", got)
	}
	// Both cluster subnets, sorted by zone — so a same-zone AND a diff-zone
	// client each get a route.
	want := []string{"10.244.1.0/24", "10.244.2.0/24"}
	if strings.Join(wsCfg.Gateway.ClientSubnetLocal, ",") != strings.Join(want, ",") {
		t.Errorf("local = %v, want %v (every cluster jumphost subnet)", wsCfg.Gateway.ClientSubnetLocal, want)
	}
	if !strings.Contains(out.String(), "auto-derived") {
		t.Errorf("expected a log line about auto-derivation, got:\n%s", out.String())
	}
}

func TestTryAutoGatewayClientSubnets_RespectsConfig(t *testing.T) {
	ws := stageTestingState(t, tfstateWithSubnetCIDRs)
	wsCfg := &config.Workspace{}
	wsCfg.Gateway.ClientSubnetLocal = []string{"192.168.1.0/24"}  // operator-set
	wsCfg.Gateway.ClientSubnetRemote = []string{"192.168.2.0/24"} // operator-set
	var out bytes.Buffer

	tryAutoGatewayClientSubnets(wsCfg, ws, &out)

	if strings.Join(wsCfg.Gateway.ClientSubnetLocal, ",") != "192.168.1.0/24" ||
		strings.Join(wsCfg.Gateway.ClientSubnetRemote, ",") != "192.168.2.0/24" {
		t.Errorf("operator-set values were overwritten: local=%v remote=%v",
			wsCfg.Gateway.ClientSubnetLocal, wsCfg.Gateway.ClientSubnetRemote)
	}
	if out.Len() != 0 {
		t.Errorf("nothing should be derived or logged when both are set, got:\n%s", out.String())
	}
}

func TestTryAutoGatewayClientSubnets_NoTestingWarns(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir()) // no state staged
	wsCfg := &config.Workspace{}
	var out bytes.Buffer

	tryAutoGatewayClientSubnets(wsCfg, "absent-ws", &out)

	if len(wsCfg.Gateway.ClientSubnetLocal) != 0 || len(wsCfg.Gateway.ClientSubnetRemote) != 0 {
		t.Errorf("nothing should be set when testing is absent")
	}
	if !strings.Contains(out.String(), "warning") {
		t.Errorf("expected a fallback warning, got:\n%s", out.String())
	}
}

func TestTryAutoGatewayClientSubnets_TGWSentinelNormalised(t *testing.T) {
	// testing_create_tgw_jumphost = false → the module emits the sentinel;
	// only the cluster subnets should fill, and remote stays empty + warns.
	const stateNoTGW = `{
  "outputs": {
    "testing_tgw_jumphost_subnet_cidr": { "value": "TGW jumphost not created", "type": "string" },
    "testing_cluster_jumphost_subnet_cidrs": { "value": { "z1": "10.244.1.0/24" }, "type": ["map","string"] }
  }
}`
	ws := stageTestingState(t, stateNoTGW)
	wsCfg := &config.Workspace{}
	var out bytes.Buffer

	tryAutoGatewayClientSubnets(wsCfg, ws, &out)

	if len(wsCfg.Gateway.ClientSubnetRemote) != 0 {
		t.Errorf("remote should stay empty for the sentinel, got %v", wsCfg.Gateway.ClientSubnetRemote)
	}
	if strings.Join(wsCfg.Gateway.ClientSubnetLocal, ",") != "10.244.1.0/24" {
		t.Errorf("local should still fill, got %v", wsCfg.Gateway.ClientSubnetLocal)
	}
	if !strings.Contains(out.String(), "warning") {
		t.Errorf("expected a partial-derivation warning, got:\n%s", out.String())
	}
}
