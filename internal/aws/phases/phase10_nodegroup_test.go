package phases

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
)

func clientsWithEKS(eksMock *mockEKS) *Clients {
	return &Clients{
		EC2:     &mockEC2{},
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     newMockIAM(),
		EKS:     eksMock,
		Profile: "test",
	}
}

func TestPhase10NodeGroup_CreatesWithPublicSubnetsOnly(t *testing.T) {
	awsmw.ResetForTest()
	cl := testClusterWithEKS()
	st, _ := stateWithIAMAndSubnets(t)
	st.Set("EKS_CLUSTER_NAME", cl.Metadata.Name)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	if eksMock.createNodegroupCalls != 1 {
		t.Errorf("createNodegroupCalls = %d, want 1", eksMock.createNodegroupCalls)
	}

	ngName := cl.Metadata.Name + "-ng-default"
	ng := eksMock.nodegroups[cl.Metadata.Name][ngName]
	if ng == nil {
		t.Fatal("node group not created in mock")
	}

	// Subnets must be public only (2 subnets).
	if len(ng.Subnets) != 2 {
		t.Errorf("node group subnet count = %d, want 2 (public only)", len(ng.Subnets))
	}

	// AMI type must be AL2_x86_64.
	if ng.AmiType != ekstypes.AMITypesAl2X8664 {
		t.Errorf("AMI type = %v, want AL2_x86_64", ng.AmiType)
	}

	// Instance type.
	if len(ng.InstanceTypes) == 0 || ng.InstanceTypes[0] != "t3.medium" {
		t.Errorf("instance types = %v, want [t3.medium]", ng.InstanceTypes)
	}

	// Scaling config.
	if ng.ScalingConfig == nil {
		t.Fatal("scaling config is nil")
	}
	if *ng.ScalingConfig.DesiredSize != 1 {
		t.Errorf("DesiredSize = %d, want 1", *ng.ScalingConfig.DesiredSize)
	}
	if *ng.ScalingConfig.MinSize != 1 {
		t.Errorf("MinSize = %d, want 1", *ng.ScalingConfig.MinSize)
	}
	if *ng.ScalingConfig.MaxSize != 2 {
		t.Errorf("MaxSize = %d, want 2", *ng.ScalingConfig.MaxSize)
	}

	// State keys.
	if st.Get("NODEGROUP_DEFAULT_NAME") == "" {
		t.Error("NODEGROUP_DEFAULT_NAME not set in state")
	}
	if st.Get("NODEGROUP_DEFAULT_ARN") == "" {
		t.Error("NODEGROUP_DEFAULT_ARN not set in state")
	}
}

func TestPhase10NodeGroup_Idempotent(t *testing.T) {
	awsmw.ResetForTest()
	cl := testClusterWithEKS()
	st, _ := stateWithIAMAndSubnets(t)
	st.Set("EKS_CLUSTER_NAME", cl.Metadata.Name)
	eksMock := newMockEKS()

	ctx := context.Background()

	// First run.
	if err := Phase10NodeGroup(ctx, cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("run1 Phase10NodeGroup: %v", err)
	}
	createAfterRun1 := eksMock.createNodegroupCalls

	// Second run.
	if err := Phase10NodeGroup(ctx, cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("run2 Phase10NodeGroup: %v", err)
	}
	if eksMock.createNodegroupCalls != createAfterRun1 {
		t.Errorf("run2 called CreateNodegroup: %d → %d, want no increase", createAfterRun1, eksMock.createNodegroupCalls)
	}
}

func TestPhase10NodeGroupDown_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	cl := testClusterWithEKS()
	st, _ := stateWithIAMAndSubnets(t)
	st.Set("EKS_CLUSTER_NAME", cl.Metadata.Name)
	eksMock := newMockEKS() // empty

	if err := Phase10NodeGroupDown(context.Background(), cl, st, clientsWithEKS(eksMock)); err != nil {
		t.Fatalf("Phase10NodeGroupDown (not-found): %v", err)
	}
}

