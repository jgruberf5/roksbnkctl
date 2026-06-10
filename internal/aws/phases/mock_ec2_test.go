package phases

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithy "github.com/aws/smithy-go"
)

// mockEC2 is the shared test double for EC2API.
// Each method has a configurable result; call counts support idempotency assertions.
type mockEC2 struct {
	// VPC
	describeVpcsOut *ec2.DescribeVpcsOutput
	describeVpcsErr error
	createVpcOut    *ec2.CreateVpcOutput
	createVpcErr    error
	createVpcCalls  int

	// Subnet
	describeSubnetsOut *ec2.DescribeSubnetsOutput
	describeSubnetsErr error
	createSubnetOut    *ec2.CreateSubnetOutput
	createSubnetErr    error
	createSubnetCalls  int

	// IGW
	describeIGWsOut *ec2.DescribeInternetGatewaysOutput
	describeIGWsErr error
	createIGWOut    *ec2.CreateInternetGatewayOutput
	createIGWErr    error
	createIGWCalls  int
	attachIGWCalls  int

	// NAT
	describeNATsOut *ec2.DescribeNatGatewaysOutput
	describeNATsErr error
	createNATOut    *ec2.CreateNatGatewayOutput
	createNATErr    error
	createNATCalls  int

	// EIP
	describeAddrsOut *ec2.DescribeAddressesOutput
	describeAddrsErr error
	allocAddrOut     *ec2.AllocateAddressOutput
	allocAddrErr     error
	allocAddrCalls   int
	releaseAddrCalls int

	// Route table
	describeRTBsOut  *ec2.DescribeRouteTablesOutput
	describeRTBsErr  error
	createRTBOut     *ec2.CreateRouteTableOutput
	createRTBErr     error
	createRTBCalls   int
	createRouteCalls int
	assocRTBCalls    int
	disassocRTBCalls int

	// Security Groups (slice 7+)
	describeSGsOut         *ec2.DescribeSecurityGroupsOutput
	describeSGsErr         error
	createSGOut            *ec2.CreateSecurityGroupOutput
	createSGErr            error
	createSGCalls          int
	deleteSGCalls          int
	deleteSGErr            error
	authorizeIngressCalls  int
	authorizeIngressErr    error
	authorizeIngressInput  *ec2.AuthorizeSecurityGroupIngressInput
	authorizeIngressInputs []*ec2.AuthorizeSecurityGroupIngressInput
	revokeIngressCalls     int
	revokeIngressErr       error
	revokeIngressInputs    []*ec2.RevokeSecurityGroupIngressInput

	// Network Interfaces (slice 7+)
	describeENIsOut   *ec2.DescribeNetworkInterfacesOutput
	describeENIsErr   error
	createENIOut      *ec2.CreateNetworkInterfaceOutput
	createENIErr      error
	createENICalls    int
	deleteENICalls    int
	deleteENIErr      error
	attachENICalls    int
	attachENIErr      error
	detachENICalls    int
	detachENIErr      error
	assignSelfIPCalls int
	assignSelfIPErr   error
	assignedSelfIPs   []string

	modifyENIAttrCalls  int
	modifyENIAttrInputs []*ec2.ModifyNetworkInterfaceAttributeInput

	// Instances (slice 7+)
	describeInstancesOut    *ec2.DescribeInstancesOutput
	describeInstancesErr    error
	runInstancesOut         *ec2.RunInstancesOutput
	runInstancesErr         error
	runInstancesCalls       int
	terminateInstancesErr   error
	terminateInstancesCalls int

	// Launch Templates (slice 7+)
	describeLTsOut *ec2.DescribeLaunchTemplatesOutput
	describeLTsErr error
	createLTOut    *ec2.CreateLaunchTemplateOutput
	createLTErr    error
	createLTCalls  int
	deleteLTCalls  int
	deleteLTErr    error

	// EC2 Instance Connect Endpoints (slice 12+)
	createEICEOut    *ec2.CreateInstanceConnectEndpointOutput
	createEICEErr    error
	createEICECalls  int
	describeEICEsOut *ec2.DescribeInstanceConnectEndpointsOutput
	describeEICEsErr error
	deleteEICECalls  int
	deleteEICEErr    error

	// Images (slice 12+)
	describeImagesOut *ec2.DescribeImagesOutput
	describeImagesErr error

	// Instance Types (slice 13+)
	describeInstanceTypesOut *ec2.DescribeInstanceTypesOutput
	describeInstanceTypesErr error

	// Key Pairs (F2-B1+)
	createKeyPairOut    *ec2.CreateKeyPairOutput
	createKeyPairErr    error
	createKeyPairCalls  int
	createKeyPairNames  []string
	deleteKeyPairCalls  int
	deleteKeyPairErr    error
	deleteKeyPairNames  []string
	describeKeyPairsOut *ec2.DescribeKeyPairsOutput
	describeKeyPairsErr error
}

