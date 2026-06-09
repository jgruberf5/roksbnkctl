package orchestration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func TestPickClusterJumphost(t *testing.T) {
	ip, zone := pickClusterJumphost(map[string]string{
		"jp-osa-3": "10.0.3.5",
		"jp-osa-1": "10.0.1.5",
		"jp-osa-2": "10.0.2.5",
	})
	if zone != "jp-osa-1" || ip != "10.0.1.5" {
		t.Errorf("pickClusterJumphost = (%q,%q), want (10.0.1.5, jp-osa-1) — lowest zone name", ip, zone)
	}
	// Blank IPs are skipped; empty map yields empties.
	if ip, _ := pickClusterJumphost(map[string]string{"z": "  "}); ip != "" {
		t.Errorf("blank IP should be skipped, got %q", ip)
	}
	if ip, _ := pickClusterJumphost(nil); ip != "" {
		t.Errorf("nil map should yield empty, got %q", ip)
	}
}

// stageTestingState writes a state-testing/terraform.tfstate carrying the
// private-IP outputs, under a per-test ROKSBNKCTL_HOME.
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

const tfstateWithPrivateIPs = `{
  "outputs": {
    "testing_tgw_jumphost_private_ip": { "value": "10.245.64.7", "type": "string" },
    "testing_cluster_jumphost_private_ips": {
      "value": { "jp-osa-2": "10.244.2.9", "jp-osa-1": "10.244.1.9" },
      "type": ["map", "string"]
    }
  }
}`

func TestTryAutoGatewayClientSubnets_FillsEmpty(t *testing.T) {
	ws := stageTestingState(t, tfstateWithPrivateIPs)
	wsCfg := &config.Workspace{} // both client subnets empty
	var out bytes.Buffer

	tryAutoGatewayClientSubnets(wsCfg, ws, &out)

	if wsCfg.Gateway.ClientSubnetRemote != "10.245.64.7" {
		t.Errorf("remote = %q, want the TGW jumphost IP 10.245.64.7", wsCfg.Gateway.ClientSubnetRemote)
	}
	if wsCfg.Gateway.ClientSubnetLocal != "10.244.1.9" {
		t.Errorf("local = %q, want the lowest-zone cluster jumphost IP 10.244.1.9", wsCfg.Gateway.ClientSubnetLocal)
	}
	if !strings.Contains(out.String(), "auto-derived") {
		t.Errorf("expected a log line about auto-derivation, got:\n%s", out.String())
	}
}

func TestTryAutoGatewayClientSubnets_RespectsConfig(t *testing.T) {
	ws := stageTestingState(t, tfstateWithPrivateIPs)
	wsCfg := &config.Workspace{}
	wsCfg.Gateway.ClientSubnetLocal = "192.168.1.1"  // operator-set
	wsCfg.Gateway.ClientSubnetRemote = "192.168.2.1" // operator-set
	var out bytes.Buffer

	tryAutoGatewayClientSubnets(wsCfg, ws, &out)

	if wsCfg.Gateway.ClientSubnetLocal != "192.168.1.1" || wsCfg.Gateway.ClientSubnetRemote != "192.168.2.1" {
		t.Errorf("operator-set values were overwritten: local=%q remote=%q",
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

	if wsCfg.Gateway.ClientSubnetLocal != "" || wsCfg.Gateway.ClientSubnetRemote != "" {
		t.Errorf("nothing should be set when testing is absent")
	}
	if !strings.Contains(out.String(), "warning") {
		t.Errorf("expected a fallback warning, got:\n%s", out.String())
	}
}

func TestTryAutoGatewayClientSubnets_TGWSentinelNormalised(t *testing.T) {
	// testing_create_tgw_jumphost = false → the module emits the sentinel;
	// only the cluster jumphost should fill, and remote stays empty + warns.
	const stateNoTGW = `{
  "outputs": {
    "testing_tgw_jumphost_private_ip": { "value": "TGW jumphost not created", "type": "string" },
    "testing_cluster_jumphost_private_ips": { "value": { "z1": "10.244.1.9" }, "type": ["map","string"] }
  }
}`
	ws := stageTestingState(t, stateNoTGW)
	wsCfg := &config.Workspace{}
	var out bytes.Buffer

	tryAutoGatewayClientSubnets(wsCfg, ws, &out)

	if wsCfg.Gateway.ClientSubnetRemote != "" {
		t.Errorf("remote should stay empty for the sentinel, got %q", wsCfg.Gateway.ClientSubnetRemote)
	}
	if wsCfg.Gateway.ClientSubnetLocal != "10.244.1.9" {
		t.Errorf("local should still fill, got %q", wsCfg.Gateway.ClientSubnetLocal)
	}
	if !strings.Contains(out.String(), "warning") {
		t.Errorf("expected a partial-derivation warning, got:\n%s", out.String())
	}
}