func TestPhase10NodeGroupDown_DeletesNodeGroup(t *testing.T) {
	awsmw.ResetForTest()
	cl := testClusterWithEKS()
	st, _ := stateWithIAMAndSubnets(t)
	st.Set("EKS_CLUSTER_NAME", cl.Metadata.Name)
	eksMock := newMockEKS()

	ctx := context.Background()

	// Create first.
	if err := Phase10NodeGroup(ctx, cl, st, clientsWithEKS(eksMock), false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	// Down.
	if err := Phase10NodeGroupDown(ctx, cl, st, clientsWithEKS(eksMock)); err != nil {
		t.Fatalf("Phase10NodeGroupDown: %v", err)
	}

	if eksMock.deleteNodegroupCalls != 1 {
		t.Errorf("deleteNodegroupCalls = %d, want 1", eksMock.deleteNodegroupCalls)
	}
	if st.Get("NODEGROUP_DEFAULT_NAME") != "" {
		t.Error("NODEGROUP_DEFAULT_NAME not cleared after down")
	}
}

func TestPhase10NodeGroup_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	cl := testClusterWithEKS()
	st, _ := stateWithIAMAndSubnets(t)
	st.Set("EKS_CLUSTER_NAME", cl.Metadata.Name)
	eksMock := newMockEKS()

	if err := Phase10NodeGroup(context.Background(), cl, st, clientsWithEKS(eksMock), true); err != nil {
		t.Fatalf("Phase10NodeGroup dryRun: %v", err)
	}
	if eksMock.createNodegroupCalls != 0 {
		t.Errorf("dry-run: createNodegroupCalls = %d, want 0", eksMock.createNodegroupCalls)
	}
	if st.Get("NODEGROUP_DEFAULT_NAME") == "" {
		t.Error("dry-run: NODEGROUP_DEFAULT_NAME not populated")
	}
}

// TestPhase10NodeGroup_CreatesLaunchTemplate verifies the launch template is
// created before the node group and LT_ID is persisted in state.
func TestPhase10NodeGroup_CreatesLaunchTemplate(t *testing.T) {
	awsmw.ResetForTest()
	cl := testClusterWithEKS()
	st, _ := stateWithIAMAndSubnets(t)
	st.Set("EKS_CLUSTER_NAME", cl.Metadata.Name)
	eksMock := newMockEKS()
	ec2m := &mockEC2{} // createLTCalls will be incremented

	clients := &Clients{
		EC2:     ec2m,
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     newMockIAM(),
		EKS:     eksMock,
		Profile: "test",
	}

	if err := Phase10NodeGroup(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase10NodeGroup: %v", err)
	}

	// Launch template must have been created.
	if ec2m.createLTCalls != 1 {
		t.Errorf("createLTCalls = %d, want 1", ec2m.createLTCalls)
	}
	if st.Get("LT_ID") == "" {
		t.Error("LT_ID not set in state after node group create")
	}
}

// TestPhase10NodeGroup_LTIdempotent verifies no re-create of LT on second run.
func TestPhase10NodeGroup_LTIdempotent(t *testing.T) {
	awsmw.ResetForTest()
	cl := testClusterWithEKS()
	st, _ := stateWithIAMAndSubnets(t)
	st.Set("EKS_CLUSTER_NAME", cl.Metadata.Name)
	eksMock := newMockEKS()

	// Pre-populate DescribeLaunchTemplates to return existing LT.
	ltID := "lt-mock-existing"
	ltName := cl.Metadata.Name + "-bnk-lt"
	ec2m := &mockEC2{
		describeLTsOut: &ec2.DescribeLaunchTemplatesOutput{
			LaunchTemplates: []ec2types.LaunchTemplate{
				{LaunchTemplateId: &ltID, LaunchTemplateName: &ltName},
			},
		},
	}

	clients := &Clients{
		EC2:     ec2m,
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     newMockIAM(),
		EKS:     eksMock,
		Profile: "test",
	}

	if err := Phase10NodeGroup(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase10NodeGroup idempotent: %v", err)
	}

	// No new LT created when one already exists.
	if ec2m.createLTCalls != 0 {
		t.Errorf("idempotent: createLTCalls = %d, want 0", ec2m.createLTCalls)
	}
}

// TestPhase10NodeGroupDown_DeletesLT verifies the launch template is deleted
// during node group teardown.
func TestPhase10NodeGroupDown_DeletesLT(t *testing.T) {
	awsmw.ResetForTest()
	cl := testClusterWithEKS()
	st, _ := stateWithIAMAndSubnets(t)
	st.Set("EKS_CLUSTER_NAME", cl.Metadata.Name)
	st.Set("LT_ID", "lt-mock-1")
	eksMock := newMockEKS()
	ec2m := &mockEC2{}

	clients := &Clients{
		EC2:     ec2m,
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     newMockIAM(),
		EKS:     eksMock,
		Profile: "test",
	}

	if err := Phase10NodeGroupDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase10NodeGroupDown: %v", err)
	}

	// LT deleted.
	if ec2m.deleteLTCalls != 1 {
		t.Errorf("deleteLTCalls = %d, want 1", ec2m.deleteLTCalls)
	}
	// LT_ID cleared.
	if st.Get("LT_ID") != "" {
		t.Error("LT_ID not cleared after down")
	}
}
