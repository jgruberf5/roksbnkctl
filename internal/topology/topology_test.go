package topology_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/topology"
)

// minimalCluster returns a minimal valid *intent.Cluster for testing without
// needing a real cluster.yaml on disk.
func minimalCluster() *intent.Cluster {
	yes := true
	_ = yes
	return &intent.Cluster{
		APIVersion: "awsbnkctl/v1",
		Kind:       "Cluster",
		Metadata: intent.Metadata{
			Name:   "test-cluster",
			Region: "ap-southeast-2",
		},
		Pattern: "host-device",
		Network: intent.Network{
			VPCCidr:     "10.0.0.0/16",
			AZs:         []string{"ap-southeast-2a"},
			NatGateways: 1,
			Subnets: intent.Subnets{
				Public:  []intent.SubnetSpec{{CIDR: "10.0.1.0/24", AZ: "ap-southeast-2a"}},
				Private: []intent.SubnetSpec{{CIDR: "10.0.11.0/24", AZ: "ap-southeast-2a"}},
			},
			DataPath: &intent.DataPathSpec{
				External: intent.SubnetSpec{CIDR: "10.0.10.0/24", AZ: "ap-southeast-2a"},
				Internal: intent.SubnetSpec{CIDR: "10.0.20.0/24", AZ: "ap-southeast-2a"},
				SelfIPs: &intent.SelfIPsSpec{
					External:  "10.0.10.240",
					Internal:  "10.0.20.240",
					PrefixLen: 24,
				},
			},
		},
		ClusterSpec: &intent.ClusterSpec{
			KubernetesVersion: "1.30",
			NodeGroups: []intent.NodeGroupSpec{
				{
					Name:         "default",
					InstanceType: "m6i.4xlarge",
					DesiredSize:  3,
					MinSize:      3,
					MaxSize:      4,
				},
			},
		},
		Bnk: &intent.BnkSpec{
			FARArchive: "/tmp/far.json",
			JWT:        "/tmp/license.jwt",
		},
	}
}

// writeStateEnv writes a minimal state.env file to dir and returns a loaded *state.State.
func writeStateEnv(t *testing.T, dir string, kvs map[string]string) *state.State {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var lines []string
	for k, v := range kvs {
		lines = append(lines, k+"="+v)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "state.env"), []byte(content), 0o644); err != nil {
		t.Fatalf("writing state.env: %v", err)
	}
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	return st
}

// TestBuild_StateOverlaysIntent verifies that non-empty state keys win over intent values.
func TestBuild_StateOverlaysIntent(t *testing.T) {
	cl := minimalCluster()
	dir := t.TempDir()
	st := writeStateEnv(t, dir, map[string]string{
		"VPC_ID":               "vpc-abc123",
		"TMM_EXT_SELFIP":       "10.0.10.241", // overrides intent 10.0.10.240
		"EXTERNAL_IFNAME":      "ens8",
		"INTERNAL_IFNAME":      "ens7",
		"JUMPHOST_INSTANCE_ID": "i-0deadbeef",
		"GATEWAYCLASS_NAME":    "f5-gateway",
	})

	m := topology.Build(cl, st)

	if m.VPCID != "vpc-abc123" {
		t.Errorf("VPCID: got %q want vpc-abc123", m.VPCID)
	}
	// State overrides the intent-derived SelfIP.
	if m.TMM.ExtSelfIP != "10.0.10.241" {
		t.Errorf("TMM.ExtSelfIP: got %q want 10.0.10.241", m.TMM.ExtSelfIP)
	}
	if m.TMM.ExtIfname != "ens8" {
		t.Errorf("TMM.ExtIfname: got %q want ens8", m.TMM.ExtIfname)
	}
	if m.Jumphost.InstanceID != "i-0deadbeef" {
		t.Errorf("Jumphost.InstanceID: got %q want i-0deadbeef", m.Jumphost.InstanceID)
	}
	if m.GatewayClassName != "f5-gateway" {
		t.Errorf("GatewayClassName: got %q want f5-gateway", m.GatewayClassName)
	}
	// Intent CIDRs must survive (not clobbered by state).
	if m.DataExtSubnet.CIDR != "10.0.10.0/24" {
		t.Errorf("DataExtSubnet.CIDR: got %q want 10.0.10.0/24", m.DataExtSubnet.CIDR)
	}
}

