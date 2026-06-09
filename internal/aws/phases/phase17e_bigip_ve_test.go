package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithy "github.com/aws/smithy-go"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// bigipCluster returns a minimal Cluster with bigipVE.enabled=true and all
// required supporting configuration (dual-interface, jumphost, demo).
func bigipCluster() *intent.Cluster {
	cl := testCluster()
	cl.Pattern = "dual-interface"
	cl.Network.DataPath = &intent.DataPathSpec{
		External: intent.SubnetSpec{CIDR: "10.0.10.0/24", AZ: "ap-southeast-2a"},
		Internal: intent.SubnetSpec{CIDR: "10.0.20.0/24", AZ: "ap-southeast-2a"},
	}
	cl.Testing = &intent.TestingSpec{
		Jumphost: &intent.JumphostSpec{
			Enabled:      true,
			InstanceType: "t3.small",
		},
	}
	cl.Demo = &intent.DemoSpec{Enabled: true}
	cl.BigIPVE = &intent.BigIPVESpec{
		Enabled:         true,
		InstanceType:    "c5n.2xlarge",
		MgmtSubnetIndex: 0,
		VIP:             "10.0.10.120",
		LicenseTier:     "Good",
	}
	return cl
}

// stateWithBigIPPrereqs returns state pre-populated with all keys Phase17e needs.
func stateWithBigIPPrereqs(t *testing.T) *state.State {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	st.Set("VPC_ID", "vpc-test-bigip")
	st.Set("SG_BNK_DATA", "sg-bnk-data-bigip")
	st.Set("BNK_EXT_SUBNET", "subnet-ext-bigip")
	st.Set("BNK_INT_SUBNET", "subnet-int-bigip")
	st.Set("JUMPHOST_SG_ID", "sg-jumphost-bigip")
	st.Set("EKS_SECURITY_GROUP", "sg-eks-nodes-bigip")
	st.Set("PUBLIC_SUBNETS", "subnet-mgmt-bigip-0,subnet-mgmt-bigip-1")
	return st
}

// bigipAMIOutput returns a DescribeImages output with two images — the one
// with the later CreationDate should be selected.
func bigipAMIOutput() *ec2.DescribeImagesOutput {
	id1 := "ami-older"
	name1 := "F5 BIGIP-17.1.0-0.0.10 PAYG-Good 25Mbps-1234567890abcdef0"
	date1 := "2024-01-01T00:00:00.000Z"

	id2 := "ami-newer"
	name2 := "F5 BIGIP-17.5.1.6-0.0.25 PAYG-Good 25Mbps-1234567890abcdef0"
	date2 := "2025-06-01T00:00:00.000Z"

	return &ec2.DescribeImagesOutput{
		Images: []ec2types.Image{
			{ImageId: &id1, Name: &name1, CreationDate: &date1},
			{ImageId: &id2, Name: &name2, CreationDate: &date2},
		},
	}
}

// TestPhase17eBigIPVE_FeatureGateDisabled verifies zero AWS calls when
// bigipVE.enabled is false.
func TestPhase17eBigIPVE_FeatureGateDisabled(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster() // no BigIPVE block
	ec2m := &mockEC2{}

	if err := Phase17eBigIPVE(context.Background(), cl, st, testClientsWithSSM(ec2m, newMockIAM(), &mockSSM{}), false); err != nil {
		t.Fatalf("Phase17eBigIPVE (disabled): %v", err)
	}
	if ec2m.createSGCalls != 0 {
		t.Errorf("feature-gate disabled: createSGCalls = %d, want 0", ec2m.createSGCalls)
	}
	if ec2m.runInstancesCalls != 0 {
		t.Errorf("feature-gate disabled: runInstancesCalls = %d, want 0", ec2m.runInstancesCalls)
	}
	if ec2m.createKeyPairCalls != 0 {
		t.Errorf("feature-gate disabled: createKeyPairCalls = %d, want 0", ec2m.createKeyPairCalls)
	}
}