func (m *mockEC2) DescribeVpcs(_ context.Context, _ *ec2.DescribeVpcsInput, _ ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	if m.describeVpcsOut == nil {
		return &ec2.DescribeVpcsOutput{}, m.describeVpcsErr
	}
	return m.describeVpcsOut, m.describeVpcsErr
}
func (m *mockEC2) CreateVpc(_ context.Context, _ *ec2.CreateVpcInput, _ ...func(*ec2.Options)) (*ec2.CreateVpcOutput, error) {
	m.createVpcCalls++
	return m.createVpcOut, m.createVpcErr
}
func (m *mockEC2) ModifyVpcAttribute(_ context.Context, _ *ec2.ModifyVpcAttributeInput, _ ...func(*ec2.Options)) (*ec2.ModifyVpcAttributeOutput, error) {
	return &ec2.ModifyVpcAttributeOutput{}, nil
}
func (m *mockEC2) DeleteVpc(_ context.Context, _ *ec2.DeleteVpcInput, _ ...func(*ec2.Options)) (*ec2.DeleteVpcOutput, error) {
	return &ec2.DeleteVpcOutput{}, nil
}

func (m *mockEC2) DescribeSubnets(_ context.Context, _ *ec2.DescribeSubnetsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	if m.describeSubnetsOut == nil {
		return &ec2.DescribeSubnetsOutput{}, m.describeSubnetsErr
	}
	return m.describeSubnetsOut, m.describeSubnetsErr
}
func (m *mockEC2) CreateSubnet(_ context.Context, _ *ec2.CreateSubnetInput, _ ...func(*ec2.Options)) (*ec2.CreateSubnetOutput, error) {
	m.createSubnetCalls++
	return m.createSubnetOut, m.createSubnetErr
}
func (m *mockEC2) ModifySubnetAttribute(_ context.Context, _ *ec2.ModifySubnetAttributeInput, _ ...func(*ec2.Options)) (*ec2.ModifySubnetAttributeOutput, error) {
	return &ec2.ModifySubnetAttributeOutput{}, nil
}
func (m *mockEC2) DeleteSubnet(_ context.Context, _ *ec2.DeleteSubnetInput, _ ...func(*ec2.Options)) (*ec2.DeleteSubnetOutput, error) {
	return &ec2.DeleteSubnetOutput{}, nil
}

func (m *mockEC2) DescribeInternetGateways(_ context.Context, _ *ec2.DescribeInternetGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error) {
	if m.describeIGWsOut == nil {
		return &ec2.DescribeInternetGatewaysOutput{}, m.describeIGWsErr
	}
	return m.describeIGWsOut, m.describeIGWsErr
}
func (m *mockEC2) CreateInternetGateway(_ context.Context, _ *ec2.CreateInternetGatewayInput, _ ...func(*ec2.Options)) (*ec2.CreateInternetGatewayOutput, error) {
	m.createIGWCalls++
	return m.createIGWOut, m.createIGWErr
}
func (m *mockEC2) AttachInternetGateway(_ context.Context, _ *ec2.AttachInternetGatewayInput, _ ...func(*ec2.Options)) (*ec2.AttachInternetGatewayOutput, error) {
	m.attachIGWCalls++
	return &ec2.AttachInternetGatewayOutput{}, nil
}
func (m *mockEC2) DetachInternetGateway(_ context.Context, _ *ec2.DetachInternetGatewayInput, _ ...func(*ec2.Options)) (*ec2.DetachInternetGatewayOutput, error) {
	return &ec2.DetachInternetGatewayOutput{}, nil
}
func (m *mockEC2) DeleteInternetGateway(_ context.Context, _ *ec2.DeleteInternetGatewayInput, _ ...func(*ec2.Options)) (*ec2.DeleteInternetGatewayOutput, error) {
	return &ec2.DeleteInternetGatewayOutput{}, nil
}