// TestBuild_EmptyStateFallsBackToIntent verifies that missing state keys fall
// back to intent-derived values.
func TestBuild_EmptyStateFallsBackToIntent(t *testing.T) {
	cl := minimalCluster()
	// Empty state — no state.env file in the temp dir.
	dir := t.TempDir()
	st, err := state.Load(dir) // file absent → empty state
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}

	m := topology.Build(cl, st)

	if m.VPCID != "" {
		t.Errorf("VPCID: expected empty (not provisioned), got %q", m.VPCID)
	}
	// Intent self-IPs present.
	if m.TMM.ExtSelfIP != "10.0.10.240" {
		t.Errorf("TMM.ExtSelfIP: got %q want 10.0.10.240 (from intent)", m.TMM.ExtSelfIP)
	}
	if m.TMM.IntSelfIP != "10.0.20.240" {
		t.Errorf("TMM.IntSelfIP: got %q want 10.0.20.240 (from intent)", m.TMM.IntSelfIP)
	}
	// Cluster identity from intent.
	if m.ClusterName != "test-cluster" {
		t.Errorf("ClusterName: got %q want test-cluster", m.ClusterName)
	}
	if m.NodeGroup.InstanceType != "m6i.4xlarge" {
		t.Errorf("NodeGroup.InstanceType: got %q want m6i.4xlarge", m.NodeGroup.InstanceType)
	}
}

// TestBuild_MissingEverythingNoFields verifies that a minimal cluster with nil
// state produces "" in live-only fields (no panic).
func TestBuild_MissingEverythingNoFields(t *testing.T) {
	cl := minimalCluster()
	m := topology.Build(cl, nil)

	if m.VPCID != "" {
		t.Errorf("VPCID should be empty, got %q", m.VPCID)
	}
	if m.Jumphost.InstanceID != "" {
		t.Errorf("Jumphost.InstanceID should be empty, got %q", m.Jumphost.InstanceID)
	}
	if m.GatewayClassName != "" {
		t.Errorf("GatewayClassName should be empty, got %q", m.GatewayClassName)
	}
}

// TestRenderASCII_FullModel checks that a fully-populated Model contains
// expected strings in the ASCII output.
func TestRenderASCII_FullModel(t *testing.T) {
	cl := minimalCluster()
	dir := t.TempDir()
	st := writeStateEnv(t, dir, map[string]string{
		"TMM_EXT_SELFIP":          "10.0.10.240",
		"TMM_INT_SELFIP":          "10.0.20.240",
		"JUMPHOST_INSTANCE_ID":    "i-0abc1234",
		"JUMPHOST_BNK_EXT_ENI_IP": "10.0.10.5",
		"GATEWAYCLASS_NAME":       "f5-spk-gateway",
	})

	m := topology.Build(cl, st)
	out := topology.RenderASCII(m)

	checks := []string{
		"test-cluster",
		"10.0.10.240",
		"10.0.20.240",
		"10.0.10.0/24",
		"10.0.20.0/24",
		"i-0abc1234",
		"10.0.10.5",
		"f5-spk-gateway",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("RenderASCII: expected %q in output\n---\n%s\n---", want, out)
		}
	}
}

// TestRenderASCII_EmptyModel verifies that an all-empty Model renders without
// panicking and includes the "(not provisioned)" sentinel.
func TestRenderASCII_EmptyModel(t *testing.T) {
	// Use a bare minimum cluster so Build doesn't panic on nil fields.
	cl := &intent.Cluster{
		APIVersion: "awsbnkctl/v1",
		Kind:       "Cluster",
		Metadata: intent.Metadata{
			Name:   "empty",
			Region: "us-east-1",
		},
		Network: intent.Network{
			VPCCidr:     "10.0.0.0/16",
			AZs:         []string{"us-east-1a"},
			NatGateways: 1,
			Subnets: intent.Subnets{
				Public:  []intent.SubnetSpec{{CIDR: "10.0.1.0/24", AZ: "us-east-1a"}},
				Private: []intent.SubnetSpec{{CIDR: "10.0.11.0/24", AZ: "us-east-1a"}},
			},
		},
	}
	m := topology.Build(cl, nil)
	out := topology.RenderASCII(m)

	if !strings.Contains(out, "(not provisioned)") {
		t.Errorf("RenderASCII: expected (not provisioned) in empty-model output\n%s", out)
	}
	// Must not panic and must produce a box.
	if !strings.Contains(out, "┌") || !strings.Contains(out, "└") {
		t.Errorf("RenderASCII: missing box-drawing characters in output\n%s", out)
	}
}

// TestRenderMermaid_GraphTDAndLabels verifies that RenderMermaid produces
// a graph TD header and includes expected node labels.
func TestRenderMermaid_GraphTDAndLabels(t *testing.T) {
	cl := minimalCluster()
	m := topology.Build(cl, nil)
	out := topology.RenderMermaid(m)

	if !strings.HasPrefix(out, "graph TD") {
		t.Errorf("RenderMermaid: expected output to start with 'graph TD', got:\n%s", out)
	}
	checks := []string{
		"10.0.0.0/16",  // VPC CIDR in VPC node
		"10.0.10.0/24", // BNK_EXT CIDR
		"10.0.20.0/24", // BNK_INT CIDR
		"10.0.10.240",  // ext self-IP
		"10.0.20.240",  // int self-IP
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("RenderMermaid: expected %q in output\n---\n%s\n---", want, out)
		}
	}
}