// TestPhase17eBigIPVE_DryRun verifies no AWS mutations and all placeholder state keys.
func TestPhase17eBigIPVE_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := bigipCluster()
	ec2m := &mockEC2{}

	if err := Phase17eBigIPVE(context.Background(), cl, st, testClientsWithSSM(ec2m, newMockIAM(), &mockSSM{}), true); err != nil {
		t.Fatalf("Phase17eBigIPVE dry-run: %v", err)
	}

	// Zero AWS mutations.
	if ec2m.createSGCalls != 0 {
		t.Errorf("dry-run: createSGCalls = %d, want 0", ec2m.createSGCalls)
	}
	if ec2m.runInstancesCalls != 0 {
		t.Errorf("dry-run: runInstancesCalls = %d, want 0", ec2m.runInstancesCalls)
	}
	if ec2m.createKeyPairCalls != 0 {
		t.Errorf("dry-run: createKeyPairCalls = %d, want 0", ec2m.createKeyPairCalls)
	}

	// All placeholder state keys must be set.
	wantKeys := []string{
		"BIGIP_INSTANCE_ID",
		"BIGIP_MGMT_ENI_ID",
		"BIGIP_MGMT_IP",
		"BIGIP_EXT_ENI_ID",
		"BIGIP_EXT_IP",
		"BIGIP_VIP",
		"BIGIP_INT_ENI_ID",
		"BIGIP_INT_IP",
		"BIGIP_MGMT_SG_ID",
		"BIGIP_KEY_NAME",
		"BIGIP_AMI_ID",
		"BIGIP_SSH_KEY_PATH",
	}
	for _, key := range wantKeys {
		if v := st.Get(key); v == "" {
			t.Errorf("dry-run: state key %s is empty, want placeholder", key)
		}
	}
	if got := st.Get("BIGIP_INSTANCE_ID"); got != "i-dry-run-bigip" {
		t.Errorf("BIGIP_INSTANCE_ID = %q, want i-dry-run-bigip", got)
	}
	if got := st.Get("BIGIP_VIP"); got != "10.0.10.120" {
		t.Errorf("BIGIP_VIP = %q, want 10.0.10.120", got)
	}
}

// TestPhase17eBigIPVE_MissingPrereqs verifies fail-fast errors for each
// required prerequisite state key.
func TestPhase17eBigIPVE_MissingPrereqs(t *testing.T) {
	tests := []struct {
		name      string
		omitKey   string
		wantInErr string
	}{
		{"missing VPC_ID", "VPC_ID", "VPC_ID"},
		{"missing SG_BNK_DATA", "SG_BNK_DATA", "SG_BNK_DATA"},
		{"missing BNK_EXT_SUBNET", "BNK_EXT_SUBNET", "BNK_EXT_SUBNET"},
		{"missing BNK_INT_SUBNET", "BNK_INT_SUBNET", "BNK_INT_SUBNET"},
		{"missing JUMPHOST_SG_ID", "JUMPHOST_SG_ID", "JUMPHOST_SG_ID"},
		{"missing EKS_SECURITY_GROUP", "EKS_SECURITY_GROUP", "EKS_SECURITY_GROUP"},
		{"missing PUBLIC_SUBNETS", "PUBLIC_SUBNETS", "PUBLIC_SUBNETS"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			awsmw.ResetForTest()
			st := stateWithBigIPPrereqs(t)
			st.Set(tc.omitKey, "")
			cl := bigipCluster()

			err := Phase17eBigIPVE(context.Background(), cl, st, testClientsWithSSM(&mockEC2{}, newMockIAM(), &mockSSM{}), false)
			if err == nil {
				t.Fatalf("expected error for missing %s, got nil", tc.omitKey)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q should contain %q", err.Error(), tc.wantInErr)
			}
		})
	}
}

