package intent

import (
	"os"
	"strings"
	"testing"
)

// baseGPURigYAML is a minimal external-only cluster with a BNK ng (idx0)
// and a GPU ng (idx1). Used by multiple tests below.
const baseGPURigYAML = `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: ai-rig
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs:
    - ap-southeast-2a
    - ap-southeast-2c
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
      - cidr: 10.0.3.0/24
        az: ap-southeast-2c
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
      - cidr: 10.0.13.0/24
        az: ap-southeast-2c
  natGateways: 1
  dataPath:
    external:
      cidr: 10.0.10.0/24
      az: ap-southeast-2a
pattern: external-only
cluster:
  nodeGroups:
    - name: bnk
      instanceType: m6i.4xlarge
      desiredSize: 3
      minSize: 3
      maxSize: 4
      diskSize: 50
    - name: gpu
      gpu: true
      instanceType: g5.2xlarge
      capacityType: spot
      desiredSize: 1
      minSize: 1
      maxSize: 2
      diskSize: 50
      azs:
        - ap-southeast-2a
        - ap-southeast-2c
      taints:
        - key: nvidia.com/gpu
          value: "present"
          effect: NoSchedule
`

// TestGPUNodeGroup_ParsesFields verifies that GPU, CapacityType, Taints, AZs
// fields round-trip through Load.
func TestGPUNodeGroup_ParsesFields(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", baseGPURigYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(c.ClusterSpec.NodeGroups) != 2 {
		t.Fatalf("expected 2 node groups, got %d", len(c.ClusterSpec.NodeGroups))
	}

	gpu := c.ClusterSpec.NodeGroups[1]
	if !gpu.GPU {
		t.Error("NodeGroups[1].GPU = false, want true")
	}
	if gpu.CapacityType != "spot" {
		t.Errorf("CapacityType = %q, want %q", gpu.CapacityType, "spot")
	}
	if len(gpu.Taints) != 1 {
		t.Fatalf("Taints len = %d, want 1", len(gpu.Taints))
	}
	if gpu.Taints[0].Key != "nvidia.com/gpu" || gpu.Taints[0].Effect != "NoSchedule" {
		t.Errorf("Taint = %+v, want key=nvidia.com/gpu effect=NoSchedule", gpu.Taints[0])
	}
	if len(gpu.AZs) != 2 {
		t.Errorf("AZs len = %d, want 2", len(gpu.AZs))
	}
}

// TestIsGPU verifies the helper method.
func TestIsGPU(t *testing.T) {
	if (NodeGroupSpec{GPU: true}).IsGPU() != true {
		t.Error("IsGPU() on GPU=true returned false")
	}
	if (NodeGroupSpec{GPU: false}).IsGPU() != false {
		t.Error("IsGPU() on GPU=false returned true")
	}
	if (NodeGroupSpec{}).IsGPU() != false {
		t.Error("IsGPU() on zero-value returned true")
	}
}

// TestHasGPUNodeGroup verifies the cluster-level helper.
func TestHasGPUNodeGroup(t *testing.T) {
	dir := t.TempDir()

	// With GPU ng.
	p := writeFile(t, dir, "gpu.yaml", baseGPURigYAML)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load gpu: %v", err)
	}
	if !c.HasGPUNodeGroup() {
		t.Error("HasGPUNodeGroup() = false, want true for GPU rig")
	}

	// Without GPU ng.
	p2 := writeFile(t, dir, "no-gpu.yaml", minimalYAML)
	c2, err := Load(p2)
	if err != nil {
		t.Fatalf("Load no-gpu: %v", err)
	}
	if c2.HasGPUNodeGroup() {
		t.Error("HasGPUNodeGroup() = true, want false for no-GPU cluster")
	}
}

// TestGPUNodeGroup_CapacityTypeDefault verifies that a GPU ng without explicit
// capacityType gets the "on-demand" default after Load (applyDefaults).
func TestGPUNodeGroup_CapacityTypeDefault(t *testing.T) {
	yaml := baseGPURigYAML
	// Remove capacityType from the GPU ng to test the default.
	yaml = strings.ReplaceAll(yaml, "      capacityType: spot\n", "")

	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gpu := c.ClusterSpec.NodeGroups[1]
	if gpu.CapacityType != "on-demand" {
		t.Errorf("default CapacityType = %q, want %q", gpu.CapacityType, "on-demand")
	}
}

