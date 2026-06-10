package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// testClientsIAM returns a Clients with the given mockIAM wired in.
// EC2 and STS use the standard mock stubs from mock_ec2_test.go.
func testClientsIAM(iamMock IAMAPI) *Clients {
	return &Clients{
		EC2:     &mockEC2{},
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     iamMock,
		Profile: "test",
	}
}

// stateWithVPCID returns a state with VPC_ID pre-seeded, required since
// Phase07IAM now creates the SG_BNK_DATA security group which needs VPC_ID.
func stateWithVPCIDOnly(t *testing.T) (*state.State, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	st.Set("VPC_ID", "vpc-0abc")
	return st, dir
}

// TestPhase07IAM_CreatesClusterRole verifies the cluster role is created with
// the correct trust principal and both managed policies attached.
func TestPhase07IAM_CreatesClusterRole(t *testing.T) {
	awsmw.ResetForTest()
	st, _ := stateWithVPCIDOnly(t)
	mock := newMockIAM()
	cl := testCluster()

	if err := Phase07IAM(context.Background(), cl, st, testClientsIAM(mock), false); err != nil {
		t.Fatalf("Phase07IAM: %v", err)
	}

	// Cluster role must have been created.
	clusterRoleName := cl.Metadata.Name + "-eks-cluster-role"
	if mock.createRoleCalls < 1 {
		t.Errorf("expected at least 1 CreateRole call, got %d", mock.createRoleCalls)
	}
	if _, ok := mock.roles[clusterRoleName]; !ok {
		t.Errorf("cluster role %s not in mock after create", clusterRoleName)
	}

	// ARN stored in state.
	arn := st.Get("EKS_CLUSTER_ROLE_ARN")
	if !strings.HasPrefix(arn, "arn:aws:iam::") {
		t.Errorf("EKS_CLUSTER_ROLE_ARN not set or malformed: %q", arn)
	}

	// Both managed policies must be attached.
	attached := mock.attachedPolicies[clusterRoleName]
	for _, want := range []string{
		"arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
		"arn:aws:iam::aws:policy/AmazonEKSVPCResourceController",
	} {
		if !attached[want] {
			t.Errorf("cluster role missing attached policy: %s", want)
		}
	}
}

// TestPhase07IAM_CreatesNodeRole verifies the node role is created with 4
// managed policies and the TmmVpcRoute inline policy. Also checks the
// service-role/ ARN path for AmazonEBSCSIDriverPolicy.
func TestPhase07IAM_CreatesNodeRole(t *testing.T) {
	awsmw.ResetForTest()
	st, _ := stateWithVPCIDOnly(t)
	mock := newMockIAM()
	cl := testCluster()

	if err := Phase07IAM(context.Background(), cl, st, testClientsIAM(mock), false); err != nil {
		t.Fatalf("Phase07IAM: %v", err)
	}

	nodeRoleName := cl.Metadata.Name + "-eks-node-role"

	// Node role ARN in state.
	arn := st.Get("EKS_NODE_ROLE_ARN")
	if !strings.HasPrefix(arn, "arn:aws:iam::") {
		t.Errorf("EKS_NODE_ROLE_ARN not set or malformed: %q", arn)
	}

	// 4 managed policies — including the service-role/ path.
	attached := mock.attachedPolicies[nodeRoleName]
	for _, want := range []string{
		"arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
		"arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
		"arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
		"arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy",
	} {
		if !attached[want] {
			t.Errorf("node role missing attached policy: %s", want)
		}
	}

	// service-role/ path must NOT be confused with policy/ path.
	if attached["arn:aws:iam::aws:policy/AmazonEBSCSIDriverPolicy"] {
		t.Error("node role has the WRONG path for AmazonEBSCSIDriverPolicy (missing service-role/)")
	}

	// TmmVpcRoute inline policy.
	if mock.putRolePolicyCalls == 0 {
		t.Error("expected PutRolePolicy call for TmmVpcRoute, got 0")
	}
	inlines := mock.inlinePolicies[nodeRoleName]
	found := false
	for _, n := range inlines {
		if n == "TmmVpcRoute" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("TmmVpcRoute not in inline policies for %s: %v", nodeRoleName, inlines)
	}
}

// TestPhase07IAM_CreatesInstanceProfile verifies the instance profile is
// created and the node role is added to it.
func TestPhase07IAM_CreatesInstanceProfile(t *testing.T) {
	awsmw.ResetForTest()
	st, _ := stateWithVPCIDOnly(t)
	mock := newMockIAM()
	cl := testCluster()

	if err := Phase07IAM(context.Background(), cl, st, testClientsIAM(mock), false); err != nil {
		t.Fatalf("Phase07IAM: %v", err)
	}

	profileName := cl.Metadata.Name + "-node-instance-profile"

	if mock.createInstanceProfileCalls != 1 {
		t.Errorf("expected 1 CreateInstanceProfile call, got %d", mock.createInstanceProfileCalls)
	}
	if mock.addRoleToInstanceProfileCalls != 1 {
		t.Errorf("expected 1 AddRoleToInstanceProfile call, got %d", mock.addRoleToInstanceProfileCalls)
	}

	if st.Get("NODE_INSTANCE_PROFILE_NAME") != profileName {
		t.Errorf("NODE_INSTANCE_PROFILE_NAME: got %q, want %q", st.Get("NODE_INSTANCE_PROFILE_NAME"), profileName)
	}
	arn := st.Get("NODE_INSTANCE_PROFILE_ARN")
	if !strings.HasPrefix(arn, "arn:aws:iam::") {
		t.Errorf("NODE_INSTANCE_PROFILE_ARN not set or malformed: %q", arn)
	}
}

