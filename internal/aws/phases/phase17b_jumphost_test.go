package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// jumphostCluster returns a cluster with testing.jumphost.enabled=true and host-device pattern.
func jumphostCluster() *intent.Cluster {
	enabled := true
	_ = enabled
	cl := testCluster()
	cl.Pattern = "host-device"
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
	return cl
}

// stateWithJumphostPrereqs returns state pre-populated with Phase 17b required keys.
func stateWithJumphostPrereqs(t *testing.T) *state.State {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	st.Set("VPC_ID", "vpc-test-1")
	st.Set("SG_BNK_DATA", "sg-bnk-data-1")
	st.Set("BNK_EXT_SUBNET", "subnet-ext-1")
	st.Set("MGMT_SUBNET", "subnet-mgmt-1")
	return st
}

// testClientsWithSSM returns a Clients with EC2 + IAM + SSM mocks.
func testClientsWithSSM(ec2m EC2API, iamm IAMAPI, ssmm SSMAPI) *Clients {
	return &Clients{
		EC2:     ec2m,
		IAM:     iamm,
		SSM:     ssmm,
		STS:     &mockSTSImpl{accountID: "111122223333"},
		Profile: "test",
	}
}

// TestPhase17bJumphost_FeatureGateDisabled verifies zero AWS calls when disabled.
func TestPhase17bJumphost_FeatureGateDisabled(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	// Cluster without testing block.
	cl := testCluster()
	ec2m := &mockEC2{}
	iamm := newMockIAM()
	ssmm := &mockSSM{}

	if err := Phase17bJumphost(context.Background(), cl, st, testClientsWithSSM(ec2m, iamm, ssmm), false); err != nil {
		t.Fatalf("Phase17bJumphost (disabled): %v", err)
	}
	if ec2m.createSGCalls != 0 {
		t.Errorf("feature-gate disabled: createSGCalls = %d, want 0", ec2m.createSGCalls)
	}
	if ssmm.getParameterCalls != 0 {
		t.Errorf("feature-gate disabled: getParameterCalls = %d, want 0", ssmm.getParameterCalls)
	}
}

// TestPhase17bJumphost_DryRun verifies no AWS mutations and all placeholder state keys.
func TestPhase17bJumphost_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := jumphostCluster()
	ec2m := &mockEC2{}
	iamm := newMockIAM()
	ssmm := &mockSSM{}

	if err := Phase17bJumphost(context.Background(), cl, st, testClientsWithSSM(ec2m, iamm, ssmm), true); err != nil {
		t.Fatalf("Phase17bJumphost dry-run: %v", err)
	}

	// Zero AWS mutations.
	if ec2m.createSGCalls != 0 {
		t.Errorf("dry-run: createSGCalls = %d, want 0", ec2m.createSGCalls)
	}
	if ec2m.runInstancesCalls != 0 {
		t.Errorf("dry-run: runInstancesCalls = %d, want 0", ec2m.runInstancesCalls)
	}
	if ec2m.createEICECalls != 0 {
		t.Errorf("dry-run: createEICECalls = %d, want 0", ec2m.createEICECalls)
	}
	if ssmm.getParameterCalls != 0 {
		t.Errorf("dry-run: getParameterCalls = %d, want 0", ssmm.getParameterCalls)
	}

	// Placeholder state values.
	wantKeys := []string{
		"JUMPHOST_INSTANCE_ID",
		"JUMPHOST_MGMT_ENI_ID",
		"JUMPHOST_MGMT_ENI_IP",
		"JUMPHOST_BNK_EXT_ENI_ID",
		"JUMPHOST_BNK_EXT_ENI_IP",
		"JUMPHOST_EICE_ID",
		"JUMPHOST_EICE_SG_ID",
		"JUMPHOST_SG_ID",
		"JUMPHOST_AMI_ID",
		"JUMPHOST_INSTANCE_TYPE",
		"JUMPHOST_INSTANCE_PROFILE_NAME",
	}
	for _, key := range wantKeys {
		if v := st.Get(key); v == "" {
			t.Errorf("dry-run: state key %s is empty, want placeholder", key)
		}
	}
	if got := st.Get("JUMPHOST_INSTANCE_ID"); got != "i-dry-run" {
		t.Errorf("JUMPHOST_INSTANCE_ID = %q, want i-dry-run", got)
	}
}

