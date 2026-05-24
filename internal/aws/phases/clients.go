// Package phases implements the imperative phased provisioning model for
// awsbnkctl's post-Terraform direction.
//
// Each phase is a top-level function with a consistent signature. Phases are
// called in order by the up/down orchestrators in internal/cli. Phase 01 is
// reserved for IAM (slice 2). Network phases are numbered 02–06.
//
// See docs/POST_TERRAFORM_DIRECTION.md §6–§7 for the full ordering spec.
package phases

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithymw "github.com/aws/smithy-go/middleware"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	k8sclient "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// EC2API is the subset of ec2.Client surface used by the phase functions.
// Tests inject a fake implementation.
type EC2API interface {
	// VPC
	DescribeVpcs(ctx context.Context, in *ec2.DescribeVpcsInput, opts ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error)
	CreateVpc(ctx context.Context, in *ec2.CreateVpcInput, opts ...func(*ec2.Options)) (*ec2.CreateVpcOutput, error)
	ModifyVpcAttribute(ctx context.Context, in *ec2.ModifyVpcAttributeInput, opts ...func(*ec2.Options)) (*ec2.ModifyVpcAttributeOutput, error)
	DeleteVpc(ctx context.Context, in *ec2.DeleteVpcInput, opts ...func(*ec2.Options)) (*ec2.DeleteVpcOutput, error)

	// Subnets
	DescribeSubnets(ctx context.Context, in *ec2.DescribeSubnetsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error)
	CreateSubnet(ctx context.Context, in *ec2.CreateSubnetInput, opts ...func(*ec2.Options)) (*ec2.CreateSubnetOutput, error)
	ModifySubnetAttribute(ctx context.Context, in *ec2.ModifySubnetAttributeInput, opts ...func(*ec2.Options)) (*ec2.ModifySubnetAttributeOutput, error)
	DeleteSubnet(ctx context.Context, in *ec2.DeleteSubnetInput, opts ...func(*ec2.Options)) (*ec2.DeleteSubnetOutput, error)

	// IGW
	DescribeInternetGateways(ctx context.Context, in *ec2.DescribeInternetGatewaysInput, opts ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error)
	CreateInternetGateway(ctx context.Context, in *ec2.CreateInternetGatewayInput, opts ...func(*ec2.Options)) (*ec2.CreateInternetGatewayOutput, error)
	AttachInternetGateway(ctx context.Context, in *ec2.AttachInternetGatewayInput, opts ...func(*ec2.Options)) (*ec2.AttachInternetGatewayOutput, error)
	DetachInternetGateway(ctx context.Context, in *ec2.DetachInternetGatewayInput, opts ...func(*ec2.Options)) (*ec2.DetachInternetGatewayOutput, error)
	DeleteInternetGateway(ctx context.Context, in *ec2.DeleteInternetGatewayInput, opts ...func(*ec2.Options)) (*ec2.DeleteInternetGatewayOutput, error)

	// NAT / EIP
	DescribeNatGateways(ctx context.Context, in *ec2.DescribeNatGatewaysInput, opts ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error)
	CreateNatGateway(ctx context.Context, in *ec2.CreateNatGatewayInput, opts ...func(*ec2.Options)) (*ec2.CreateNatGatewayOutput, error)
	DeleteNatGateway(ctx context.Context, in *ec2.DeleteNatGatewayInput, opts ...func(*ec2.Options)) (*ec2.DeleteNatGatewayOutput, error)
	DescribeAddresses(ctx context.Context, in *ec2.DescribeAddressesInput, opts ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error)
	AllocateAddress(ctx context.Context, in *ec2.AllocateAddressInput, opts ...func(*ec2.Options)) (*ec2.AllocateAddressOutput, error)
	ReleaseAddress(ctx context.Context, in *ec2.ReleaseAddressInput, opts ...func(*ec2.Options)) (*ec2.ReleaseAddressOutput, error)
	CreateTags(ctx context.Context, in *ec2.CreateTagsInput, opts ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)

	// Route tables
	DescribeRouteTables(ctx context.Context, in *ec2.DescribeRouteTablesInput, opts ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error)
	CreateRouteTable(ctx context.Context, in *ec2.CreateRouteTableInput, opts ...func(*ec2.Options)) (*ec2.CreateRouteTableOutput, error)
	CreateRoute(ctx context.Context, in *ec2.CreateRouteInput, opts ...func(*ec2.Options)) (*ec2.CreateRouteOutput, error)
	AssociateRouteTable(ctx context.Context, in *ec2.AssociateRouteTableInput, opts ...func(*ec2.Options)) (*ec2.AssociateRouteTableOutput, error)
	DeleteRouteTable(ctx context.Context, in *ec2.DeleteRouteTableInput, opts ...func(*ec2.Options)) (*ec2.DeleteRouteTableOutput, error)
	DisassociateRouteTable(ctx context.Context, in *ec2.DisassociateRouteTableInput, opts ...func(*ec2.Options)) (*ec2.DisassociateRouteTableOutput, error)

	// STS-like: needed by preflight (caller identity check in phases package)
	DescribeAvailabilityZones(ctx context.Context, in *ec2.DescribeAvailabilityZonesInput, opts ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error)

	// Security Groups (slice 7+)
	DescribeSecurityGroups(ctx context.Context, in *ec2.DescribeSecurityGroupsInput, opts ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error)
	CreateSecurityGroup(ctx context.Context, in *ec2.CreateSecurityGroupInput, opts ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error)
	DeleteSecurityGroup(ctx context.Context, in *ec2.DeleteSecurityGroupInput, opts ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error)
	AuthorizeSecurityGroupIngress(ctx context.Context, in *ec2.AuthorizeSecurityGroupIngressInput, opts ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error)
	AuthorizeSecurityGroupEgress(ctx context.Context, in *ec2.AuthorizeSecurityGroupEgressInput, opts ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupEgressOutput, error)

	// Network Interfaces (slice 7+)
	DescribeNetworkInterfaces(ctx context.Context, in *ec2.DescribeNetworkInterfacesInput, opts ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error)
	CreateNetworkInterface(ctx context.Context, in *ec2.CreateNetworkInterfaceInput, opts ...func(*ec2.Options)) (*ec2.CreateNetworkInterfaceOutput, error)
	DeleteNetworkInterface(ctx context.Context, in *ec2.DeleteNetworkInterfaceInput, opts ...func(*ec2.Options)) (*ec2.DeleteNetworkInterfaceOutput, error)
	ModifyNetworkInterfaceAttribute(ctx context.Context, in *ec2.ModifyNetworkInterfaceAttributeInput, opts ...func(*ec2.Options)) (*ec2.ModifyNetworkInterfaceAttributeOutput, error)
	AttachNetworkInterface(ctx context.Context, in *ec2.AttachNetworkInterfaceInput, opts ...func(*ec2.Options)) (*ec2.AttachNetworkInterfaceOutput, error)
	DetachNetworkInterface(ctx context.Context, in *ec2.DetachNetworkInterfaceInput, opts ...func(*ec2.Options)) (*ec2.DetachNetworkInterfaceOutput, error)
	AssignPrivateIpAddresses(ctx context.Context, in *ec2.AssignPrivateIpAddressesInput, opts ...func(*ec2.Options)) (*ec2.AssignPrivateIpAddressesOutput, error)

	// Instances (slice 7+)
	DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	RunInstances(ctx context.Context, in *ec2.RunInstancesInput, opts ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(ctx context.Context, in *ec2.TerminateInstancesInput, opts ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)

	// Launch Templates (slice 7+)
	DescribeLaunchTemplates(ctx context.Context, in *ec2.DescribeLaunchTemplatesInput, opts ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error)
	CreateLaunchTemplate(ctx context.Context, in *ec2.CreateLaunchTemplateInput, opts ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error)
	DeleteLaunchTemplate(ctx context.Context, in *ec2.DeleteLaunchTemplateInput, opts ...func(*ec2.Options)) (*ec2.DeleteLaunchTemplateOutput, error)

	// EC2 Instance Connect Endpoints (slice 12+)
	CreateInstanceConnectEndpoint(ctx context.Context, in *ec2.CreateInstanceConnectEndpointInput, opts ...func(*ec2.Options)) (*ec2.CreateInstanceConnectEndpointOutput, error)
	DescribeInstanceConnectEndpoints(ctx context.Context, in *ec2.DescribeInstanceConnectEndpointsInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstanceConnectEndpointsOutput, error)
	DeleteInstanceConnectEndpoint(ctx context.Context, in *ec2.DeleteInstanceConnectEndpointInput, opts ...func(*ec2.Options)) (*ec2.DeleteInstanceConnectEndpointOutput, error)

	// Images (slice 12+, defensive for AMI ID audit)
	DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, opts ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)

	// Instance Types (slice 13+, preflight ENI-limit validation for host-device pattern)
	DescribeInstanceTypes(ctx context.Context, in *ec2.DescribeInstanceTypesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error)
}