// TestPhase07IAM_Idempotent verifies that a second call against a pre-populated
// mock produces zero CreateRole and CreateInstanceProfile calls.
func TestPhase07IAM_Idempotent(t *testing.T) {
	awsmw.ResetForTest()
	st, dir := stateWithVPCIDOnly(t)
	mock := newMockIAM()
	cl := testCluster()
	ctx := context.Background()

	// First run.
	if err := Phase07IAM(ctx, cl, st, testClientsIAM(mock), false); err != nil {
		t.Fatalf("Phase07IAM run1: %v", err)
	}
	snap := struct{ roles, profiles, add int }{
		mock.createRoleCalls,
		mock.createInstanceProfileCalls,
		mock.addRoleToInstanceProfileCalls,
	}
	if snap.roles == 0 {
		t.Fatal("run1: expected CreateRole calls, got 0")
	}

	// Second run — reload state from disk.
	st2, _ := state.Load(dir)
	if err := Phase07IAM(ctx, cl, st2, testClientsIAM(mock), false); err != nil {
		t.Fatalf("Phase07IAM run2: %v", err)
	}

	if mock.createRoleCalls != snap.roles {
		t.Errorf("run2: createRoleCalls increased from %d to %d", snap.roles, mock.createRoleCalls)
	}
	if mock.createInstanceProfileCalls != snap.profiles {
		t.Errorf("run2: createInstanceProfileCalls increased from %d to %d", snap.profiles, mock.createInstanceProfileCalls)
	}
	if mock.addRoleToInstanceProfileCalls != snap.add {
		t.Errorf("run2: addRoleToInstanceProfileCalls increased from %d to %d", snap.add, mock.addRoleToInstanceProfileCalls)
	}
}

// TestPhase07IAMDown_DestroysInOrder verifies the down path removes role from
// profile, deletes profile, detaches policies, and deletes both roles.
func TestPhase07IAMDown_DestroysInOrder(t *testing.T) {
	awsmw.ResetForTest()
	st, _ := stateWithVPCIDOnly(t)
	mock := newMockIAM()
	cl := testCluster()
	ctx := context.Background()

	// Create resources first.
	if err := Phase07IAM(ctx, cl, st, testClientsIAM(mock), false); err != nil {
		t.Fatalf("Phase07IAM: %v", err)
	}

	// Now destroy.
	if err := Phase07IAMDown(ctx, cl, st, testClientsIAM(mock)); err != nil {
		t.Fatalf("Phase07IAMDown: %v", err)
	}

	// Both roles must be gone from the mock.
	clusterRoleName := cl.Metadata.Name + "-eks-cluster-role"
	nodeRoleName := cl.Metadata.Name + "-eks-node-role"
	if _, ok := mock.roles[clusterRoleName]; ok {
		t.Errorf("cluster role still in mock after down")
	}
	if _, ok := mock.roles[nodeRoleName]; ok {
		t.Errorf("node role still in mock after down")
	}

	profileName := cl.Metadata.Name + "-node-instance-profile"
	if _, ok := mock.profiles[profileName]; ok {
		t.Errorf("instance profile still in mock after down")
	}

	if mock.deleteInstanceProfileCalls == 0 {
		t.Error("expected DeleteInstanceProfile call, got 0")
	}
	if mock.deleteRoleCalls < 2 {
		t.Errorf("expected 2 DeleteRole calls (cluster + node), got %d", mock.deleteRoleCalls)
	}
}

// TestPhase07IAMDown_ToleratesNoSuchEntity verifies that down is a no-op when
// all resources are already gone (NoSuchEntity on every call).
func TestPhase07IAMDown_ToleratesNoSuchEntity(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	mock := newMockIAM() // empty — nothing exists
	cl := testCluster()

	// Should not return an error even though nothing exists.
	if err := Phase07IAMDown(context.Background(), cl, st, testClientsIAM(mock)); err != nil {
		t.Fatalf("Phase07IAMDown on empty mock: %v", err)
	}
}

// TestPhase07IAMDown_NameBasedFallback verifies that when state is empty the
// down phase reconstructs names from cluster metadata and tolerates not-found.
func TestPhase07IAMDown_NameBasedFallback(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir) // empty state
	mock := newMockIAM()     // empty — nothing exists
	cl := testCluster()

	// No state keys set — phase must derive names from cluster.metadata.name.
	if err := Phase07IAMDown(context.Background(), cl, st, testClientsIAM(mock)); err != nil {
		t.Fatalf("Phase07IAMDown with empty state: %v", err)
	}
}

