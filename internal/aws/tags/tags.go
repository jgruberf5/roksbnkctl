// Package tags defines the awsbnkctl tag scheme and helpers.
//
// Every AWS resource created by awsbnkctl carries four mandatory tags:
//
//   - awsbnkctl:cluster=<name>    — identifies the owning cluster
//   - awsbnkctl:component=<comp>  — per-resource category (vpc, subnet-public, …)
//   - awsbnkctl:managed=true      — marks the resource as awsbnkctl-managed
//   - Name=<name>-<comp>          — human-readable AWS console label
//
// Additional tags from cluster.yaml: tags: and metadata.labels: are merged in.
//
// See docs/POST_TERRAFORM_DIRECTION.md §9 for the full tag scheme spec.
package tags

import (
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// Tag key constants.
const (
	KeyCluster   = "awsbnkctl:cluster"
	KeyComponent = "awsbnkctl:component"
	KeyManaged   = "awsbnkctl:managed"
	KeyName      = "Name"
)

// Component values used as the awsbnkctl:component tag value.
const (
	CompVPC           = "vpc"
	CompSubnetPublic  = "subnet-public"
	CompSubnetPrivate = "subnet-private"
	CompIGW           = "igw"
	CompNAT           = "nat"
	CompEIP           = "eip"
	CompRTB           = "rtb"

	// IAM component constants (slice 2).
	CompIAMClusterRole = "iam-cluster-role"
	CompIAMNodeRole    = "iam-node-role"
	CompIAMNodeProfile = "iam-node-profile"

	// EKS component constants (slice 3).
	CompEKSCluster   = "eks-cluster"
	CompEKSNodeGroup = "eks-nodegroup"

	// BNK data-path component constants (slice 7).
	CompSubnetBNKExt   = "subnet-bnk-ext"
	CompSubnetBNKInt   = "subnet-bnk-int"
	CompSGBNKData      = "sg-bnk-data"
	CompLaunchTemplate = "launch-template"
	CompENIInternal    = "eni-internal"
	CompENIExternal    = "eni-external"
	CompOIDCProvider   = "oidc-provider"
	CompIRSARole       = "irsa-cne-controller"

	// Jumphost component constants (slice 12).
	CompJumphostSG       = "jumphost-sg"
	CompJumphostInstance = "jumphost-instance"
	CompJumphostENIExt   = "jumphost-eni-bnk-ext"
	CompJumphostEICE     = "jumphost-eice"
	CompJumphostRole     = "jumphost-iam-role"
	CompJumphostProfile  = "jumphost-instance-profile"
)

// Required returns the four mandatory awsbnkctl tags for a resource.
// clusterName is the value of metadata.name from cluster.yaml.
// component is one of the Comp* constants.
func Required(clusterName, component string) map[string]string {
	return map[string]string{
		KeyCluster:   clusterName,
		KeyComponent: component,
		KeyManaged:   "true",
		KeyName:      clusterName + "-" + component,
	}
}

// Merge combines one or more tag maps into a deduplicated []ec2types.Tag
// slice. Later maps override earlier maps on key conflicts. The mandatory
// awsbnkctl:* keys should be the first map so user-supplied tags cannot
// silently remove them (the four required keys always win last).
//
// Usage:
//
//	Merge(Required(name, CompVPC), cluster.Tags, cluster.Metadata.Labels)
func Merge(maps ...map[string]string) []ec2types.Tag {
	merged := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	out := make([]ec2types.Tag, 0, len(merged))
	for k, v := range merged {
		k, v := k, v
		out = append(out, ec2types.Tag{Key: &k, Value: &v})
	}
	return out
}

// Filter returns an EC2 tag filter that matches resources where key == value.
// Used by phase code to implement list-by-tag idempotency checks.
func Filter(key, value string) ec2types.Filter {
	name := "tag:" + key
	return ec2types.Filter{
		Name:   &name,
		Values: []string{value},
	}
}

// ClusterFilter returns the EC2 filter for the awsbnkctl:cluster=<name> tag.
// This is the primary filter for tag-discovery fallback in down.
func ClusterFilter(clusterName string) ec2types.Filter {
	return Filter(KeyCluster, clusterName)
}

// ComponentFilter returns the EC2 filter for a specific component tag value.
func ComponentFilter(component string) ec2types.Filter {
	return Filter(KeyComponent, component)
}

// IAMTags returns the same merged tag set as Merge but as []iamtypes.Tag,
// which the IAM SDK requires (different type from []ec2types.Tag).
//
// Usage:
//
//	IAMTags(Required(name, CompIAMClusterRole), cluster.Tags, cluster.Metadata.Labels)
func IAMTags(maps ...map[string]string) []iamtypes.Tag {
	merged := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	out := make([]iamtypes.Tag, 0, len(merged))
	for k, v := range merged {
		k, v := k, v
		out = append(out, iamtypes.Tag{Key: &k, Value: &v})
	}
	return out
}

// EKSTags returns the merged tag set as map[string]string for EKS resources.
// EKS CreateCluster and CreateNodegroup accept map[string]string, not a slice.
//
// Usage:
//
//	EKSTags(Required(name, CompEKSCluster), cluster.Tags, cluster.Metadata.Labels)
func EKSTags(maps ...map[string]string) map[string]string {
	merged := make(map[string]string)
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged
}
