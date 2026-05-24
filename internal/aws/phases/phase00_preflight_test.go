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
			{Name: "default", InstanceType: instanceType, DesiredSize: 1, MinSize: 1, MaxSize: 2, DiskSize: 50},
		},
	}
	return cl
}

func makeInstanceTypesOut(typ string, maxENIs int32) *ec2.DescribeInstanceTypesOutput {
	return &ec2.DescribeInstanceTypesOutput{
		InstanceTypes: []ec2types.InstanceTypeInfo{
			{
				InstanceType: ec2types.InstanceType(typ),
				NetworkInfo: &ec2types.NetworkInfo{
					MaximumNetworkInterfaces: aws.Int32(maxENIs),
				},
			},
		},
	}
}

func TestCheckHostDeviceENICapacity_OK(t *testing.T) {
	cl := newHostDeviceCluster("m5.xlarge")
	m := &mockEC2{describeInstanceTypesOut: makeInstanceTypesOut("m5.xlarge", 4)}
	if err := checkHostDeviceENICapacity(context.Background(), cl, m); err != nil {
		t.Fatalf("expected nil error for m5.xlarge (4 ENIs), got %v", err)
	}
}

func TestCheckHostDeviceENICapacity_TooSmall(t *testing.T) {
	cl := newHostDeviceCluster("t3.medium")
	m := &mockEC2{describeInstanceTypesOut: makeInstanceTypesOut("t3.medium", 3)}
	err := checkHostDeviceENICapacity(context.Background(), cl, m)
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

func TestCheckHostDeviceENICapacity_APIError(t *testing.T) {
	cl := newHostDeviceCluster("m5.xlarge")
	m := &mockEC2{describeInstanceTypesErr: errStub("api boom")}
	err := checkHostDeviceENICapacity(context.Background(), cl, m)
	if err == nil {
		t.Fatal("expected error when DescribeInstanceTypes fails, got nil")
	}
	if !strings.Contains(err.Error(), "DescribeInstanceTypes") {
		t.Errorf("error %q does not mention DescribeInstanceTypes", err)
	}
}

func TestCheckHostDeviceENICapacity_NoNodeGroups_OK(t *testing.T) {
	cl := &intent.Cluster{}
	cl.Pattern = "host-device"
	m := &mockEC2{}
	if err := checkHostDeviceENICapacity(context.Background(), cl, m); err != nil {
		t.Fatalf("expected nil when no node groups defined, got %v", err)
	}
}

func TestCheckHostDeviceENICapacity_EmptyInstanceType_OK(t *testing.T) {
	cl := newHostDeviceCluster("")
	m := &mockEC2{}
	if err := checkHostDeviceENICapacity(context.Background(), cl, m); err != nil {
		t.Fatalf("expected nil when instance type is empty (Validate fills default later), got %v", err)
	}
}

// errStub is a tiny error type for tests that need a non-nil error without
// importing fmt every time.
type errStub string

func (e errStub) Error() string { return string(e) }