// TestGPUNodeGroup_RoleNotInjected verifies that role=bnk is NOT injected into
// the GPU node group even when it is at index 0 (defensive guard).
func TestGPUNodeGroup_RoleNotInjected(t *testing.T) {
	// Cluster where GPU ng is NodeGroups[0] — the defensive guard must fire.
	gpuFirstYAML := `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: gpu-first
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs:
    - ap-southeast-2a
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
  natGateways: 1
cluster:
  nodeGroups:
    - name: gpu
      gpu: true
      instanceType: g5.2xlarge
      desiredSize: 1
      minSize: 1
      maxSize: 1
      diskSize: 50
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", gpuFirstYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ng0 := c.ClusterSpec.NodeGroups[0]
	if ng0.Labels != nil {
		if role, ok := ng0.Labels["role"]; ok && role == "bnk" {
			t.Error("role=bnk injected onto GPU node group — must NOT happen")
		}
	}
}

// TestGPUNodeGroup_AZWithoutG5Rejected verifies that a GPU ng pinned to
// ap-southeast-2b (known gap for g5) is rejected by validation.
func TestGPUNodeGroup_AZWithoutG5Rejected(t *testing.T) {
	bad := strings.ReplaceAll(
		baseGPURigYAML,
		// Replace the AZs in the GPU ng with 2b (which has no g5).
		`      azs:
        - ap-southeast-2a
        - ap-southeast-2c`,
		`      azs:
        - ap-southeast-2b`,
	)
	// Also add 2b to the cluster AZs so it doesn't fail on "not in network.azs".
	bad = strings.ReplaceAll(bad,
		"    - ap-southeast-2c",
		"    - ap-southeast-2c\n    - ap-southeast-2b",
	)
	// And add matching subnets.
	bad = strings.ReplaceAll(bad,
		`      - cidr: 10.0.3.0/24
        az: ap-southeast-2c`,
		`      - cidr: 10.0.3.0/24
        az: ap-southeast-2c
      - cidr: 10.0.5.0/24
        az: ap-southeast-2b`,
	)
	bad = strings.ReplaceAll(bad,
		`      - cidr: 10.0.13.0/24
        az: ap-southeast-2c`,
		`      - cidr: 10.0.13.0/24
        az: ap-southeast-2c
      - cidr: 10.0.15.0/24
        az: ap-southeast-2b`,
	)

	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", bad)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for GPU ng pinned to AZ without g5, got nil")
	}
	if !strings.Contains(err.Error(), "ap-southeast-2b") {
		t.Errorf("error %q should mention the denied AZ ap-southeast-2b", err.Error())
	}
	if !strings.Contains(err.Error(), "g5/GPU") {
		t.Errorf("error %q should mention g5/GPU capacity", err.Error())
	}
}

// TestGPUNodeGroup_AZNotInNetworkAZsRejected verifies AZ membership check.
func TestGPUNodeGroup_AZNotInNetworkAZsRejected(t *testing.T) {
	bad := strings.ReplaceAll(
		baseGPURigYAML,
		`        - ap-southeast-2c`,
		`        - ap-southeast-2z`,
	)

	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", bad)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for GPU ng AZ not in network.azs, got nil")
	}
	if !strings.Contains(err.Error(), "not in network.azs") {
		t.Errorf("error %q should mention 'not in network.azs'", err.Error())
	}
}

// TestGPUNodeGroup_AZDenyEnvOverride verifies AWSBNKCTL_GPU_AZ_DENY override.
func TestGPUNodeGroup_AZDenyEnvOverride(t *testing.T) {
	// Normally ap-southeast-2b is denied; override the deny list to empty for
	// ap-southeast-2 so the cluster with 2b becomes valid.
	badWithOverride := strings.ReplaceAll(
		baseGPURigYAML,
		`      azs:
        - ap-southeast-2a
        - ap-southeast-2c`,
		`      azs:
        - ap-southeast-2a`,
	)
	// Use a different override entirely: deny 2a instead, so the cluster FAILS
	// even though the static table doesn't deny 2a.
	t.Setenv("AWSBNKCTL_GPU_AZ_DENY", "ap-southeast-2:ap-southeast-2a")

	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", badWithOverride)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error when env override denies 2a, got nil")
	}
	if !strings.Contains(err.Error(), "ap-southeast-2a") {
		t.Errorf("error %q should mention denied AZ ap-southeast-2a", err.Error())
	}
}

// TestGPUNodeGroup_DSSMExempt verifies that a GPU ng with desiredSize=1 passes
// the dSSM quorum validation in a BNK pattern (non-GPU ng must still satisfy it).
func TestGPUNodeGroup_DSSMExempt(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", baseGPURigYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with GPU ng desiredSize=1 should pass dSSM: %v", err)
	}

	// GPU ng must still be desiredSize=1.
	gpuNG := c.ClusterSpec.NodeGroups[1]
	if gpuNG.DesiredSize != 1 {
		t.Errorf("GPU ng DesiredSize = %d, want 1 (not bumped by dSSM logic)", gpuNG.DesiredSize)
	}
}

// TestParseGPUAZDenyEnv verifies the env override parser.
func TestParseGPUAZDenyEnv(t *testing.T) {
	table := parseGPUAZDenyEnv("ap-southeast-2:ap-southeast-2b,ap-southeast-2c;us-east-1:us-east-1d")
	if len(table["ap-southeast-2"]) != 2 {
		t.Errorf("ap-southeast-2 deny len = %d, want 2", len(table["ap-southeast-2"]))
	}
	if len(table["us-east-1"]) != 1 || table["us-east-1"][0] != "us-east-1d" {
		t.Errorf("us-east-1 deny = %v, want [us-east-1d]", table["us-east-1"])
	}
	// Malformed entry: silently skipped.
	table2 := parseGPUAZDenyEnv("nocolon")
	if len(table2) != 0 {
		t.Errorf("malformed entry should produce empty table, got %v", table2)
	}
}

// TestGPUNodeGroup_BackwardCompat verifies that loading existing cluster.yaml
// files (no GPU fields) still works unchanged.
func TestGPUNodeGroup_BackwardCompat(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", minimalYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load minimal YAML (no GPU fields): %v", err)
	}
	if c.HasGPUNodeGroup() {
		t.Error("HasGPUNodeGroup() = true for cluster without GPU ng")
	}
}

// TestGPUNodeGroup_UnknownFieldsRejected verifies strict YAML decoding still works.
func TestGPUNodeGroup_UnknownFieldsRejected(t *testing.T) {
	bad := minimalYAML + `
cluster:
  nodeGroups:
    - name: default
      unknownField: should-fail
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", bad)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected strict decode error for unknown field, got nil")
	}
}