// TestResolveBigIPAMI_PicksNewest verifies that when multiple AMIs are returned,
// the one with the latest CreationDate is selected.
func TestResolveBigIPAMI_PicksNewest(t *testing.T) {
	ec2m := &mockEC2{
		describeImagesOut: bigipAMIOutput(),
	}
	amiID, err := resolveBigIPAMI(context.Background(), ec2m, "Good", "")
	if err != nil {
		t.Fatalf("resolveBigIPAMI: %v", err)
	}
	if amiID != "ami-newer" {
		t.Errorf("resolveBigIPAMI = %q, want ami-newer (newest by creation date)", amiID)
	}
}

// TestResolveBigIPAMI_NoImages verifies an actionable error when no AMIs are found.
func TestResolveBigIPAMI_NoImages(t *testing.T) {
	ec2m := &mockEC2{
		describeImagesOut: &ec2.DescribeImagesOutput{},
	}
	_, err := resolveBigIPAMI(context.Background(), ec2m, "Good", "")
	if err == nil {
		t.Fatal("expected error when no AMIs found, got nil")
	}
	if !strings.Contains(err.Error(), "no BIG-IP VE PAYG") {
		t.Errorf("error %q should mention 'no BIG-IP VE PAYG'", err.Error())
	}
}

// TestPhase17eBigIPVE_OptInRequired verifies the actionable marketplace error.
func TestPhase17eBigIPVE_OptInRequired(t *testing.T) {
	awsmw.ResetForTest()
	st := stateWithBigIPPrereqs(t)
	cl := bigipCluster()

	// AMI resolves fine, but RunInstances returns OptInRequired.
	optInErr := &notFoundAPIError{code: "OptInRequired"}
	ec2m := &mockEC2{
		describeImagesOut: bigipAMIOutput(),
		runInstancesErr:   optInErr,
	}

	err := Phase17eBigIPVE(context.Background(), cl, st, testClientsWithSSM(ec2m, newMockIAM(), &mockSSM{}), false)
	if err == nil {
		t.Fatal("expected error for OptInRequired, got nil")
	}
	if !strings.Contains(err.Error(), "AWS Marketplace subscription") {
		t.Errorf("error %q should mention 'AWS Marketplace subscription'", err.Error())
	}
	if !strings.Contains(err.Error(), "marketplace") {
		t.Errorf("error %q should contain 'marketplace'", err.Error())
	}
}

// TestPhase17eBigIPVE_IdempotentOnRerun verifies that when state already has a
// running BIGIP_INSTANCE_ID, no RunInstances call is made.
func TestPhase17eBigIPVE_IdempotentOnRerun(t *testing.T) {
	awsmw.ResetForTest()
	st := stateWithBigIPPrereqs(t)

	// Pre-populate all state keys as if a prior run succeeded.
	st.Set("BIGIP_INSTANCE_ID", "i-existing-bigip")
	st.Set("BIGIP_MGMT_ENI_ID", "eni-mgmt-existing")
	st.Set("BIGIP_MGMT_IP", "10.0.1.50")
	st.Set("BIGIP_EXT_ENI_ID", "eni-ext-existing")
	st.Set("BIGIP_EXT_IP", "10.0.10.50")
	st.Set("BIGIP_VIP", "10.0.10.120")
	st.Set("BIGIP_INT_ENI_ID", "eni-int-existing")
	st.Set("BIGIP_INT_IP", "10.0.20.50")
	st.Set("BIGIP_MGMT_SG_ID", "sg-bigip-mgmt-existing")
	st.Set("BIGIP_KEY_NAME", bigipVEKeyName)
	st.Set("BIGIP_AMI_ID", "ami-cached-bigip")
	st.Set("BIGIP_SSH_KEY_PATH", "/tmp/test/bigip-ssh.pem")

	cl := bigipCluster()
	instanceID := "i-existing-bigip"

	// DescribeInstances returns running instance; SGs and ENIs found in state.
	ec2m := &mockEC2{
		describeInstancesOut: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				{
					Instances: []ec2types.Instance{
						{
							InstanceId: &instanceID,
							State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
						},
					},
				},
			},
		},
		// Key pair already exists — DescribeKeyPairs returns it.
		describeKeyPairsOut: &ec2.DescribeKeyPairsOutput{
			KeyPairs: []ec2types.KeyPairInfo{{KeyName: ptr(bigipVEKeyName)}},
		},
		// SG already exists.
		describeSGsOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: ptr("sg-bigip-mgmt-existing")},
			},
		},
		// ENIs already exist.
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{NetworkInterfaceId: ptr("eni-mgmt-existing"), PrivateIpAddress: ptr("10.0.1.50")},
			},
		},
	}

	if err := Phase17eBigIPVE(context.Background(), cl, st, testClientsWithSSM(ec2m, newMockIAM(), &mockSSM{}), false); err != nil {
		t.Fatalf("Phase17eBigIPVE idempotent: %v", err)
	}

	// No new resources created.
	if ec2m.runInstancesCalls != 0 {
		t.Errorf("idempotent: runInstancesCalls = %d, want 0", ec2m.runInstancesCalls)
	}
	if ec2m.createSGCalls != 0 {
		t.Errorf("idempotent: createSGCalls = %d, want 0", ec2m.createSGCalls)
	}
	if ec2m.createENICalls != 0 {
		t.Errorf("idempotent: createENICalls = %d, want 0", ec2m.createENICalls)
	}
	if ec2m.createKeyPairCalls != 0 {
		t.Errorf("idempotent: createKeyPairCalls = %d, want 0", ec2m.createKeyPairCalls)
	}
}

