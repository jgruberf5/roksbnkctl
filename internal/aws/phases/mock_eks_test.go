package phases

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// mockEKS is the test double for EKSAPI.
// Maintains in-memory cluster and nodegroup registries with state-machine
// transitions (CREATING → ACTIVE → DELETING → gone).
// Call counts support idempotency and dry-run assertions.
type mockEKS struct {
	// In-memory registries.
	clusters   map[string]*ekstypes.Cluster
	nodegroups map[string]map[string]*ekstypes.Nodegroup  // clusterName → ngName → ng
	addons     map[string]map[string]ekstypes.AddonStatus // clusterName → addonName → status

	// Per-method call counts.
	createClusterCalls   int
	deleteClusterCalls   int
	createNodegroupCalls int
	deleteNodegroupCalls int
	createAddonCalls     int
	describeAddonCalls   int
	updateAddonCalls     int
	deleteAddonCalls     int

	// Capture fields — populated by CreateAddon and UpdateAddon.
	lastAddonConfig      string
	lastResolveConflicts ekstypes.ResolveConflicts

	// Configurable errors.
	createClusterErr   error
	describeClusterErr error
}

func newMockEKS() *mockEKS {
	return &mockEKS{
		clusters:   make(map[string]*ekstypes.Cluster),
		nodegroups: make(map[string]map[string]*ekstypes.Nodegroup),
	}
}

// mkEKSNotFound returns a *ekstypes.ResourceNotFoundException for testing.
func mkEKSNotFound(msg string) error {
	return &ekstypes.ResourceNotFoundException{Message: &msg}
}

func (m *mockEKS) CreateCluster(_ context.Context, in *eks.CreateClusterInput, _ ...func(*eks.Options)) (*eks.CreateClusterOutput, error) {
	m.createClusterCalls++
	if m.createClusterErr != nil {
		return nil, m.createClusterErr
	}
	name := *in.Name
	arn := "arn:aws:eks:ap-southeast-2:111122223333:cluster/" + name
	endpoint := "https://" + name + ".eks.ap-southeast-2.amazonaws.com"
	ca := "dGVzdC1jYQ==" // base64 "test-ca"
	issuer := "https://oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"
	sgID := "sg-" + name
	version := "1.30"
	if in.Version != nil {
		version = *in.Version
	}
	status := ekstypes.ClusterStatusActive // mock immediately transitions to ACTIVE

	c := &ekstypes.Cluster{
		Name:     ptr(name),
		Arn:      &arn,
		Status:   status,
		Endpoint: &endpoint,
		CertificateAuthority: &ekstypes.Certificate{
			Data: &ca,
		},
		Identity: &ekstypes.Identity{
			Oidc: &ekstypes.OIDC{Issuer: &issuer},
		},
		ResourcesVpcConfig: &ekstypes.VpcConfigResponse{
			SubnetIds:              in.ResourcesVpcConfig.SubnetIds,
			ClusterSecurityGroupId: &sgID,
		},
		Tags:    in.Tags,
		Version: &version,
	}
	m.clusters[name] = c
	return &eks.CreateClusterOutput{Cluster: c}, nil
}

func (m *mockEKS) DescribeCluster(_ context.Context, in *eks.DescribeClusterInput, _ ...func(*eks.Options)) (*eks.DescribeClusterOutput, error) {
	if m.describeClusterErr != nil {
		return nil, m.describeClusterErr
	}
	c, ok := m.clusters[*in.Name]
	if !ok {
		return nil, mkEKSNotFound("cluster not found: " + *in.Name)
	}
	return &eks.DescribeClusterOutput{Cluster: c}, nil
}

func (m *mockEKS) DeleteCluster(_ context.Context, in *eks.DeleteClusterInput, _ ...func(*eks.Options)) (*eks.DeleteClusterOutput, error) {
	m.deleteClusterCalls++
	name := *in.Name
	c, ok := m.clusters[name]
	if !ok {
		return nil, mkEKSNotFound("cluster not found: " + name)
	}
	// Transition: remove immediately (mock doesn't need async delete).
	out := &eks.DeleteClusterOutput{Cluster: c}
	delete(m.clusters, name)
	return out, nil
}

func (m *mockEKS) CreateNodegroup(_ context.Context, in *eks.CreateNodegroupInput, _ ...func(*eks.Options)) (*eks.CreateNodegroupOutput, error) {
	m.createNodegroupCalls++
	clusterName := *in.ClusterName
	ngName := *in.NodegroupName
	arn := fmt.Sprintf("arn:aws:eks:ap-southeast-2:111122223333:nodegroup/%s/%s/test", clusterName, ngName)
	status := ekstypes.NodegroupStatusActive // mock immediately ACTIVE

	ng := &ekstypes.Nodegroup{
		ClusterName:    in.ClusterName,
		NodegroupName:  in.NodegroupName,
		NodegroupArn:   &arn,
		Status:         status,
		NodeRole:       in.NodeRole,
		Subnets:        in.Subnets,
		AmiType:        in.AmiType,
		InstanceTypes:  in.InstanceTypes,
		ScalingConfig:  in.ScalingConfig,
		DiskSize:       in.DiskSize,
		Labels:         in.Labels,
		Tags:           in.Tags,
		CapacityType:   in.CapacityType,
		Taints:         in.Taints,
		LaunchTemplate: in.LaunchTemplate,
	}
	if m.nodegroups[clusterName] == nil {
		m.nodegroups[clusterName] = make(map[string]*ekstypes.Nodegroup)
	}
	m.nodegroups[clusterName][ngName] = ng
	return &eks.CreateNodegroupOutput{Nodegroup: ng}, nil
}