// TestGPUNodeGroup_InstanceTypeDefault verifies that GPU ng gets g5.2xlarge default.
func TestGPUNodeGroup_InstanceTypeDefault(t *testing.T) {
	gpuNoInstanceType := strings.ReplaceAll(baseGPURigYAML, "      instanceType: g5.2xlarge\n", "")

	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", gpuNoInstanceType)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gpu := c.ClusterSpec.NodeGroups[1]
	if gpu.InstanceType != "g5.2xlarge" {
		t.Errorf("GPU ng default InstanceType = %q, want g5.2xlarge", gpu.InstanceType)
	}
}

// TestGPUInstanceAZDenyTable verifies the static table has the Sydney entry.
func TestGPUInstanceAZDenyTable(t *testing.T) {
	denied, ok := gpuInstanceAZDeny["ap-southeast-2"]
	if !ok {
		t.Fatal("gpuInstanceAZDeny missing ap-southeast-2")
	}
	found := false
	for _, az := range denied {
		if az == "ap-southeast-2b" {
			found = true
		}
	}
	if !found {
		t.Errorf("gpuInstanceAZDeny[ap-southeast-2] = %v, expected to contain ap-southeast-2b", denied)
	}
}

// TestGPUAZDenyEnvEmpty verifies that setting AWSBNKCTL_GPU_AZ_DENY to empty
// string does not crash (parses to empty table, which causes fail-open behavior).
func TestGPUAZDenyEnvEmpty(t *testing.T) {
	t.Setenv("AWSBNKCTL_GPU_AZ_DENY", "")

	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", baseGPURigYAML)

	// With empty override, validation uses the empty override table (fail-open).
	// The cluster previously valid (2a + 2c are good AZs) stays valid.
	_, err := Load(p)
	// Empty env means deny table is now empty (everything allowed).
	if err != nil {
		t.Fatalf("empty AWSBNKCTL_GPU_AZ_DENY should not cause errors: %v", err)
	}
}

// TestNodeGroupSpec_ZeroValueIsNonGPU ensures the zero value is safe.
func TestNodeGroupSpec_ZeroValueIsNonGPU(t *testing.T) {
	var ng NodeGroupSpec
	if ng.IsGPU() {
		t.Error("zero-value NodeGroupSpec.IsGPU() = true, want false")
	}
}

// TestHasGPUNodeGroup_NilClusterSpec confirms nil safety.
func TestHasGPUNodeGroup_NilClusterSpec(t *testing.T) {
	c := &Cluster{}
	if c.HasGPUNodeGroup() {
		t.Error("HasGPUNodeGroup() = true for Cluster with nil ClusterSpec")
	}
}

