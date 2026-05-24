package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

func newHostDeviceCluster(instanceType string) *intent.Cluster {
	cl := &intent.Cluster{}
	cl.Pattern = "host-device"
	cl.ClusterSpec = &intent.ClusterSpec{
		NodeGroups: []intent.NodeGroupSpec{
			{Name: "default", InstanceType: instanceType, DesiredSize: 3, MinSize: 1, MaxSize: 5, DiskSize: 50},
		},
	}
	return cl
}

func makeInstanceTypesOut(typ string, maxENIs int32, vcpus int32, memoryMiB int64) *ec2.DescribeInstanceTypesOutput {
	return &ec2.DescribeInstanceTypesOutput{
		InstanceTypes: []ec2types.InstanceTypeInfo{
			{
				InstanceType: ec2types.InstanceType(typ),
				NetworkInfo: &ec2types.NetworkInfo{
					MaximumNetworkInterfaces: aws.Int32(maxENIs),
				},
				VCpuInfo: &ec2types.VCpuInfo{
					DefaultVCpus: aws.Int32(vcpus),
				},
				MemoryInfo: &ec2types.MemoryInfo{
					SizeInMiB: aws.Int64(memoryMiB),
				},
			},
		},
	}
}

func TestCheckHostDeviceCapacity_OK(t *testing.T) {
	cl := newHostDeviceCluster("m5.xlarge")
	m := &mockEC2{describeInstanceTypesOut: makeInstanceTypesOut("m5.xlarge", 4, 16, 65536)}
	if err := checkHostDeviceCapacity(context.Background(), cl, m); err != nil {
		t.Fatalf("expected nil error for m5.xlarge, got %v", err)
	}
}

func TestCheckHostDeviceCapacity_TooSmall(t *testing.T) {
	cl := newHostDeviceCluster("t3.medium")
	m := &mockEC2{describeInstanceTypesOut: makeInstanceTypesOut("t3.medium", 3, 2, 4096)}
	err := checkHostDeviceCapacity(context.Background(), cl, m)
	if err == nil {
		t.Fatal("expected error for t3.medium (3 ENIs), got nil")
	}
	if !strings.Contains(err.Error(), "host-device requires") {
		t.Errorf("error %q does not mention 'host-device requires'", err)
	}
	if !strings.Contains(err.Error(), "m5.xlarge") {
		t.Errorf("error %q does not suggest m5.xlarge", err)
	}
}

func TestCheckHostDeviceCapacity_APIError(t *testing.T) {
	cl := newHostDeviceCluster("m5.xlarge")
	m := &mockEC2{describeInstanceTypesErr: errStub("api boom")}
	err := checkHostDeviceCapacity(context.Background(), cl, m)
	if err == nil {
		t.Fatal("expected error when DescribeInstanceTypes fails, got nil")
	}
	if !strings.Contains(err.Error(), "DescribeInstanceTypes") {
		t.Errorf("error %q does not mention DescribeInstanceTypes", err)
	}
}

func TestCheckHostDeviceCapacity_NoNodeGroups_OK(t *testing.T) {
	cl := &intent.Cluster{}
	cl.Pattern = "host-device"
	m := &mockEC2{}
	if err := checkHostDeviceCapacity(context.Background(), cl, m); err != nil {
		t.Fatalf("expected nil when no node groups defined, got %v", err)
	}
}

func TestCheckHostDeviceCapacity_EmptyInstanceType_OK(t *testing.T) {
	cl := newHostDeviceCluster("")
	m := &mockEC2{}
	if err := checkHostDeviceCapacity(context.Background(), cl, m); err != nil {
		t.Fatalf("expected nil when instance type is empty (Validate fills default later), got %v", err)
	}
}

func TestCheckHostDeviceCapacity_InsufficientCPU(t *testing.T) {
	cl := newHostDeviceCluster("m5.xlarge")
	// 4 ENIs but only 4 vCPUs — should fail on vCPU check.
	m := &mockEC2{describeInstanceTypesOut: makeInstanceTypesOut("m5.xlarge", 4, 4, 65536)}
	err := checkHostDeviceCapacity(context.Background(), cl, m)
	if err == nil {
		t.Fatal("expected error for insufficient vCPUs, got nil")
	}
	if !strings.Contains(err.Error(), "vCPUs") {
		t.Errorf("error %q does not mention 'vCPUs'", err)
	}
	if !strings.Contains(err.Error(), "16") {
		t.Errorf("error %q does not mention required count '16'", err)
	}
}

func TestCheckHostDeviceCapacity_InsufficientMemory(t *testing.T) {
	cl := newHostDeviceCluster("m5.xlarge")
	// 4 ENIs + 16 vCPUs but only 16384 MiB — should fail on memory check.
	m := &mockEC2{describeInstanceTypesOut: makeInstanceTypesOut("m5.xlarge", 4, 16, 16384)}
	err := checkHostDeviceCapacity(context.Background(), cl, m)
	if err == nil {
		t.Fatal("expected error for insufficient memory, got nil")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Errorf("error %q does not mention 'memory'", err)
	}
	if !strings.Contains(err.Error(), "65536") {
		t.Errorf("error %q does not mention required MiB '65536'", err)
	}
}

func TestCheckHostDeviceCapacity_InsufficientDesiredSize(t *testing.T) {
	cl := newHostDeviceCluster("m6i.4xlarge")
	cl.ClusterSpec.NodeGroups[0].DesiredSize = 1
	// 8 ENIs + 16 vCPUs + 64 GiB — all instance checks pass, but desiredSize=1.
	m := &mockEC2{describeInstanceTypesOut: makeInstanceTypesOut("m6i.4xlarge", 8, 16, 65536)}
	err := checkHostDeviceCapacity(context.Background(), cl, m)
	if err == nil {
		t.Fatal("expected error for desiredSize=1, got nil")
	}
	if !strings.Contains(err.Error(), "desiredSize") {
		t.Errorf("error %q does not mention 'desiredSize'", err)
	}
	if !strings.Contains(err.Error(), "3") {
		t.Errorf("error %q does not mention required size '3'", err)
	}
}

func TestCheckHostDeviceCapacity_AllFailures(t *testing.T) {
	// t3.medium equivalent: 3 ENI, 2 vCPU, 4 GiB (4096 MiB), DesiredSize=1.
	cl := newHostDeviceCluster("t3.medium")
	cl.ClusterSpec.NodeGroups[0].DesiredSize = 1
	m := &mockEC2{describeInstanceTypesOut: makeInstanceTypesOut("t3.medium", 3, 2, 4096)}
	err := checkHostDeviceCapacity(context.Background(), cl, m)
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	errStr := err.Error()
	for _, substr := range []string{"network interfaces", "vCPUs", "memory", "desiredSize"} {
		if !strings.Contains(errStr, substr) {
			t.Errorf("aggregated error missing %q; got: %s", substr, errStr)
		}
	}
}

// errStub is a tiny error type for tests that need a non-nil error without
// importing fmt every time.
type errStub string

func (e errStub) Error() string { return string(e) }