// TestPhase07IAM_DryRun verifies zero IAM mutations and placeholder state values.
func TestPhase07IAM_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	mock := newMockIAM()
	cl := testCluster()

	if err := Phase07IAM(context.Background(), cl, st, testClientsIAM(mock), true); err != nil {
		t.Fatalf("Phase07IAM dry-run: %v", err)
	}

	// Zero mutations.
	if mock.createRoleCalls != 0 {
		t.Errorf("dry-run: createRoleCalls = %d, want 0", mock.createRoleCalls)
	}
	if mock.createInstanceProfileCalls != 0 {
		t.Errorf("dry-run: createInstanceProfileCalls = %d, want 0", mock.createInstanceProfileCalls)
	}
	if mock.addRoleToInstanceProfileCalls != 0 {
		t.Errorf("dry-run: addRoleToInstanceProfileCalls = %d, want 0", mock.addRoleToInstanceProfileCalls)
	}

	// Placeholder state keys present.
	name := cl.Metadata.Name
	checks := map[string]string{
		"EKS_CLUSTER_ROLE_ARN":       "arn:aws:iam::dry-run:role/" + name + "-eks-cluster-role",
		"EKS_NODE_ROLE_ARN":          "arn:aws:iam::dry-run:role/" + name + "-eks-node-role",
		"NODE_INSTANCE_PROFILE_NAME": name + "-node-instance-profile",
		"NODE_INSTANCE_PROFILE_ARN":  "arn:aws:iam::dry-run:instance-profile/" + name + "-node-instance-profile",
		"SG_BNK_DATA":                "sg-dry-run-bnk-data",
	}
	for key, want := range checks {
		if got := st.Get(key); got != want {
			t.Errorf("dry-run state[%s] = %q, want %q", key, got, want)
		}
	}
}

// TestPhase07IAM_CreatesSGBNKData verifies the SG_BNK_DATA security group is
// created in the cluster's VPC and persisted in state.
func TestPhase07IAM_CreatesSGBNKData(t *testing.T) {
	awsmw.ResetForTest()
	st, _ := stateWithVPCIDOnly(t)
	mock := newMockIAM()
	cl := testCluster()

	if err := Phase07IAM(context.Background(), cl, st, testClientsIAM(mock), false); err != nil {
		t.Fatalf("Phase07IAM: %v", err)
	}

	// SG_BNK_DATA must be set in state with an sg- prefix.
	sgID := st.Get("SG_BNK_DATA")
	if !strings.HasPrefix(sgID, "sg-") {
		t.Errorf("SG_BNK_DATA = %q, want sg-... prefix", sgID)
	}
}

// TestClearSGReferences_RevokesRefsAndDeletesOrphanEKSSG pins the
// DependencyViolation regression hit live on bnk-demo teardown (2026-06-10):
// the EKS-managed cluster SG survives cluster deletion when cross-SG rules
// reference SG_BNK_DATA, and DeleteSecurityGroup then fails. clearSGReferences
// must revoke exactly the referencing rules and delete the orphaned
// eks-cluster-sg-<cluster>-* shell.
func TestClearSGReferences_RevokesRefsAndDeletesOrphanEKSSG(t *testing.T) {
	bnkData := "sg-0bnkdata"
	eksSG := "sg-0ekscluster"
	eksSGName := "eks-cluster-sg-tracer-12345"
	otherSG := "sg-0unrelated"
	proto := "-1"

	ec2m := &mockEC2{
		describeSGsOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{
					GroupId:   &eksSG,
					GroupName: &eksSGName,
					IpPermissions: []ec2types.IpPermission{{
						IpProtocol: &proto,
						UserIdGroupPairs: []ec2types.UserIdGroupPair{
							{GroupId: &bnkData},
							{GroupId: &otherSG}, // must NOT be revoked
						},
					}},
				},
			},
		},
	}

	if err := clearSGReferences(context.Background(), ec2m, bnkData, "tracer"); err != nil {
		t.Fatalf("clearSGReferences: %v", err)
	}

	if len(ec2m.revokeIngressInputs) != 1 {
		t.Fatalf("expected 1 RevokeSecurityGroupIngress call, got %d", len(ec2m.revokeIngressInputs))
	}
	rev := ec2m.revokeIngressInputs[0]
	if *rev.GroupId != eksSG {
		t.Errorf("revoke targeted %q, want %q", *rev.GroupId, eksSG)
	}
	var revokedPairs []string
	for _, p := range rev.IpPermissions {
		for _, g := range p.UserIdGroupPairs {
			revokedPairs = append(revokedPairs, *g.GroupId)
		}
	}
	if len(revokedPairs) != 1 || revokedPairs[0] != bnkData {
		t.Errorf("revoked pairs = %v, want exactly [%s] (unrelated refs must be preserved)", revokedPairs, bnkData)
	}
	if ec2m.deleteSGCalls != 1 {
		t.Errorf("expected the orphaned EKS cluster SG to be deleted (1 DeleteSecurityGroup call), got %d", ec2m.deleteSGCalls)
	}
}