// STSAPI is the subset of sts.Client used by the preflight phase.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// SSMAPI is the subset of ssm.Client used by Phase17b (AL2023 AMI resolution).
type SSMAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// EKSAPI is the subset of eks.Client surface used by phases 08 and 10.
// Tests inject a fake implementation. Only methods used in slice 3 are listed.
type EKSAPI interface {
	CreateCluster(ctx context.Context, in *eks.CreateClusterInput, opts ...func(*eks.Options)) (*eks.CreateClusterOutput, error)
	DescribeCluster(ctx context.Context, in *eks.DescribeClusterInput, opts ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
	DeleteCluster(ctx context.Context, in *eks.DeleteClusterInput, opts ...func(*eks.Options)) (*eks.DeleteClusterOutput, error)
	CreateNodegroup(ctx context.Context, in *eks.CreateNodegroupInput, opts ...func(*eks.Options)) (*eks.CreateNodegroupOutput, error)
	DescribeNodegroup(ctx context.Context, in *eks.DescribeNodegroupInput, opts ...func(*eks.Options)) (*eks.DescribeNodegroupOutput, error)
	DeleteNodegroup(ctx context.Context, in *eks.DeleteNodegroupInput, opts ...func(*eks.Options)) (*eks.DeleteNodegroupOutput, error)
	CreateAddon(ctx context.Context, in *eks.CreateAddonInput, opts ...func(*eks.Options)) (*eks.CreateAddonOutput, error)
	DescribeAddon(ctx context.Context, in *eks.DescribeAddonInput, opts ...func(*eks.Options)) (*eks.DescribeAddonOutput, error)
	DeleteAddon(ctx context.Context, in *eks.DeleteAddonInput, opts ...func(*eks.Options)) (*eks.DeleteAddonOutput, error)
}

// IAMAPI is the subset of iam.Client surface used by phase07. Tests inject a
// fake implementation. Only methods used in this slice are listed here.
type IAMAPI interface {
	CreateRole(ctx context.Context, in *iam.CreateRoleInput, opts ...func(*iam.Options)) (*iam.CreateRoleOutput, error)
	GetRole(ctx context.Context, in *iam.GetRoleInput, opts ...func(*iam.Options)) (*iam.GetRoleOutput, error)
	DeleteRole(ctx context.Context, in *iam.DeleteRoleInput, opts ...func(*iam.Options)) (*iam.DeleteRoleOutput, error)
	ListAttachedRolePolicies(ctx context.Context, in *iam.ListAttachedRolePoliciesInput, opts ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error)
	AttachRolePolicy(ctx context.Context, in *iam.AttachRolePolicyInput, opts ...func(*iam.Options)) (*iam.AttachRolePolicyOutput, error)
	DetachRolePolicy(ctx context.Context, in *iam.DetachRolePolicyInput, opts ...func(*iam.Options)) (*iam.DetachRolePolicyOutput, error)
	ListRolePolicies(ctx context.Context, in *iam.ListRolePoliciesInput, opts ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error)
	PutRolePolicy(ctx context.Context, in *iam.PutRolePolicyInput, opts ...func(*iam.Options)) (*iam.PutRolePolicyOutput, error)
	DeleteRolePolicy(ctx context.Context, in *iam.DeleteRolePolicyInput, opts ...func(*iam.Options)) (*iam.DeleteRolePolicyOutput, error)
	CreateInstanceProfile(ctx context.Context, in *iam.CreateInstanceProfileInput, opts ...func(*iam.Options)) (*iam.CreateInstanceProfileOutput, error)
	GetInstanceProfile(ctx context.Context, in *iam.GetInstanceProfileInput, opts ...func(*iam.Options)) (*iam.GetInstanceProfileOutput, error)
	DeleteInstanceProfile(ctx context.Context, in *iam.DeleteInstanceProfileInput, opts ...func(*iam.Options)) (*iam.DeleteInstanceProfileOutput, error)
	AddRoleToInstanceProfile(ctx context.Context, in *iam.AddRoleToInstanceProfileInput, opts ...func(*iam.Options)) (*iam.AddRoleToInstanceProfileOutput, error)
	RemoveRoleFromInstanceProfile(ctx context.Context, in *iam.RemoveRoleFromInstanceProfileInput, opts ...func(*iam.Options)) (*iam.RemoveRoleFromInstanceProfileOutput, error)
	ListInstanceProfilesForRole(ctx context.Context, in *iam.ListInstanceProfilesForRoleInput, opts ...func(*iam.Options)) (*iam.ListInstanceProfilesForRoleOutput, error)
	TagRole(ctx context.Context, in *iam.TagRoleInput, opts ...func(*iam.Options)) (*iam.TagRoleOutput, error)
	TagInstanceProfile(ctx context.Context, in *iam.TagInstanceProfileInput, opts ...func(*iam.Options)) (*iam.TagInstanceProfileOutput, error)

	// OIDC provider (slice 7+)
	CreateOpenIDConnectProvider(ctx context.Context, in *iam.CreateOpenIDConnectProviderInput, opts ...func(*iam.Options)) (*iam.CreateOpenIDConnectProviderOutput, error)
	GetOpenIDConnectProvider(ctx context.Context, in *iam.GetOpenIDConnectProviderInput, opts ...func(*iam.Options)) (*iam.GetOpenIDConnectProviderOutput, error)
	DeleteOpenIDConnectProvider(ctx context.Context, in *iam.DeleteOpenIDConnectProviderInput, opts ...func(*iam.Options)) (*iam.DeleteOpenIDConnectProviderOutput, error)
	TagOpenIDConnectProvider(ctx context.Context, in *iam.TagOpenIDConnectProviderInput, opts ...func(*iam.Options)) (*iam.TagOpenIDConnectProviderOutput, error)
}

// Clients bundles the AWS service clients needed by phases in this slice.
// Later slices add EKS/S3 fields here without changing existing phases.
type Clients struct {
	EC2     EC2API
	STS     STSAPI
	IAM     IAMAPI
	EKS     EKSAPI
	SSM     SSMAPI
	Profile string // the AWS profile used — passed to CheckAuthOrDie hints

	// ForgeClient is the forge MCP client used by Phase09. Nil when forge is
	// disabled (cl.Forge == nil || !cl.Forge.Enabled). Set via
	// AttachForgeClient after NewClients to avoid changing the existing
	// NewClients signature.
	ForgeClient *forge.Client

	// K8s is the typed Kubernetes client used by Phase12/13. Nil until
	// AttachK8s is called (after Phase11 writes the kubeconfig). Phase 12
	// returns a clear error if K8s is nil.
	K8s kubernetes.Interface

	// Dynamic is the unstructured Kubernetes client used for cert-manager CRs
	// (Certificate, ClusterIssuer) which are not in the typed client-go scheme.
	// Constructed alongside K8s by AttachK8s.
	Dynamic dynamic.Interface
}

// AttachForgeClient constructs and attaches a forge MCP client when forge is
// enabled in the cluster intent. Call this after NewClients — it is the only
// acceptable addition to the Clients construction path that avoids a signature
// change (see spec gotcha #9 option (c): least invasive approach).
//
// If mcpURL is empty the forge.Client uses its DefaultMCPURL.
func (c *Clients) AttachForgeClient(enabled bool, mcpURL string) {
	if !enabled {
		c.ForgeClient = nil
		return
	}
	c.ForgeClient = forge.NewClient(mcpURL)
}

// AttachK8s builds a typed kubernetes.Interface and a dynamic.Interface from
// the kubeconfig file written by Phase11. Called by runPhasedUp AFTER
// Phase11Kubeconfig completes. Phase 12 returns a clear error if K8s is nil.
//
// Uses internal/k8s.BuildClientset and BuildDynamicClient so the kubeconfig
// resolution logic is consistent with the rest of the k8s package.
func (c *Clients) AttachK8s(kubeconfigPath string) error {
	cs, err := k8sclient.BuildClientset(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("AttachK8s: build typed clientset: %w", err)
	}
	dyn, err := k8sclient.BuildDynamicClient(kubeconfigPath)
	if err != nil {
		return fmt.Errorf("AttachK8s: build dynamic client: %w", err)
	}
	c.K8s = cs
	c.Dynamic = dyn
	return nil
}

// NewClients constructs real AWS SDK clients wrapped with the SSO sentinel
// middleware. Region and Profile are read from the cluster intent by the
// caller — this constructor is the single place the middleware is applied.
func NewClients(ctx context.Context, region, profile string) (*Clients, error) {
	loadOpts := []func(*config.LoadOptions) error{
		config.WithAPIOptions([]func(*smithymw.Stack) error{
			awsmw.WithSSOWatch,
		}),
	}
	if region != "" {
		loadOpts = append(loadOpts, config.WithRegion(region))
	}
	if profile != "" {
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("AWS region is empty; set metadata.region in cluster.yaml or AWS_REGION")
	}

	return &Clients{
		EC2:     ec2.NewFromConfig(cfg),
		STS:     sts.NewFromConfig(cfg),
		IAM:     iam.NewFromConfig(cfg),
		EKS:     eks.NewFromConfig(cfg),
		SSM:     ssm.NewFromConfig(cfg),
		Profile: profile,
	}, nil
}

// ptr returns a pointer to a string — avoids aws.String import at every call
// site within the phases package.
func ptr(s string) *string { return &s }

// boolPtr returns a pointer to a bool.
func boolPtr(b bool) *bool { return &b }

// isNotFoundCode reports whether the smithy error code is one of the EC2
// "already gone" codes that down phases should swallow. See spec §7.
func isNotFoundCode(code string) bool {
	switch code {
	case "InvalidVpcID.NotFound",
		"InvalidSubnetID.NotFound",
		"InvalidRouteTableID.NotFound",
		"InvalidInternetGatewayID.NotFound",
		"InvalidNatGatewayID.NotFound",
		"InvalidAllocationID.NotFound",
		"InvalidNetworkInterfaceID.NotFound",
		"InvalidInstanceID.NotFound",
		"InvalidGroup.NotFound",
		"InvalidInstanceConnectEndpoint.NotFound":
		return true
	}
	return false
}

// ignoreNotFound swallows EC2 "already gone" errors on destroy. Returns nil
// when the error is a known not-found code; otherwise returns err unchanged.
func ignoreNotFound(err error) error {
	if err == nil {
		return nil
	}
	// Extract the smithy APIError code.
	type coder interface{ ErrorCode() string }
	var c coder
	// Walk the error chain.
	e := err
	for e != nil {
		if ce, ok := e.(coder); ok {
			c = ce
			break
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	if c != nil && isNotFoundCode(c.ErrorCode()) {
		return nil
	}
	return err
}

// tagSpecification builds the EC2 TagSpecification for resource creation.
func tagSpecification(resourceType ec2types.ResourceType, tags []ec2types.Tag) ec2types.TagSpecification {
	return ec2types.TagSpecification{
		ResourceType: resourceType,
		Tags:         tags,
	}
}
