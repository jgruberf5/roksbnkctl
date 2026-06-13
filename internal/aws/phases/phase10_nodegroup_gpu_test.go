package phases

import (
	"context"
	"strings"
	"testing"

	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

// TestPhase10NodeGroup_GPU_CarriesGPULT verifies the MUST-CARRY guard R6:
// GPU node groups must carry the GPU LT (not the BNK LT).
//   - BNK ng: LaunchTemplate non-nil (ENA udev rules required).
//   - GPU ng: LaunchTemplate non-nil AND its Id != BNK ng's LaunchTemplate Id
//     (GPU uses the dedicated GPU LT, not the BNK LT).
func TestPhase10NodeGroup_GPU_CarriesGPULT(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	st := stateForGPURig(t)
	eksMock := newMockEKS()
	ec2Mock := &mockEC2{}

	clients := &Clients{
		EC2:     ec2Mock,
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     newMockIAM(),
		EKS:     eksMock,
		Profile: "test",
	}
	if err := Phase10NodeGroup(context.Background(), cl, st, clients, false); err != nil {
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

	// GPU ng MUST have a launch template (GPU LT for IMDS hop-limit + disk size).
	if gpuNG.LaunchTemplate == nil {
		t.Fatal("GPU ng LaunchTemplate = nil, want non-nil (GPU LT required for IMDS + disk)")
	}

	// GPU ng MUST NOT carry the BNK LT (R6: incompatible with NVIDIA AMI).
	// Two CreateLaunchTemplate calls are made: call 1 = BNK LT (lt-mock-1),
	// call 2 = GPU LT (lt-mock-2). Their IDs must differ.
	bnkLTID := ""
	if bnkNG.LaunchTemplate.Id != nil {
		bnkLTID = *bnkNG.LaunchTemplate.Id
	}
	gpuLTID := ""
	if gpuNG.LaunchTemplate.Id != nil {
		gpuLTID = *gpuNG.LaunchTemplate.Id
	}
	if gpuLTID == "" {
		t.Fatal("GPU ng LaunchTemplate.Id = nil, want non-empty")
	}
	if gpuLTID == bnkLTID {
		t.Errorf("GPU ng LaunchTemplate.Id = %q == BNK ng LaunchTemplate.Id: GPU must use the GPU LT, not the BNK LT", gpuLTID)
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

// TestPhase10NodeGroup_GPU_SubnetFilteredByAZ verifies that the GPU ng is created
// in one AZ at a time (AZ-sweep). The mock returns ACTIVE immediately, so the first
// candidate AZ (2a → subnet-2a) wins and the nodegroup is created with exactly one
// subnet — not both 2a+2c. The key assertion is that the subnet is from the declared
// AZ set and NOT subnet-2b (which has no g5 capacity).
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

	// AZ-sweep: nodegroup is created in exactly one AZ per attempt.
	// The mock returns ACTIVE immediately, so the first candidate AZ (2a) wins.
	if len(gpuNG.Subnets) != 1 {
		t.Errorf("GPU ng Subnets len = %d, want 1 (AZ-sweep pins to one AZ per attempt)", len(gpuNG.Subnets))
	}
	// The subnet must be from the declared AZ list (subnet-2a or subnet-2c, not subnet-2b).
	subnet := gpuNG.Subnets[0]
	if subnet != "subnet-2a" && subnet != "subnet-2c" {
		t.Errorf("GPU ng Subnet = %q, want one of [subnet-2a, subnet-2c] (declared AZs with g5 capacity)", subnet)
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

// TestPhase10NodeGroup_GPU_DiskSizePropagated verifies the GPU disk-size fix.
// Under the LT path, CreateNodegroupInput.DiskSize must be nil for GPU nodes
// (EKS rejects LT + DiskSize together). Instead, the GPU LT's BlockDeviceMapping
// carries the disk size so GPU nodes get a large-enough root volume for vLLM.
// The example sets diskSize: 100 on the GPU nodegroup.
func TestPhase10NodeGroup_GPU_DiskSizePropagated(t *testing.T) {
	awsmw.ResetForTest()
	cl := gpuRigCluster()
	// Set DiskSize explicitly to 100 on the GPU ng (matches the example).
	cl.ClusterSpec.NodeGroups[1].DiskSize = 100
	st := stateForGPURig(t)
	eksMock := newMockEKS()
	ec2Mock := &mockEC2{}

	clients := &Clients{
		EC2:     ec2Mock,
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     newMockIAM(),
		EKS:     eksMock,
		Profile: "test",
	}
	if err := Phase10NodeGroup(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	gpuNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-gpu"]
	if gpuNG == nil {
		t.Fatal("GPU ng not found")
	}

	// GPU ng MUST NOT have DiskSize set on CreateNodegroupInput (LT + DiskSize rejected by EKS).
	if gpuNG.DiskSize != nil {
		t.Errorf("GPU ng DiskSize = %d, want nil (LT path: disk size lives in GPU LT BlockDeviceMapping)", *gpuNG.DiskSize)
	}

	// BNK ng must NOT have DiskSize set (uses BNK launch template; same EKS constraint).
	bnkNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-bnk"]
	if bnkNG == nil {
		t.Fatal("BNK ng not found")
	}
	if bnkNG.DiskSize != nil {
		t.Errorf("BNK ng DiskSize = %d, want nil (LT + DiskSize incompatible)", *bnkNG.DiskSize)
	}

	// The GPU LT (second CreateLaunchTemplate call = index 1) must carry a
	// BlockDeviceMapping for /dev/xvda with VolumeSize=100.
	if len(ec2Mock.createLTInputs) < 2 {
		t.Fatalf("expected ≥2 CreateLaunchTemplate calls (BNK LT + GPU LT), got %d", len(ec2Mock.createLTInputs))
	}
	gpuLTInput := ec2Mock.createLTInputs[1]
	if gpuLTInput.LaunchTemplateData == nil {
		t.Fatal("GPU LT input: LaunchTemplateData = nil")
	}
	bdms := gpuLTInput.LaunchTemplateData.BlockDeviceMappings
	if len(bdms) == 0 {
		t.Fatal("GPU LT input: BlockDeviceMappings is empty, want /dev/xvda with VolumeSize=100")
	}
	bdm := bdms[0]
	if bdm.DeviceName == nil || *bdm.DeviceName != "/dev/xvda" {
		t.Errorf("GPU LT BlockDeviceMapping DeviceName = %v, want /dev/xvda", bdm.DeviceName)
	}
	if bdm.Ebs == nil {
		t.Fatal("GPU LT BlockDeviceMapping Ebs = nil")
	}
	if bdm.Ebs.VolumeSize == nil {
		t.Fatal("GPU LT BlockDeviceMapping Ebs.VolumeSize = nil, want 100")
	}
	if *bdm.Ebs.VolumeSize != 100 {
		t.Errorf("GPU LT BlockDeviceMapping Ebs.VolumeSize = %d, want 100", *bdm.Ebs.VolumeSize)
	}
	if bdm.Ebs.VolumeType != ec2types.VolumeTypeGp3 {
		t.Errorf("GPU LT BlockDeviceMapping Ebs.VolumeType = %v, want gp3", bdm.Ebs.VolumeType)
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

// --- AZ-sweep acceptance-criteria tests ---

// clientsWithEKSAndAS builds a Clients fixture with both EKS and AutoScaling mocks.
func clientsWithEKSAndAS(eksMock *mockEKS, asMock *mockAutoScaling) *Clients {
	return &Clients{
		EC2:         &mockEC2{},
		STS:         &mockSTSImpl{accountID: "111122223333"},
		IAM:         newMockIAM(),
		EKS:         eksMock,
		AutoScaling: asMock,
		Profile:     "test",
	}
}

// TestPhase10_GPUAZSweep_CapacityFailAZ1_SuccessAZ2 is Acceptance Criterion 1:
// capacity failure in AZ1 (ap-southeast-2a) → auto-delete → ACTIVE in AZ2
// (ap-southeast-2c). Asserts:
//   - AZ2 nodegroup is created with AZ2 subnet (subnet-2c).
//   - AZ1 nodegroup is deleted (deleteNodegroupCalls == 1).
//   - State keys reflect AZ2 outcome.
func TestPhase10_GPUAZSweep_CapacityFailAZ1_SuccessAZ2(t *testing.T) {
	awsmw.ResetForTest()

	// Cluster: GPU ng has AZs [2a, 2c]; 2 public subnets.
	cl := gpuRigCluster()
	st := stateForGPURig(t)

	eksMock := newMockEKS()
	asMock := newMockAutoScaling()

	// AZ1 (2a): the first CreateNodegroup will store the ng as ACTIVE, but we
	// need it to first return CREATING so the ASG capacity check can fire.
	// Configure the mock to give 1 CREATING response before ACTIVE for the gpu ng.
	ngKey2a := "ai-rig/ai-rig-ng-gpu"
	eksMock.ngCreatingTicks = map[string]int{ngKey2a: 1}
	eksMock.ngASGName = map[string]string{ngKey2a: "asg-gpu-2a"}

	// ASG for 2a: capacity failure, no instances.
	asMock.addASG("asg-gpu-2a", 0, []astypes.Activity{
		mkFailedActivity("InsufficientInstanceCapacity: g5.2xlarge in ap-southeast-2a"),
	})
	asMock.addTagIndex("ai-rig-ng-gpu", "asg-gpu-2a")

	clients := clientsWithEKSAndAS(eksMock, asMock)
	ctx := context.Background()

	if err := Phase10NodeGroup(ctx, cl, st, clients, false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	// The gpu ng in the mock should be the AZ2 one (the AZ1 one was deleted and
	// a new one created in AZ2 with subnet-2c). After deletion from AZ1 and
	// re-creation in AZ2, the mock's nodegroups map has the AZ2 nodegroup.
	gpuNG := eksMock.nodegroups["ai-rig"]["ai-rig-ng-gpu"]
	if gpuNG == nil {
		t.Fatal("GPU ng not in mock after AZ sweep")
	}

	// The AZ1 attempt was deleted (deleteNodegroupCalls == 1 for the fast-fail delete).
	if eksMock.deleteNodegroupCalls < 1 {
		t.Errorf("deleteNodegroupCalls = %d, want ≥1 (AZ1 nodegroup must be deleted on capacity fail)", eksMock.deleteNodegroupCalls)
	}

	// The final nodegroup must be in AZ2 (subnet-2c), not AZ1 (subnet-2a).
	if len(gpuNG.Subnets) != 1 {
		t.Fatalf("GPU ng Subnets len = %d, want 1", len(gpuNG.Subnets))
	}
	if gpuNG.Subnets[0] != "subnet-2c" {
		t.Errorf("GPU ng Subnet = %q, want subnet-2c (AZ2 after AZ1 capacity fail)", gpuNG.Subnets[0])
	}

	// State must be populated.
	if st.Get("NODEGROUP_GPU_ARN") == "" {
		t.Error("NODEGROUP_GPU_ARN not set in state")
	}
}

// TestPhase10_GPUAZSweep_AllAZsFail_AggregatedError is Acceptance Criterion 2:
// all candidate AZs fail with capacity errors → aggregated error naming each AZ.
func TestPhase10_GPUAZSweep_AllAZsFail_AggregatedError(t *testing.T) {
	awsmw.ResetForTest()

	cl := gpuRigCluster()
	st := stateForGPURig(t)

	eksMock := newMockEKS()
	asMock := newMockAutoScaling()

	// Both AZs return CREATING first so the ASG activity check can fire.
	// After the fast-fail delete, the same ng name is re-created in the next AZ.
	// We use a high tick count so every describe returns CREATING until we break.
	eksMock.ngCreatingTicks = map[string]int{
		"ai-rig/ai-rig-ng-gpu": 100,
	}
	eksMock.ngASGName = map[string]string{
		"ai-rig/ai-rig-ng-gpu": "asg-gpu-fail",
	}

	// ASG for both AZs: capacity failure, no instances.
	asMock.addASG("asg-gpu-fail", 0, []astypes.Activity{
		mkFailedActivity("UnfulfillableCapacity: g5.2xlarge spot"),
	})
	asMock.addTagIndex("ai-rig-ng-gpu", "asg-gpu-fail")

	clients := clientsWithEKSAndAS(eksMock, asMock)

	err := Phase10NodeGroup(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected aggregated error when all AZs fail, got nil")
	}

	// Error must mention both AZs.
	if !strings.Contains(err.Error(), "ap-southeast-2a") {
		t.Errorf("error %q should mention ap-southeast-2a", err.Error())
	}
	if !strings.Contains(err.Error(), "ap-southeast-2c") {
		t.Errorf("error %q should mention ap-southeast-2c", err.Error())
	}
	// Error must mention the capacity message.
	if !strings.Contains(err.Error(), "UnfulfillableCapacity") {
		t.Errorf("error %q should mention UnfulfillableCapacity", err.Error())
	}
}

// TestPhase10_GPUAZSweep_SpotFailAllAZs_OnDemandFallback is Acceptance Criterion 3:
// spot fails all AZs + onDemandFallback=true → on-demand sweep succeeds; with
// onDemandFallback=false the spot failure is surfaced directly (no on-demand attempt).
func TestPhase10_GPUAZSweep_SpotFailAllAZs_OnDemandFallback(t *testing.T) {
	awsmw.ResetForTest()

	// Sub-test A: onDemandFallback=true → succeeds via on-demand.
	t.Run("fallback_true_succeeds", func(t *testing.T) {
		awsmw.ResetForTest()
		cl := gpuRigCluster()
		cl.ClusterSpec.NodeGroups[1].OnDemandFallback = true
		st := stateForGPURig(t)

		eksMock := newMockEKS()
		asMock := newMockAutoScaling()

		// All spot attempts return CREATING + capacity error.
		eksMock.ngCreatingTicks = map[string]int{"ai-rig/ai-rig-ng-gpu": 100}
		eksMock.ngASGName = map[string]string{"ai-rig/ai-rig-ng-gpu": "asg-spot-fail"}
		asMock.addASG("asg-spot-fail", 0, []astypes.Activity{
			mkFailedActivity("UnfulfillableCapacity: spot g5.2xlarge"),
		})
		asMock.addTagIndex("ai-rig-ng-gpu", "asg-spot-fail")

		clients := clientsWithEKSAndAS(eksMock, asMock)

		// After the spot sweep is exhausted, the on-demand sweep should succeed
		// because the mock will return ACTIVE after the ng is re-created
		// (ngCreatingTicks is not replenished for the on-demand retry).
		// Reset ngCreatingTicks so the fallback on-demand attempt returns ACTIVE.
		eksMock.ngCreatingTicks = map[string]int{"ai-rig/ai-rig-ng-gpu": 100}

		// The spot sweep will fail (both AZs). After that, the code retries with
		// on-demand. At that point ngCreatingTicks is still 100 so it would CREATING
		// again... we need to clear it for the second sweep. Use a trick: zero ticks.
		// Actually we need to model this differently: after the spot sweep exhausts,
		// the ng is deleted each time. When re-created in the fallback on-demand sweep,
		// the ngCreatingTicks key remains in the map but the node group is freshly
		// created. The key in ngCreatingTicks uses the nodegroup name, not the AZ, so
		// all attempts share the tick counter.
		//
		// To make the on-demand fallback succeed: set ticks to exactly 2*(number of spot AZs)
		// so spot attempts consume all ticks and the on-demand attempt gets ACTIVE.
		// 2 AZs × 1 CREATING per attempt = 2 ticks consumed by spot sweep.
		// After that, on-demand attempt gets ACTIVE immediately.
		eksMock.ngCreatingTicks = map[string]int{"ai-rig/ai-rig-ng-gpu": 2}

		if err := Phase10NodeGroup(context.Background(), cl, st, clients, false); err != nil {
			t.Fatalf("onDemandFallback=true: expected success via on-demand, got: %v", err)
		}

		// Verify state is populated.
		if st.Get("NODEGROUP_GPU_ARN") == "" {
			t.Error("NODEGROUP_GPU_ARN not set after on-demand fallback success")
		}
	})

	// Sub-test B: onDemandFallback=false → spot failure surfaces, no on-demand attempt.
	t.Run("fallback_false_fails", func(t *testing.T) {
		awsmw.ResetForTest()
		cl := gpuRigCluster()
		cl.ClusterSpec.NodeGroups[1].OnDemandFallback = false // explicit false
		st := stateForGPURig(t)

		eksMock := newMockEKS()
		asMock := newMockAutoScaling()

		eksMock.ngCreatingTicks = map[string]int{"ai-rig/ai-rig-ng-gpu": 100}
		eksMock.ngASGName = map[string]string{"ai-rig/ai-rig-ng-gpu": "asg-spot-fail2"}
		asMock.addASG("asg-spot-fail2", 0, []astypes.Activity{
			mkFailedActivity("InsufficientInstanceCapacity: g5.2xlarge spot"),
		})
		asMock.addTagIndex("ai-rig-ng-gpu", "asg-spot-fail2")

		clients := clientsWithEKSAndAS(eksMock, asMock)

		err := Phase10NodeGroup(context.Background(), cl, st, clients, false)
		if err == nil {
			t.Fatal("onDemandFallback=false: expected failure when all spot AZs exhausted, got nil")
		}
		if !strings.Contains(err.Error(), "InsufficientInstanceCapacity") {
			t.Errorf("error %q should mention InsufficientInstanceCapacity", err.Error())
		}
	})
}

// TestPhase10_GPUAZSweep_HappyPath_FirstAZActive is Acceptance Criterion 4:
// first AZ returns ACTIVE → no delete, no extra AZ attempts.
func TestPhase10_GPUAZSweep_HappyPath_FirstAZActive(t *testing.T) {
	awsmw.ResetForTest()

	cl := gpuRigCluster()
	st := stateForGPURig(t)

	// No ngCreatingTicks: mock returns ACTIVE immediately.
	eksMock := newMockEKS()
	asMock := newMockAutoScaling()

	clients := clientsWithEKSAndAS(eksMock, asMock)
	ctx := context.Background()

	if err := Phase10NodeGroup(ctx, cl, st, clients, false); err != nil {
		t.Fatalf("Phase10NodeGroup happy path: %v", err)
	}

	// No nodegroup was deleted (no capacity failure path taken).
	if eksMock.deleteNodegroupCalls != 0 {
		t.Errorf("deleteNodegroupCalls = %d, want 0 (first AZ ACTIVE, no delete needed)", eksMock.deleteNodegroupCalls)
	}

	// Exactly 2 nodegroups created: BNK ng + GPU ng (one AZ only for GPU).
	if eksMock.createNodegroupCalls != 2 {
		t.Errorf("createNodegroupCalls = %d, want 2 (BNK + GPU once, no extra AZ attempts)", eksMock.createNodegroupCalls)
	}

	// State must be populated.
	if st.Get("NODEGROUP_GPU_NAME") == "" {
		t.Error("NODEGROUP_GPU_NAME not set in state")
	}
	if st.Get("NODEGROUP_GPU_ARN") == "" {
		t.Error("NODEGROUP_GPU_ARN not set in state")
	}
}