func (m *mockEC2) DescribeNatGateways(_ context.Context, _ *ec2.DescribeNatGatewaysInput, _ ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error) {
	if m.describeNATsOut == nil {
		return &ec2.DescribeNatGatewaysOutput{}, m.describeNATsErr
	}
	return m.describeNATsOut, m.describeNATsErr
}
func (m *mockEC2) CreateNatGateway(_ context.Context, _ *ec2.CreateNatGatewayInput, _ ...func(*ec2.Options)) (*ec2.CreateNatGatewayOutput, error) {
	m.createNATCalls++
	return m.createNATOut, m.createNATErr
}
func (m *mockEC2) DeleteNatGateway(_ context.Context, _ *ec2.DeleteNatGatewayInput, _ ...func(*ec2.Options)) (*ec2.DeleteNatGatewayOutput, error) {
	return &ec2.DeleteNatGatewayOutput{}, nil
}

func (m *mockEC2) DescribeAddresses(_ context.Context, _ *ec2.DescribeAddressesInput, _ ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	if m.describeAddrsOut == nil {
		return &ec2.DescribeAddressesOutput{}, m.describeAddrsErr
	}
	return m.describeAddrsOut, m.describeAddrsErr
}
func (m *mockEC2) AllocateAddress(_ context.Context, _ *ec2.AllocateAddressInput, _ ...func(*ec2.Options)) (*ec2.AllocateAddressOutput, error) {
	m.allocAddrCalls++
	return m.allocAddrOut, m.allocAddrErr
}
func (m *mockEC2) ReleaseAddress(_ context.Context, _ *ec2.ReleaseAddressInput, _ ...func(*ec2.Options)) (*ec2.ReleaseAddressOutput, error) {
	m.releaseAddrCalls++
	return &ec2.ReleaseAddressOutput{}, nil
}
func (m *mockEC2) CreateTags(_ context.Context, _ *ec2.CreateTagsInput, _ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return &ec2.CreateTagsOutput{}, nil
}

func (m *mockEC2) DescribeRouteTables(_ context.Context, _ *ec2.DescribeRouteTablesInput, _ ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	if m.describeRTBsOut == nil {
		return &ec2.DescribeRouteTablesOutput{}, m.describeRTBsErr
	}
	return m.describeRTBsOut, m.describeRTBsErr
}
func (m *mockEC2) CreateRouteTable(_ context.Context, _ *ec2.CreateRouteTableInput, _ ...func(*ec2.Options)) (*ec2.CreateRouteTableOutput, error) {
	m.createRTBCalls++
	return m.createRTBOut, m.createRTBErr
}
func (m *mockEC2) CreateRoute(_ context.Context, _ *ec2.CreateRouteInput, _ ...func(*ec2.Options)) (*ec2.CreateRouteOutput, error) {
	m.createRouteCalls++
	return &ec2.CreateRouteOutput{}, nil
}
func (m *mockEC2) AssociateRouteTable(_ context.Context, _ *ec2.AssociateRouteTableInput, _ ...func(*ec2.Options)) (*ec2.AssociateRouteTableOutput, error) {
	m.assocRTBCalls++
	assocID := "rtbassoc-mock"
	return &ec2.AssociateRouteTableOutput{AssociationId: &assocID}, nil
}
func (m *mockEC2) DeleteRouteTable(_ context.Context, _ *ec2.DeleteRouteTableInput, _ ...func(*ec2.Options)) (*ec2.DeleteRouteTableOutput, error) {
	return &ec2.DeleteRouteTableOutput{}, nil
}
func (m *mockEC2) DisassociateRouteTable(_ context.Context, _ *ec2.DisassociateRouteTableInput, _ ...func(*ec2.Options)) (*ec2.DisassociateRouteTableOutput, error) {
	m.disassocRTBCalls++
	return &ec2.DisassociateRouteTableOutput{}, nil
}
func (m *mockEC2) DescribeAvailabilityZones(_ context.Context, _ *ec2.DescribeAvailabilityZonesInput, _ ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error) {
	return &ec2.DescribeAvailabilityZonesOutput{}, nil
}

// mockEC2 slice-7 additions: security groups, network interfaces, instances, LTs.

