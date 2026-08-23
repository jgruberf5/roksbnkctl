package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// isolateEnv blanks every ROKSBNKCTL_* variable the ambient shell happens to be
// carrying. runInitFromEnv reads the real process environment, so without this
// an operator who has exported an override in their own shell — say
// ROKSBNKCTL_CERT_MANAGER_CREATE=false while driving a disconnected install —
// sees these tests fail for a reason that has nothing to do with the code under
// test. t.Setenv restores the originals at cleanup, and the override reader
// treats an empty value as unset.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "ROKSBNKCTL_") {
			t.Setenv(k, "")
		}
	}
	t.Setenv("IBMCLOUD_API_KEY", "")
}

// TestRunInitFromEnv pins the `--non-interactive` env-only init path: a workspace
// config.yaml assembled purely from the ROKSBNKCTL_* / IBMCLOUD_API_KEY env vars,
// no file, no prompt — the argv+env container-runner contract.
func TestRunInitFromEnv(t *testing.T) {
	isolateEnv(t)
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	t.Setenv("IBMCLOUD_API_KEY", "test-api-key")
	t.Setenv("ROKSBNKCTL_REGION", "eu-de")
	t.Setenv("ROKSBNKCTL_RESOURCE_GROUP", "default")
	t.Setenv("ROKSBNKCTL_PREFIX", "acme-eu")
	t.Setenv("ROKSBNKCTL_CLUSTER_NAME", "acme-eu-roks")
	t.Setenv("ROKSBNKCTL_CLUSTER_CREATE", "true")
	t.Setenv("ROKSBNKCTL_WORKERS_PER_ZONE", "2")

	cctx, err := config.New("forge")
	if err != nil {
		t.Fatal(err)
	}
	if err := runInitFromEnv(cctx); err != nil {
		t.Fatalf("runInitFromEnv: %v", err)
	}

	ws, err := config.LoadWorkspace("forge")
	if err != nil {
		t.Fatal(err)
	}
	if ws.IBMCloud.Region != "eu-de" || ws.IBMCloud.ResourceGroup != "default" {
		t.Errorf("ibmcloud = %+v; want eu-de/default", ws.IBMCloud)
	}
	if ws.Prefix != "acme-eu" {
		t.Errorf("prefix = %q; want acme-eu", ws.Prefix)
	}
	if !ws.Cluster.Create || ws.Cluster.Name != "acme-eu-roks" || ws.Cluster.WorkersPerZone != 2 {
		t.Errorf("cluster = %+v; want create:true name:acme-eu-roks workers:2", ws.Cluster)
	}
	if ws.TFSource.Type != "embedded" {
		t.Errorf("tf_source.type = %q; want embedded (defaulted)", ws.TFSource.Type)
	}
	if ws.IBMCloud.APIKeyB64 == "" {
		t.Error("api_key_b64 not set from IBMCLOUD_API_KEY")
	}
}

// TestRunInitFromEnv_AdvancedFields pins the BNK Forge advanced overrides: TGW
// adoption (without zeroing the other resource toggles), testing-VPC name, CIS
// BIG-IP creds, and a full per-zone network mapping.
func TestRunInitFromEnv_AdvancedFields(t *testing.T) {
	isolateEnv(t)
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	t.Setenv("IBMCLOUD_API_KEY", "k")
	t.Setenv("ROKSBNKCTL_REGION", "eu-de")
	t.Setenv("ROKSBNKCTL_RESOURCE_GROUP", "default")
	t.Setenv("ROKSBNKCTL_PREFIX", "acme")
	t.Setenv("ROKSBNKCTL_CLUSTER_NAME", "acme-roks")
	t.Setenv("ROKSBNKCTL_TRANSIT_GATEWAY_NAME", "shared-corp-tgw")
	t.Setenv("ROKSBNKCTL_CLUSTER_VPC_ID", "r006-vpc-abc123")
	t.Setenv("ROKSBNKCTL_TESTING_VPC_NAME", "acme-client-vpc")
	t.Setenv("ROKSBNKCTL_BIGIP_URL", "https://10.1.1.5")
	t.Setenv("ROKSBNKCTL_BIGIP_USERNAME", "admin")
	t.Setenv("ROKSBNKCTL_BIGIP_PASSWORD", "s3cret")
	t.Setenv("ROKSBNKCTL_ZONE1_EXT_VLAN_CIDR", "10.10.1.0/24")
	t.Setenv("ROKSBNKCTL_ZONE1_INT_VLAN_CIDR", "10.10.2.0/24")
	t.Setenv("ROKSBNKCTL_ZONE1_INT_SNAT_CIDR", "10.10.3.0/24")
	t.Setenv("ROKSBNKCTL_ZONE1_INT_VIP_CIDR", "10.10.4.0/24")
	t.Setenv("ROKSBNKCTL_ZONE1_EXTERNAL_SELFIP", "10.10.1.10")
	t.Setenv("ROKSBNKCTL_ZONE1_INTERNAL_SELFIP", "10.10.2.10")

	cctx, err := config.New("forge")
	if err != nil {
		t.Fatal(err)
	}
	if err := runInitFromEnv(cctx); err != nil {
		t.Fatalf("runInitFromEnv: %v", err)
	}
	ws, err := config.LoadWorkspace("forge")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Resources == nil {
		t.Fatal("resources nil")
	}
	if ws.Resources.TransitGateway.Create || ws.Resources.TransitGateway.Existing != "shared-corp-tgw" {
		t.Errorf("transit_gateway = %+v; want {create:false existing:shared-corp-tgw}", ws.Resources.TransitGateway)
	}
	// the TGW override must NOT zero the other toggles
	if !ws.Resources.BNK.Create || !ws.Resources.RegistryCOS.Create || !ws.Resources.CertManager.Create {
		t.Errorf("default toggles got zeroed: %+v", ws.Resources)
	}
	if ws.Resources.ClusterVPC.Create || ws.Resources.ClusterVPC.Existing != "r006-vpc-abc123" {
		t.Errorf("cluster_vpc = %+v; want {create:false existing:r006-vpc-abc123}", ws.Resources.ClusterVPC)
	}
	if ws.Resources.TestingClientVPCName != "acme-client-vpc" {
		t.Errorf("testing_client_vpc_name = %q", ws.Resources.TestingClientVPCName)
	}
	if ws.BNK.CIS == nil || ws.BNK.CIS.BigIPURL != "https://10.1.1.5" || ws.BNK.CIS.BigIPUsername != "admin" || ws.BNK.CIS.BigIPPasswordB64 == "" {
		t.Errorf("cis = %+v; want url/user set + password b64", ws.BNK.CIS)
	}
	if ws.BNK.Network == nil || len(ws.BNK.Network.Zones) != 1 {
		t.Fatalf("network zones = %+v; want 1", ws.BNK.Network)
	}
	z := ws.BNK.Network.Zones[0]
	if z.IntVIPCIDR != "10.10.4.0/24" || z.IntSNATCIDR != "10.10.3.0/24" {
		t.Errorf("zone1 = %+v; want vip/snat set", z)
	}
}

// TestRunInitFromEnv_MissingRequired pins that an incomplete env errors (not
// prompts) — the non-interactive contract must fail fast, never hang.
func TestRunInitFromEnv_MissingRequired(t *testing.T) {
	isolateEnv(t)
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	t.Setenv("ROKSBNKCTL_REGION", "eu-de") // resource_group + prefix missing
	cctx, err := config.New("forge")
	if err != nil {
		t.Fatal(err)
	}
	if err := runInitFromEnv(cctx); err == nil {
		t.Fatal("want an error for incomplete env, got nil")
	}
}