// TestPhase17eBigIPVEDown_ToleratesNotFound verifies down succeeds when no
// resources exist (empty state + tag-discovery finds nothing).
func TestPhase17eBigIPVEDown_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir) // empty state
	cl := bigipCluster()

	ec2m := &mockEC2{
		describeInstancesOut: &ec2.DescribeInstancesOutput{},
		describeENIsOut:      &ec2.DescribeNetworkInterfacesOutput{},
		describeSGsOut:       &ec2.DescribeSecurityGroupsOutput{},
	}

	if err := Phase17eBigIPVEDown(context.Background(), cl, st, testClientsWithSSM(ec2m, newMockIAM(), &mockSSM{})); err != nil {
		t.Fatalf("Phase17eBigIPVEDown (not-found): %v", err)
	}
	if ec2m.terminateInstancesCalls != 0 {
		t.Errorf("terminateInstancesCalls = %d, want 0 (nothing to terminate)", ec2m.terminateInstancesCalls)
	}
	if ec2m.deleteKeyPairCalls != 1 {
		// DeleteKeyPair is always called (idempotent — tolerates not-found).
		t.Errorf("deleteKeyPairCalls = %d, want 1", ec2m.deleteKeyPairCalls)
	}
}

// TestPhase17eBigIPVEDown_TeardownOrder verifies that when state is fully populated,
// terminate is called, all 3 ENIs are deleted, SG is deleted, key pair is deleted,
// and state is cleared.
func TestPhase17eBigIPVEDown_TeardownOrder(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := bigipCluster()

	// Pre-populate state.
	st.Set("BIGIP_INSTANCE_ID", "i-bigip-teardown")
	st.Set("BIGIP_MGMT_ENI_ID", "eni-mgmt-teardown")
	st.Set("BIGIP_EXT_ENI_ID", "eni-ext-teardown")
	st.Set("BIGIP_INT_ENI_ID", "eni-int-teardown")
	st.Set("BIGIP_MGMT_SG_ID", "sg-bigip-mgmt-teardown")
	st.Set("BIGIP_KEY_NAME", bigipVEKeyName)
	st.Set("BIGIP_AMI_ID", "ami-bigip-cached")

	instanceID := "i-bigip-teardown"
	terminatedState := ec2types.InstanceStateNameTerminated

	ec2m := &mockEC2{
		// DescribeInstances returns terminated immediately (so the wait loop exits).
		describeInstancesOut: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				{
					Instances: []ec2types.Instance{
						{
							InstanceId: &instanceID,
							State:      &ec2types.InstanceState{Name: terminatedState},
						},
					},
				},
			},
		},
		// ENI describe: return available (detached) for the delete path.
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{
					NetworkInterfaceId: ptr("eni-mgmt-teardown"),
					Status:             ec2types.NetworkInterfaceStatusAvailable,
				},
			},
		},
	}

	if err := Phase17eBigIPVEDown(context.Background(), cl, st, testClientsWithSSM(ec2m, newMockIAM(), &mockSSM{})); err != nil {
		t.Fatalf("Phase17eBigIPVEDown teardown: %v", err)
	}

	// Instance was terminated.
	if ec2m.terminateInstancesCalls != 1 {
		t.Errorf("terminateInstancesCalls = %d, want 1", ec2m.terminateInstancesCalls)
	}
	// 3 ENIs deleted.
	if ec2m.deleteENICalls != 3 {
		t.Errorf("deleteENICalls = %d, want 3", ec2m.deleteENICalls)
	}
	// SG deleted.
	if ec2m.deleteSGCalls != 1 {
		t.Errorf("deleteSGCalls = %d, want 1", ec2m.deleteSGCalls)
	}
	// Key pair deleted.
	if ec2m.deleteKeyPairCalls != 1 {
		t.Errorf("deleteKeyPairCalls = %d, want 1", ec2m.deleteKeyPairCalls)
	}
	// State cleared.
	if got := st.Get("BIGIP_INSTANCE_ID"); got != "" {
		t.Errorf("BIGIP_INSTANCE_ID = %q after down, want empty", got)
	}
	if got := st.Get("BIGIP_VIP"); got != "" {
		t.Errorf("BIGIP_VIP = %q after down, want empty", got)
	}
}