func (m *mockEC2) DescribeSecurityGroups(_ context.Context, _ *ec2.DescribeSecurityGroupsInput, _ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	if m.describeSGsOut == nil {
		return &ec2.DescribeSecurityGroupsOutput{}, m.describeSGsErr
	}
	return m.describeSGsOut, m.describeSGsErr
}
func (m *mockEC2) CreateSecurityGroup(_ context.Context, _ *ec2.CreateSecurityGroupInput, _ ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	m.createSGCalls++
	if m.createSGErr != nil {
		return nil, m.createSGErr
	}
	if m.createSGOut != nil {
		return m.createSGOut, nil
	}
	id := "sg-mock-" + fmt.Sprintf("%d", m.createSGCalls)
	return &ec2.CreateSecurityGroupOutput{GroupId: &id}, nil
}
func (m *mockEC2) DeleteSecurityGroup(_ context.Context, _ *ec2.DeleteSecurityGroupInput, _ ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error) {
	m.deleteSGCalls++
	return &ec2.DeleteSecurityGroupOutput{}, m.deleteSGErr
}
func (m *mockEC2) AuthorizeSecurityGroupIngress(_ context.Context, in *ec2.AuthorizeSecurityGroupIngressInput, _ ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	m.authorizeIngressCalls++
	m.authorizeIngressInput = in
	m.authorizeIngressInputs = append(m.authorizeIngressInputs, in)
	return &ec2.AuthorizeSecurityGroupIngressOutput{}, m.authorizeIngressErr
}
func (m *mockEC2) AuthorizeSecurityGroupEgress(_ context.Context, _ *ec2.AuthorizeSecurityGroupEgressInput, _ ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupEgressOutput, error) {
	return &ec2.AuthorizeSecurityGroupEgressOutput{}, nil
}
func (m *mockEC2) RevokeSecurityGroupIngress(_ context.Context, in *ec2.RevokeSecurityGroupIngressInput, _ ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	m.revokeIngressCalls++
	m.revokeIngressInputs = append(m.revokeIngressInputs, in)
	return &ec2.RevokeSecurityGroupIngressOutput{}, m.revokeIngressErr
}
func (m *mockEC2) DescribeNetworkInterfaces(_ context.Context, _ *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
	if m.describeENIsOut == nil {
		return &ec2.DescribeNetworkInterfacesOutput{}, m.describeENIsErr
	}
	return m.describeENIsOut, m.describeENIsErr
}
func (m *mockEC2) CreateNetworkInterface(_ context.Context, _ *ec2.CreateNetworkInterfaceInput, _ ...func(*ec2.Options)) (*ec2.CreateNetworkInterfaceOutput, error) {
	m.createENICalls++
	if m.createENIErr != nil {
		return nil, m.createENIErr
	}
	if m.createENIOut != nil {
		return m.createENIOut, nil
	}
	id := "eni-mock-" + fmt.Sprintf("%d", m.createENICalls)
	return &ec2.CreateNetworkInterfaceOutput{NetworkInterface: &ec2types.NetworkInterface{NetworkInterfaceId: &id}}, nil
}
func (m *mockEC2) DeleteNetworkInterface(_ context.Context, _ *ec2.DeleteNetworkInterfaceInput, _ ...func(*ec2.Options)) (*ec2.DeleteNetworkInterfaceOutput, error) {
	m.deleteENICalls++
	return &ec2.DeleteNetworkInterfaceOutput{}, m.deleteENIErr
}
func (m *mockEC2) ModifyNetworkInterfaceAttribute(_ context.Context, in *ec2.ModifyNetworkInterfaceAttributeInput, _ ...func(*ec2.Options)) (*ec2.ModifyNetworkInterfaceAttributeOutput, error) {
	m.modifyENIAttrCalls++
	m.modifyENIAttrInputs = append(m.modifyENIAttrInputs, in)
	return &ec2.ModifyNetworkInterfaceAttributeOutput{}, nil
}
func (m *mockEC2) AttachNetworkInterface(_ context.Context, _ *ec2.AttachNetworkInterfaceInput, _ ...func(*ec2.Options)) (*ec2.AttachNetworkInterfaceOutput, error) {
	m.attachENICalls++
	if m.attachENIErr != nil {
		return nil, m.attachENIErr
	}
	attachID := "eni-attach-mock"
	return &ec2.AttachNetworkInterfaceOutput{AttachmentId: &attachID}, nil
}
func (m *mockEC2) DetachNetworkInterface(_ context.Context, _ *ec2.DetachNetworkInterfaceInput, _ ...func(*ec2.Options)) (*ec2.DetachNetworkInterfaceOutput, error) {
	m.detachENICalls++
	return &ec2.DetachNetworkInterfaceOutput{}, m.detachENIErr
}
func (m *mockEC2) AssignPrivateIpAddresses(_ context.Context, in *ec2.AssignPrivateIpAddressesInput, _ ...func(*ec2.Options)) (*ec2.AssignPrivateIpAddressesOutput, error) {
	m.assignSelfIPCalls++
	if in != nil {
		m.assignedSelfIPs = append(m.assignedSelfIPs, in.PrivateIpAddresses...)
	}
	return &ec2.AssignPrivateIpAddressesOutput{}, m.assignSelfIPErr
}
func (m *mockEC2) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	if m.describeInstancesOut == nil {
		return &ec2.DescribeInstancesOutput{}, m.describeInstancesErr
	}
	return m.describeInstancesOut, m.describeInstancesErr
}
func (m *mockEC2) RunInstances(_ context.Context, _ *ec2.RunInstancesInput, _ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	m.runInstancesCalls++
	if m.runInstancesErr != nil {
		return nil, m.runInstancesErr
	}
	if m.runInstancesOut != nil {
		return m.runInstancesOut, nil
	}
	id := "i-mock-jumphost"
	devIdx := int32(0)
	ip := "10.0.1.100"
	eniID := "eni-mock-mgmt"
	return &ec2.RunInstancesOutput{Instances: []ec2types.Instance{
		{
			InstanceId: &id,
			State:      &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
			NetworkInterfaces: []ec2types.InstanceNetworkInterface{
				{
					NetworkInterfaceId: &eniID,
					PrivateIpAddress:   &ip,
					Attachment:         &ec2types.InstanceNetworkInterfaceAttachment{DeviceIndex: &devIdx},
				},
			},
		},
	}}, nil
}
func (m *mockEC2) TerminateInstances(_ context.Context, _ *ec2.TerminateInstancesInput, _ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	m.terminateInstancesCalls++
	return &ec2.TerminateInstancesOutput{}, m.terminateInstancesErr
}
func (m *mockEC2) DescribeLaunchTemplates(_ context.Context, _ *ec2.DescribeLaunchTemplatesInput, _ ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
	if m.describeLTsOut == nil {
		return &ec2.DescribeLaunchTemplatesOutput{}, m.describeLTsErr
	}
	return m.describeLTsOut, m.describeLTsErr
}
func (m *mockEC2) CreateLaunchTemplate(_ context.Context, _ *ec2.CreateLaunchTemplateInput, _ ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error) {
	m.createLTCalls++
	if m.createLTErr != nil {
		return nil, m.createLTErr
	}
	if m.createLTOut != nil {
		return m.createLTOut, nil
	}
	id := "lt-mock-1"
	ver := int64(1)
	return &ec2.CreateLaunchTemplateOutput{LaunchTemplate: &ec2types.LaunchTemplate{LaunchTemplateId: &id, LatestVersionNumber: &ver}}, nil
}
func (m *mockEC2) DeleteLaunchTemplate(_ context.Context, _ *ec2.DeleteLaunchTemplateInput, _ ...func(*ec2.Options)) (*ec2.DeleteLaunchTemplateOutput, error) {
	m.deleteLTCalls++
	return &ec2.DeleteLaunchTemplateOutput{}, m.deleteLTErr
}

