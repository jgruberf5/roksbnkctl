// Package intent holds the cluster.yaml schema (v1) and loader.
//
// The canonical format is described in docs/ARCHITECTURE.md.
// Every field maps directly to an AWS resource or provisioning decision —
// there is no intermediate Terraform variable layer.
package intent

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// instanceTypeRE is a loose sanity check for EC2 instance type strings.
// Catches obvious typos (e.g. "T3.small", "t3small") without an exhaustive list.
var instanceTypeRE = regexp.MustCompile(`^[a-z][0-9a-z]+\.[a-z0-9]+$`)

// BnkSpec holds the operator-supplied BNK supply-chain credentials required by
// Phase 12 (k8s install foundation). The bnk: block is optional at schema load
// time (slices 1–4 don't need it); Phase 12 returns a clear error if absent.
//
// certManagerVersion is validated at phase entry to match the pinned embedded
// YAML version (1.16.1). Mismatch → clear error.
type BnkSpec struct {
	// FARArchive is the path to F5's FAR pull credentials JSON file.
	// Type: kubernetes.io/dockerconfigjson. File must be readable + non-empty.
	FARArchive string `yaml:"farArchive"`
	// JWT is the path to F5's subscription JWT file.
	// Type: Opaque, key: license.jwt. File must be readable + non-empty.
	JWT string `yaml:"jwt"`
	// CertManagerVersion pins the embedded cert-manager YAML version.
	// Default "1.16.1". Must match the embedded YAML or phase 12 errors.
	CertManagerVersion string `yaml:"certManagerVersion,omitempty"`

	// --- slice 7 operator-knobs (all optional; defaults match aws-gpu-setup vars.env) ---

	// DeploymentSize is the BNK CNEInstance deployment size. Default "Small".
	// Mirrors aws-gpu-setup DEPLOYMENT_SIZE variable.
	DeploymentSize string `yaml:"deploymentSize,omitempty"`
	// StorageClassName for BNK persistent volumes. Default "gp2" — matches
	// aws-gpu-setup STORAGE_CLASS. Leverages the EKS-default in-tree gp2
	// StorageClass, which is CSI-migrated to ebs.csi.aws.com after the
	// aws-ebs-csi-driver addon is installed (Phase 11b).
	StorageClassName string `yaml:"storageClassName,omitempty"`
	// ManifestVersion is the BNK manifest version pulled by FLO from
	// oci://repo.f5.com/release/f5-bigip-k8s-manifest. Default matches
	// aws-gpu-setup MANIFEST_VERSION — the FLO chart version
	// (v2.21.13-0.0.28) is unrelated to this manifest version.
	ManifestVersion string `yaml:"manifestVersion,omitempty"`
	// TmmMtu is the TMM interface MTU. Default 9000.
	TmmMtu int `yaml:"tmmMtu,omitempty"`
	// TmmCpu is the TMM CPU request (string form for k8s ResourceList). Default "4".
	TmmCpu string `yaml:"tmmCpu,omitempty"`
	// TmmMemory is the TMM memory request. Default "16Gi".
	TmmMemory string `yaml:"tmmMemory,omitempty"`
	// TmmHugepages is the TMM hugepages request. Default "4Gi" — matches
	// aws-gpu-setup TMM_HUGEPAGES + the hugepages-setup DaemonSet which
	// allocates 2048 × 2Mi pages (= 4 GiB) on role=bnk nodes.
	TmmHugepages string `yaml:"tmmHugepages,omitempty"`
	// PalCpuSet is the PAL CPU set string. Default "0-3".
	PalCpuSet string `yaml:"palCpuSet,omitempty"`
}

// DataPathSpec describes the TMM data-plane subnets required by every BNK
// interface pattern. External (BNK_EXT) is the TMM client-side ingress subnet
// and is required by all patterns. Internal (BNK_INT) is the TMM backend-side
// subnet and is used only by pattern: dual-interface; single-interface patterns
// (external-only, sriov-external) leave it empty and reach in-cluster backends
// over CNI.
type DataPathSpec struct {
	External SubnetSpec   `yaml:"external"`           // BNK_EXT — TMM client-side subnet (all patterns)
	Internal SubnetSpec   `yaml:"internal,omitempty"` // BNK_INT — TMM backend-side subnet (dual-interface only)
	SelfIPs  *SelfIPsSpec `yaml:"selfIPs,omitempty"`
}

// SelfIPsSpec carries the TMM SelfIP addresses that get assigned as secondary
// private IPs on each TMM data-plane ENI (Phase 17) and announced inside the
// TMM pod netns via F5SPKVlan CRs (Phase 23b). Per F5 Multi-AZ PDF p.9, AWS
// won't route SelfIPs to the ENI unless they're also listed as secondary IPs
// on the ENI. Auto-derived to <subnet>.240/<prefix> from the data-path
// subnets when omitted (e.g. 10.0.10.0/24 → 10.0.10.240).
type SelfIPsSpec struct {
	External string `yaml:"external,omitempty"`
	Internal string `yaml:"internal,omitempty"`
	// PrefixLen mirrors the subnet prefix length. Default 24.
	PrefixLen int `yaml:"prefixLen,omitempty"`
}

// nodeGroupNameRE enforces Kubernetes label/name rules for node group names:
// lowercase alphanumeric + hyphens, must start/end with alphanumeric.
var nodeGroupNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$`)

// clusterNameRE enforces EKS cluster name rules: lowercase alphanumeric +
// hyphens, 2–40 chars, must start with a letter and end with a letter or digit.
var clusterNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,38}[a-z0-9]$`)

// Cluster is the Go representation of cluster.yaml (apiVersion: awsbnkctl/v1,
// kind: Cluster). Unknown fields are rejected at load time.
type Cluster struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Network    Network  `yaml:"network"`
	// ClusterSpec holds the EKS control plane + node group configuration
	// (slice 3+). Optional for slices 1+2 (network + IAM only). Required
	// when running phases 08+.
	ClusterSpec *ClusterSpec `yaml:"cluster,omitempty"`
	// Pattern selects the BNK data-plane interface topology + binding. One of
	// PatternExternalOnly, PatternDualInterface, PatternSRIOVExternal — or the
	// legacy alias "host-device" (= dual-interface), normalized at Load. Empty
	// means a network-only/tracer cluster (no BNK data plane). See the
	// IsBNKPattern/HasInternalInterface/DataplaneBinding helpers.
	Pattern string `yaml:"pattern,omitempty"`
	// Forge declares the bnk-forge integration shape (slice 4+). Optional;
	// when omitted the new Go-SDK phased path skips the forge handoff
	// silently. Shape inspired by mwiget/kindbnkctl examples/two-node.yaml.
	Forge *ForgeSpec `yaml:"forge,omitempty"`
	// Bnk declares the BNK supply-chain credentials (slice 5+). Optional at
	// schema load time; required when running phase 12+. When present, FARArchive
	// and JWT paths are shape-validated at Load time (files exist + readable);
	// file-content validation (non-empty) is deferred to phase 12 entry.
	Bnk *BnkSpec `yaml:"bnk,omitempty"`
	// Addons declares optional add-on configuration (slice 6+). When absent,
	// all add-ons run with their built-in defaults (FLO enabled at pinned version).
	Addons *AddonsSpec `yaml:"addons,omitempty"`
	// Tags are merged into every AWS resource created by awsbnkctl alongside
	// the required awsbnkctl:* keys.
	Tags map[string]string `yaml:"tags,omitempty"`
	// Testing holds optional test-infrastructure configuration (slice 12+).
	// When absent, no test infrastructure is provisioned (zero AWS calls in Phase 17b).
	Testing *TestingSpec `yaml:"testing,omitempty"`
	// Demo declares this as a demo deployment. When present and enabled, `up`
	// writes DEMO_MODE/DEMO_STAGED_AT/DEMO_EXPIRY to state.env.
	// Omitting the block (or leaving enabled: false) is the default (not a demo).
	Demo *DemoSpec `yaml:"demo,omitempty"`
	// BigIPVE declares a F5 BIG-IP VE appliance for a migration demo (F2+). When
	// present and enabled, a later provisioning slice will create a c5n.2xlarge
	// BIG-IP VE instance + F5 CIS side-by-side with the BNK cluster so operators
	// can demonstrate a live traffic migration path. This slice adds schema +
	// validation only — no AWS calls are made here.
	//
	// Admin password: NEVER stored in cluster.yaml. Supply via the
	// AWSBNKCTL_BIGIP_PASSWORD environment variable at provisioning time.
	BigIPVE *BigIPVESpec `yaml:"bigipVE,omitempty"`
	// AI declares the opt-in AI inference block (PRD-11 M4+). When present and
	// ai.sagemaker.enabled is true, awsbnkctl creates a disposable SageMaker LMI
	// (v15 / vLLM) endpoint on up and deletes it on down so no AI infra bills
	// between sessions. Omitting the block (or setting enabled: false) is the
	// default — all existing cluster.yaml files are unaffected.
	AI *AISpec `yaml:"ai,omitempty"`
}

// EndpointAccessSpec controls who can reach the EKS control-plane API endpoint.
type EndpointAccessSpec struct {
	// PublicAccessCidrs restricts the public endpoint to these CIDRs.
	// Default ["0.0.0.0/0"]. Set to your operator IP (e.g. "203.0.113.10/32") for
	// security hardening. Phase 08 passes the value directly to CreateCluster.
	PublicAccessCidrs []string `yaml:"publicAccessCidrs,omitempty"`
}

// ClusterSpec holds the EKS control plane and node group configuration.
// Corresponds to the `cluster:` block in cluster.yaml.
type ClusterSpec struct {
	// KubernetesVersion is the EKS Kubernetes version to deploy. Default "1.30".
	KubernetesVersion string `yaml:"kubernetesVersion,omitempty"`
	// NodeGroups defines one or more managed node groups. At least one is required
	// when the cluster block is present.
	NodeGroups []NodeGroupSpec `yaml:"nodeGroups,omitempty"`
	// EndpointAccess controls the public-endpoint CIDR allowlist.
	// Omit to accept the default open access (0.0.0.0/0).
	EndpointAccess *EndpointAccessSpec `yaml:"endpointAccess,omitempty"`
}

// NodeGroupSpec configures one managed node group.
type NodeGroupSpec struct {
	// Name is required; used to form the node group name <cluster>-ng-<name>.
	// Must be lowercase alphanumeric + hyphens.
	Name string `yaml:"name"`
	// InstanceType for the Auto Scaling group. Default "t3.medium".
	InstanceType string `yaml:"instanceType,omitempty"`
	// DesiredSize is the initial node count. Default 1.
	DesiredSize int `yaml:"desiredSize,omitempty"`
	// MinSize for the Auto Scaling group. Default 1.
	MinSize int `yaml:"minSize,omitempty"`
	// MaxSize for the Auto Scaling group. Default 2.
	MaxSize int `yaml:"maxSize,omitempty"`
	// DiskSize in GiB for each node's root volume. Default 50.
	DiskSize int `yaml:"diskSize,omitempty"`
	// Labels are additional Kubernetes node labels.
	Labels map[string]string `yaml:"labels,omitempty"`
	// GPU marks this node group as an NVIDIA GPU inference node group. When true,
	// phase10 selects the AL2023 NVIDIA EKS AMI (AL2023_x86_64_NVIDIA), the node
	// group is exempted from the BNK dSSM desiredSize>=3 rule + the BNK AZ pin, and
	// the NVIDIA device-plugin phase targets it. A GPU node group is NEVER a BNK
	// TMM node — it must not carry role=bnk. Default false (existing node groups
	// are non-GPU; backward-compatible).
	GPU bool `yaml:"gpu,omitempty"`
	// CapacityType selects the EC2 purchasing option: "on-demand" (default) or
	// "spot". Maps to EKS CapacityType. Applies to any node group; spot is the
	// demo-appropriate default for GPU rigs.
	CapacityType string `yaml:"capacityType,omitempty"`
	// Taints are Kubernetes node taints applied at node-group creation, e.g.
	// {key: "nvidia.com/gpu", value: "present", effect: "NoSchedule"}. Keeps
	// non-GPU workloads off GPU nodes. Empty for normal node groups.
	Taints []NodeTaintSpec `yaml:"taints,omitempty"`
	// AZs optionally pins this node group's nodes to a subset of availability
	// zones (e.g. ["ap-southeast-2a","ap-southeast-2c"] for g5). Empty = all
	// public-subnet AZs. Independent of the BNK data-path AZ pin.
	AZs []string `yaml:"azs,omitempty"`
	// OnDemandFallback enables an automatic spot→on-demand retry sweep when
	// capacityType is "spot" and ALL candidate AZs have exhausted spot capacity.
	// Default false — no fallback means phase10 fails fast with a clear aggregated
	// error listing every (AZ, spot) attempt, rather than silently switching
	// purchasing options and incurring higher cost. Set true only when continuous
	// availability is more important than cost predictability.
	// Only meaningful for GPU node groups with capacityType: spot.
	OnDemandFallback bool `yaml:"onDemandFallback,omitempty"`
}

// IsGPU reports whether this node group is an NVIDIA GPU inference node group.
func (ng NodeGroupSpec) IsGPU() bool { return ng.GPU }

// NodeTaintSpec is one Kubernetes node taint applied to a managed node group.
type NodeTaintSpec struct {
	Key    string `yaml:"key"`
	Value  string `yaml:"value,omitempty"`
	Effect string `yaml:"effect"` // NoSchedule | PreferNoSchedule | NoExecute
}

// Metadata carries the cluster identity fields.
type Metadata struct {
	// Name is load-bearing: it becomes the awsbnkctl:cluster tag value and the
	// directory name under .awsbnkctl/. Must match clusterNameRE.
	Name   string            `yaml:"name"`
	Region string            `yaml:"region"`
	Labels map[string]string `yaml:"labels,omitempty"`
}

// Network describes the VPC topology the provisioner creates.
type Network struct {
	VPCCidr string   `yaml:"vpcCidr"`
	AZs     []string `yaml:"azs"`
	Subnets Subnets  `yaml:"subnets"`
	// NatGateways is 1 (cost-optimised) or the number of AZs (HA).
	NatGateways int `yaml:"natGateways"`
	// DataPath declares the TMM data-plane subnets (slice 7+).
	// Required by every BNK interface pattern (external subnet always; internal
	// subnet only for dual-interface).
	DataPath *DataPathSpec `yaml:"dataPath,omitempty"`
}

// Subnets groups the public and private subnet definitions.
type Subnets struct {
	Public  []SubnetSpec `yaml:"public"`
	Private []SubnetSpec `yaml:"private"`
}

// SubnetSpec is one CIDR + AZ pair.
type SubnetSpec struct {
	CIDR string `yaml:"cidr"`
	AZ   string `yaml:"az"`
}

// ForgeSpec captures the operator-declared forge integration for slice 4+.
// When Enabled is false (or the whole block is omitted), the phased path
// skips the forge handoff entirely. When Enabled is true, slice 4 registers
// the cluster with a running bnk-forge instance via MCP (preferred) or
// REST (fallback). awsbnkctl NEVER auto-installs forge — if Enabled is
// true and the URL is unreachable, the soft-fail-with-retry path writes
// a `pending` link file and exits 0.
//
// See docs/FORGE_INTEGRATION.md for the handoff details. Shape borrowed
// from mwiget/kindbnkctl's bnk_forge: block (camelCase here to match the
// rest of our schema).
type ForgeSpec struct {
	// Enabled is the master switch. Default false (omitted block = disabled).
	Enabled bool `yaml:"enabled"`
	// URL is the forge REST base. Default http://localhost:8000.
	// Override via AWSBNKCTL_FORGE_URL env (env > yaml > default).
	URL string `yaml:"url,omitempty"`
	// MCPURL is the forge MCP endpoint. Default http://localhost:8081/mcp/.
	// MCP is preferred over REST; falls back to REST at URL on capability gaps.
	MCPURL string `yaml:"mcpUrl,omitempty"`
	// Username is the forge REST login username. Default "admin".
	// Set here or pass --forge-user (flag > yaml > default).
	Username string `yaml:"username,omitempty"`
	// Password is the forge REST login password. Dev-only; discouraged for
	// production — supply via AWSBNKCTL_FORGE_PASSWORD env instead so the
	// value is never written to a checked-in file.
	// Resolution order: AWSBNKCTL_FORGE_PASSWORD env > this field > "changeme".
	// When the built-in default "changeme" is used a one-line warning is emitted.
	Password string `yaml:"password,omitempty"`
	// CredentialTemplateID is the forge credential template to attach to the
	// newly-registered project so forge can `kubectl get` the EKS cluster.
	// If 0/unset, no credential is attached (operator must wire manually).
	// Forge's default "1 AWS Production" template is typically the right value.
	CredentialTemplateID int `yaml:"credentialTemplateId,omitempty"`
}

// DefaultForgeRESTURL is the fallback REST base when forge.url is not set.
const DefaultForgeRESTURL = "http://localhost:8000"

// ResolveURL returns the forge REST URL to use, in priority order:
//  1. AWSBNKCTL_FORGE_URL environment variable
//  2. f.URL (cluster.yaml forge.url)
//  3. DefaultForgeRESTURL ("http://localhost:8000")
func (f *ForgeSpec) ResolveURL() string {
	if v := os.Getenv("AWSBNKCTL_FORGE_URL"); v != "" {
		return v
	}
	if f != nil && f.URL != "" {
		return f.URL
	}
	return DefaultForgeRESTURL
}

// ResolveUsername returns the forge REST login username, in priority order:
//  1. f.Username (cluster.yaml forge.username)
//  2. default "admin"
func (f *ForgeSpec) ResolveUsername() string {
	if f != nil && f.Username != "" {
		return f.Username
	}
	return "admin"
}

// ResolvePassword returns the forge REST login password and whether the
// built-in default was used. Resolution order:
//  1. AWSBNKCTL_FORGE_PASSWORD environment variable
//  2. f.Password (cluster.yaml forge.password — dev-only, discouraged)
//  3. "changeme" (back-compat default; usingDefault=true signals callers to warn)
func (f *ForgeSpec) ResolvePassword() (password string, usingDefault bool) {
	if v := os.Getenv("AWSBNKCTL_FORGE_PASSWORD"); v != "" {
		return v, false
	}
	if f != nil && f.Password != "" {
		return f.Password, false
	}
	return "changeme", true
}

// AddonsSpec holds optional add-on configuration for slice 6+.
// When the block is absent, all add-ons run with their built-in defaults
// (e.g. FLO enabled at the pinned version).
type AddonsSpec struct {
	// Flo configures the F5 Lifecycle Operator installation. When absent,
	// FLO is installed with the pinned chart version.
	Flo *FloSpec `yaml:"flo,omitempty"`
	// LBController configures the AWS Load Balancer Controller installation.
	// When absent (nil), the controller is NOT installed (opt-in; default OFF).
	// Set enabled: true to install the AWS LB Controller for internal NLB support.
	LBController *LBControllerSpec `yaml:"lbController,omitempty"`
}

// TestingSpec holds optional test-infrastructure configuration (slice 12+).
// The entire block is opt-in; omitting it is the default (nothing provisioned).
type TestingSpec struct {
	Jumphost *JumphostSpec `yaml:"jumphost,omitempty"`
}

// JumphostSpec configures the multi-ENI EC2 jumphost provisioned by Phase 17b.
// The jumphost provides a test-traffic vantage point inside the BNK_EXT subnet
// (10.0.10.0/24) so operators can verify TMM SelfIP routing without standing up
// EC2 by hand. Requires pattern: host-device and network.dataPath to be set.
type JumphostSpec struct {
	// Enabled is the master switch. Default false — existing cluster.yaml files
	// that omit the testing: block are unaffected.
	Enabled bool `yaml:"enabled"`
	// InstanceType for the jumphost. Default "t3.small".
	InstanceType string `yaml:"instanceType,omitempty"`
	// MgmtSubnetIndex selects which public subnet to use for the primary ENI.
	// Default 0 (first public subnet, which is MGMT_CIDR = 10.0.1.0/24 in syd-tracer).
	MgmtSubnetIndex int `yaml:"mgmtSubnetIndex,omitempty"`
}

// DemoSpec declares that this cluster is a demo deployment. When Enabled is
// true, `awsbnkctl up` writes DEMO_MODE, DEMO_STAGED_AT, and DEMO_EXPIRY to
// the cluster's state.env before the provisioning phase graph. Demo mode
// requires testing.jumphost.enabled: true — every demo use-case runs a test
// client from the EICE jumphost. The `--demo` CLI flag is syntactic sugar that
// forces Enabled=true without requiring this block.
type DemoSpec struct {
	// Enabled is the master switch. Default false (omitted block = not a demo).
	Enabled bool `yaml:"enabled"`
	// TTL is the lifetime of the demo cluster as a Go duration string (e.g. "24h",
	// "48h"). Default "24h" when Enabled is true and TTL is omitted. Must parse as
	// a positive duration. DEMO_EXPIRY = DEMO_STAGED_AT + TTL.
	TTL string `yaml:"ttl,omitempty"`
}

// BigIPVESpec declares an F5 BIG-IP VE appliance to be provisioned alongside
// the BNK cluster as a migration demo target (feature F2). When Enabled is
// true a later provisioning slice will launch a PAYG BIG-IP VE EC2 instance +
// F5 CIS into the same VPC so operators can demonstrate a live traffic
// migration from BIG-IP to BNK.
//
// Admin password: NEVER add a password field here. The BIG-IP admin password is
// read from the AWSBNKCTL_BIGIP_PASSWORD environment variable at provisioning
// time — it must never be written into a checked-in cluster.yaml.
//
// Requires: pattern: dual-interface (internal subnet needed for the VE's server-
// side NIC), testing.jumphost.enabled: true, demo.enabled: true.
type BigIPVESpec struct {
	// Enabled is the master switch. Default false (omitted block = disabled).
	// WARNING: enabling this provisions a chargeable c5n.2xlarge PAYG BIG-IP VE
	// appliance (~15 min extra onboarding). Set Enabled: true only when you
	// intend to run the full migration demo.
	Enabled bool `yaml:"enabled"`
	// InstanceType for the BIG-IP VE EC2 instance. Default "c5n.2xlarge".
	// Must be an AWS instance type that is available in the BIG-IP PAYG AMI
	// catalog (c5n.2xlarge is the recommended minimum for PAYG Good/Better/Best).
	InstanceType string `yaml:"instanceType,omitempty"`
	// MgmtSubnetIndex selects which public subnet to use for the BIG-IP management
	// ENI (primary interface). Default 0 (first public subnet — same as the
	// jumphost convention). Same semantics as testing.jumphost.mgmtSubnetIndex.
	MgmtSubnetIndex int `yaml:"mgmtSubnetIndex,omitempty"`
	// VIP is the BIG-IP virtual-server address — a secondary private IP that will
	// be assigned to the BIG-IP's external (data-plane) ENI. Default "10.0.10.120".
	// Must be a valid IPv4 host address inside network.dataPath.external.cidr and
	// must not collide with the reserved BNK/TMM addresses in that subnet
	// (.100, .110, .111, .112, .113 BNK VIPs; .200 jumphost ENI; .240 TMM SelfIP).
	VIP string `yaml:"vip,omitempty"`
	// LicenseTier is the PAYG BIG-IP license tier: Good, Better, or Best.
	// Default "Good". Controls which AMI is resolved and which feature set is
	// activated on first boot.
	LicenseTier string `yaml:"licenseTier,omitempty"`
	// Version is an optional glob override for AMI resolution, e.g. "17.5.1*".
	// When empty (default), the newest available BIG-IP VE AMI for the chosen
	// tier is used. No provisioning is performed in this slice — the field is
	// carried here for the later provisioning slice.
	Version string `yaml:"version,omitempty"`
}

// FloSpec configures the FLO (F5 Lifecycle Operator) Helm install in Phase 14.
type FloSpec struct {
	// Version overrides the default pinned chart version ("v2.21.13-0.0.28").
	// Omit to use the default.
	Version string `yaml:"version,omitempty"`
	// Enabled is the master switch. Nil or true means FLO is installed.
	// Explicitly false → Phase 14/15 log a skip and return nil immediately.
	Enabled *bool `yaml:"enabled,omitempty"`
}

// FloEnabled returns true when FLO should be installed. The zero value (nil
// Enabled field) is treated as true so that existing cluster.yaml files without
// an addons: block still get FLO installed.
func (f *FloSpec) FloEnabled() bool {
	if f == nil {
		return true
	}
	if f.Enabled == nil {
		return true
	}
	return *f.Enabled
}

// FLOVersion returns the chart version to install. Falls back to the pinned
// default when not overridden.
const DefaultFLOVersion = "v2.21.13-0.0.28"

func (f *FloSpec) FLOVersion() string {
	if f == nil || f.Version == "" {
		return DefaultFLOVersion
	}
	return f.Version
}

// LBControllerSpec configures the AWS Load Balancer Controller Helm install in Phase 14b.
// Default: disabled (nil receiver or absent block means the controller is NOT installed).
// This is the INVERSE of FloSpec/FloEnabled which defaults ON for backward-compat.
type LBControllerSpec struct {
	// EnabledFlag is the master switch. Default false (nil or absent block = disabled).
	// Set true via "enabled: true" in cluster.yaml to install the AWS LB Controller.
	// Note: the yaml tag is "enabled" for a clean cluster.yaml interface.
	EnabledFlag *bool `yaml:"enabled,omitempty"`
	// Version overrides the default pinned chart version.
	// Omit to use DefaultLBControllerVersion.
	Version string `yaml:"version,omitempty"`
}

// DefaultLBControllerVersion is the pinned AWS Load Balancer Controller Helm chart version.
// Chart 1.8.1 installs controller app v2.8.1. IAM policy vendored from
// https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/v2.8.1/docs/install/iam_policy.json
const DefaultLBControllerVersion = "1.8.1"

// Enabled returns true when the AWS Load Balancer Controller should be installed.
// A nil receiver (absent addons.lbController block) returns FALSE — this is the
// INVERSE of FloEnabled which returns true for nil (backward-compat default-on).
// Phase 14b is opt-in: existing clusters without an lbController block are unaffected.
func (l *LBControllerSpec) Enabled() bool {
	if l == nil {
		return false
	}
	if l.EnabledFlag == nil {
		return false
	}
	return *l.EnabledFlag
}

// LBControllerVersion returns the chart version to install. Falls back to the
// pinned default when not overridden.
func (l *LBControllerSpec) LBControllerVersion() string {
	if l == nil || l.Version == "" {
		return DefaultLBControllerVersion
	}
	return l.Version
}

// BNK interface patterns. A pattern fixes two orthogonal axes of the TMM data
// plane: interface *topology* (single external vs external+internal) and
// interface *binding* (kernel host-device vs SR-IOV/vfio-pci DPDK).
//
//	pattern          topology          binding       backend pods
//	external-only    1 (external)      host-device   CNI
//	dual-interface   2 (ext+internal)  host-device   CNI   (legacy alias: host-device)
//	sriov-external   1 (external)      sriov/vfio     CNI   (experimental, gated)
//
// The phases read intent through the IsBNKPattern/HasInternalInterface/
// DataplaneBinding helpers rather than comparing this string directly, so a new
// preset (e.g. dual+sriov) only needs a row in those helpers.
const (
	// PatternExternalOnly: single external interface for ingress, CNI backend (A).
	PatternExternalOnly = "external-only"
	// PatternDualInterface: external ingress + internal server-side interface, CNI backend (B).
	// This is the topology the codebase has always provisioned.
	PatternDualInterface = "dual-interface"
	// PatternSRIOVExternal: single external interface bound via SR-IOV/vfio-pci
	// DPDK instead of kernel host-device, CNI backend (C). Reserved but gated
	// behind a live ENA/vfio feasibility spike — see validatePattern.
	PatternSRIOVExternal = "sriov-external"
	// PatternHostDevice is the legacy alias for PatternDualInterface. Accepted in
	// cluster.yaml for backward-compat (existing examples/state); normalized to
	// dual-interface at Load so downstream code sees a single canonical value.
	PatternHostDevice = "host-device"
)

// normalizePattern maps the legacy host-device alias onto dual-interface and
// returns the canonical pattern string. Empty stays empty. Idempotent, so it is
// safe to call on already-normalized values (the helpers rely on this).
func normalizePattern(p string) string {
	if p == PatternHostDevice {
		return PatternDualInterface
	}
	return p
}

// IsBNKPattern reports whether the pattern provisions a BNK data plane — any of
// external-only, dual-interface, sriov-external. Empty (network-only/tracer) is
// false. Phases that touch BNK gate on this instead of "== host-device".
func (c *Cluster) IsBNKPattern() bool {
	switch normalizePattern(c.Pattern) {
	case PatternExternalOnly, PatternDualInterface, PatternSRIOVExternal:
		return true
	default:
		return false
	}
}

// HasInternalInterface reports whether a second (internal, server-side) ENI,
// data-path subnet, NetworkAttachmentDefinition and F5SPKVlan should be
// provisioned. True only for dual-interface. Single-interface patterns reach
// in-cluster backends over CNI and have no internal interface.
func (c *Cluster) HasInternalInterface() bool {
	return normalizePattern(c.Pattern) == PatternDualInterface
}

// DataplaneBinding returns how TMM binds its external interface: "host-device"
// (kernel netdevice via Multus) or "sriov" (vfio-pci DPDK). Non-BNK patterns
// return "".
func (c *Cluster) DataplaneBinding() string {
	switch normalizePattern(c.Pattern) {
	case PatternExternalOnly, PatternDualInterface:
		return "host-device"
	case PatternSRIOVExternal:
		return "sriov"
	default:
		return ""
	}
}

// StateDir returns the path to the IDs-cache directory for this cluster
// relative to the caller's working directory. Callers that need an absolute
// path should use filepath.Abs on the result.
func (c *Cluster) StateDir() string {
	return ".awsbnkctl/" + c.Metadata.Name
}

// DefaultVIP returns the default Gateway VIP for this cluster.
// Convention: <dataPath.external.cidr network>.100 — e.g. 10.0.10.0/24 → 10.0.10.100.
// Returns an error when network.dataPath.external.cidr is not set.
func (c *Cluster) DefaultVIP() (string, error) {
	if c.Network.DataPath == nil || c.Network.DataPath.External.CIDR == "" {
		return "", errors.New("network.dataPath.external.cidr not set; pass --vip explicitly")
	}
	cidr := c.Network.DataPath.External.CIDR
	slash := strings.IndexByte(cidr, '/')
	if slash <= 0 {
		return "", fmt.Errorf("malformed dataPath.external.cidr %q", cidr)
	}
	network := cidr[:slash]
	parts := strings.Split(network, ".")
	if len(parts) != 4 {
		return "", fmt.Errorf("non-IPv4 dataPath.external.cidr %q", cidr)
	}
	parts[3] = "100"
	return strings.Join(parts, "."), nil
}

// Load reads and validates a cluster.yaml file at path.
//
// Validation rules:
//   - Unknown fields are errors (strict decoding).
//   - metadata.name must match clusterNameRE.
//   - network.azs must be non-empty.
//   - network.subnets.public and network.subnets.private must be non-empty.
func Load(path string) (*Cluster, error) {
	// #nosec G304 -- path is operator-supplied via --config flag; awsbnkctl is
	// a CLI tool so reading a user-named config file is intentional behaviour,
	// not a directory-traversal risk.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading cluster.yaml %s: %w", path, err)
	}

	var c Cluster
	if err := decodeStrict(data, &c); err != nil {
		return nil, fmt.Errorf("parsing cluster.yaml %s: %w", path, err)
	}

	applyDefaults(&c)
	if err := validate(&c); err != nil {
		return nil, fmt.Errorf("validating cluster.yaml %s: %w", path, err)
	}
	return &c, nil
}

// decodeStrict decodes YAML rejecting unknown fields.
func decodeStrict(data []byte, out interface{}) error {
	dec := yaml.NewDecoder(bytesReader(data))
	dec.KnownFields(true)
	return dec.Decode(out)
}

// bytesReader wraps a byte slice in an io.Reader for yaml.NewDecoder.
type byteReader struct {
	data []byte
	pos  int
}

func bytesReader(data []byte) *byteReader { return &byteReader{data: data} }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// EmbeddedCertManagerVersion is the cert-manager version baked into the binary.
// Phase 12 validates that bnk.certManagerVersion (if set) matches this exactly.
// Why: pinned to match the FLO 2.21.13 dependency surface. Bump alongside DefaultFLOVersion.
const EmbeddedCertManagerVersion = "1.16.1"

// applyDefaults fills in zero-value fields with their documented defaults.
// Called before validate so validation sees the post-default values.
func applyDefaults(c *Cluster) {
	// Normalize the legacy host-device alias to the canonical dual-interface
	// pattern up front so every default below + every downstream phase sees one
	// value. Must run before the IsBNKPattern()/HasInternalInterface() checks.
	c.Pattern = normalizePattern(c.Pattern)

	if c.ClusterSpec != nil {
		if c.ClusterSpec.KubernetesVersion == "" {
			c.ClusterSpec.KubernetesVersion = "1.30"
		}
		for i := range c.ClusterSpec.NodeGroups {
			ng := &c.ClusterSpec.NodeGroups[i]
			if ng.InstanceType == "" {
				// GPU node groups default to g5.2xlarge; they are not BNK TMM nodes.
				// BNK patterns need ≥16 vCPU / ≥64 GB for the full BNK 2.3 Small
				// control plane + TMM packed onto one labeled node, plus enough ENIs
				// for the TMM data-plane secondaries. m6i.4xlarge is the documented
				// minimum per docs/audits/slice-09-aws-gpu-setup-audit.md row 27 and
				// slice-12 audit. Non-BNK (network-only) clusters use smaller workers.
				if ng.IsGPU() {
					ng.InstanceType = "g5.2xlarge"
				} else if c.IsBNKPattern() {
					ng.InstanceType = "m6i.4xlarge"
				} else {
					ng.InstanceType = "t3.medium"
				}
			}
			if ng.DesiredSize == 0 {
				// GPU node groups default to 1 — they are not subject to the BNK
				// dSSM quorum. BNK patterns need ≥3 nodes for dSSM quorum
				// (slice-09 audit row 28, un-deferred 2026-05-24). Non-BNK default 1.
				if !ng.IsGPU() && c.IsBNKPattern() {
					ng.DesiredSize = 3
				} else {
					ng.DesiredSize = 1
				}
			}
			if ng.MinSize == 0 {
				if !ng.IsGPU() && c.IsBNKPattern() {
					ng.MinSize = 3
				} else {
					ng.MinSize = 1
				}
			}
			if ng.MaxSize == 0 {
				if !ng.IsGPU() && c.IsBNKPattern() {
					ng.MaxSize = 4
				} else {
					ng.MaxSize = 2
				}
			}
			if ng.DiskSize == 0 {
				ng.DiskSize = 50
			}
			// CapacityType defaults to "on-demand" for all node groups.
			if ng.CapacityType == "" {
				ng.CapacityType = "on-demand"
			}
		}

		// BNK patterns: auto-inject role=bnk into the first node group's
		// labels if not already set. Phase 16 reads `kubectl get nodes -l role=bnk`
		// to find the TMM-target node — missing this label causes a "no nodes found"
		// failure at Phase 16 entry. Preserve an explicitly-set value.
		// GUARD: skip if NodeGroups[0] is a GPU node group (GPU nodes are NEVER
		// BNK TMM nodes — role=bnk must not be injected onto them).
		if c.IsBNKPattern() && len(c.ClusterSpec.NodeGroups) > 0 &&
			!c.ClusterSpec.NodeGroups[0].IsGPU() {
			ng := &c.ClusterSpec.NodeGroups[0]
			if ng.Labels == nil {
				ng.Labels = make(map[string]string)
			}
			if _, ok := ng.Labels["role"]; !ok {
				ng.Labels["role"] = "bnk"
			}

			// host-device pattern: bump defaults to 3 workers for dSSM quorum.
			// aws-gpu-setup vars.env:110 explicitly requires `BNK_WORKER_COUNT="3"`
			// (≥3 for dSSM quorum per F9). Single-node packs the BNK pod set
			// onto one node which leaves no room for f5-tmm (7-container pod,
			// ~7.6 vCPU requested) and dSSM only reaches 2/3 ready (no quorum).
			// Only bump if the operator left the default (1) — preserve explicit
			// overrides for cost-sensitive lab use. See docs/audits/slice-10.
			if ng.DesiredSize == 1 {
				ng.DesiredSize = 3
			}
			if ng.MinSize == 1 {
				ng.MinSize = 3
			}
			if ng.MaxSize < ng.DesiredSize {
				ng.MaxSize = ng.DesiredSize
			}
		}
	}
	if c.Bnk != nil {
		if c.Bnk.CertManagerVersion == "" {
			c.Bnk.CertManagerVersion = EmbeddedCertManagerVersion
		}
		// Slice-7 BnkSpec defaults.
		if c.Bnk.DeploymentSize == "" {
			c.Bnk.DeploymentSize = "Small"
		}
		if c.Bnk.StorageClassName == "" {
			c.Bnk.StorageClassName = "gp2"
		}
		if c.Bnk.ManifestVersion == "" {
			c.Bnk.ManifestVersion = "2.3.0-3.2598.3-0.0.170"
		}
		if c.Bnk.TmmMtu == 0 {
			c.Bnk.TmmMtu = 9000
		}
		// TMM resource defaults — must match aws-gpu-setup's proven-working values.
		// 2026-05-23 live test on syd-tracer: TmmCpu="4" + PalCpuSet="0-3" caused
		// mapres SIGSEGV at startup ("init.tmm.sh: Segmentation fault (core dumped)").
		// Live-patched CNEInstance with the values below → TMM reached 7/7 Running
		// in 25s. aws-gpu-setup's working syd-test-lab (BNK 2.3.0, AL2023, m6i.4xlarge)
		// uses the same values; FLO renders TMM_MAPRES_HUGEPAGES=1536 (3Gi) which
		// fits the 4Gi hugepages cap. Tokyo's working BNK 2.3 CNEInstance also uses
		// cpu=2/mem=8Gi/PAL=0,2. Don't change without re-validating against a fresh
		// EKS up.
		if c.Bnk.TmmCpu == "" {
			c.Bnk.TmmCpu = "2"
		}
		if c.Bnk.TmmMemory == "" {
			c.Bnk.TmmMemory = "8Gi"
		}
		if c.Bnk.TmmHugepages == "" {
			c.Bnk.TmmHugepages = "4Gi"
		}
		if c.Bnk.PalCpuSet == "" {
			c.Bnk.PalCpuSet = "0,2"
		}
	}

	// testing.jumphost defaults.
	if c.Testing != nil && c.Testing.Jumphost != nil {
		jh := c.Testing.Jumphost
		if jh.InstanceType == "" {
			jh.InstanceType = "t3.small"
		}
		// MgmtSubnetIndex defaults to 0; zero value is already correct.
	}

	// demo defaults: TTL defaults to DefaultDemoTTL when demo mode is enabled.
	if c.Demo != nil && c.Demo.Enabled && c.Demo.TTL == "" {
		c.Demo.TTL = DefaultDemoTTL
	}

	// bigipVE defaults: fill zero fields when the block is present and enabled.
	if c.BigIPVE != nil && c.BigIPVE.Enabled {
		if c.BigIPVE.InstanceType == "" {
			c.BigIPVE.InstanceType = "c5n.2xlarge"
		}
		// MgmtSubnetIndex defaults to 0; zero value is already correct.
		if c.BigIPVE.VIP == "" {
			c.BigIPVE.VIP = "10.0.10.120"
		}
		if c.BigIPVE.LicenseTier == "" {
			c.BigIPVE.LicenseTier = "Good"
		}
		// Version intentionally left empty by default (newest AMI).
	}

	// ai.sagemaker defaults.
	if c.AI != nil && c.AI.SageMaker != nil {
		applySageMakerDefaults(c.AI.SageMaker)
	}

	// BNK patterns: auto-derive TMM SelfIPs as <subnet>.240 when not explicitly
	// set. Matches aws-gpu-setup vars.env (TMM_EXT_SELFIP=10.0.10.240,
	// TMM_INT_SELFIP=10.0.20.240). Per F5 Multi-AZ PDF p.9 these SelfIPs MUST be
	// assigned as secondary IPs on each ENI (Phase 17). The internal SelfIP is
	// only derived for dual-interface — single-interface patterns have no
	// internal ENI.
	if c.IsBNKPattern() && c.Network.DataPath != nil {
		if c.Network.DataPath.SelfIPs == nil {
			c.Network.DataPath.SelfIPs = &SelfIPsSpec{}
		}
		sip := c.Network.DataPath.SelfIPs
		if sip.External == "" {
			if ip, p := DeriveSelfIP(c.Network.DataPath.External.CIDR, 240); ip != "" {
				sip.External = ip
				if sip.PrefixLen == 0 {
					sip.PrefixLen = p
				}
			}
		}
		if c.HasInternalInterface() && sip.Internal == "" {
			if ip, p := DeriveSelfIP(c.Network.DataPath.Internal.CIDR, 240); ip != "" {
				sip.Internal = ip
				if sip.PrefixLen == 0 {
					sip.PrefixLen = p
				}
			}
		}
		if sip.PrefixLen == 0 {
			sip.PrefixLen = 24
		}
	}
}

// DeriveSelfIP returns the host-offset IP and prefix length for a /24 CIDR.
// For non-/24 CIDRs, returns "" and the actual prefix length.
// Example: DeriveSelfIP("10.0.10.0/24", 240) -> ("10.0.10.240", 24).
func DeriveSelfIP(cidr string, hostOffset int) (string, int) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", 0
	}
	prefix, _ := ipnet.Mask.Size()
	base := ipnet.IP.To4()
	if base == nil || prefix != 24 || hostOffset < 1 || hostOffset > 254 {
		return "", prefix
	}
	return net.IPv4(base[0], base[1], base[2], byte(hostOffset)).String(), prefix
}

// validate checks semantic constraints on the loaded cluster.
func validate(c *Cluster) error {
	if !clusterNameRE.MatchString(c.Metadata.Name) {
		return fmt.Errorf("metadata.name %q does not match required pattern %s", c.Metadata.Name, clusterNameRE.String())
	}
	if c.Metadata.Region == "" {
		return fmt.Errorf("metadata.region is required")
	}
	if len(c.Network.AZs) == 0 {
		return fmt.Errorf("network.azs must contain at least one availability zone")
	}
	if len(c.Network.Subnets.Public) == 0 {
		return fmt.Errorf("network.subnets.public must contain at least one subnet")
	}
	if len(c.Network.Subnets.Private) == 0 {
		return fmt.Errorf("network.subnets.private must contain at least one subnet")
	}
	if c.Network.VPCCidr == "" {
		return fmt.Errorf("network.vpcCidr is required")
	}
	if c.ClusterSpec != nil {
		if len(c.ClusterSpec.NodeGroups) == 0 {
			return fmt.Errorf("cluster.nodeGroups must contain at least one node group when cluster block is present")
		}
		for _, ng := range c.ClusterSpec.NodeGroups {
			if !nodeGroupNameRE.MatchString(ng.Name) {
				return fmt.Errorf("cluster.nodeGroups[].name %q must be lowercase alphanumeric + hyphens", ng.Name)
			}
		}
	}
	if c.Bnk != nil {
		if err := validateBnk(c.Bnk); err != nil {
			return err
		}
	}
	if err := validatePattern(c); err != nil {
		return err
	}
	if c.Testing != nil {
		if err := validateTesting(c); err != nil {
			return err
		}
	}
	if c.Demo != nil && c.Demo.Enabled {
		if err := ValidateDemo(c); err != nil {
			return err
		}
	}
	if c.BigIPVE != nil && c.BigIPVE.Enabled {
		if err := validateBigIPVE(c); err != nil {
			return err
		}
	}
	if c.AI != nil && c.AI.SageMaker != nil && c.AI.SageMaker.Enabled {
		if err := validateSageMaker(c.AI.SageMaker); err != nil {
			return err
		}
	}
	if err := validateNodeGroups(c); err != nil {
		return err
	}
	return nil
}

// gpuInstanceAZDeny maps a region to AZs where g5-family GPU instances are NOT
// available, so validation can reject a GPU node group pinned to an AZ with no
// g5 capacity. KNOWN-GAPS table, not an allow-list: regions/AZs absent are
// treated as "available" (fail-open) so a new region needs no code change.
// Override via AWSBNKCTL_GPU_AZ_DENY (format: "region:az1,az2;region2:az3")
// for ad-hoc gaps.
var gpuInstanceAZDeny = map[string][]string{
	// g5 absent from ap-southeast-2b (Sydney) as of 2026-06. 2a + 2c only.
	"ap-southeast-2": {"ap-southeast-2b"},
}

// validateNodeGroups validates GPU node group constraints (AZ gaps, AZ membership).
// Runs for all clusters; non-GPU node groups pass through immediately.
func validateNodeGroups(c *Cluster) error {
	if c.ClusterSpec == nil {
		return nil
	}

	// Build the deny table: env entries MERGE into the static table so a
	// US-region override doesn't silently drop ap-southeast-2→2b and vice versa.
	// Env entries add to / extend per-region lists; a region absent from env
	// keeps its static deny entries (F2 fix).
	denyTable := gpuInstanceAZDeny
	if envVal := os.Getenv("AWSBNKCTL_GPU_AZ_DENY"); envVal != "" {
		envTable := parseGPUAZDenyEnv(envVal)
		merged := make(map[string][]string, len(gpuInstanceAZDeny)+len(envTable))
		for r, azs := range gpuInstanceAZDeny {
			merged[r] = append(merged[r], azs...)
		}
		for r, azs := range envTable {
			merged[r] = append(merged[r], azs...)
		}
		denyTable = merged
	}

	// Build the cluster AZ set for membership checks.
	azSet := make(map[string]bool, len(c.Network.AZs))
	for _, az := range c.Network.AZs {
		azSet[az] = true
	}

	region := c.Metadata.Region

	for i, ng := range c.ClusterSpec.NodeGroups {
		// Validate CapacityType if explicitly set.
		if ng.CapacityType != "" && ng.CapacityType != "on-demand" && ng.CapacityType != "spot" {
			return fmt.Errorf(
				"cluster.nodeGroups[%d] (%s): capacityType %q is not valid (expected on-demand or spot)",
				i, ng.Name, ng.CapacityType,
			)
		}

		// Validate taint effects against the allowed EKS enum set.
		for j, taint := range ng.Taints {
			switch taint.Effect {
			case "NoSchedule", "NoExecute", "PreferNoSchedule":
				// valid
			default:
				return fmt.Errorf(
					"cluster.nodeGroups[%d] (%s) taints[%d]: effect %q is not valid "+
						"(expected NoSchedule, NoExecute, or PreferNoSchedule)",
					i, ng.Name, j, taint.Effect,
				)
			}
		}

		if !ng.IsGPU() || len(ng.AZs) == 0 {
			continue
		}
		deniedAZs := denyTable[region]
		deniedSet := make(map[string]bool, len(deniedAZs))
		for _, az := range deniedAZs {
			deniedSet[az] = true
		}

		// Build available AZ list (region AZs minus denied) for error messages.
		var available []string
		for _, az := range c.Network.AZs {
			if !deniedSet[az] {
				available = append(available, az)
			}
		}

		for _, az := range ng.AZs {
			// Check AZ is in network.azs.
			if !azSet[az] {
				return fmt.Errorf(
					"cluster.nodeGroups[%d] (%s): az %q is not in network.azs %v",
					i, ng.Name, az, c.Network.AZs,
				)
			}
			// Check AZ is not in the deny list.
			if deniedSet[az] {
				return fmt.Errorf(
					"cluster.nodeGroups[%d] (%s) pins az %q which has no g5/GPU "+
						"capacity in region %s (available: %v); "+
						"remove it from nodeGroups[%d].azs or set AWSBNKCTL_GPU_AZ_DENY to override",
					i, ng.Name, az, region, available, i,
				)
			}
		}
	}
	return nil
}

// parseGPUAZDenyEnv parses AWSBNKCTL_GPU_AZ_DENY in the format
// "region:az1,az2;region2:az3" into the same map shape as gpuInstanceAZDeny.
// Malformed entries are silently skipped (fail-open).
func parseGPUAZDenyEnv(val string) map[string][]string {
	result := make(map[string][]string)
	for _, entry := range strings.Split(val, ";") {
		parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
		if len(parts) != 2 {
			continue
		}
		region := strings.TrimSpace(parts[0])
		for _, az := range strings.Split(parts[1], ",") {
			az = strings.TrimSpace(az)
			if az != "" {
				result[region] = append(result[region], az)
			}
		}
	}
	return result
}

// validatePattern checks pattern-specific constraints.
//
//   - Empty pattern (network-only/tracer) is always valid.
//   - The value must be a recognised BNK pattern (or the host-device alias,
//     already normalized to dual-interface by applyDefaults).
//   - sriov-external is reserved but gated behind the ENA/vfio spike → hard error.
//   - All BNK patterns require network.dataPath.external{cidr,az}.
//   - dual-interface additionally requires network.dataPath.internal{cidr,az};
//     single-interface patterns must NOT set an internal block.
//   - Every referenced AZ must appear in network.azs.
//   - All BNK patterns require desiredSize >= 3 (dSSM quorum).
func validatePattern(c *Cluster) error {
	if c.Pattern == "" {
		return nil
	}
	if !c.IsBNKPattern() {
		return fmt.Errorf("pattern %q is not a recognised value "+
			"(expected one of: external-only, dual-interface, sriov-external; or the host-device alias)", c.Pattern)
	}
	// sriov-external is EXPERIMENTAL. The SR-IOV/vfio-pci DPDK substrate is proven
	// live on AL2023 EKS nodes (ENA→vfio-pci No-IOMMU, clean testpmd RX+TX). TMM's
	// own vfio dataplane on the Host build is still being validated. Warn loudly
	// but allow it through so it can
	// be exercised end-to-end.
	if normalizePattern(c.Pattern) == PatternSRIOVExternal {
		fmt.Fprintln(os.Stderr, "[warn] pattern sriov-external is EXPERIMENTAL: SR-IOV/vfio-pci DPDK dataplane "+
			"(vfio substrate proven; TMM-on-vfio under validation)")
	}

	// All BNK patterns need the external data-path subnet.
	if c.Network.DataPath == nil {
		return fmt.Errorf("pattern %s requires network.dataPath (external subnet) to be set", c.Pattern)
	}
	dp := c.Network.DataPath
	if dp.External.CIDR == "" || dp.External.AZ == "" {
		return fmt.Errorf("pattern %s requires network.dataPath.external.{cidr,az}", c.Pattern)
	}
	if err := requireSlash24(dp.External.CIDR, "network.dataPath.external.cidr"); err != nil {
		return err
	}
	azSet := make(map[string]bool, len(c.Network.AZs))
	for _, az := range c.Network.AZs {
		azSet[az] = true
	}
	if !azSet[dp.External.AZ] {
		return fmt.Errorf("network.dataPath.external.az %q is not in network.azs %v", dp.External.AZ, c.Network.AZs)
	}

	// Internal subnet: required for dual-interface, forbidden for single-interface
	// patterns (where it would be silently ignored otherwise).
	if c.HasInternalInterface() {
		if dp.Internal.CIDR == "" || dp.Internal.AZ == "" {
			return fmt.Errorf("pattern dual-interface requires network.dataPath.internal.{cidr,az}")
		}
		if err := requireSlash24(dp.Internal.CIDR, "network.dataPath.internal.cidr"); err != nil {
			return err
		}
		if !azSet[dp.Internal.AZ] {
			return fmt.Errorf("network.dataPath.internal.az %q is not in network.azs %v", dp.Internal.AZ, c.Network.AZs)
		}
	} else if dp.Internal.CIDR != "" || dp.Internal.AZ != "" {
		return fmt.Errorf("pattern %s is single-interface; remove network.dataPath.internal "+
			"(the internal subnet is only used by pattern: dual-interface)", c.Pattern)
	}

	// dSSM quorum applies to every BNK pattern, but NOT GPU node groups.
	// GPU node groups are inference workers, not BNK quorum members.
	if c.ClusterSpec != nil {
		for i, ng := range c.ClusterSpec.NodeGroups {
			if ng.IsGPU() {
				continue // GPU node groups are exempt from dSSM quorum rule
			}
			if ng.DesiredSize > 0 && ng.DesiredSize < 3 {
				return fmt.Errorf(
					"pattern %s requires cluster.nodeGroups[%d].desiredSize >= 3 (dSSM quorum), got %d. "+
						"See docs/audits/slice-12-cold-start-audit.md",
					c.Pattern, i, ng.DesiredSize,
				)
			}
		}
	}
	return nil
}

// requireSlash24 rejects data-path CIDRs that are not exactly IPv4 /24.
// SelfIP derivation (DeriveSelfIP), the BIG-IP VE reserved-offset table,
// phase 17's host-offset math and phase17f's "address <ip>/24" self-IP
// rendering all assume a /24 host part — a non-/24 CIDR would validate
// here and then fail (or silently misbehave) deep in provisioning.
func requireSlash24(cidr, field string) error {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return fmt.Errorf("%s %q is not a valid CIDR: %w", field, cidr, err)
	}
	if ones, bits := ipnet.Mask.Size(); bits != 32 || ones != 24 {
		return fmt.Errorf("%s %q: currently only /24 dataPath subnets are supported (got /%d)", field, cidr, ones)
	}
	return nil
}

// validateTesting checks constraints on the testing: block.
//
//   - testing.jumphost.enabled=true requires pattern: host-device AND network.dataPath.
//   - instanceType must match the loose instanceTypeRE when set.
//   - mgmtSubnetIndex must be a valid index into network.subnets.public.
func validateTesting(c *Cluster) error {
	if c.Testing.Jumphost == nil {
		return nil
	}
	jh := c.Testing.Jumphost
	if jh.Enabled {
		if !c.IsBNKPattern() || c.Network.DataPath == nil {
			return fmt.Errorf("testing.jumphost requires network.dataPath (BNK_EXT subnet) " +
				"which is only created for a BNK interface pattern (external-only, dual-interface)")
		}
	}
	if jh.InstanceType != "" && !instanceTypeRE.MatchString(jh.InstanceType) {
		return fmt.Errorf("testing.jumphost.instanceType %q does not match expected pattern (e.g. t3.small, m5.large)", jh.InstanceType)
	}
	if idx := jh.MgmtSubnetIndex; idx < 0 || idx >= len(c.Network.Subnets.Public) {
		return fmt.Errorf("testing.jumphost.mgmtSubnetIndex %d is out of range (network.subnets.public has %d entries)",
			idx, len(c.Network.Subnets.Public))
	}
	return nil
}

// validateBnk shape-validates the bnk: block. File-content validation (non-empty
// check, dockerconfigjson parse) is deferred to phase 12 entry so operators can
// construct and validate cluster.yaml without having the supply-chain files
// present (e.g. during dry-run prep on a laptop before a lab session).
//
// Rules:
//   - farArchive must be a non-empty path string
//   - jwt must be a non-empty path string
//   - certManagerVersion must match the embedded YAML version (1.16.1)
func validateBnk(b *BnkSpec) error {
	if b.FARArchive == "" {
		return fmt.Errorf("bnk.farArchive is required when the bnk: block is present")
	}
	if b.JWT == "" {
		return fmt.Errorf("bnk.jwt is required when the bnk: block is present")
	}
	if b.CertManagerVersion != "" && b.CertManagerVersion != EmbeddedCertManagerVersion {
		return fmt.Errorf("bnk.certManagerVersion %q does not match the embedded cert-manager version %q; "+
			"slice 6+ may add multi-version support — for now omit the field to use the default",
			b.CertManagerVersion, EmbeddedCertManagerVersion)
	}
	return nil
}

// DefaultDemoTTL is the TTL applied when demo mode is enabled and no explicit
// TTL is provided — either by applyDefaults (config-block path) or by EnableDemo
// (--demo flag path).
const DefaultDemoTTL = "24h"

// DemoEnabled reports whether demo mode is active on this cluster.
// True when c.Demo is non-nil and c.Demo.Enabled is true.
func (c *Cluster) DemoEnabled() bool {
	return c.Demo != nil && c.Demo.Enabled
}

// BigIPVEEnabled reports whether the BIG-IP VE appliance is enabled.
// True when c.BigIPVE is non-nil and c.BigIPVE.Enabled is true.
func (c *Cluster) BigIPVEEnabled() bool {
	return c.BigIPVE != nil && c.BigIPVE.Enabled
}

// HasGPUNodeGroup reports whether any node group is a GPU node group.
// Used to gate the NVIDIA device-plugin phase (clean skip when false).
func (c *Cluster) HasGPUNodeGroup() bool {
	if c.ClusterSpec == nil {
		return false
	}
	for _, ng := range c.ClusterSpec.NodeGroups {
		if ng.IsGPU() {
			return true
		}
	}
	return false
}

// bigipVEReservedOffsets lists the host-part offsets that are already allocated
// in the external data-path subnet and may not be used as the BIG-IP VIP.
//
//	.50  — BIG-IP VE ENI primary IP (phase 17e hardcodes .50 on mgmt/ext/int)
//	.100 — default BNK Gateway VIP (DefaultVIP)
//	.110 — Diameter demo VIP (scnVIP)
//	.111 — HTTP/2 demo VIP
//	.112 — gRPC demo VIP
//	.113 — additional BNK demo VIP
//	.200 — jumphost external ENI secondary IP
//	.240 — TMM SelfIP (auto-derived by applyDefaults)
var bigipVEReservedOffsets = []int{50, 100, 110, 111, 112, 113, 200, 240}

// validateBigIPVE enforces the bigipVE: block constraints. Called from validate()
// only when BigIPVE != nil && BigIPVE.Enabled.
//
// Rules:
//  1. Requires pattern: dual-interface (internal subnet for the VE's backend NIC).
//  2. Requires testing.jumphost.enabled: true.
//  3. Requires demo.enabled: true.
//  4. InstanceType must match instanceTypeRE.
//  5. LicenseTier must be one of Good | Better | Best.
//  6. VIP must be a valid IPv4 host address inside network.dataPath.external.cidr
//     and must not equal any reserved address in that subnet.
//  7. MgmtSubnetIndex must be a valid index into network.subnets.public.
func validateBigIPVE(c *Cluster) error {
	ve := c.BigIPVE

	// Rule 1: dual-interface only (internal subnet needed for VE server-side NIC).
	if !c.HasInternalInterface() {
		return fmt.Errorf("bigipVE requires pattern: dual-interface "+
			"(the BIG-IP VE needs the internal subnet for its server-side NIC); "+
			"current pattern %q does not have an internal interface — "+
			"set pattern: dual-interface in cluster.yaml",
			c.Pattern)
	}

	// Rule 2: jumphost required (onboarding readiness poll + traffic probe).
	if c.Testing == nil || c.Testing.Jumphost == nil || !c.Testing.Jumphost.Enabled {
		return fmt.Errorf("bigipVE requires testing.jumphost.enabled: true " +
			"(the migration demo uses the jumphost for traffic probes); " +
			"add testing.jumphost.enabled: true to cluster.yaml")
	}

	// Rule 3: demo mode required (BIG-IP VE is a demo-only appliance).
	if c.Demo == nil || !c.Demo.Enabled {
		return fmt.Errorf("bigipVE requires demo.enabled: true " +
			"(the BIG-IP VE is a demo-only appliance); " +
			"add demo.enabled: true to cluster.yaml")
	}

	// Rule 4: instanceType format.
	if !instanceTypeRE.MatchString(ve.InstanceType) {
		return fmt.Errorf("bigipVE.instanceType %q does not match expected pattern (e.g. c5n.2xlarge, m5.xlarge)", ve.InstanceType)
	}

	// Rule 5: licenseTier membership.
	switch ve.LicenseTier {
	case "Good", "Better", "Best":
		// valid
	default:
		return fmt.Errorf("bigipVE.licenseTier %q is not valid; must be one of: Good, Better, Best", ve.LicenseTier)
	}

	// Rule 6: VIP must be inside the external CIDR and not reserved.
	if c.Network.DataPath == nil || c.Network.DataPath.External.CIDR == "" {
		return fmt.Errorf("bigipVE.vip validation requires network.dataPath.external.cidr to be set")
	}
	extCIDR := c.Network.DataPath.External.CIDR
	_, extNet, err := net.ParseCIDR(extCIDR)
	if err != nil {
		return fmt.Errorf("network.dataPath.external.cidr %q is not a valid CIDR: %w", extCIDR, err)
	}
	vipIP := net.ParseIP(ve.VIP)
	if vipIP == nil {
		return fmt.Errorf("bigipVE.vip %q is not a valid IPv4 address", ve.VIP)
	}
	if !extNet.Contains(vipIP) {
		return fmt.Errorf("bigipVE.vip %q is not inside network.dataPath.external.cidr %s", ve.VIP, extCIDR)
	}
	// AWS reserves the first four host addresses (.0-.3: network, VPC router,
	// DNS, future use) and the last (.255 broadcast) in every subnet — a VIP
	// at any of these would validate here then fail deep in provisioning.
	vip4 := vipIP.To4()
	if vip4 == nil {
		return fmt.Errorf("bigipVE.vip %q is not a valid IPv4 address", ve.VIP)
	}
	if off := int(vip4[3]); off <= 3 || off == 255 {
		return fmt.Errorf("bigipVE.vip %q uses host offset .%d, which AWS reserves in every subnet "+
			"(.0-.3 and .255 are unusable); choose a different host address", ve.VIP, off)
	}
	// Build reserved IP set for the error message.
	base := extNet.IP.To4()
	if base == nil {
		return fmt.Errorf("network.dataPath.external.cidr %q is not an IPv4 network", extCIDR)
	}
	var reserved []string
	for _, off := range bigipVEReservedOffsets {
		rip := net.IPv4(base[0], base[1], base[2], byte(off)).String() // #nosec G115 -- off is a fixed reserved host byte (<256) from bigipVEReservedOffsets
		reserved = append(reserved, rip)
		if vipIP.Equal(net.ParseIP(rip)) {
			return fmt.Errorf("bigipVE.vip %q collides with a reserved address in %s "+
				"(reserved: %s)",
				ve.VIP, extCIDR, strings.Join(reserved, ", "))
		}
	}

	// Rule 7: mgmtSubnetIndex must be a valid index.
	if idx := ve.MgmtSubnetIndex; idx < 0 || idx >= len(c.Network.Subnets.Public) {
		return fmt.Errorf("bigipVE.mgmtSubnetIndex %d is out of range (network.subnets.public has %d entries)",
			idx, len(c.Network.Subnets.Public))
	}

	return nil
}

// EnableDemo forces demo mode on (the --demo flag's effect), creating the
// Demo block if absent and defaulting TTL to DefaultDemoTTL when empty.
// Idempotent: safe to call when demo is already enabled. Callers must still
// run ValidateDemo afterward (e.g. to enforce the jumphost requirement).
func (c *Cluster) EnableDemo() {
	if c.Demo == nil {
		c.Demo = &DemoSpec{}
	}
	c.Demo.Enabled = true
	if c.Demo.TTL == "" {
		c.Demo.TTL = DefaultDemoTTL
	}
}

// DemoTagKey is the AWS tag key written to every resource when demo mode is active.
// Matches the awsbnkctl: prefix convention used by tags.KeyCluster / tags.KeyComponent.
const DemoTagKey = "awsbnkctl:demo"

// DemoExpiryTagKey is the AWS tag key that records the RFC3339 UTC expiry time
// for demo resources. Its value equals the DEMO_EXPIRY state key written at up time.
const DemoExpiryTagKey = "awsbnkctl:demo-expiry"

// SetDemoTags injects the demo marker tags into c.Tags so every phase's
// tags.Merge carries them onto created AWS resources. expiry should equal the
// DEMO_EXPIRY state value (now + ttl). Nil-inits c.Tags. Idempotent.
func (c *Cluster) SetDemoTags(expiry time.Time) {
	if c.Tags == nil {
		c.Tags = map[string]string{}
	}
	c.Tags[DemoTagKey] = "true"
	c.Tags[DemoExpiryTagKey] = expiry.UTC().Format(time.RFC3339)
}

// ValidateDemo enforces the demo-mode invariants shared by both validation paths:
//  1. TTL (if non-empty) must parse as a positive Go duration.
//  2. testing.jumphost.enabled must be true — every demo use-case runs from the jumphost.
//
// Called from validate() for the config-block path (demo: enabled: true in YAML)
// and from runUp for the CLI flag path (--demo forces Enabled=true after Load).
// applyDefaults must have run before ValidateDemo so that an omitted TTL is
// already defaulted to "24h".
func ValidateDemo(c *Cluster) error {
	if c.Demo == nil || !c.Demo.Enabled {
		return nil
	}
	// TTL must be a positive duration when non-empty (post-defaults it will be "24h").
	if c.Demo.TTL != "" {
		d, err := time.ParseDuration(c.Demo.TTL)
		if err != nil {
			return fmt.Errorf("demo.ttl %q is not a valid duration (e.g. \"24h\", \"48h\"): %w", c.Demo.TTL, err)
		}
		if d <= 0 {
			return fmt.Errorf("demo.ttl %q must be a positive duration (e.g. \"24h\")", c.Demo.TTL)
		}
	}
	// Demo mode requires testing.jumphost.enabled: true.
	if c.Testing == nil || c.Testing.Jumphost == nil || !c.Testing.Jumphost.Enabled {
		return fmt.Errorf("--demo requires testing.jumphost.enabled: true " +
			"(the demo use-cases run from the jumphost); add it to cluster.yaml")
	}
	return nil
}
