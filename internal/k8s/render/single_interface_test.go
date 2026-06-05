package render

import (
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
)

// These tests pin the single-interface (external-only) rendering contract: the
// embedded templates must emit the external interface but OMIT every internal
// counterpart so an external-only cluster doesn't reference a nonexistent ENI.

func TestRenderNADs_SingleInterface_OmitsInternal(t *testing.T) {
	tmpl, err := manifests.FS.ReadFile("host-device/network-attachment-defs.yaml.tmpl")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	out, err := RenderNADs(tmpl, "f5-cne-system", false, func(string) string { return "" })
	if err != nil {
		t.Fatalf("RenderNADs: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "external-netdevice") {
		t.Errorf("single-interface NADs must keep the external NAD:\n%s", s)
	}
	if strings.Contains(s, "internal-netdevice") {
		t.Errorf("single-interface NADs must omit the internal NAD:\n%s", s)
	}
}

func TestRenderF5SPKVlan_SingleInterface_OmitsIntVlan(t *testing.T) {
	tmpl, err := manifests.FS.ReadFile("host-device/f5spkvlan.yaml.tmpl")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	out, err := RenderF5SPKVlan(tmpl, "10.0.10.240", "", 24, false)
	if err != nil {
		t.Fatalf("RenderF5SPKVlan: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "name: ext-vlan") {
		t.Errorf("single-interface F5SPKVlan must keep ext-vlan:\n%s", s)
	}
	// "int-vlan" appears in the header comment; assert the CR body is gone.
	if strings.Contains(s, "name: int-vlan") || strings.Contains(s, "internal: true") {
		t.Errorf("single-interface F5SPKVlan must omit the int-vlan CR:\n%s", s)
	}
}

func TestRenderCNEInstance_SingleInterface_OmitsInternal(t *testing.T) {
	tmpl, err := manifests.FS.ReadFile("shared/cneinstance.yaml.tmpl")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	cl := cneInstanceCluster()
	cl.Pattern = intent.PatternExternalOnly // override the dual default
	out, err := RenderCNEInstance(tmpl, cl, func(k string) string {
		if k == "VPC_ID" {
			return "vpc-123"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("RenderCNEInstance: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "- external-netdevice") {
		t.Errorf("CNEInstance must list the external NAD:\n%s", s)
	}
	if strings.Contains(s, "- internal-netdevice") {
		t.Errorf("single-interface CNEInstance must not list the internal NAD:\n%s", s)
	}
	if strings.Contains(s, "ROBIN_VFIO_RESOURCE_2") {
		t.Errorf("single-interface CNEInstance must not set ROBIN_VFIO_RESOURCE_2:\n%s", s)
	}
	// The external resource must still be present.
	if !strings.Contains(s, "ROBIN_VFIO_RESOURCE_1") {
		t.Errorf("CNEInstance must keep ROBIN_VFIO_RESOURCE_1:\n%s", s)
	}
}

func TestRenderCloudNetworkMapping_SingleInterface_OmitsInternalSubnet(t *testing.T) {
	tmpl, err := manifests.FS.ReadFile("shared/cloud-network-mapping.yaml.tmpl")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	cl := &intent.Cluster{
		Metadata: intent.Metadata{Name: "ext-only", Region: "ap-southeast-2"},
		Pattern:  intent.PatternExternalOnly,
		Network: intent.Network{
			AZs:     []string{"ap-southeast-2a"},
			Subnets: intent.Subnets{Public: []intent.SubnetSpec{{CIDR: "10.0.1.0/24", AZ: "ap-southeast-2a"}}},
			DataPath: &intent.DataPathSpec{
				External: intent.SubnetSpec{CIDR: "10.0.10.0/24", AZ: "ap-southeast-2a"},
			},
		},
	}
	// Note: BNK_INT_SUBNET deliberately absent — single-interface must not need it.
	getter := func(k string) string {
		switch k {
		case "MGMT_SUBNET":
			return "subnet-mgmt"
		case "BNK_EXT_SUBNET":
			return "subnet-ext"
		}
		return ""
	}
	out, err := RenderCloudNetworkMapping(tmpl, cl, getter)
	if err != nil {
		t.Fatalf("RenderCloudNetworkMapping (single-interface): %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "subnet-ext") {
		t.Errorf("cloud-network-mapping must include the external subnet:\n%s", s)
	}
	if strings.Contains(s, "10.0.20.0/24") {
		t.Errorf("single-interface cloud-network-mapping must omit the internal subnet entry:\n%s", s)
	}
}