func (m *mockEKS) DescribeNodegroup(_ context.Context, in *eks.DescribeNodegroupInput, _ ...func(*eks.Options)) (*eks.DescribeNodegroupOutput, error) {
	clusterNGs, ok := m.nodegroups[*in.ClusterName]
	if !ok {
		return nil, mkEKSNotFound("nodegroup not found: " + *in.NodegroupName)
	}
	ng, ok := clusterNGs[*in.NodegroupName]
	if !ok {
		return nil, mkEKSNotFound("nodegroup not found: " + *in.NodegroupName)
	}
	return &eks.DescribeNodegroupOutput{Nodegroup: ng}, nil
}

func (m *mockEKS) DeleteNodegroup(_ context.Context, in *eks.DeleteNodegroupInput, _ ...func(*eks.Options)) (*eks.DeleteNodegroupOutput, error) {
	m.deleteNodegroupCalls++
	clusterName := *in.ClusterName
	ngName := *in.NodegroupName
	clusterNGs, ok := m.nodegroups[clusterName]
	if !ok {
		return nil, mkEKSNotFound("nodegroup not found: " + ngName)
	}
	ng, ok := clusterNGs[ngName]
	if !ok {
		return nil, mkEKSNotFound("nodegroup not found: " + ngName)
	}
	out := &eks.DeleteNodegroupOutput{Nodegroup: ng}
	delete(clusterNGs, ngName)
	return out, nil
}

// CreateAddon — slice 8 stub. Records the call + tracks addon state per cluster.
func (m *mockEKS) CreateAddon(_ context.Context, in *eks.CreateAddonInput, _ ...func(*eks.Options)) (*eks.CreateAddonOutput, error) {
	m.createAddonCalls++
	clusterName := *in.ClusterName
	addonName := *in.AddonName
	if in.ConfigurationValues != nil {
		m.lastAddonConfig = *in.ConfigurationValues
	}
	m.lastResolveConflicts = in.ResolveConflicts
	if m.addons == nil {
		m.addons = map[string]map[string]ekstypes.AddonStatus{}
	}
	if _, ok := m.addons[clusterName]; !ok {
		m.addons[clusterName] = map[string]ekstypes.AddonStatus{}
	}
	m.addons[clusterName][addonName] = ekstypes.AddonStatusActive
	return &eks.CreateAddonOutput{Addon: &ekstypes.Addon{Status: ekstypes.AddonStatusActive}}, nil
}

// UpdateAddon — records the call, applies the new config, and sets the addon to Active.
func (m *mockEKS) UpdateAddon(_ context.Context, in *eks.UpdateAddonInput, _ ...func(*eks.Options)) (*eks.UpdateAddonOutput, error) {
	m.updateAddonCalls++
	clusterName := *in.ClusterName
	addonName := *in.AddonName
	if in.ConfigurationValues != nil {
		m.lastAddonConfig = *in.ConfigurationValues
	}
	m.lastResolveConflicts = in.ResolveConflicts
	if m.addons == nil {
		m.addons = map[string]map[string]ekstypes.AddonStatus{}
	}
	if _, ok := m.addons[clusterName]; !ok {
		m.addons[clusterName] = map[string]ekstypes.AddonStatus{}
	}
	m.addons[clusterName][addonName] = ekstypes.AddonStatusActive
	return &eks.UpdateAddonOutput{Update: &ekstypes.Update{}}, nil
}

func (m *mockEKS) DescribeAddon(_ context.Context, in *eks.DescribeAddonInput, _ ...func(*eks.Options)) (*eks.DescribeAddonOutput, error) {
	m.describeAddonCalls++
	clusterName := *in.ClusterName
	addonName := *in.AddonName
	if m.addons == nil {
		return nil, mkEKSNotFound("addon not found: " + addonName)
	}
	clusterAddons, ok := m.addons[clusterName]
	if !ok {
		return nil, mkEKSNotFound("addon not found: " + addonName)
	}
	status, ok := clusterAddons[addonName]
	if !ok {
		return nil, mkEKSNotFound("addon not found: " + addonName)
	}
	return &eks.DescribeAddonOutput{Addon: &ekstypes.Addon{Status: status}}, nil
}

func (m *mockEKS) DeleteAddon(_ context.Context, in *eks.DeleteAddonInput, _ ...func(*eks.Options)) (*eks.DeleteAddonOutput, error) {
	m.deleteAddonCalls++
	clusterName := *in.ClusterName
	addonName := *in.AddonName
	if m.addons != nil && m.addons[clusterName] != nil {
		delete(m.addons[clusterName], addonName)
	}
	return &eks.DeleteAddonOutput{}, nil
}