// mockEC2 slice-12 additions: EICE, RunInstances, DescribeImages.

func (m *mockEC2) CreateInstanceConnectEndpoint(_ context.Context, _ *ec2.CreateInstanceConnectEndpointInput, _ ...func(*ec2.Options)) (*ec2.CreateInstanceConnectEndpointOutput, error) {
	m.createEICECalls++
	if m.createEICEErr != nil {
		return nil, m.createEICEErr
	}
	if m.createEICEOut != nil {
		return m.createEICEOut, nil
	}
	id := "eice-mock-1"
	sgID := "sg-eice-mock"
	s := ec2types.Ec2InstanceConnectEndpointStateCreateComplete
	return &ec2.CreateInstanceConnectEndpointOutput{
		InstanceConnectEndpoint: &ec2types.Ec2InstanceConnectEndpoint{
			InstanceConnectEndpointId: &id,
			SecurityGroupIds:          []string{sgID},
			State:                     s,
		},
	}, nil
}
func (m *mockEC2) DescribeInstanceConnectEndpoints(_ context.Context, _ *ec2.DescribeInstanceConnectEndpointsInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceConnectEndpointsOutput, error) {
	if m.describeEICEsOut == nil {
		return &ec2.DescribeInstanceConnectEndpointsOutput{}, m.describeEICEsErr
	}
	return m.describeEICEsOut, m.describeEICEsErr
}
func (m *mockEC2) DeleteInstanceConnectEndpoint(_ context.Context, _ *ec2.DeleteInstanceConnectEndpointInput, _ ...func(*ec2.Options)) (*ec2.DeleteInstanceConnectEndpointOutput, error) {
	m.deleteEICECalls++
	return &ec2.DeleteInstanceConnectEndpointOutput{}, m.deleteEICEErr
}
func (m *mockEC2) DescribeImages(_ context.Context, _ *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if m.describeImagesOut == nil {
		return &ec2.DescribeImagesOutput{}, m.describeImagesErr
	}
	return m.describeImagesOut, m.describeImagesErr
}
func (m *mockEC2) DescribeInstanceTypes(_ context.Context, _ *ec2.DescribeInstanceTypesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	if m.describeInstanceTypesOut == nil {
		return &ec2.DescribeInstanceTypesOutput{}, m.describeInstanceTypesErr
	}
	return m.describeInstanceTypesOut, m.describeInstanceTypesErr
}

