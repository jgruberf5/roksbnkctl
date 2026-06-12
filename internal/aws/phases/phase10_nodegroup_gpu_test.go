package phases

import (
	"context"
	"strings"
	"testing"

	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// gpuRigCluster returns a 2-nodegroup cluster:
// - NodeGroups[0]: BNK ng (not GPU, instance m6i.4xlarge, desiredSize 3)
// - NodeGroups[1]: GPU ng (gpu=true, g5.2xlarge, spot, AZs 2a+2c, tainted)
// The cluster has 2 public subnets: 2a + 2c (no 2b).
// This matches the PRD-11 ai-rig shape.
func gpuRigCluster() *intent.Cluster {
	return &intent.Cluster{
		Metadata: intent.Metadata{Name: "ai-rig", Region: "ap-southeast-2"},
		Network: intent.Network{
			VPCCidr: "10.0.0.0/16",
			AZs:     []string{"ap-southeast-2a", "ap-southeast-2c"},
			Subnets: intent.Subnets{
				Public: []intent.SubnetSpec{
					{CIDR: "10.0.1.0/24", AZ: "ap-southeast-2a"},
					{CIDR: "10.0.3.0/24", AZ: "ap-southeast-2c"},
				},
				Private: []intent.SubnetSpec{
					{CIDR: "10.0.11.0/24", AZ: "ap-southeast-2a"},
					{CIDR: "10.0.13.0/24", AZ: "ap-southeast-2c"},
				},
			},
			NatGateways: 1,
			DataPath: &intent.DataPathSpec{
				External: intent.SubnetSpec{CIDR: "10.0.10.0/24", AZ: "ap-southeast-2a"},
			},
		},
		Pattern: intent.PatternExternalOnly,
		ClusterSpec: &intent.ClusterSpec{
			KubernetesVersion: "1.30",
			NodeGroups: []intent.NodeGroupSpec{
				{
					// BNK ng at index 0 — must use AL2023_x86_64_STANDARD + BNK LT.
					Name:         "bnk",
					InstanceType: "m6i.4xlarge",
					DesiredSize:  3,
					MinSize:      3,
					MaxSize:      4,
					DiskSize:     50,
					CapacityType: "on-demand",
				},
				{
					// GPU ng at index 1 — must use AL2023_x86_64_NVIDIA + no BNK LT.
					Name:         "gpu",
					GPU:          true,
					InstanceType: "g5.2xlarge",
					DesiredSize:  1,
					MinSize:      1,
					MaxSize:      2,
					DiskSize:     50,
					CapacityType: "spot",
					AZs:          []string{"ap-southeast-2a", "ap-southeast-2c"},
					Taints: []intent.NodeTaintSpec{
						{Key: "nvidia.com/gpu", Value: "present", Effect: "NoSchedule"},
					},
				},
			},
		},
	}
}

// stateForGPURig sets up state for the 2-subnet (2a+2c) GPU rig cluster.
func stateForGPURig(t *testing.T) *state.State {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	st.Set("EKS_CLUSTER_NAME", "ai-rig")
	st.Set("EKS_NODE_ROLE_ARN", "arn:aws:iam::111122223333:role/ai-rig-node-role")
	// Two public subnets: 2a + 2c (matching gpuRigCluster's public specs).
	st.Set("PUBLIC_SUBNETS", "subnet-2a,subnet-2c")
	st.Set("PRIVATE_SUBNETS", "subnet-priv-2a,subnet-priv-2c")
	return st
}

// TestPhase10NodeGroup_GPU_AmiTypeSelection verifies:
// - BNK ng gets AMITypesAl2023X8664Standard.
// - GPU ng gets AMITypesAl2023X8664Nvidia.
// This is the core BNK-path-untouched guard (R1).
func TestPhase10NodeGroup_GPU_AmiTypeSelection(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	st := stateForGPURig(t)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	if eksMock.createNodegroupCalls != 2 {
		t.Errorf("createNodegroupCalls = %d, want 2", eksMock.createNodegroupCalls)
	}

	bnkNGName := "ai-rig-ng-bnk"
	gpuNGName := "ai-rig-ng-gpu"

	bnkNG := eksMock.nodegroups["ai-rig"][bnkNGName]
	if bnkNG == nil {
		t.Fatalf("BNK node group %s not found in mock", bnkNGName)
	}
	gpuNG := eksMock.nodegroups["ai-rig"][gpuNGName]
	if gpuNG == nil {
		t.Fatalf("GPU node group %s not found in mock", gpuNGName)
	}

	// BNK ng: AL2023_x86_64_STANDARD (R1: byte-for-byte untouched BNK path).
	if bnkNG.AmiType != ekstypes.AMITypesAl2023X8664Standard {
		t.Errorf("BNK ng AmiType = %v, want AL2023_x86_64_STANDARD", bnkNG.AmiType)
	}
	// GPU ng: AL2023_x86_64_NVIDIA.
	if gpuNG.AmiType != ekstypes.AMITypesAl2023X8664Nvidia {
		t.Errorf("GPU ng AmiType = %v, want AL2023_x86_64_NVIDIA", gpuNG.AmiType)
	}
}

// TestPhase10NodeGroup_GPU_NoLaunchTemplate verifies the MUST-CARRY guard R6:
// GPU node groups must have LaunchTemplate == nil in CreateNodegroup input.
// BNK node groups must have a LaunchTemplate set.
func TestPhase10NodeGroup_GPU_NoLaunchTemplate(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	st := stateForGPURig(t)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	bnkNGName := "ai-rig-ng-bnk"
	gpuNGName := "ai-rig-ng-gpu"

	bnkNG := eksMock.nodegroups["ai-rig"][bnkNGName]
	gpuNG := eksMock.nodegroups["ai-rig"][gpuNGName]

	// BNK ng MUST have a launch template (ENA udev rules).
	if bnkNG.LaunchTemplate == nil {
		t.Error("BNK ng LaunchTemplate = nil, want non-nil (BNK ENA udev rules required)")
	}

	// GPU ng MUST NOT have the BNK launch template (R6: incompatible with NVIDIA AMI).
	if gpuNG.LaunchTemplate != nil {
		t.Errorf("GPU ng LaunchTemplate = %+v, want nil (GPU nodes must not carry BNK LT)", gpuNG.LaunchTemplate)
	}
}

// TestPhase10NodeGroup_GPU_CapacityTypeSpot verifies spot CapacityType propagation.
func TestPhase10NodeGroup_GPU_CapacityTypeSpot(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	st := stateForGPURig(t)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	gpuNGName := "ai-rig-ng-gpu"
	gpuNG := eksMock.nodegroups["ai-rig"][gpuNGName]
	if gpuNG == nil {
		t.Fatalf("GPU ng not found")
	}

	if gpuNG.CapacityType != ekstypes.CapacityTypesSpot {
		t.Errorf("GPU ng CapacityType = %v, want SPOT", gpuNG.CapacityType)
	}

	// BNK ng should be ON_DEMAND (default).
	bnkNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-bnk"]
	if bnkNG.CapacityType != ekstypes.CapacityTypesOnDemand {
		t.Errorf("BNK ng CapacityType = %v, want ON_DEMAND", bnkNG.CapacityType)
	}
}

// TestPhase10NodeGroup_GPU_TaintsPresent verifies taint propagation to EKS.
func TestPhase10NodeGroup_GPU_TaintsPresent(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	st := stateForGPURig(t)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	gpuNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-gpu"]
	if gpuNG == nil {
		t.Fatal("GPU ng not found")
	}

	if len(gpuNG.Taints) != 1 {
		t.Fatalf("GPU ng Taints len = %d, want 1", len(gpuNG.Taints))
	}
	taint := gpuNG.Taints[0]
	if taint.Key == nil || *taint.Key != "nvidia.com/gpu" {
		t.Errorf("Taint.Key = %v, want nvidia.com/gpu", taint.Key)
	}
	if taint.Effect != ekstypes.TaintEffectNoSchedule {
		t.Errorf("Taint.Effect = %v, want NO_SCHEDULE", taint.Effect)
	}
}

// TestPhase10NodeGroup_GPU_SubnetFilteredByAZ verifies that the GPU ng's subnets
// are filtered to the declared AZs (2a+2c), not all public subnets.
func TestPhase10NodeGroup_GPU_SubnetFilteredByAZ(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	st := stateForGPURig(t)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	gpuNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-gpu"]
	if gpuNG == nil {
		t.Fatal("GPU ng not found")
	}

	// Both 2a and 2c subnets should be present (GPU ng declared both AZs).
	if len(gpuNG.Subnets) != 2 {
		t.Errorf("GPU ng Subnets len = %d, want 2 (2a+2c)", len(gpuNG.Subnets))
	}
}

// TestPhase10NodeGroup_GPU_LabelPresent verifies awsbnkctl.io/gpu=true label.
func TestPhase10NodeGroup_GPU_LabelPresent(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	st := stateForGPURig(t)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	gpuNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-gpu"]
	if gpuNG == nil {
		t.Fatal("GPU ng not found")
	}

	if gpuNG.Labels["awsbnkctl.io/gpu"] != "true" {
		t.Errorf("GPU ng missing label awsbnkctl.io/gpu=true, got %v", gpuNG.Labels)
	}

	// BNK ng must NOT have the GPU label.
	bnkNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-bnk"]
	if bnkNG.Labels["awsbnkctl.io/gpu"] == "true" {
		t.Error("BNK ng has awsbnkctl.io/gpu=true label — must not")
	}
}

// TestPhase10NodeGroup_GPU_BNKSubnetUnchanged verifies the BNK ng still gets the
// BNK AZ-pinned subnet (not both public subnets), even when a GPU ng is present.
func TestPhase10NodeGroup_GPU_BNKSubnetUnchanged(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	st := stateForGPURig(t)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	bnkNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-bnk"]
	if bnkNG == nil {
		t.Fatal("BNK ng not found")
	}

	// BNK ng is pinned to 2a (data-path AZ) — should have exactly 1 subnet.
	if len(bnkNG.Subnets) != 1 {
		t.Errorf("BNK ng Subnets len = %d, want 1 (AZ-pinned to 2a)", len(bnkNG.Subnets))
	}
}

// TestPhase10NodeGroup_GPU_DiskSizePropagated is the Fix 1 test (blocking defect).
// Verifies that the GPU ng's DiskSize is passed to CreateNodegroupInput when
// no launch template is used. The example sets diskSize: 100.
func TestPhase10NodeGroup_GPU_DiskSizePropagated(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	// Set DiskSize explicitly to 100 on the GPU ng (matches the example).
	cl.ClusterSpec.NodeGroups[1].DiskSize = 100
	st := stateForGPURig(t)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	gpuNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-gpu"]
	if gpuNG == nil {
		t.Fatal("GPU ng not found")
	}

	// GPU ng must have DiskSize set (no launch template → EKS accepts it).
	if gpuNG.DiskSize == nil {
		t.Fatal("GPU ng DiskSize = nil, want non-nil (*int32 = 100)")
	}
	if *gpuNG.DiskSize != 100 {
		t.Errorf("GPU ng DiskSize = %d, want 100", *gpuNG.DiskSize)
	}

	// BNK ng must NOT have DiskSize set (uses launch template; EKS rejects the combo).
	bnkNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-bnk"]
	if bnkNG == nil {
		t.Fatal("BNK ng not found")
	}
	if bnkNG.DiskSize != nil {
		t.Errorf("BNK ng DiskSize = %d, want nil (LT + DiskSize incompatible)", *bnkNG.DiskSize)
	}
}

// TestPhase10NodeGroup_GPU_EmptyAZFilter_Errors verifies Fix 2:
// when GPU ng.AZs is set but no public subnet matches (impossible AZ), the
// outer loop returns an error rather than silently falling back to all subnets.
func TestPhase10NodeGroup_GPU_EmptyAZFilter_Errors(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	// Pin to an AZ that has no subnet in the fixture (ap-southeast-2z = nonexistent).
	cl.ClusterSpec.NodeGroups[1].AZs = []string{"ap-southeast-2z"}
	st := stateForGPURig(t)
	eksMock := newMockEKS()

	err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false)
	if err == nil {
		t.Fatal("expected error when GPU ng AZ filter yields empty subnets, got nil")
	}
	if !strings.Contains(err.Error(), "no public subnets match") {
		t.Errorf("error %q should mention 'no public subnets match'", err.Error())
	}
}