// TestCidrDotN verifies the IP derivation helper.
func TestCidrDotN(t *testing.T) {
	tests := []struct {
		cidr    string
		n       int
		want    string
		wantErr bool
	}{
		{"10.0.10.0/24", 50, "10.0.10.50", false},
		{"10.0.1.0/24", 50, "10.0.1.50", false},
		{"10.0.20.0/24", 50, "10.0.20.50", false},
		{"10.0.10.0/24", 1, "10.0.10.1", false},
		{"10.0.10.0/24", 254, "10.0.10.254", false},
		{"not-a-cidr", 50, "", true},
	}
	for _, tc := range tests {
		got, err := cidrDotN(tc.cidr, tc.n)
		if tc.wantErr {
			if err == nil {
				t.Errorf("cidrDotN(%q, %d): expected error, got %q", tc.cidr, tc.n, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("cidrDotN(%q, %d): unexpected error: %v", tc.cidr, tc.n, err)
			continue
		}
		if got != tc.want {
			t.Errorf("cidrDotN(%q, %d) = %q, want %q", tc.cidr, tc.n, got, tc.want)
		}
	}
}

// TestIsBigIPOptInRequired verifies the OptInRequired detection.
func TestIsBigIPOptInRequired(t *testing.T) {
	optInErr := &notFoundAPIError{code: "OptInRequired"}
	if !isBigIPOptInRequired(optInErr) {
		t.Error("isBigIPOptInRequired: expected true for OptInRequired error")
	}
	otherErr := &notFoundAPIError{code: "InvalidInstanceID.NotFound"}
	if isBigIPOptInRequired(otherErr) {
		t.Error("isBigIPOptInRequired: expected false for non-OptInRequired error")
	}
	if isBigIPOptInRequired(nil) {
		t.Error("isBigIPOptInRequired: expected false for nil error")
	}
}

// TestEnsureBigIPMgmtSG_IngressRules verifies the exact ingress permission
// structure of the management SG:
//   - tcp/443 is allowed from BOTH the jumphost SG and the EKS node SG via
//     UserIdGroupPairs (no IpRanges / VPC CIDR).
//   - tcp/22 is allowed only from the jumphost SG (no EKS SG, no CIDR).
func TestEnsureBigIPMgmtSG_IngressRules(t *testing.T) {
	const (
		jumphostSG = "sg-jumphost-test"
		eksSG      = "sg-eks-nodes-test"
		vpcCIDR    = "10.0.0.0/16" // must NOT appear in any rule
	)

	dir := t.TempDir()
	st, _ := state.Load(dir)

	ec2m := &mockEC2{}
	_, err := ensureBigIPMgmtSG(context.Background(), ec2m, "test-cluster", "vpc-test", jumphostSG, eksSG, nil, nil, st)
	if err != nil {
		t.Fatalf("ensureBigIPMgmtSG: %v", err)
	}

	if ec2m.authorizeIngressCalls != 1 {
		t.Fatalf("authorizeIngressCalls = %d, want 1", ec2m.authorizeIngressCalls)
	}
	perms := ec2m.authorizeIngressInput.IpPermissions

	// Verify no CIDR ranges appear anywhere.
	for i, p := range perms {
		if len(p.IpRanges) != 0 {
			t.Errorf("permission[%d]: IpRanges is non-empty (got %v), want none — VPC CIDR must not be used",
				i, p.IpRanges)
		}
	}

	// Find the port-443 permission.
	var perm443 *ec2types.IpPermission
	for i := range perms {
		if perms[i].FromPort != nil && *perms[i].FromPort == 443 {
			perm443 = &perms[i]
			break
		}
	}
	if perm443 == nil {
		t.Fatal("no tcp/443 permission found in ingress rules")
	}

	// tcp/443 must have exactly two group-pairs: jumphost + EKS node SG.
	groupIDs443 := make(map[string]bool)
	for _, gp := range perm443.UserIdGroupPairs {
		if gp.GroupId != nil {
			groupIDs443[*gp.GroupId] = true
		}
	}
	if !groupIDs443[jumphostSG] {
		t.Errorf("tcp/443: jumphost SG %q not found in UserIdGroupPairs", jumphostSG)
	}
	if !groupIDs443[eksSG] {
		t.Errorf("tcp/443: EKS node SG %q not found in UserIdGroupPairs", eksSG)
	}
	if groupIDs443[vpcCIDR] {
		t.Errorf("tcp/443: VPC CIDR %q must not appear as a group-pair", vpcCIDR)
	}

	// Find the port-22 permission.
	var perm22 *ec2types.IpPermission
	for i := range perms {
		if perms[i].FromPort != nil && *perms[i].FromPort == 22 {
			perm22 = &perms[i]
			break
		}
	}
	if perm22 == nil {
		t.Fatal("no tcp/22 permission found in ingress rules")
	}

	// tcp/22 must have exactly one group-pair: jumphost SG only.
	if len(perm22.UserIdGroupPairs) != 1 {
		t.Errorf("tcp/22: want 1 UserIdGroupPair, got %d", len(perm22.UserIdGroupPairs))
	} else if perm22.UserIdGroupPairs[0].GroupId == nil || *perm22.UserIdGroupPairs[0].GroupId != jumphostSG {
		t.Errorf("tcp/22: want jumphost SG %q, got %v", jumphostSG, perm22.UserIdGroupPairs[0].GroupId)
	}
	// EKS SG must NOT be in tcp/22 pairs.
	for _, gp := range perm22.UserIdGroupPairs {
		if gp.GroupId != nil && *gp.GroupId == eksSG {
			t.Errorf("tcp/22: EKS node SG %q must not appear — 22 is jumphost-only", eksSG)
		}
	}
}

// notFoundAPIError from mock_ec2_test.go satisfies smithy.APIError and is used
// above for OptInRequired simulation. Ensure it implements the interface.
var _ smithy.APIError = (*notFoundAPIError)(nil)