// TestPhase17bJumphost_MissingPrereqs verifies fail-fast errors for each required
// state key, matching the phase17 table-driven pattern.
func TestPhase17bJumphost_MissingPrereqs(t *testing.T) {
	tests := []struct {
		name      string
		omitKey   string
		wantInErr string
	}{
		{"missing VPC_ID", "VPC_ID", "VPC_ID"},
		{"missing SG_BNK_DATA", "SG_BNK_DATA", "SG_BNK_DATA"},
		{"missing BNK_EXT_SUBNET", "BNK_EXT_SUBNET", "BNK_EXT_SUBNET"},
		{"missing MGMT_SUBNET", "MGMT_SUBNET", "MGMT_SUBNET"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			awsmw.ResetForTest()
			st := stateWithJumphostPrereqs(t)
			st.Set(tc.omitKey, "")
			cl := jumphostCluster()

			err := Phase17bJumphost(context.Background(), cl, st, testClientsWithSSM(&mockEC2{}, newMockIAM(), &mockSSM{}), false)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.omitKey)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q should contain %q", err.Error(), tc.wantInErr)
			}
		})
	}
}

// TestPhase17bJumphost_IdempotentOnRerun verifies that when state already has
// JUMPHOST_INSTANCE_ID pointing to a running instance, no RunInstances or
// CreateInstanceConnectEndpoint calls are made.
func TestPhase17bJumphost_IdempotentOnRerun(t *testing.T) {
	awsmw.ResetForTest()
	st := stateWithJumphostPrereqs(t)
	// Pre-populate all state keys as if a prior run succeeded.
	st.Set("JUMPHOST_INSTANCE_ID", "i-existing-jumphost")
	st.Set("JUMPHOST_MGMT_ENI_ID", "eni-mgmt-existing")
	st.Set("JUMPHOST_MGMT_ENI_IP", "10.0.1.50")
	st.Set("JUMPHOST_BNK_EXT_ENI_ID", "eni-ext-existing")
	st.Set("JUMPHOST_BNK_EXT_ENI_IP", "10.0.10.50")
	st.Set("JUMPHOST_EICE_ID", "eice-existing")
	st.Set("JUMPHOST_EICE_SG_ID", "sg-eice-existing")
	st.Set("JUMPHOST_SG_ID", "sg-jumphost-existing")
	st.Set("JUMPHOST_AMI_ID", "ami-cached")
	st.Set("JUMPHOST_INSTANCE_TYPE", "t3.small")
	st.Set("JUMPHOST_INSTANCE_PROFILE_NAME", "tracer-jumphost-profile")
	st.Set("JUMPHOST_ROLE_NAME", "tracer-jumphost-role")

	cl := jumphostCluster()

	instanceID := "i-existing-jumphost"
	// DescribeInstances: mock returns the instance as running.
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
		// SG describe: returns existing SG for tag-discovery skip.
		describeSGsOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: ptr("sg-jumphost-existing")},
			},
		},
		// EICE describe: returns existing EICE.
		describeEICEsOut: &ec2.DescribeInstanceConnectEndpointsOutput{
			InstanceConnectEndpoints: []ec2types.Ec2InstanceConnectEndpoint{
				{InstanceConnectEndpointId: ptr("eice-existing")},
			},
		},
		// ENI describe: returns existing ext ENI.
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{NetworkInterfaceId: ptr("eni-ext-existing"), PrivateIpAddress: ptr("10.0.10.50")},
			},
		},
	}
	iamm := newMockIAM()
	// Pre-populate role and profile so GetRole/GetInstanceProfile find them (idempotent path).
	roleName := "tracer-jumphost-role"
	roleArn := "arn:aws:iam::111122223333:role/tracer-jumphost-role"
	iamm.roles[roleName] = &iamtypes.Role{RoleName: &roleName, Arn: &roleArn}
	profileName := "tracer-jumphost-profile"
	profileArn := "arn:aws:iam::111122223333:instance-profile/tracer-jumphost-profile"
	iamm.profiles[profileName] = &iamtypes.InstanceProfile{
		InstanceProfileName: &profileName,
		Arn:                 &profileArn,
	}
	ssmm := &mockSSM{}

	if err := Phase17bJumphost(context.Background(), cl, st, testClientsWithSSM(ec2m, iamm, ssmm), false); err != nil {
		t.Fatalf("Phase17bJumphost idempotent: %v", err)
	}

	// No new resources created.
	if ec2m.runInstancesCalls != 0 {
		t.Errorf("idempotent: runInstancesCalls = %d, want 0", ec2m.runInstancesCalls)
	}
	if ec2m.createEICECalls != 0 {
		t.Errorf("idempotent: createEICECalls = %d, want 0", ec2m.createEICECalls)
	}
	if ec2m.createSGCalls != 0 {
		t.Errorf("idempotent: createSGCalls = %d, want 0", ec2m.createSGCalls)
	}
	if ec2m.createENICalls != 0 {
		t.Errorf("idempotent: createENICalls = %d, want 0", ec2m.createENICalls)
	}
}

