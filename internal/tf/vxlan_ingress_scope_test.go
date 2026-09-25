package tf

import (
	"os"
	"strings"
	"testing"
)

// #314: the gateway phase opened UDP 6789 inbound on the CLUSTER'S OWN worker
// security group from 0.0.0.0/0. The rule itself is required — TMM answers a
// remote node's VXLAN from its external-VLAN self-IP, which the workers' SG
// does not otherwise admit, so egress from any node without a TMM fails without
// it — but the breadth never was.
//
// The senders are the TMM self-IPs, which are by construction inside the
// per-zone ext_vlan_cidr. On the verified 2.4 GA install the Infra CR's
// external-vlan IPAM pools are 10.155.15.0/24, 10.156.16.0/24, 10.157.17.0/24
// and the controller reports self-IPs 10.155.15.2, 10.156.16.2, 10.157.17.2.
//
// These evaluate local.vxlan_remote_cidrs rather than scanning the source, for
// the reason in line_gating_eval_test.go: a commented-out line is still text.

var gatewayModule = []string{"gateway"}

// The defaults must produce exactly the three external-VLAN CIDRs, and must NOT
// produce 0.0.0.0/0.
func TestVXLANIngressIsScopedToExternalVLANCIDRs(t *testing.T) {
	got := consoleStrings(t, gatewayModule, "bnk_line = \"2.4\"\n", "local.vxlan_remote_cidrs")

	want := map[string]bool{"10.155.15.0/24": true, "10.156.16.0/24": true, "10.157.17.0/24": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want the three per-zone ext_vlan_cidr values", got)
	}
	for _, c := range got {
		if c == "0.0.0.0/0" {
			t.Fatal("0.0.0.0/0 is still in the permitted set; the rule is open to the world (#314)")
		}
		if !want[c] {
			t.Errorf("unexpected CIDR %q in the VXLAN ingress set", c)
		}
	}
}

// Empty entries must be dropped rather than becoming an empty `remote`, which
// the API would reject at apply time instead of plan time.
func TestVXLANIngressDropsEmptyCIDRs(t *testing.T) {
	tfvars := `bnk_line = "2.4"
cneinstance_network_zones = [
  { ext_vlan_cidr = "10.1.0.0/24", int_vlan_cidr = "", int_snat_cidr = "", int_vip_cidr = "", external_selfip = "", internal_selfip = "" },
  { ext_vlan_cidr = "",            int_vlan_cidr = "", int_snat_cidr = "", int_vip_cidr = "", external_selfip = "", internal_selfip = "" },
]
`
	got := consoleStrings(t, gatewayModule, tfvars, "local.vxlan_remote_cidrs")
	if len(got) != 1 || got[0] != "10.1.0.0/24" {
		t.Errorf("got %v, want only the non-empty CIDR", got)
	}
}

// Duplicate zone CIDRs must collapse: ibm_is_security_group_rule is keyed by
// CIDR, and a duplicate key is a plan-time error rather than a second rule.
func TestVXLANIngressDeduplicates(t *testing.T) {
	tfvars := `bnk_line = "2.4"
cneinstance_network_zones = [
  { ext_vlan_cidr = "10.1.0.0/24", int_vlan_cidr = "", int_snat_cidr = "", int_vip_cidr = "", external_selfip = "", internal_selfip = "" },
  { ext_vlan_cidr = "10.1.0.0/24", int_vlan_cidr = "", int_snat_cidr = "", int_vip_cidr = "", external_selfip = "", internal_selfip = "" },
]
`
	got := consoleStrings(t, gatewayModule, tfvars, "local.vxlan_remote_cidrs")
	if len(got) != 1 {
		t.Errorf("got %v, want one entry — for_each over duplicates is a plan error", got)
	}
}

// The source must no longer carry the open remote. This is a text check on
// purpose and it is NOT the primary guard: it catches a 0.0.0.0/0 reintroduced
// on some other rule in this module, which evaluating one local cannot see.
func TestGatewayHasNoOpenVXLANRemote(t *testing.T) {
	src := readGatewaySource(t)
	idx := strings.Index(src, `resource "ibm_is_security_group_rule" "vxlan_ingress"`)
	if idx < 0 {
		t.Fatal("vxlan_ingress rule not found; this guard cannot check what it cannot find")
	}
	body := src[idx:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	if strings.Contains(body, `"0.0.0.0/0"`) {
		t.Error("vxlan_ingress permits 0.0.0.0/0 again (#314)")
	}
	if !strings.Contains(body, "each.value") {
		t.Error("vxlan_ingress no longer uses a per-CIDR remote")
	}
}

func readGatewaySource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../terraform/modules/gateway/main.tf")
	if err != nil {
		t.Fatalf("read gateway/main.tf: %v", err)
	}
	return string(b)
}