// TestGPURig_ExampleLoads verifies that examples/ai-rig/cluster.yaml (Group 5)
// loads cleanly and declares the expected GPU nodegroup shape.
func TestGPURig_ExampleLoads(t *testing.T) {
	c, err := Load("../../examples/ai-rig/cluster.yaml")
	if err != nil {
		t.Fatalf("Load examples/ai-rig/cluster.yaml: %v", err)
	}
	if c.ClusterSpec == nil || len(c.ClusterSpec.NodeGroups) != 2 {
		t.Fatalf("expected 2 nodegroups, got %v", c.ClusterSpec)
	}
	// Index 0: BNK ng.
	bnk := c.ClusterSpec.NodeGroups[0]
	if bnk.IsGPU() {
		t.Error("nodeGroups[0] (bnk) must not be a GPU nodegroup")
	}
	// Index 1: GPU ng.
	gpu := c.ClusterSpec.NodeGroups[1]
	if !gpu.IsGPU() {
		t.Error("nodeGroups[1] (gpu) must be a GPU nodegroup (gpu: true)")
	}
	if gpu.CapacityType != "spot" {
		t.Errorf("GPU ng capacityType = %q, want spot", gpu.CapacityType)
	}
	if gpu.InstanceType != "g5.2xlarge" {
		t.Errorf("GPU ng instanceType = %q, want g5.2xlarge", gpu.InstanceType)
	}
	if len(gpu.AZs) != 2 {
		t.Errorf("GPU ng AZs len = %d, want 2 (ap-southeast-2a, ap-southeast-2c)", len(gpu.AZs))
	}
	if len(gpu.Taints) == 0 {
		t.Error("GPU ng must declare taints (nvidia.com/gpu)")
	}
	// HasGPUNodeGroup must return true.
	if !c.HasGPUNodeGroup() {
		t.Error("HasGPUNodeGroup() = false for ai-rig example")
	}
}

// TestGPURig_Example2bRejected verifies the AZ validation: pinning the GPU
// nodegroup to ap-southeast-2b (g5 gap) must be rejected.
func TestGPURig_Example2bRejected(t *testing.T) {
	// Build a cluster equivalent to the example but with 2b added to network.azs
	// and the GPU ng pinned exclusively to 2b.
	bad := baseGPURigYAML
	// Replace GPU ng AZs with 2b only.
	bad = strings.ReplaceAll(bad,
		`      azs:
        - ap-southeast-2a
        - ap-southeast-2c`,
		`      azs:
        - ap-southeast-2b`,
	)
	// Add 2b to the cluster network AZs and subnets so the "not in network.azs"
	// check doesn't fire before the g5 gap check.
	bad = strings.ReplaceAll(bad, "    - ap-southeast-2c\n", "    - ap-southeast-2c\n    - ap-southeast-2b\n")
	bad = strings.ReplaceAll(bad,
		`      - cidr: 10.0.3.0/24
        az: ap-southeast-2c`,
		`      - cidr: 10.0.3.0/24
        az: ap-southeast-2c
      - cidr: 10.0.5.0/24
        az: ap-southeast-2b`,
	)
	bad = strings.ReplaceAll(bad,
		`      - cidr: 10.0.13.0/24
        az: ap-southeast-2c`,
		`      - cidr: 10.0.13.0/24
        az: ap-southeast-2c
      - cidr: 10.0.15.0/24
        az: ap-southeast-2b`,
	)

	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", bad)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for GPU ng pinned to ap-southeast-2b, got nil")
	}
	if !strings.Contains(err.Error(), "ap-southeast-2b") {
		t.Errorf("error %q must mention the denied AZ ap-southeast-2b", err.Error())
	}
}

// TestAllExamplesContinueToLoad ensures backward compatibility — all examples/
// cluster.yaml files that existed before this feature must still load cleanly.
func TestAllExamplesContinueToLoad(t *testing.T) {
	examples := []string{
		"../../examples/external-only/cluster.yaml",
		"../../examples/sriov-external/cluster.yaml",
		"../../examples/tracer/cluster.yaml",
		"../../examples/demo/cluster.yaml",
		"../../examples/full-cluster/cluster.yaml",
	}
	for _, path := range examples {
		t.Run(path, func(t *testing.T) {
			_, err := Load(path)
			if err != nil {
				t.Errorf("Load(%s): %v", path, err)
			}
		})
	}
}

// Verify os.Getenv dependency is not needed at the package level.
var _ = os.Getenv