// TestPhase17bJumphostDown_ToleratesNotFound verifies down succeeds when no
// resources exist (empty state + tag-discovery finds nothing).
func TestPhase17bJumphostDown_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir) // empty state
	cl := jumphostCluster()

	ec2m := &mockEC2{
		describeInstancesOut: &ec2.DescribeInstancesOutput{},
		describeENIsOut:      &ec2.DescribeNetworkInterfacesOutput{},
		describeEICEsOut:     &ec2.DescribeInstanceConnectEndpointsOutput{},
		describeSGsOut:       &ec2.DescribeSecurityGroupsOutput{},
	}
	iamm := newMockIAM()

	if err := Phase17bJumphostDown(context.Background(), cl, st, testClientsWithSSM(ec2m, iamm, &mockSSM{})); err != nil {
		t.Fatalf("Phase17bJumphostDown (not-found): %v", err)
	}
	if ec2m.terminateInstancesCalls != 0 {
		t.Errorf("terminateInstancesCalls = %d, want 0 (nothing to terminate)", ec2m.terminateInstancesCalls)
	}
}

// TestPhase17bJumphostDown_TagDiscoveryFallback verifies that down can find and
// clean up resources even when state.env is empty (tag-discovery fallback).
func TestPhase17bJumphostDown_TagDiscoveryFallback(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir) // empty state — simulates corrupted/pruned state.env
	cl := jumphostCluster()

	instanceID := "i-found-by-tag"
	extENIID := "eni-ext-found"
	eiceID := "eice-found"
	jumphostSGID := "sg-jumphost-found"

	// Tag-discovery returns resources; terminate+delete are all called.
	// DescribeInstances: first call is tag-discovery (returns running), subsequent
	// calls for waitInstanceTerminated return terminated.
	callCount := 0
	_ = callCount
	terminatedState := ec2types.InstanceStateNameTerminated
	runningState := ec2types.InstanceStateNameRunning

	ec2m := &mockEC2{
		// Tag-discovery for instance: returns running instance.
		// Subsequent DescribeInstances (wait) returns terminated.
		describeInstancesOut: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				{
					Instances: []ec2types.Instance{
						{
							InstanceId: &instanceID,
							State:      &ec2types.InstanceState{Name: runningState},
						},
					},
				},
			},
		},
		// ENI describe for tag-discovery.
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{NetworkInterfaceId: &extENIID, Status: ec2types.NetworkInterfaceStatusAvailable},
			},
		},
		// EICE describe for tag-discovery.
		describeEICEsOut: &ec2.DescribeInstanceConnectEndpointsOutput{
			InstanceConnectEndpoints: []ec2types.Ec2InstanceConnectEndpoint{
				{
					InstanceConnectEndpointId: &eiceID,
					State:                     ec2types.Ec2InstanceConnectEndpointStateCreateComplete,
				},
			},
		},
		// SG describe for tag-discovery.
		describeSGsOut: &ec2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{
				{GroupId: &jumphostSGID},
			},
		},
	}

	// Make waitInstanceTerminated return quickly: after TerminateInstances is called,
	// the next DescribeInstances needs to return terminated. We do this by overriding
	// the DescribeInstancesOutput after terminate is called — but our mock is simple.
	// Instead, let terminateInstances succeed and set describeInstancesOut to terminated.
	// The waitInstanceTerminated loop will call DescribeInstances; since the mock
	// returns the same output, we set it to terminated state from the start for the
	// wait to complete in the first poll.
	ec2m.describeInstancesOut = &ec2.DescribeInstancesOutput{
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
	}

	iamm := newMockIAM()

	if err := Phase17bJumphostDown(context.Background(), cl, st, testClientsWithSSM(ec2m, iamm, &mockSSM{})); err != nil {
		t.Fatalf("Phase17bJumphostDown tag-discovery: %v", err)
	}

	// Terminate was called (tag-discovery found the instance).
	if ec2m.terminateInstancesCalls != 1 {
		t.Errorf("terminateInstancesCalls = %d, want 1", ec2m.terminateInstancesCalls)
	}
	// EICE deleted.
	if ec2m.deleteEICECalls != 1 {
		t.Errorf("deleteEICECalls = %d, want 1", ec2m.deleteEICECalls)
	}
	// Jumphost SG deleted.
	if ec2m.deleteSGCalls != 1 {
		t.Errorf("deleteSGCalls = %d, want 1", ec2m.deleteSGCalls)
	}
	// State cleared.
	if got := st.Get("JUMPHOST_INSTANCE_ID"); got != "" {
		t.Errorf("JUMPHOST_INSTANCE_ID = %q after down, want empty", got)
	}
}