// mockEC2 F2-B1 additions: key pairs.

func (m *mockEC2) CreateKeyPair(_ context.Context, in *ec2.CreateKeyPairInput, _ ...func(*ec2.Options)) (*ec2.CreateKeyPairOutput, error) {
	m.createKeyPairCalls++
	keyName := "mock-bigip-key"
	if in != nil && in.KeyName != nil {
		keyName = *in.KeyName
	}
	m.createKeyPairNames = append(m.createKeyPairNames, keyName)
	if m.createKeyPairErr != nil {
		return nil, m.createKeyPairErr
	}
	if m.createKeyPairOut != nil {
		return m.createKeyPairOut, nil
	}
	keyID := "key-mock-bigip"
	material := "-----BEGIN RSA PRIVATE KEY-----\nmock-pem-for-test\n-----END RSA PRIVATE KEY-----\n"
	return &ec2.CreateKeyPairOutput{
		KeyName:     &keyName,
		KeyPairId:   &keyID,
		KeyMaterial: &material,
	}, nil
}

func (m *mockEC2) DeleteKeyPair(_ context.Context, in *ec2.DeleteKeyPairInput, _ ...func(*ec2.Options)) (*ec2.DeleteKeyPairOutput, error) {
	m.deleteKeyPairCalls++
	if in != nil && in.KeyName != nil {
		m.deleteKeyPairNames = append(m.deleteKeyPairNames, *in.KeyName)
	}
	return &ec2.DeleteKeyPairOutput{}, m.deleteKeyPairErr
}

func (m *mockEC2) DescribeKeyPairs(_ context.Context, _ *ec2.DescribeKeyPairsInput, _ ...func(*ec2.Options)) (*ec2.DescribeKeyPairsOutput, error) {
	if m.describeKeyPairsOut == nil {
		// Default: return not-found (key does not exist yet).
		return &ec2.DescribeKeyPairsOutput{}, m.describeKeyPairsErr
	}
	return m.describeKeyPairsOut, m.describeKeyPairsErr
}

// mockSTSImpl implements STSAPI for tests.
type mockSTSImpl struct {
	accountID string
	err       error
}

func (m *mockSTSImpl) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput, _ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &sts.GetCallerIdentityOutput{Account: &m.accountID}, m.err
}

// notFoundAPIError implements smithy.APIError with a configurable error code.
type notFoundAPIError struct{ code string }

func (e *notFoundAPIError) Error() string                 { return fmt.Sprintf("api error %s", e.code) }
func (e *notFoundAPIError) ErrorCode() string             { return e.code }
func (e *notFoundAPIError) ErrorMessage() string          { return e.code }
func (e *notFoundAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }
