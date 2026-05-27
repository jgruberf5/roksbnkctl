// Package intent holds the cluster.yaml schema (v1) and loader.
//
// The canonical format is described in docs/POST_TERRAFORM_DIRECTION.md §5.
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

// DataPathSpec describes the two TMM data-plane subnets required when
// pattern: host-device is set. External is the TMM client-side (public-ish)
// subnet; Internal is the TMM backend-side (private) subnet.
type DataPathSpec struct {
	External SubnetSpec   `yaml:"external"` // BNK_EXT — TMM client-side subnet
	Internal SubnetSpec   `yaml:"internal"` // BNK_INT — TMM backend-side subnet
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
	// Pattern selects internal/k8s/manifests/<pattern>/ (not used in slice 1).
	// Loaded here for forward-compat so later slices don't change the struct.
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
	// Demo declares this as a demo deployment (PRD 10, Slice A1+). When present
	// and enabled, `up` writes DEMO_MODE/DEMO_STAGED_AT/DEMO_EXPIRY to state.env.
	// Omitting the block (or leaving enabled: false) is the default (not a demo).
	Demo *DemoSpec `yaml:"demo,omitempty"`
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
	// DataPath declares the two TMM data-plane subnets (slice 7+).
	// Required when pattern: host-device is set.
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
// See docs/FORGE_MCP_INTEGRATION.md for the handoff details. Shape borrowed
// from mwiget/kindbnkctl's bnk_forge: block (camelCase here to match the
// rest of our schema).
type ForgeSpec struct {
	// Enabled is the master switch. Default false (omitted block = disabled).
	Enabled bool `yaml:"enabled"`
	// URL is the forge REST base. Default http://localhost:8000.
	// Override via AWSBNKCTL_FORGE_URL env (env > yaml > default).
	URL string `yaml:"url,omitempty"`
	// MCPURL is the forge MCP endpoint. Default http://localhost:8081/mcp/.
	// Slice 4 prefers MCP and falls back to REST at URL on capability gaps.
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

// DemoSpec declares that this cluster is a demo deployment (PRD 10, Slice A1).
// When Enabled is true, `awsbnkctl up` writes DEMO_MODE, DEMO_STAGED_AT, and
// DEMO_EXPIRY to the cluster's state.env before the provisioning phase graph.
// Demo mode requires testing.jumphost.enabled: true — every demo use-case runs
// a test client from the EICE jumphost (Slice B onwards). The `--demo` CLI flag
// is syntactic sugar that forces Enabled=true without requiring this block.
type DemoSpec struct {
	// Enabled is the master switch. Default false (omitted block = not a demo).
	Enabled bool `yaml:"enabled"`
	// TTL is the lifetime of the demo cluster as a Go duration string (e.g. "24h",
	// "48h"). Default "24h" when Enabled is true and TTL is omitted. Must parse as
	// a positive duration. DEMO_EXPIRY = DEMO_STAGED_AT + TTL.
	TTL string `yaml:"ttl,omitempty"`
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
	if c.ClusterSpec != nil {
		if c.ClusterSpec.KubernetesVersion == "" {
			c.ClusterSpec.KubernetesVersion = "1.30"
		}
		for i := range c.ClusterSpec.NodeGroups {
			ng := &c.ClusterSpec.NodeGroups[i]
			if ng.InstanceType == "" {
				// host-device pattern needs ≥4 ENIs (primary + EKS CNI + 2 BNK secondaries)
				// AND ≥16 vCPU / ≥64 GB for the full BNK 2.3 Small control plane + TMM
				// packed onto one labeled node. m6i.4xlarge is the documented minimum per
				// docs/audits/slice-09-aws-gpu-setup-audit.md row 27 and slice-12 audit §5.
				// Other patterns can run on smaller workers.
				if c.Pattern == "host-device" {
					ng.InstanceType = "m6i.4xlarge"
				} else {
					ng.InstanceType = "t3.medium"
				}
			}
			if ng.DesiredSize == 0 {
				// host-device pattern needs ≥3 nodes for dSSM quorum (slice-09 audit row 28,
				// un-deferred 2026-05-24). Other patterns default to 1.
				if c.Pattern == "host-device" {
					ng.DesiredSize = 3
				} else {
					ng.DesiredSize = 1
				}
			}
			if ng.MinSize == 0 {
				if c.Pattern == "host-device" {
					ng.MinSize = 3
				} else {
					ng.MinSize = 1
				}
			}
			if ng.MaxSize == 0 {
				if c.Pattern == "host-device" {
					ng.MaxSize = 4
				} else {
					ng.MaxSize = 2
				}
			}
			if ng.DiskSize == 0 {
				ng.DiskSize = 50
			}
		}

		// host-device pattern: auto-inject role=bnk into the first node group's
		// labels if not already set. Phase 16 reads `kubectl get nodes -l role=bnk`
		// to find the TMM-target node — missing this label causes a "no nodes found"
		// failure at Phase 16 entry. Preserve an explicitly-set value.
		if c.Pattern == "host-device" && len(c.ClusterSpec.NodeGroups) > 0 {
			ng := &c.ClusterSpec.NodeGroups[0]
			if ng.Labels == nil {
				ng.Labels = make(map[string]string)
			}
			if _, ok := ng.Labels["role"]; !ok {
				ng.Labels["role"] = "bnk"
			}

			// host-device pattern: bump defaults to 3 workers for dSSM quorum.
			// aws-gpu-setup vars.env:110 explicitly requires `BNK_WORKER_COUNT="3"`
			// (≥3 for dSSM quorum per §9 F9). Single-node packs the BNK pod set
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

	// host-device pattern: auto-derive TMM SelfIPs as <subnet>.240 when not
	// explicitly set. Matches aws-gpu-setup vars.env (TMM_EXT_SELFIP=10.0.10.240,
	// TMM_INT_SELFIP=10.0.20.240). Per F5 Multi-AZ PDF p.9 these SelfIPs MUST
	// be assigned as secondary IPs on each ENI (Phase 17).
	if c.Pattern == "host-device" && c.Network.DataPath != nil {
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
		if sip.Internal == "" {
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
	return nil
}

// validatePattern checks pattern-specific constraints.
// host-device: network.dataPath is required; both external/internal AZs must
// appear in network.azs.
func validatePattern(c *Cluster) error {
	if c.Pattern == "" {
		return nil
	}
	if c.Pattern != "host-device" {
		return fmt.Errorf("pattern %q is not a recognised value (expected host-device)", c.Pattern)
	}
	// host-device requires dataPath.
	if c.Network.DataPath == nil {
		return fmt.Errorf("pattern host-device requires network.dataPath to be set")
	}
	dp := c.Network.DataPath
	azSet := make(map[string]bool, len(c.Network.AZs))
	for _, az := range c.Network.AZs {
		azSet[az] = true
	}
	if !azSet[dp.External.AZ] {
		return fmt.Errorf("network.dataPath.external.az %q is not in network.azs %v", dp.External.AZ, c.Network.AZs)
	}
	if !azSet[dp.Internal.AZ] {
		return fmt.Errorf("network.dataPath.internal.az %q is not in network.azs %v", dp.Internal.AZ, c.Network.AZs)
	}
	if c.Pattern == "host-device" && c.ClusterSpec != nil {
		for i, ng := range c.ClusterSpec.NodeGroups {
			if ng.DesiredSize > 0 && ng.DesiredSize < 3 {
				return fmt.Errorf(
					"pattern host-device requires cluster.nodeGroups[%d].desiredSize >= 3 (dSSM quorum), got %d. "+
						"See docs/audits/slice-12-cold-start-audit.md §5",
					i, ng.DesiredSize,
				)
			}
		}
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
		if c.Pattern != "host-device" || c.Network.DataPath == nil {
			return fmt.Errorf("testing.jumphost requires network.dataPath (BNK_EXT subnet) " +
				"which is only created when pattern: host-device is set")
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
