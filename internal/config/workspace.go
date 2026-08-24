package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Workspace is ~/.roksbnkctl/<name>/config.yaml.
//
// Mirrors the per-workspace example in docs/PRD.md. Note that there is no
// `api_key` field — secrets live in env vars or the OS keychain, never in
// this struct. Plaintext keys in the YAML are rejected at load time by
// rejectPlaintextSecrets.
type Workspace struct {
	// IBMCloud is the account context every phase runs against: region,
	// resource group, and the API key (base64-obfuscated, never plaintext).
	IBMCloud IBMCloudCfg `yaml:"ibmcloud"`

	// Cluster describes the ROKS cluster — either one to CREATE, or one that
	// already exists and is being adopted (`create: false` plus its name).
	// Adopting is the common case: most estates build their own clusters.
	Cluster ClusterCfg `yaml:"cluster"`

	// BNK configures the BIG-IP Next for Kubernetes install itself: the
	// manifest version (which also decides whether the 2.3 or 2.4 model is
	// rendered), licensing, the per-zone network mapping, and the CNEInstance
	// settings that place the TMM pods.
	BNK BNKCfg `yaml:"bnk,omitempty"`

	// Gateway configures the Gateway API resources installed after BNK. On 2.4
	// this is the Infra + GatewaySettings model; on 2.3 it is BNKGateway plus
	// the static routes.
	Gateway GatewayCfg `yaml:"gateway,omitempty"`

	// Registry selects where images and charts are pulled from: F5's Artifact
	// Repository directly, or a mirror that a disconnected cluster can reach —
	// IBM Container Registry, or any OCI registry (`generic`) such as
	// Artifactory or Harbor. nil means no mirror; images come from F5.
	Registry *RegistryCfg `yaml:"registry,omitempty"`

	// Test configures the connectivity, DNS and throughput probes `roksbnkctl
	// test` runs, and the jumphost they run FROM.
	Test TestCfg `yaml:"test,omitempty"`

	// TFSource selects which Terraform the binary applies: the `embedded` tree
	// shipped inside this binary (the default, and the only version matched to
	// it), a GitHub release, or a local path for testing a fork.
	TFSource TFSourceCfg `yaml:"tf_source"`

	// COS is the IBM Cloud Object Storage bucket the FAR service-account
	// credential is read from, for estates that stage it there rather than
	// passing a local file. nil means the credential comes from a file.
	COS *COSCfg `yaml:"cos,omitempty"`

	// Targets are named remote hosts (`ssh:<target>`) that Exec backends can
	// run tools on — a jumphost inside the VPC, say, when the operator's
	// workstation cannot reach the cluster directly.
	Targets map[string]TargetCfg `yaml:"targets,omitempty"`

	// State controls where Terraform state is kept and how it is locked.
	State StateCfg `yaml:"state,omitempty"`

	// BNKForge points at a BNK Forge server, which takes over a durable cluster
	// once roksbnkctl has built it. nil means no Forge; every phase runs
	// standalone.
	BNKForge *BNKForgeCfg `yaml:"bnkforge,omitempty"`

	// Agent configures `roksbnkctl agent`, which hands the workspace to a
	// coding agent. nil means the feature is unconfigured.
	Agent *AgentCfg `yaml:"agent,omitempty"`

	// Prefix is the workspace's account-scoped resource-name base.
	// When non-empty, the
	// tfvars render derives every IBM Cloud resource name from it via
	// internal/naming.Derive and emits the full name set, so two
	// workspaces that both create infra no longer collide on the upstream
	// module's default names. Empty (legacy config) keeps the old sparse
	// render. Additive + omitempty, so old config.yaml loads unchanged.
	Prefix string `yaml:"prefix,omitempty"`

	// Resources carries the per-resource create toggles (and the
	// existing-resource name/ID for any declined-but-still-depended-on
	// resource). nil (legacy config) means the render falls back to the
	// upstream module defaults for each toggle.
	Resources *ResourcesCfg `yaml:"resources,omitempty"`

	// Exec is the per-tool execution-backend config block introduced
	// in Sprint 3 (PRD 03). Maps a tool name (`ibmcloud`, `iperf3`,
	// `terraform`) to its preferred backend (`local`, `docker`,
	// `k8s`, or `ssh:<target>`). Per-invocation `--backend` flag wins
	// over the workspace config; missing entries default to `local`.
	//
	// Example:
	//
	//   exec:
	//     ibmcloud:  { backend: docker }
	//     iperf3:    { backend: k8s }
	//     terraform: { backend: local }
	Exec map[string]ExecToolCfg `yaml:"exec,omitempty"`
}

// BNKForgeCfg is the optional integration with a co-located BNK Forge (v3)
// install. When Register is true, `cluster up` registers the just-provisioned
// ROKS cluster with BNK Forge via its REST API — credential-backed, so BNK
// Forge derives the kubeconfig on demand from an IBM Cloud credential template
// rather than storing a perishable one. Best-effort: registration never blocks
// or fails the deploy. nil/absent (legacy config) = no-op.
type BNKForgeCfg struct {
	// Register opts the workspace in. Default false.
	Register bool `yaml:"register,omitempty"`
	// URL is the BNK Forge server base URL (e.g. https://forge.example.com).
	// Also settable via BNK_FORGE_URL / --url.
	URL string `yaml:"url,omitempty"`
	// Project is the target BNK Forge project NAME. Ensured-or-created at
	// register time. Empty = the workspace name.
	Project string `yaml:"project,omitempty"`
	// Username is the BNK Forge login user. Also settable via BNK_FORGE_USER /
	// --username. The password is NEVER stored here — it comes from
	// BNK_FORGE_PASSWORD or an interactive prompt; the resulting session token
	// is cached in the OS keychain.
	Username string `yaml:"username,omitempty"`
	// Insecure skips TLS verification against the Forge server.
	//
	// The session token is sent on every request, so this makes the connection
	// encrypted but UNAUTHENTICATED — anyone on the path can present a
	// certificate for the Forge host and read the token. Prefer CAB64. Every
	// request made with this set prints a warning naming the host.
	Insecure bool `yaml:"insecure,omitempty"`

	// CAB64 pins the CA the Forge server's certificate must chain to, PEM,
	// base64-encoded. This is the correct answer for a self-signed lab install:
	// you generated the CA, so you already hold it, and pinning authenticates
	// the connection rather than abandoning authentication. When set it wins
	// over Insecure.
	//
	// A certificate is public data, so — unlike the `_b64` credential fields —
	// this is encoded only for single-line YAML safety. Settable via
	// `--forge-ca <file>` and ROKSBNKCTL_BNKFORGE_CA_B64.
	CAB64 string `yaml:"ca_b64,omitempty"`
}

// BNKTrustedProfileCfg is the IBM Cloud Trusted Profile the CNE controller
// assumes at runtime — the identity that lets it manage the VPC network
// attachments it creates for TMM, without a stored API key.
//
// Both fields default in the HCL rather than here, so an absent block renders
// nothing and the shipped terraform decides — the same rule every other
// optional BNK field follows.
type BNKTrustedProfileCfg struct {
	// ServiceAccount is the Kubernetes service account the profile is LINKED to:
	// which account may assume it.
	//
	// EMPTY (the default) derives the account FLO actually creates:
	// "f5-cne-controller-<flo_namespace>-f5-cne-controller-serviceaccount".
	//
	// This is a MATCHER, not a pointer. The IBM IAM trust relationship compares a
	// pod's service-account token against crn/namespace/name with EQUALS, so a
	// name that does not match the account the CNE controller runs as makes the
	// profile unassumable — with no error anywhere. The pod simply loses its IBM
	// Cloud permissions, and it surfaces as an authorization failure at
	// VPC-attachment time naming neither this setting nor the profile.
	//
	// Set it only if you can ALSO make FLO name the account differently.
	// roksbnkctl cannot: FLO creates the account when it reconciles the
	// CNEInstance, and that spec has no service-account field. A shorter name in
	// the IAM trust rule without a matching change on the F5 side produces a
	// profile that looks right in the IBM Cloud console and works for nothing.
	//
	// Safe to share across clusters. Uniqueness comes from the profile name
	// (which carries the cluster name) and from the link's cluster CRN, so the
	// same account name on every cluster in an account cannot collide.
	ServiceAccount string `yaml:"service_account,omitempty"`

	// Roles granted to the profile, scoped to the cluster's OWN VPC. Default
	// ["Viewer", "Editor"]. Editor is what lets the controller manage TMM's
	// network attachments; narrowing it fails at attachment time on a running
	// cluster rather than at apply, so change it only against a tested policy.
	Roles []string `yaml:"roles,omitempty" default:"[Viewer, Editor]"`
}

// AgentCfg configures roksbnkctl's agentic mode (the `agent` command). It is
// purely advisory metadata for launching an external coding-agent CLI against
// the workspace's scaffolded AGENTS.md + personas/ — roksbnkctl embeds no LLM.
// nil/absent = `agent` defaults to claude and the CLI's own endpoint config.
type AgentCfg struct {
	// Default is the agentic CLI `roksbnkctl agent` (no arg) reports as this
	// workspace's default — claude | gemini | aider | openai | pi | opencode.
	Default string `yaml:"default,omitempty"`
	// LLMEndpoint is an optional OpenAI-/Anthropic-compatible base URL woven
	// into the printed invocation (cloud vendor, local vLLM, etc.). Empty =
	// rely on the CLI's own environment/config.
	LLMEndpoint string `yaml:"llm_endpoint,omitempty"`
}

// ExecToolCfg is one entry under workspace.Exec — the chosen backend
// for a given tool.
type ExecToolCfg struct {
	// Backend is the execution-backend spec: "local" | "docker" |
	// "k8s" | "ssh:<target>". Empty string defaults to "local" at
	// resolution time.
	Backend string `yaml:"backend"`
}

// TargetCfg is the on-disk shape of one entry under `targets:` in the
// workspace config. Lives in this package (rather than internal/remote)
// to avoid an import cycle: workspace.go needs to (de)serialise it,
// internal/remote needs to consume it. Keeping the wire shape here and
// the runtime Target type in internal/remote keeps the dep direction
// clean (remote → config, never the reverse).
type TargetCfg struct {
	// Host is the target's address, reached over SSH.
	Host string `yaml:"host"`
	// Port is its SSH port. 0 → 22.
	Port int `yaml:"port,omitempty" default:"22"`
	// User is the SSH login.
	User      string `yaml:"user"`
	KeyPath   string `yaml:"key_path,omitempty"`   // file path (PEM)
	KeySource string `yaml:"key_source,omitempty"` // "agent" | "tf-output:<name>"
}

type IBMCloudCfg struct {
	// Region is the IBM Cloud region everything in this workspace is created in
	// or adopted from, e.g. "us-east". The testing client can live elsewhere via
	// resources.client_region.
	Region string `yaml:"region"`

	// ResourceGroup is the IBM Cloud resource group resources are placed in, and
	// the one an adopted cluster is looked up in.
	ResourceGroup string `yaml:"resource_group" default:"default"`
	APIKeySource  string `yaml:"api_key_source,omitempty"` // env | keychain | config | prompt — see secrets.go

	// APIKeyB64 stores the API key base64-encoded inline in the workspace
	// config. This is OBFUSCATION, NOT ENCRYPTION — anyone with the file
	// can decode it instantly. Treat the file like a plaintext credential:
	// chmod 600, .gitignore, never commit. Provided as a convenience for
	// single-user setups; the keychain or env-var path is the recommended
	// secure default.
	//
	// Note that the field name does NOT match the rejectPlaintextSecrets
	// regex (which guards `api_key`, not `api_key_b64`), so the value
	// loads normally without tripping the plaintext rejection.
	APIKeyB64 string `yaml:"api_key_b64,omitempty"`
}

type ClusterCfg struct {
	// Create decides whether this workspace BUILDS the ROKS cluster or adopts one
	// that already exists. false means adopt: Name must then match a cluster in
	// the account, and `cluster register` takes it over without changing it.
	// Adopting is the common case — most estates build clusters to their own
	// standards and ask roksbnkctl only for BNK.
	Create bool `yaml:"create"`

	// Name is the ROKS cluster's name — the one to create, or the existing one to
	// adopt when Create is false.
	Name string `yaml:"name"`

	// OpenShiftVersion pins the OpenShift version for a CREATED cluster (e.g.
	// "4.20"). Empty takes IBM's current default, which moves over time — pin it
	// when a run has to be reproducible. Ignored when adopting.
	OpenShiftVersion string `yaml:"openshift_version,omitempty"`

	// WorkersPerZone is the worker count PER ZONE, not in total: ROKS spans three
	// availability zones, so 2 here is a six-node cluster. Ignored when adopting.
	WorkersPerZone int `yaml:"workers_per_zone,omitempty" default:"1"`

	// PublicGateway controls whether the cluster subnets attach a public gateway
	// for worker Internet egress. nil → the terraform default (true, current
	// behavior). false → a private/disconnected cluster with NO egress — the
	// operator must supply private connectivity (VPEs / private service endpoints)
	// for image pulls and IBM Cloud services. A pointer so "unset" is distinct from
	// an explicit false. Rendered as cluster_public_gateway.
	PublicGateway *bool `yaml:"public_gateway,omitempty" default:"true"`

	// VPCCIDR is the block the cluster VPC's per-zone address prefixes are carved
	// from — "10.241.0.0/16" becomes 10.241.0.0/18, 10.241.64.0/18, 10.241.128.0/18.
	//
	// Empty leaves IBM's "auto" address prefix management, which hands EVERY VPC in
	// a region the same prefixes. Two roksbnkctl-created clusters then overlap, and a
	// Transit Gateway they share cannot route to both — it silently blackholes one,
	// which surfaces as intermittent image-pull timeouts rather than as a routing
	// error (issue #46). Give each cluster its own block when they must share a
	// gateway, which is the norm for disconnected installs: the cluster has to reach
	// the private mirror over that gateway.
	//
	// Only meaningful when the cluster is CREATED; an adopted VPC keeps its own
	// prefixes. Rendered as cluster_vpc_cidr.
	VPCCIDR string `yaml:"vpc_cidr,omitempty"`

	// NetworkMode selects how the cluster's worker nodes are attached:
	// "single-nic" (the default, and today's only behaviour) or "multi-nic".
	//
	// CREATE-TIME ONLY and immutable thereafter. Converting a cluster between
	// modes is not supported: terraform would plan a replacement of a running
	// cluster, so a workspace whose mode disagrees with its cluster-outputs.json
	// is refused rather than planned.
	//
	// Empty means single-nic, so every existing config.yaml is unaffected.
	NetworkMode string `yaml:"network_mode,omitempty" default:"single-nic"`

	// ExistingSubnetIDs places the cluster in subnets that ALREADY EXIST, one per
	// zone in zone order, instead of creating them (#61).
	//
	// Adopting the VPC alone is only half of "bring your own network": in an estate
	// that allocates address space centrally the subnets carry the ACLs and routing
	// that make them acceptable, and a cluster placed in freshly-created subnets
	// sits outside all of it.
	//
	// Requires resources.cluster_vpc = { create: false, existing: <vpc-id> } — a
	// subnet cannot be adopted independently of the VPC containing it. Rendered as
	// use_existing_cluster_subnets + existing_cluster_subnet_ids; the subnets' zones
	// are read from the subnets themselves, not from the region default.
	ExistingSubnetIDs []string `yaml:"existing_subnet_ids,omitempty"`

	// WorkerFlavor names the worker profile EXACTLY, e.g. "cx3d.8x20". Empty
	// auto-selects from MinWorkerVCPUCount / MinWorkerMemoryGB.
	//
	// The auto-select only considers the bx2 family, so any other profile is
	// unreachable without naming it here — F5's approved reference cluster runs
	// cx3d.8x20, which no combination of minimums can produce. Naming the flavor
	// also pins the exact variant, where two profiles meeting the same minimums
	// can differ in attributes nothing here tests.
	WorkerFlavor string `yaml:"worker_flavor,omitempty"`

	// MinWorkerVCPUCount is the vCPU floor for the worker-flavor auto-select: the
	// cluster module picks the smallest bx2 profile meeting this and
	// MinWorkerMemoryGB. Ignored when WorkerFlavor names a profile outright.
	// Rendered as roks_min_worker_vcpu_count; 0 leaves the terraform default (16).
	// Only meaningful when the cluster is created.
	MinWorkerVCPUCount int `yaml:"min_worker_vcpu_count,omitempty" default:"16"`

	// MinWorkerMemoryGB is the memory floor for the same auto-select. Rendered as
	// roks_min_worker_memory_gb; 0 leaves the terraform default (64).
	MinWorkerMemoryGB int `yaml:"min_worker_memory_gb,omitempty" default:"64"`
}

// HugepagesCfg allocates hugepages on the worker pool through the OpenShift
// Node Tuning Operator.
//
// BNK's deploymentSize decides how much TMM asks for: Tiny requests none, Small
// requests 4Gi of hugepages-2Mi. A stock ROKS worker reports zero — including
// F5's approved reference cluster, which runs Tiny for exactly this reason. So
// any size above Tiny needs this, or nodes prepared some other way.
//
// APPLYING THIS REBOOTS WORKERS. The profile sets a bootloader kernel argument,
// and the Machine Config Operator rolls the pool to apply it — draining and
// restarting each node in turn. On a live cluster that is a maintenance event,
// not a configuration change.
type HugepagesCfg struct {
	// Enabled allocates hugepages. Default false.
	Enabled bool `yaml:"enabled"`

	// Size is the hugepage size, e.g. "2M" or "1G". TMM asks for hugepages-2Mi,
	// so 2M is what matches unless F5 says otherwise for a given size.
	Size string `yaml:"size,omitempty" default:"2M"`

	// Count is the number of pages PER NODE. 2048 x 2M = 4Gi, which is what
	// deploymentSize Small was observed to request.
	Count int `yaml:"count,omitempty" default:"2048"`

	// NodeRole is the machineconfiguration.openshift.io/role the profile applies
	// to. "worker" is every worker in the pool.
	NodeRole string `yaml:"node_role,omitempty" default:"worker"`

	// ProfileName names the Tuned profile and CR.
	ProfileName string `yaml:"profile_name,omitempty" default:"bnk-hugepages"`
}

// ResourcesCfg holds the per-resource create toggles for a prefix-driven
// workspace (Sprint 26). The cluster itself is NOT here — it reuses the
// existing ClusterCfg.Create / ClusterCfg.Name (Name doubles as the
// existing id/name when Create=false, as today). Each toggle carries an
// Existing name/ID used when Create=false and a live dependent still needs
// to reference the resource by name.
type ResourcesCfg struct {
	// TransitGateway controls the transit gateway the cluster VPC attaches to.
	// Create=false + Existing=<name-or-id> ADOPTS one that already exists, which
	// is almost always what you want: gateways are account-scoped, quota-limited
	// and usually shared, so creating one silently burns a connection.
	TransitGateway ResourceToggle `yaml:"transit_gateway"`

	// RegistryCOS controls the IBM Cloud Object Storage bucket that backs a
	// mirror registry. Set Create=false when the bucket already exists, or when
	// the mirror is an external registry (Artifactory, Harbor) that needs none.
	RegistryCOS ResourceToggle `yaml:"registry_cos"`

	// CertManager controls whether cert-manager is INSTALLED or adopted. Set
	// Create=false to adopt one the cluster already runs — an OpenShift estate
	// usually installs it as a day-1 add-on, and `bnk up` otherwise fails with
	// `namespaces "cert-manager" already exists`. Adopting also means `bnk down`
	// cannot delete it, or the certificates it has issued.
	CertManager ResourceToggle `yaml:"cert_manager"`

	// BNK controls whether the BIG-IP Next for Kubernetes install runs at all.
	// Create=false leaves the cluster built but empty — useful for staging the
	// infrastructure ahead of the entitlement.
	BNK ResourceToggle `yaml:"bnk"`

	// TGWJumphost controls the optional testing jumphost in the client VPC.
	// Defaults OFF, as the `init` interview does. `roksbnkctl test` runs its
	// probes FROM a jumphost, so the testing phase provisions nothing without it.
	TGWJumphost ResourceToggle `yaml:"tgw_jumphost"`

	// ClusterJumphosts controls the per-cluster jumphosts. Defaults OFF.
	ClusterJumphosts ResourceToggle `yaml:"cluster_jumphosts"`

	// ClientVPC controls the testing client VPC that the jumphost lives in.
	// Defaults OFF; it consumes a transit-gateway connection. Create=false +
	// Existing=<name> adopts one instead.
	ClientVPC ResourceToggle `yaml:"client_vpc"`
	// ClusterVPC controls the ROKS cluster's OWN VPC. Create=true (default)
	// provisions a new one (named from the prefix); Create=false + Existing=<vpc-id>
	// brings your own — rendered as use_existing_cluster_vpc + existing_cluster_vpc_id.
	// (Existing is the VPC *ID*, unlike the transit-gateway/client-vpc adopt-by-name.)
	ClusterVPC ResourceToggle `yaml:"cluster_vpc"`
	// ClientRegion is the region the testing client (TGW jumphost + client VPC)
	// is installed in. Empty → the terraform default (testing_client_vpc_region).
	// Lets the test client live in a different region from the cluster.
	ClientRegion string `yaml:"client_region,omitempty"`
	// TestingClientVPCName names the testing client VPC when ClientVPC.Create is
	// true (rendered as testing_client_vpc_name). Empty → the prefix-derived
	// default. (When ClientVPC.Create is false, ClientVPC.Existing names the VPC
	// to adopt instead.)
	TestingClientVPCName string `yaml:"testing_client_vpc_name,omitempty" default:"tf-testing-vpc"`
	// TestingSSHKeyName is the IBM Cloud VPC SSH key name attached to the testing
	// jumphosts (rendered as testing_ssh_key_name). `roksbnkctl init` resolves it:
	// an existing key is used as-is, otherwise roksbnkctl generates one, stores the
	// private key per-workspace, and uploads the public key. Empty → no named key
	// (the jumphosts use only the generated cloud-init key).
	TestingSSHKeyName string `yaml:"testing_ssh_key_name,omitempty"`
	// Jumphost sizing. TestingJumphostProfile pins an explicit instance profile for
	// ALL jumphosts (rendered as testing_jumphost_profile); empty → auto-select the
	// smallest profile meeting TestingMinVCPUCount / TestingMinMemoryGB (rendered as
	// testing_min_vcpu_count / testing_min_memory_gb; 0 → the terraform defaults of
	// 4 vCPU / 8 GB). An explicit profile wins over the minimums.
	TestingJumphostProfile string `yaml:"testing_jumphost_profile,omitempty"`

	// TestingMinVCPUCount is the vCPU floor for the jumphost auto-select. Ignored
	// when TestingJumphostProfile names a profile outright.
	TestingMinVCPUCount int `yaml:"testing_min_vcpu_count,omitempty" default:"4"`

	// TestingMinMemoryGB is the memory floor for the same auto-select.
	TestingMinMemoryGB int `yaml:"testing_min_memory_gb,omitempty" default:"8"`
	// Security-group source CIDRs, following the flp_vsi module's split between
	// a management plane and an in-fabric plane.
	//
	// TestingJumphostAllowedCIDRs gates SSH (:22) to the testing jumphosts, which
	// carry a public floating IP and are reached from wherever the operator
	// happens to be — so empty means open, and access is key-only. Narrow it to
	// the operator's public /32 on a shared account.
	TestingJumphostAllowedCIDRs []string `yaml:"testing_jumphost_allowed_cidrs,omitempty"`
	// TestingClientVPCInboundCIDRs gates the client VPC's DEFAULT security group,
	// which the testing phase widens to all protocols and ports. Empty → the
	// RFC-1918 ranges, where in-fabric test traffic arrives from (the cluster
	// reaches the client VPC over the Transit Gateway).
	TestingClientVPCInboundCIDRs []string `yaml:"testing_client_vpc_inbound_cidrs,omitempty"`
	// ClusterHTTPAllowedCIDRs gates :80 on the cluster security group — the
	// ingress/ALB path, which is meant to be publicly reachable, so empty means
	// open.
	ClusterHTTPAllowedCIDRs []string `yaml:"cluster_http_allowed_cidrs,omitempty"`
	// ClusterVPCDefaultSGInboundCIDRs gates the cluster VPC's DEFAULT security
	// group, which the cluster phase widens to all protocols and ports. Empty →
	// 0.0.0.0/0, the historical behaviour, kept because this SG governs the
	// cluster's own data path. Narrow it to your private ranges unless a workload
	// in that VPC needs a public source.
	ClusterVPCDefaultSGInboundCIDRs []string `yaml:"cluster_vpc_default_sg_inbound_cidrs,omitempty"`
	// CopiedSSHKeyFiles lists the ~/.ssh basenames `roksbnkctl init` ACTUALLY
	// wrote when the user accepted the "copy the private key to ~/.ssh" prompt
	// (only files it created — pre-existing files are skipped, never recorded).
	// `ws delete` removes exactly these so a generated key doesn't outlive its
	// workspace. Empty when nothing was copied.
	CopiedSSHKeyFiles []string `yaml:"copied_ssh_key_files,omitempty"`
}

// DefaultResources returns the standard create/reuse toggle set a fresh
// workspace gets (mirrors the `init` interview defaults + the upstream module
// defaults). Used by the non-interactive paths so that an env override touching
// ONE toggle (e.g. adopting an existing transit gateway) doesn't leave the rest
// at their bool zero value (create:false) and silently disable BNK / COS / etc.
//
// The testing client (TGWJumphost + ClientVPC) is OFF here because that is what
// the interview defaults to — `init` asks "Add a testing client?" and defaults
// to no. These previously defaulted ON, so a non-interactive run built a
// jumphost VSI and a client VPC nobody asked for, and the client VPC consumed a
// Transit Gateway connection. Opt in with ROKSBNKCTL_TGW_JUMPHOST_CREATE /
// ROKSBNKCTL_CLIENT_VPC_CREATE.
func DefaultResources() *ResourcesCfg {
	return &ResourcesCfg{
		TransitGateway:   ResourceToggle{Create: true},
		RegistryCOS:      ResourceToggle{Create: true},
		CertManager:      ResourceToggle{Create: true},
		BNK:              ResourceToggle{Create: true},
		TGWJumphost:      ResourceToggle{Create: false},
		ClusterJumphosts: ResourceToggle{Create: false},
		ClientVPC:        ResourceToggle{Create: false},
		ClusterVPC:       ResourceToggle{Create: true},
	}
}

// ResourceToggle is one create/reuse decision: Create=true provisions the
// resource (under its prefix-derived name); Create=false reuses an existing
// one named by Existing (when a live dependent consumes it).
type ResourceToggle struct {
	// Create decides whether roksbnkctl provisions this resource or adopts one
	// that already exists. false means adopt, and Existing then names it.
	Create bool `yaml:"create"`
	// Existing is the name or ID of the resource to adopt when Create is false.
	// Which of the two it wants varies by resource and is documented on each —
	// cluster_vpc takes a VPC *ID*, transit_gateway takes a name or an id.
	Existing string `yaml:"existing,omitempty"`
}

// Default{ManifestVersion,FARAuthFile,SubscriptionJWTFile} mirror the
// f5_bigip_k8s_manifest_version / f5_cne_far_auth_file / f5_cne_subscription_jwt_file
// terraform-variable defaults. The init interview offers them and seeds
// bnk.{manifest_version,far_auth_file,subscription_jwt_file}; those config values
// then override the tfvar defaults (via internal/tf/vars.go) and drive `registry`
// (the manifest pull + the FAR auth).
const (
	DefaultManifestVersion     = "2.3.0-3.2598.3-0.0.170"
	DefaultFARAuthFile         = "f5-far-auth-key.tgz"
	DefaultSubscriptionJWTFile = "subscription.jwt"
	// Default{COSInstance,COSBucket,COSRegion} are the orchestration COS
	// coordinates that hold the FAR auth tarball + the subscription JWT. They
	// mirror the ibmcloud_cos_instance_name / ibmcloud_resources_cos_bucket /
	// ibmcloud_cos_bucket_region terraform-variable defaults and back the cos:
	// config block; the `registry` FAR resolver and the init supply-chain
	// provisioning both fall back to these when cos: is unset.
	DefaultCOSInstance = "bnk-supply-chain"
	DefaultCOSBucket   = "bnk-artifacts"
	DefaultCOSRegion   = "us-south"
	// DefaultLicenseMode is the terraform License CR operationMode default; an
	// empty bnk.license_mode leaves it unset (terraform defaults to "connected"),
	// so JWT/connected licensing is unchanged unless FLP is opted into.
	DefaultLicenseMode = "connected"
	// DefaultFLPNamespace is where the `flp` phase installs the F5 License Proxy.
	DefaultFLPNamespace = "f5-license-proxy"
	// DefaultFLPVSIProfile is the VSI profile for mode: vsi (4 vCPU / 16 GB — meets
	// the FLP appliance's 4 vCPU / 8 GB minimum with headroom).
	DefaultFLPVSIProfile = "bx2-4x16"
	// DefaultVLANPrefixLen and DefaultTMMK8SRoutes mirror the
	// cneinstance_vlan_prefixlen / cneinstance_tmm_k8s_routes terraform defaults
	// (the F5SPKVlan self-IP prefix length, and the ROKS pod CIDR TMM routes to).
	// They seed the interactive `init` networking prompts; an unset bnk.network
	// value still falls back to the terraform default.
	DefaultVLANPrefixLen = 24
	DefaultTMMK8SRoutes  = "172.17.0.0/18"
)

// DefaultBNKNetworkZones mirrors the cneinstance_network_zones install-guide
// default in terraform/modules/cne_instance/modules/cneinstance/variables.tf
// (three availability zones). It seeds the interactive `init` networking prompts
// so the operator edits only what differs from the guide. KEEP IN SYNC with that
// terraform default — the module is the source of truth; this is the Go mirror used
// only to pre-fill the interview (an unset bnk.network still falls back to the
// module default, so drift here changes only the prompt seed, never the applied
// value when the operator accepts a zone unchanged).
var DefaultBNKNetworkZones = []BNKZoneCfg{
	{ExtVLANCIDR: "10.155.15.0/24", IntVLANCIDR: "10.254.99.0/24", IntSNATCIDR: "10.10.11.0/24", IntVIPCIDR: "10.135.15.0/24", ExternalSelfIP: "10.155.15.101", InternalSelfIP: "10.254.99.101"},
	{ExtVLANCIDR: "10.156.16.0/24", IntVLANCIDR: "10.254.100.0/24", IntSNATCIDR: "10.10.21.0/24", IntVIPCIDR: "10.136.16.0/24", ExternalSelfIP: "10.156.16.101", InternalSelfIP: "10.254.100.101"},
	{ExtVLANCIDR: "10.157.17.0/24", IntVLANCIDR: "10.254.101.0/24", IntSNATCIDR: "10.10.31.0/24", IntVIPCIDR: "10.137.17.0/24", ExternalSelfIP: "10.157.17.101", InternalSelfIP: "10.254.101.101"},
}

type BNKCfg struct {
	// CNEInstanceSize is the CNEInstance deploymentSize: Tiny, Small, Medium,
	// Large or Max. It decides how much TMM asks for, INCLUDING hugepages — Tiny
	// requests none, Small requests 4Gi of hugepages-2Mi, and a stock ROKS worker
	// reports zero, so anything above Tiny needs bnk.hugepages to allocate them
	// first or the TMM pods stay Pending. See Appendix C for the sizing table.
	CNEInstanceSize string `yaml:"cneinstance_size,omitempty"`

	// FARRepoURL is the F5 Artifact Repository charts are pulled from. Empty uses
	// repo.f5.com. A disconnected cluster cannot reach it — configure `registry`
	// with a mirror instead, and this becomes the source that mirror is filled
	// FROM rather than what the cluster pulls from.
	FARRepoURL string `yaml:"far_repo_url,omitempty" default:"repo.f5.com"`

	// ManifestVersion pins the BNK release, e.g. "2.4.0-EA". This is the single
	// field that selects the product line: a 2.4 version renders the Infra +
	// GatewaySettings model and sets USE_GATEWAY_SETTINGS, while a 2.3 version
	// renders cloud-network-mapping and the F5SPK* CRs. There is no separate
	// `line` field and no override — the version IS the selector, so that the
	// rendered model can never disagree with the manifest being installed.
	ManifestVersion string `yaml:"manifest_version,omitempty"`
	// FarAuthFile is the FAR auth tarball's filename in the orchestration COS
	// bucket; rendered as the f5_cne_far_auth_file tfvar + used by `registry`
	// to resolve the FAR _json_key_base64 service account.
	FarAuthFile string `yaml:"far_auth_file,omitempty"`
	// SubscriptionJWTFile is the subscription/license JWT's filename in the
	// orchestration COS bucket; rendered as the f5_cne_subscription_jwt_file tfvar.
	SubscriptionJWTFile string `yaml:"subscription_jwt_file,omitempty"`

	// FarAuthLocalFile / SubscriptionJWTLocalFile point at LOCAL files instead of
	// COS objects. When both are set, the BNK phase reads them directly (roksbnkctl
	// injects the FAR service account + the JWT as tfvars and sets use_cos_bucket=
	// false), so no orchestration COS instance/bucket is needed. `init` sets these
	// automatically when the COS supply-chain check fails or is declined. When empty,
	// the phase falls back to COS (FarAuthFile / SubscriptionJWTFile).
	FarAuthLocalFile string `yaml:"far_auth_local_file,omitempty"`

	// SubscriptionJWTLocalFile is the F5 subscription JWT as a LOCAL file, the
	// companion to FarAuthLocalFile above. Both must be set for the no-COS path.
	SubscriptionJWTLocalFile string `yaml:"subscription_jwt_local_file,omitempty"`

	// TrustedProfile tunes the IBM Cloud Trusted Profile the CNE controller
	// assumes to manage its own cluster's VPC. nil/absent → the terraform
	// defaults.
	//
	// Unset reproduces today's behaviour exactly, for both fields.
	TrustedProfile *BNKTrustedProfileCfg `yaml:"trusted_profile,omitempty"`

	// FLONamespace / FLOUtilsNamespace override the namespaces the F5 Lifecycle
	// Operator and its utility components install into (rendered as flo_namespace /
	// flo_utils_namespace). Empty → the terraform defaults (f5-bnk / f5-utils). Set
	// these for multi-tenant clusters or to avoid namespace collisions.
	FLONamespace string `yaml:"flo_namespace,omitempty" default:"f5-bnk"`

	// FLOUtilsNamespace is where FLO's shared utility components install —
	// coremond, the observer, the licence. Separate from FLONamespace because the
	// two have different lifetimes: the utils namespace outlives a BNK reinstall.
	FLOUtilsNamespace string `yaml:"flo_utils_namespace,omitempty" default:"f5-utils"`

	// GatewayAPIMTLS opts into the Gateway API bundle BNK 2.4 needs for mTLS
	// (#170).
	//
	// On 2.4 the FLO crd-installer no longer forces its own Gateway API CRDs — it
	// logs a graceful skip and leaves the cluster on whatever bundle OpenShift
	// ships. That is correct for a base install. Only an mTLS deployment needs
	// Gateway API 1.5.0 standard, and installing it means deleting the
	// ingress-operator's ValidatingAdmissionPolicy and its binding, which the
	// platform recreates — the race the admission sweep exists to win.
	//
	// So the sweep is not redundant on 2.4; it is CONDITIONAL. Off by default,
	// because deleting a platform admission policy on a cluster that does not need
	// the newer bundle is a change nobody asked for.
	//
	// Ignored on 2.3, where the sweep always runs: there the crd-installer does
	// force the CRDs and is blocked without it.
	GatewayAPIMTLS bool `yaml:"gateway_api_mtls,omitempty" line:"2.4"`

	// ── BNK 2.4 conformance with F5's reference CNEInstance ──────────────────
	//
	// Defaults are F5's reference values from the live 2.4 capture, not
	// invented. All of these are emitted on 2.4 only; 2.3 renders exactly as it
	// did before.

	// TMMReplicas is the number of f5-tmm data-plane replicas. Zero means the
	// reference default (3).
	TMMReplicas int `yaml:"tmm_replicas,omitempty" line:"2.4" default:"3"`

	// WatchNamespaces are the namespaces the CNE controller watches. Empty means
	// the reference default (["All"]).
	WatchNamespaces []string `yaml:"watch_namespaces,omitempty" line:"2.4" default:"All"`

	// TMMAntiAffinity requires f5-tmm pods onto different nodes. This is what
	// REPLACED the node-labeler on 2.4: the labeler was removed as unnecessary,
	// and placement is the mechanism that took over.
	TMMAntiAffinity *bool `yaml:"tmm_anti_affinity,omitempty" line:"2.4" default:"true"`

	// TMMAntiAffinityTopologyKey is the node label the anti-affinity rule spreads
	// across — the IBM ROKS per-node label. Surfaced rather than hard-coded so a
	// cluster labelling its topology differently stays configurable.
	TMMAntiAffinityTopologyKey string `yaml:"tmm_anti_affinity_topology_key,omitempty" line:"2.4" default:"kubernetes.io/hostname"`

	// TMMZoneSpread spreads f5-tmm pods across zones.
	TMMZoneSpread *bool `yaml:"tmm_zone_spread,omitempty" line:"2.4" default:"true"`

	// TMMZoneTopologyKey is the IBM ROKS zone label the spread constraint uses.
	TMMZoneTopologyKey string `yaml:"tmm_zone_topology_key,omitempty" line:"2.4" default:"topology.kubernetes.io/zone"`

	// TMMZoneMaxSkew is maxSkew for the zone topology-spread constraint.
	TMMZoneMaxSkew int `yaml:"tmm_zone_max_skew,omitempty" line:"2.4" default:"1"`

	// TMMZoneWhenUnsatisfiable is DoNotSchedule or ScheduleAnyway.
	TMMZoneWhenUnsatisfiable string `yaml:"tmm_zone_when_unsatisfiable,omitempty" line:"2.4" default:"DoNotSchedule"`

	// TMMPodLabel is the `app` label value the placement rules select TMM by.
	TMMPodLabel string `yaml:"tmm_pod_label,omitempty" line:"2.4" default:"f5-tmm"`

	// TMMRollingUpdate pins TMM's rolling update to maxSurge 0 / maxUnavailable 1
	// — the same shape as the cwc Multi-Attach deadlock, where an unconstrained
	// rolling update on a single-attach resource wedges.
	TMMRollingUpdate *bool `yaml:"tmm_rolling_update,omitempty" line:"2.4" default:"true"`

	// ExternalBigIP enables the external BIG-IP controller.
	ExternalBigIP *bool `yaml:"external_bigip,omitempty" line:"2.4" default:"false"`

	// ExternalBigIPLoginSecret holds the external BIG-IP credentials.
	ExternalBigIPLoginSecret string `yaml:"external_bigip_login_secret,omitempty" line:"2.4" default:"f5-bigip-ctlr-login"`

	// ClusterIdentifier is passed to the external BIG-IP controller. Empty derives
	// from the cluster name.
	ClusterIdentifier string `yaml:"cluster_identifier,omitempty" line:"2.4" default:""`

	// GatewayAPIVersion is GATEWAY_API_VERSION for the CNE controller. roksbnkctl
	// previously set this nowhere, so the controller ran on whatever the operator
	// defaulted to (v1.4.1 on the verified cluster) while F5's reference pins
	// 1.5.0 — which the 2.4 EA guide requires for mTLS.
	GatewayAPIVersion string `yaml:"gateway_api_version,omitempty" line:"2.4" default:"1.5.0"`

	// DemoMode sets advanced.demoMode.enabled. Nil means the LINE default: true
	// on 2.3, which is what has always shipped, and false on 2.4, matching the
	// reference. Demo mode was being enabled on every install, which is not
	// something a customer deployment should carry.
	DemoMode *bool `yaml:"demo_mode,omitempty" default:"false on 2.4, true on 2.3"`

	// TCPSettings overrides fields on the data-plane F5BigTcpSetting CR.
	//
	// A flat map rather than 54 config fields: the CR has 54 fields across bool,
	// int and string, and enumerating them would be 54 rows nobody reads while
	// still going stale the moment F5 adds one. Values are text because a config
	// file and an environment variable can only carry text; they are coerced to
	// the CR's types on render.
	//
	// Empty writes NO CR. The product manages its own default TCP profile, and
	// emitting an empty one would fight it.
	// WholeCluster is spec.wholeCluster. Nil means the LINE default: true on 2.3,
	// false on 2.4 to match F5's reference.
	//
	// It moves WITH watch_namespaces and cannot be set independently of it: the
	// product validates the pair and rejects wholeCluster=true alongside
	// watchNamespaces=["All"] as an invalid product configuration, because that
	// says "watch everything" twice in two contradictory ways.
	WholeCluster *bool `yaml:"whole_cluster,omitempty" default:"false on 2.4, true on 2.3"`

	// Hugepages optionally allocates hugepages on the worker pool via the
	// OpenShift Node Tuning Operator.
	//
	// OFF by default, deliberately. Allocating hugepages changes the kernel
	// command line, which means the Machine Config Operator DRAINS AND REBOOTS
	// every matching worker, one at a time. That is not something to do because
	// a default said so — a cluster that does not need it should never be
	// rebooted for it. With it off, a deploymentSize that needs hugepages fails
	// fast with a diagnosis naming this setting.
	Hugepages *HugepagesCfg `yaml:"hugepages,omitempty"`

	// TCPSettings overrides individual fields on the data-plane F5BigTcpSetting
	// CR by name, e.g. {"idleTimeout": "300"}. Empty leaves F5's defaults. A map
	// rather than typed fields so a setting F5 adds needs no code change here.
	TCPSettings map[string]string `yaml:"tcp_settings,omitempty"`

	// TCPSettingsName is the F5BigTcpSetting to write. F5's reference cluster
	// carries a hand-applied `sys-default-tcp`.
	TCPSettingsName string `yaml:"tcp_settings_name,omitempty" default:"sys-default-tcp"`

	// Advanced carries per-component environment passthrough for the 2.4
	// CNEInstance's advanced.<component>.env[] lists (#175).
	//
	// A map rather than a struct because the component set belongs to the
	// product: F5 adds components between releases, and a struct would make each
	// addition a code change here before anyone could use it.
	//
	// omitempty, and the renderer emits nothing for an empty map — a 2.3
	// workspace's plan stays byte-identical.
	Advanced map[string]AdvancedComponentCfg `yaml:"advanced,omitempty"`

	// GSLBDatacenterName sets the optional CNEInstance GSLB datacenter name
	// (rendered as cneinstance_gslb_datacenter_name). Empty → the terraform default
	// (unset).
	GSLBDatacenterName string `yaml:"gslb_datacenter_name,omitempty"`
	// GTM is the BIG-IP DNS the datacenter above registers with (#51). nil →
	// unchanged behaviour: the datacenter name alone, as before.
	GTM *BNKGTMCfg `yaml:"gtm,omitempty"`

	// CertManager overrides cert-manager's namespace + chart version. nil → the
	// terraform defaults (cert-manager / the pinned chart version). The
	// install/skip toggle stays on resources.cert_manager.create.
	CertManager *BNKCertManagerCfg `yaml:"cert_manager,omitempty"`

	// Network holds the optional per-zone subnet CIDRs + TMM self-IPs for the
	// cloud-network-mapping ConfigMap and the external/internal F5SPKVlan CRs
	// (BNK install-guide "Configuration"). nil → the terraform module's
	// install-guide defaults apply. Zone NAMES are derived from the region, so
	// only the CIDRs/self-IPs live here. Rendered as cneinstance_network_zones.
	Network *BNKNetworkCfg `yaml:"network,omitempty"`

	// CIS holds the BIG-IP management endpoint + credentials the BNK CIS
	// controller (k8s-bigip-ctlr) uses. nil / empty → BNK is installed without
	// CIS (the bigip_* tfvars stay blank). Rendered as bigip_url / bigip_username
	// / bigip_password.
	CIS *BNKCISCfg `yaml:"cis,omitempty"`

	// LicenseMode selects the License CR operationMode: "connected" (default when
	// empty), "disconnected", or "f5licenseproxy". FLP mode additionally requires
	// the `flp` phase to be up (roksbnkctl flp up) so the BNK install can point at
	// the in-cluster F5 License Proxy. Empty → terraform default ("connected") →
	// the JWT/connected path is unchanged. Rendered as license_mode.
	LicenseMode string `yaml:"license_mode,omitempty" default:"connected"`

	// FLP holds settings for the optional F5 License Proxy phase. nil → FLP is not
	// deployed (and license_mode must not be f5licenseproxy). The proxy's root CA
	// and service endpoint are NOT config — they are produced by `flp up` and read
	// from flp-outputs.json when `bnk up` runs in FLP mode.
	FLP *BNKFLPCfg `yaml:"flp,omitempty"`

	// Preflight tunes the pre-install checks `bnk up` runs before it plans anything.
	// Nil takes the defaults; every field is independently optional.
	Preflight *BNKPreflightCfg `yaml:"preflight,omitempty"`
}

// BNKPreflightCfg tunes the per-node reachability gate.
//
// These are exposed because the right values are a property of the ENVIRONMENT, not
// of roksbnkctl. A Transit Gateway attachment is asynchronous — IBM programs the
// routes some time after the connection reports `attached` — and how long that takes
// varies by account, region and gateway. A probe run ~73s after attach saw both
// targets unreachable and refused an install whose path was healthy minutes later
// (issue #57), while a sibling cluster on the same gateway passed simply by landing
// on the other side of route programming.
//
// A site that consistently sees slower propagation should raise the budget rather
// than rediscover the race; a site with a static, long-established gateway can lower
// it to fail faster.
type BNKPreflightCfg struct {
	// ReachabilityRetrySeconds is how long a target may keep failing before the
	// verdict is believed. 0 disables retrying (one shot — the pre-v1.42 behaviour).
	// Blank/absent takes the default of 180.
	ReachabilityRetrySeconds *int `yaml:"reachability_retry_seconds,omitempty"`

	// ReachabilityTimeoutSeconds is how long to wait for the probe DaemonSet to
	// report from every node. It MUST exceed ReachabilityRetrySeconds, or the wait
	// gives up while the probe is still legitimately retrying — the config loader
	// raises it rather than let that misconfiguration through silently. Blank/absent
	// takes the default of 480.
	ReachabilityTimeoutSeconds *int `yaml:"reachability_timeout_seconds,omitempty"`
}

// BNKFLPCfg configures the F5 License Proxy (FLP) phase deployment. All optional;
// nil block means FLP is off. It never carries secrets — the FLP generates its own
// certs, and its subscription JWT is the same one resolved from COS.
type BNKFLPCfg struct {
	// Mode selects HOW the FLP phase deploys the proxy:
	//   "" | "helm" → the f5-license-proxy Helm chart into the ROKS cluster (default).
	//   "vsi"       → a standalone IBM Cloud VSI running the same four containers as a
	//                 podman pod (no Kubernetes). The VSI path reuses the cluster VPC so
	//                 the CWC reaches it directly, and terminates in the SAME
	//                 flp-outputs.json (endpoint + root CA) the helm path produces, so
	//                 `bnk up` consumes it unchanged. VSI-specific knobs live under `vsi:`.
	Mode string `yaml:"mode,omitempty"`

	// VSI configures the mode: vsi deployment backend. Ignored for mode: helm.
	VSI *BNKFLPVSICfg `yaml:"vsi,omitempty"`

	// Namespace the FLP is installed into (helm mode). Empty → DefaultFLPNamespace.
	Namespace string `yaml:"namespace,omitempty"`
	// ChartVersion pins the f5-license-proxy chart. Empty → the terraform default.
	ChartVersion string `yaml:"chart_version,omitempty"`

	// StorageClass is the dynamic StorageClass for the FLP's PVCs (rendered as
	// flp_storage_class). Empty → the terraform default (an IBM VPC block class).
	// Set it when the cluster/region exposes a different block-storage class.
	StorageClass string `yaml:"storage_class,omitempty"`

	// NodePortAccess exposes the proxy OUTSIDE its own cluster, so a BNK install in
	// a DIFFERENT cluster (same VPC, or across a transit gateway) can license
	// through it — the "shared licensing cluster" topology, where only the cluster
	// running the proxy needs egress to F5.
	//
	// Set by `flp up --add-node-port-access` (persisted here, so re-applies are
	// idempotent and the flag need not be repeated). Turning it on makes the proxy's
	// server certificate additionally cover the worker node IPs, and records an
	// externally-reachable endpoint in flp-outputs.json.
	NodePortAccess bool `yaml:"node_port_access,omitempty"`

	// NodePortSourceCIDRs, when set with NodePortAccess, opens the proxy's NodePort
	// on the cluster's worker security group to these CIDRs — the consuming cluster's
	// subnets. Empty leaves the security group alone (you are expected to have a path
	// already).
	//
	// A LIST, because a multi-zone VPC carries one address prefix PER ZONE (e.g.
	// 10.242.0.0/18, 10.242.64.0/18, 10.242.128.0/18). Allowing only one of them
	// silently works or fails depending on which zone the consuming pod happens to be
	// scheduled in — the CWC lands in an unlisted zone and its connection to the proxy
	// is dropped at the security group with a bare "connection timed out".
	NodePortSourceCIDRs []string `yaml:"node_port_source_cidrs,omitempty"`

	// External points a workspace at a FOREIGN proxy — one deployed by a DIFFERENT
	// workspace/cluster. When set, `bnk up` licenses against it and does NOT require
	// an `flp up` (nor an flp-outputs.json) in this workspace.
	External *BNKFLPExternalCfg `yaml:"external,omitempty"`
}

// BNKFLPVSICfg configures the mode: vsi FLP backend — a standalone VSI running the
// f5-license-proxy stack as a podman pod. All fields optional; sensible defaults apply.
type BNKFLPVSICfg struct {
	// NamePrefix prefixes the FLP VSI's IBM Cloud resource names — instance,
	// subnet, security group, floating IP, public gateway, boot volume, and the
	// VPC when this module creates one. Empty (the default) keeps the legacy
	// UNPREFIXED names.
	//
	// Empty is the default on purpose: renaming a terraform resource REPLACES it,
	// so defaulting this to the workspace prefix would destroy and rebuild every
	// running proxy on the next apply. Opt in deliberately.
	//
	// Set it when you need either of the two things the literals prevented (#88):
	// more than one standalone FLP in an account — the shared-licensing topology,
	// one proxy per environment — or `roksbnkctl cleanup` to be able to sweep the
	// proxy's resources at all, since that sweep matches `<prefix>-*` and
	// "flp-vsi" matches no workspace prefix.
	NamePrefix string `yaml:"name_prefix,omitempty"`

	// VPC is an existing VPC id to deploy the standalone FLP VSI into, WITHOUT any
	// ROKS cluster. Set it to run `flp up` (vsi mode) as a standalone licensing
	// appliance — e.g. in a services VPC that a disconnected cluster reaches over a
	// Transit Gateway (the cluster then references it via bnk.flp.external). Empty →
	// the FLP VSI joins the workspace's cluster VPC (from cluster-outputs.json), the
	// original behavior which requires a cluster.
	VPC string `yaml:"vpc,omitempty"`

	// CreateVPC builds the proxy its OWN VPC, address prefix and public gateway
	// rather than placing it in one that already exists (#60).
	//
	// The proxy is the component that needs egress to F5, which makes it a natural
	// FIRST deployment in an air-gapped estate — it could not be one, because there
	// was no create path and something else had to have made a VPC first. In
	// practice that meant landing it in the registry's VPC, coupling licensing to a
	// registry it has nothing to do with.
	//
	// Mutually exclusive with VPC. Default false keeps existing workspaces unchanged.
	CreateVPC bool `yaml:"create_vpc,omitempty"`
	// VPCName names the VPC created when CreateVPC is set. Empty derives it from
	// NamePrefix — `flp-vsi-vpc` with no prefix, `<prefix>-flp-vsi-vpc` with one.
	VPCName string `yaml:"vpc_name,omitempty"`
	// SubnetCIDR is the address prefix for that VPC. It must not overlap anything
	// the consuming clusters can already route to — a transit gateway silently
	// blackholes one of two overlapping VPCs.
	SubnetCIDR string `yaml:"subnet_cidr,omitempty"`
	// Profile is the IBM Cloud VSI instance profile. Empty → DefaultFLPVSIProfile
	// (bx2-4x16 — meets the FLP's 4 vCPU / 8 GB minimum).
	Profile string `yaml:"profile,omitempty"`
	// Zone the VSI lands in (e.g. us-south-1). Empty → the first zone of the cluster region.
	Zone string `yaml:"zone,omitempty"`
	// BootSizeGB is the boot volume size. 0 → 100 (clears the FLP's >80 GB requirement).
	BootSizeGB int `yaml:"boot_size_gb,omitempty"`
	// Reach selects the address the CWC dials: "private" (default — the VSI's VPC IP,
	// for a CWC in the same/peered VPC) or "floating" (a public floating IP).
	Reach string `yaml:"reach,omitempty"`
	// ManagementAllowedCIDRs are the source CIDRs permitted to reach the :80
	// flp-status web UI (read-only status). Empty → 0.0.0.0/0 (open — the page carries
	// no secrets). Rendered as flp_vsi_management_allowed_cidrs.
	ManagementAllowedCIDRs []string `yaml:"management_allowed_cidrs,omitempty"`
	// LicensingAllowedCIDRs are the source CIDRs permitted to reach the :8443 licensing
	// proxy (and :22 SSH). Empty → the RFC-1918 private ranges (the consuming cluster
	// reaches the proxy privately over the VPC / Transit Gateway). Rendered as
	// flp_vsi_licensing_allowed_cidrs.
	LicensingAllowedCIDRs []string `yaml:"licensing_allowed_cidrs,omitempty"`
	// AllowedCIDRs is DEPRECATED — a legacy single list. When set it seeds BOTH the
	// management and licensing planes (back-compat). Prefer the two per-plane fields
	// above. Rendered as flp_vsi_allowed_cidrs.
	AllowedCIDRs []string `yaml:"allowed_cidrs,omitempty"`
	// SSHKey is the name of an existing IBM Cloud VPC SSH key (RSA) to attach to the FLP
	// VSI, so an operator can SSH in to inspect/recover the licensing appliance (podman
	// pod, Vault, logs). Empty → no key attached (the VSI is unreachable by SSH). Port 22
	// is NOT opened by default; scope it via your own security-group rules if you need it.
	SSHKey string `yaml:"ssh_key,omitempty"`
	// FloatingIP attaches an operator floating IP to the FLP VSI for remote
	// management — running `roksbnkctl flp status` and reaching the :80 web UI + the
	// :8443 proxy from a machine OUTSIDE the VPC. It is NOT the CWC endpoint (the
	// consuming cluster always reaches the proxy privately over the VPC / Transit
	// Gateway); the floating IP is added to the leaf-cert SAN and recorded in
	// flp-outputs.json so `flp status` targets it. Reachability is still gated by
	// AllowedCIDRs (scope those to the operator's public IP for external access).
	// A *bool so an unset field means the module default (true); set false to opt out.
	FloatingIP *bool `yaml:"floating_ip,omitempty"`
	// StatusImage, when set, runs the flp-status web UI as a container in the FLP pod
	// (mobile-friendly status page + /api/status + live logs on :80, no auth — a
	// read-only private status endpoint). It is a container image reference, e.g.
	// <harbor>/bnk-status/flp-status:v1. Empty → no status UI (the proxy still serves
	// :8443). For an air-gapped VSI the image must be reachable from the VSI (a mirror
	// on the services VPC's Harbor, pulled by private IP).
	StatusImage string `yaml:"status_image,omitempty"`
	// StatusRegistryHost + StatusRegistryCAB64 make the VSI trust a self-signed mirror
	// so it can pull StatusImage: cloud-init drops the (base64) CA into
	// /etc/containers/certs.d/<host>/ca.crt before the pod comes up. Both empty → the
	// image host is assumed publicly trusted (or StatusImage is unset). Only needed for
	// a private/self-signed mirror such as the disconnected-deploy Harbor.
	StatusRegistryHost string `yaml:"status_registry_host,omitempty"`

	// StatusRegistryCAB64 is that registry's CA, base64-encoded, written to
	// /etc/containers/certs.d/<host>/ca.crt on the VSI. Needed only when the
	// mirror serves a private or self-signed certificate.
	StatusRegistryCAB64 string `yaml:"status_registry_ca_b64,omitempty"`
	// ForwardProxy optionally routes the VSI's egress to F5 licensing through an HTTP
	// forward proxy (air-gapped/egress-controlled networks). nil → direct egress.
	ForwardProxy *BNKFLPForwardProxyCfg `yaml:"forward_proxy,omitempty"`
}

// BNKFLPForwardProxyCfg describes an egress forward proxy for the FLP VSI's calls to
// F5's licensing backend (product-s.apis.f5.com).
type BNKFLPForwardProxyCfg struct {
	// Host is the forward proxy's address.
	Host string `yaml:"host,omitempty"`
	// Port is the forward proxy's port.
	Port     int    `yaml:"port,omitempty"`
	Protocol string `yaml:"protocol,omitempty"` // http (default) | https
}

// BNKFLPExternalCfg addresses an F5 License Proxy that this workspace does not own.
// Both fields come from the owning workspace's `roksbnkctl flp output` — the URL is
// its externally-reachable endpoint and the CA is its (base64) root CA, which the
// CWC must trust to complete the TLS handshake to the proxy.
type BNKFLPExternalCfg struct {
	// URL of the proxy, e.g. https://10.240.64.5:30001 — reachable from THIS
	// cluster's pods. Must be one of the names/IPs in the proxy's certificate.
	URL string `yaml:"url,omitempty"`
	// RootCAB64 is the proxy's root CA, base64-encoded (as `flp output` emits it).
	RootCAB64 string `yaml:"root_ca_b64,omitempty"`
}

// BNKCertManagerCfg overrides cert-manager's install coordinates. All optional;
// the create/skip decision stays on resources.cert_manager.create.
type BNKCertManagerCfg struct {
	// Namespace cert-manager installs into (rendered as cert_manager_namespace).
	// Empty → the terraform default ("cert-manager").
	Namespace string `yaml:"namespace,omitempty"`
	// Version pins the cert-manager Helm chart (rendered as cert_manager_version).
	// Empty → the terraform default. Set for air-gap / compliance version pinning.
	Version string `yaml:"version,omitempty"`
}

// BNKCISCfg configures the BNK CIS controller's BIG-IP target. All optional.
// BNKGTMCfg is the BIG-IP DNS / GTM the CNE controller registers its GSLB
// datacenter with (#51) — the connection half of GSLB, which until now only had
// the datacenter NAME.
//
// The password is stored base64-encoded (obfuscation, NOT encryption — like
// ibmcloud.api_key_b64 and bnk.cis.bigip_password_b64) and rendered raw into
// terraform.tfvars at apply time.
//
// Absent → nothing is emitted and the CNEInstance is unchanged, so GSLB stays
// exactly as it behaves today for every workspace that does not use it.
type BNKGTMCfg struct {
	// URL of the GTM/BIG-IP DNS management endpoint, e.g. https://gtm.example.com.
	URL string `yaml:"url,omitempty"`
	// Username to authenticate with.
	Username string `yaml:"username,omitempty"`
	// PasswordB64 is the base64 of the password. Env sets it from the RAW value.
	PasswordB64 string `yaml:"password_b64,omitempty"`
}

// BigIPPasswordB64 stores the password base64-encoded (obfuscation, NOT
// encryption — like ibmcloud.api_key_b64); the raw value is rendered to
// terraform.tfvars as bigip_password at apply time.
type BNKCISCfg struct {
	// BigIPURL is the management address of the classic BIG-IP that the Container
	// Ingress Services controller configures, e.g. "https://10.1.1.5". Empty
	// disables CIS; BNK's own data plane does not need it.
	BigIPURL string `yaml:"bigip_url,omitempty"`

	// BigIPUsername is the account CIS authenticates to that BIG-IP as.
	BigIPUsername string `yaml:"bigip_username,omitempty"`

	// BigIPPasswordB64 is that account's password, base64-encoded — obfuscation,
	// not encryption. Plaintext passwords in config.yaml are rejected at load.
	BigIPPasswordB64 string `yaml:"bigip_password_b64,omitempty"`
}

// BNKNetworkCfg is the optional cloud-network-mapping / VLAN zone data.
type BNKNetworkCfg struct {
	// Zones is the per-availability-zone network mapping, in zone order — one
	// entry per zone the cluster spans. Empty takes DefaultBNKNetworkZones, whose
	// addressing is arbitrary but self-consistent; override it when any of those
	// ranges collides with something the cluster can already route to.
	Zones []BNKZoneCfg `yaml:"zones,omitempty"`
	// VLANPrefixLen is the self-IP prefix length (spec.prefixlen_v4) TMM applies to
	// its external and internal self-IPs on the F5SPKVlan CRs — the size of the L2
	// subnet TMM treats as directly connected on each VLAN. nil → the terraform
	// default (24); set only when the VLAN subnets aren't /24. A pointer so "unset"
	// (fall back to the default) is distinct from a literal 0. Rendered as
	// cneinstance_vlan_prefixlen.
	VLANPrefixLen *int `yaml:"vlan_prefixlen,omitempty" default:"24"`
	// VLANPrefixLenExternal / VLANPrefixLenInternal override VLANPrefixLen for one
	// VLAN. nil → that VLAN uses VLANPrefixLen, so a deployment that does not need
	// them keeps one knob and one value.
	//
	// They exist because the two VLANs are not always the same size: TMM can front
	// a /23 externally while the internal side is a /26, which one shared scalar
	// cannot express. Setting them does NOT imply the subnets differ — the mask is
	// deliberately independent of the CIDRs, so a smaller or larger
	// directly-connected block can be forced and the remainder steered with static
	// routes.
	VLANPrefixLenExternal *int `yaml:"vlan_prefixlen_external,omitempty"`

	// VLANPrefixLenInternal is the same override for the INTERNAL VLAN. nil → it
	// uses VLANPrefixLen.
	VLANPrefixLenInternal *int `yaml:"vlan_prefixlen_internal,omitempty"`
	// TMMK8SRoutes is the Kubernetes pod CIDR TMM installs a route toward
	// (advanced.tmm.env TMM_K8S_ROUTES), so TMM can reach backend pods on the internal
	// data path. "" → the terraform default (the ROKS pod subnet 172.17.0.0/18); set
	// only for a non-default cluster pod CIDR. Rendered as cneinstance_tmm_k8s_routes.
	TMMK8SRoutes string `yaml:"tmm_k8s_routes,omitempty" default:"172.17.0.0/18"`
}

// GatewayCfg carries optional overrides for the Gateway phase (the BNK
// data-plane config). Every field is optional — unset values fall back to the
// terraform gateway module's BNK install-guide defaults. Rendered as gateway_*
// tfvars. The phase itself is driven by `roksbnkctl gateway up/down`, not a
// toggle here.
// StateCfg selects where terraform state lives (PRD 16). Backend "" or
// "local" (the default) keeps per-phase local tfstate under the workspace
// dir — the original behaviour. "s3" stores each phase's state in
// an S3-compatible bucket (IBM COS), so a stateless runner / parallel CI
// needs no shared volume, with native lockfile locking (terraform >= 1.10).
// Additive + omitempty — an absent `state:` block loads as the local default.
type StateCfg struct {
	Backend string `yaml:"backend,omitempty"` // "" | "local" | "s3"
	// S3 configures an S3-compatible backend (IBM COS) for terraform state, so a
	// workspace is not tied to one machine's disk. nil keeps state local.
	S3 *StateS3Cfg `yaml:"s3,omitempty"`
}

// StateS3Cfg configures the COS/S3 remote backend. The HMAC access/secret
// keys are NOT stored here — *KeySource names the env var they come from
// (env-first), and roksbnkctl injects them as AWS_* env to the terraform
// child, never into the rendered HCL or the state object.
type StateS3Cfg struct {
	Endpoint        string `yaml:"endpoint"`                    // COS S3 endpoint URL
	Bucket          string `yaml:"bucket"`                      // pre-provisioned bucket
	Region          string `yaml:"region"`                      // COS location / region
	KeyPrefix       string `yaml:"key_prefix,omitempty"`        // default: the workspace name
	AccessKeySource string `yaml:"access_key_source,omitempty"` // env var name; default ROKSBNKCTL_COS_HMAC_ACCESS_KEY
	SecretKeySource string `yaml:"secret_key_source,omitempty"` // env var name; default ROKSBNKCTL_COS_HMAC_SECRET_KEY
}

type GatewayCfg struct {
	// AppNamespace is the namespace the example application and its Gateway
	// resources are created in. Empty takes the terraform default.
	AppNamespace string `yaml:"app_namespace,omitempty" default:"f5-app"`

	// ClassName is the GatewayClass name. Empty → the terraform default
	// ("gateway-class"). GatewayClass is CLUSTER-scoped, so two BNK installs in
	// one cluster must not share it; that is what this exists for.
	ClassName string `yaml:"class_name,omitempty" default:"gateway-class"`
	// ControllerName is the GatewayClass controllerName. Empty → terraform
	// DERIVES it as "f5.com/<flo_namespace>-f5-cne-controller", which is the
	// value the CNE controller answers to. Leave it empty unless you are
	// pointing the GatewayClass at a controller this deployment did not install
	// — a wrong value fails silently (GatewayClass never Accepted, Gateway never
	// programmed, apply succeeds).
	ControllerName string `yaml:"controller_name,omitempty"`

	// BackendService is the Kubernetes Service the example HTTPRoute forwards to.
	BackendService string `yaml:"backend_service,omitempty" default:"nginx-service"`

	// BackendPort is the port on that Service.
	BackendPort int `yaml:"backend_port,omitempty" default:"80"`

	// EgressMode selects how return traffic is source-addressed: "snatpool",
	// "automap", or "both".
	EgressMode string `yaml:"egress_mode,omitempty" default:"snatpool"`

	// ClientSubnetLocal lists client CIDRs reachable on the same VPC as the
	// cluster — routed directly rather than over the transit gateway.
	ClientSubnetLocal []string `yaml:"client_subnet_local,omitempty"`

	// ClientSubnetRemote lists client CIDRs reached ACROSS the transit gateway,
	// e.g. the testing client VPC. Getting these wrong does not fail the apply:
	// traffic simply never returns.
	ClientSubnetRemote []string `yaml:"client_subnet_remote,omitempty"`

	// VXLANPort is the UDP port for the VXLAN overlay between TMM and the nodes.
	// Empty takes the terraform default; change it only if something else in the
	// VPC already claims that port.
	VXLANPort int `yaml:"vxlan_port,omitempty" default:"6789"`

	// RouteExamples names extra route kinds to create WORKING examples of,
	// alongside the HTTPRoute the gateway phase already creates. Empty (the
	// default) leaves an existing deployment byte-identical.
	//
	// What is valid here is a property of the Gateway API channel BNK installs,
	// not of this tool. BNK 2.3 pins Gateway API 1.4.1 STANDARD, which contains
	// no TCPRoute/TLSRoute/UDPRoute — BNK ships L4Route
	// (gateway.k8s.f5net.com/v1) for TCP instead. So on 2.3 the accepted values
	// are GRPCRoute and L4Route; terraform rejects anything else at plan time
	// rather than creating an object no controller will ever claim.
	//
	// Requesting L4Route also adds a TCP listener to the Gateway, because an
	// L4Route cannot attach to an HTTP listener.
	RouteExamples []string `yaml:"route_examples,omitempty"`
	// L4ListenerPort is the port for that TCP listener. 0 → terraform's 8080.
	L4ListenerPort int `yaml:"l4_listener_port,omitempty" default:"8080"`
}

// RegistryCfg configures the Sprint 29 air-gap registry mirror (PRD 11): which
// target the `roksbnkctl registry replicate` populates and which namespace it
// uses, plus the optional source/target credentials. All fields are optional —
// an absent block (nil) means the mirror is not configured and the BNK install
// pulls directly from FAR (far_repo_url). Additive + omitempty, so existing
// config.yaml files load unchanged.
type RegistryCfg struct {
	// Target selects the mirror backend: "icr" (IBM Container Registry — the
	// DEFAULT when unset) or "generic" (any OCI-compliant registry —
	// Artifactory / Harbor / Quay / registry:2). Empty resolves to "icr".
	Target string `yaml:"target,omitempty"`

	// ICRHost overrides the IBM Container Registry host for target=icr (e.g.
	// "de.icr.io"). Empty → derived from ibmcloud.region.
	ICRHost string `yaml:"icr_host,omitempty"`
	// ICRNamespace is the ICR namespace artifacts nest under for target=icr.
	// Empty → the workspace prefix.
	ICRNamespace string `yaml:"icr_namespace,omitempty"`

	// GenericHost is the OCI registry host for target=generic (e.g.
	// "artifactory.example.com").
	GenericHost string `yaml:"generic_host,omitempty"`
	// GenericRepoPrefix is the repository path artifacts nest under for
	// target=generic (e.g. an Artifactory repo key). Empty → no prefix.
	GenericRepoPrefix string `yaml:"generic_repo_prefix,omitempty"`
	// GenericUsername / GenericPasswordB64 are the static basic-auth credential
	// for target=generic (an Artifactory user + access token). The password is
	// base64-encoded; like the other `_b64` fields this is OBFUSCATION, not
	// encryption (it dodges rejectPlaintextSecrets) — chmod 600, never commit.
	// Both empty → anonymous push/pull. Templatable from the environment via
	// `init --override-from-env` (ROKSBNKCTL_GENERIC_PASSWORD).
	GenericUsername string `yaml:"generic_username,omitempty"`

	// GenericPasswordB64 is that user's password or access token,
	// base64-encoded — obfuscation, not encryption. REQUIRED whenever
	// GenericUsername is set: the chart pull otherwise falls through to the
	// literal username "unused" with the cluster's kube token, which is correct
	// only for the in-cluster OpenShift registry, and an external registry
	// answers `401: Bad Credentials`. `bnk up` refuses up front rather than
	// discovering this part-way through an apply.
	GenericPasswordB64 string `yaml:"generic_password_b64,omitempty"`

	// GenericCAB64 is the mirror's CA chain, PEM, base64-encoded — the
	// AUTHORITATIVE copy, recorded from the file that generated it rather than
	// learned from the network. When set, `registry replicate` never dials the
	// mirror to discover trust: it uses this, trusts it for the push, and records
	// it for the node CA-trust installer. A certificate is public data, so unlike
	// the `_b64` secret fields this is encoded only for single-line YAML safety.
	//
	// This is the preferred way to configure a self-signed mirror: you generate
	// the CA, so you already hold it and never need to learn it over the very
	// connection it is meant to authenticate.
	GenericCAB64 string `yaml:"generic_ca_b64,omitempty"`
	// GenericCASHA256 pins the mirror's CA by SHA-256 (hex; a "sha256:" prefix
	// and colons are accepted). It authenticates a CA *captured* from the host
	// when GenericCAB64 is not set — the capture is refused outright unless
	// either this pin is configured or --insecure-capture-ca is passed, because
	// a captured CA is installed into every node's trust store.
	GenericCASHA256 string `yaml:"generic_ca_sha256,omitempty"`

	// Namespace is the mirror project the artifacts land in. "" → "bnk-mirror".
	Namespace string `yaml:"namespace,omitempty"`

	// IncludeDeps unions the non-F5 dependency artifacts (Jetstack cert-manager
	// chart + images, the bitnami/kubectl node-labeler image) into the BOM. A
	// nil pointer means the default (true — a complete air-gap install set needs
	// them); set it explicitly to false to mirror only the F5 manifest artifacts.
	IncludeDeps *bool `yaml:"include_deps,omitempty"`

	// SourceServiceAccountB64 is the FAR `_json_key_base64` service-account JSON,
	// base64-encoded, used as the replication SOURCE credential for repo.f5.com.
	// Empty → roksbnkctl falls back to the COS-tarball service account (the same
	// path the FLO module uses), or an anonymous pull. Like ibmcloud.api_key_b64
	// this is OBFUSCATION, NOT ENCRYPTION — the field name deliberately ends in
	// `_b64` so it does not trip rejectPlaintextSecrets; treat the file as a
	// plaintext credential (chmod 600, never commit).
	SourceServiceAccountB64 string `yaml:"source_service_account_b64,omitempty"`
}

// MirrorNamespace returns the configured mirror namespace, or the "bnk-mirror"
// default when unset. Safe on a nil receiver (returns the default).
func (r *RegistryCfg) MirrorNamespace() string {
	if r == nil || r.Namespace == "" {
		return "bnk-mirror"
	}
	return r.Namespace
}

// IncludeDepsOrDefault returns IncludeDeps, defaulting to true when unset (a
// complete air-gap install set needs the non-F5 deps). Safe on a nil receiver.
func (r *RegistryCfg) IncludeDepsOrDefault() bool {
	if r == nil || r.IncludeDeps == nil {
		return true
	}
	return *r.IncludeDeps
}

// BNKZoneCfg is one availability zone's subnet CIDRs + TMM self-IPs. Field
// order/names match the terraform cneinstance_network_zones object.
type BNKZoneCfg struct {
	// ExtVLANCIDR is the zone's EXTERNAL VLAN — the client side of the data
	// plane, where traffic arrives. Overlay addressing internal to BNK: it does
	// not have to exist in the VPC, but it must not collide with anything the
	// cluster can route to.
	ExtVLANCIDR string `yaml:"ext_vlan_cidr"`

	// IntVLANCIDR is the zone's INTERNAL VLAN — the pod side, where BNK reaches
	// the workloads it fronts.
	IntVLANCIDR string `yaml:"int_vlan_cidr"`

	// IntSNATCIDR is the pool BNK source-NATs to when it talks to pods, so the
	// return traffic comes back through TMM rather than routing around it.
	IntSNATCIDR string `yaml:"int_snat_cidr"`

	// IntVIPCIDR is the range virtual servers are allocated from on the internal
	// side.
	IntVIPCIDR string `yaml:"int_vip_cidr"`

	// ExternalSelfIP is TMM's own address on the external VLAN. Must sit inside
	// ExtVLANCIDR.
	ExternalSelfIP string `yaml:"external_selfip"`

	// InternalSelfIP is TMM's own address on the internal VLAN. Must sit inside
	// IntVLANCIDR.
	InternalSelfIP string `yaml:"internal_selfip"`
}

type TestCfg struct {
	// Throughput configures the iperf3 bandwidth probe.
	Throughput ThroughputCfg `yaml:"throughput,omitempty"`

	// Connectivity configures the reachability probes — the hosts `roksbnkctl
	// test` tries to reach, and from where.
	Connectivity ConnectivityCfg `yaml:"connectivity,omitempty"`

	// DNS configures the name-resolution probes, including which resolvers to
	// ask. A disconnected cluster commonly resolves through its own forwarders,
	// and a probe against a public resolver would report a failure that says
	// nothing about the estate.
	DNS DNSCfg `yaml:"dns,omitempty"`
}

// DNSCfg drives the Sprint 5 flag-driven DNS probe (PRD 03 §"DNS probe
// (GSLB-aware)" §"Server resolution"). The map's keys are the names
// users pass to `--server <name>` and the values are concrete
// "<ip>[:<port>]" strings the miekg/dns client dials. DefaultTarget is
// used when --target isn't passed on the command line.
//
// Example:
//
//	test:
//	  dns:
//	    resolvers:
//	      google:     "8.8.8.8:53"
//	      cloudflare: "1.1.1.1:53"
//	      gslb-vip:   "169.45.91.5:53"
//	    default_target: "www.example.com"
type DNSCfg struct {
	// Resolvers names the DNS servers to query, as name → "<ip>[:<port>]". A
	// disconnected cluster resolves through its own forwarders, so asking a public
	// resolver would test the wrong thing.
	Resolvers map[string]string `yaml:"resolvers,omitempty"`

	// DefaultTarget is the resolver queried when a probe names none. Empty uses
	// the first entry in Resolvers.
	DefaultTarget string `yaml:"default_target,omitempty"`
}

type ThroughputCfg struct {
	Image       string `yaml:"image,omitempty" default:"networkstatic/iperf3:latest"` // default: networkstatic/iperf3:latest
	Duration    int    `yaml:"duration,omitempty" default:"30"`                       // seconds; default 30
	Streams     int    `yaml:"streams,omitempty" default:"8"`                         // parallel; default 8
	DefaultMode string `yaml:"default_mode,omitempty" default:"north-south"`          // north-south | east-west
}

type ConnectivityCfg struct {
	// ExtraHosts are additional targets the connectivity probe tries, beyond the
	// defaults. Give a disconnected estate something it can actually reach — a
	// probe against a public host reports a failure that says nothing about it.
	ExtraHosts []string `yaml:"extra_hosts,omitempty"`
}

// TFSourceCfg picks where Terraform's source tree comes from. Type
// drives which other fields apply:
//
//   - embedded — uses the HCL bundled into the roksbnkctl binary via
//     Go's embed directive. No other fields needed. This is the
//     default and what most users want; install one binary, get
//     CLI + matched TF together.
//   - github — downloads a tarball release from a GitHub repo. Repo
//     ("owner/name") and Ref (release tag) required. For testing
//     forks or pinning to a specific upstream tag.
//   - local — points Terraform at a directory on disk. Path required.
//     For active development on the HCL itself.
//
// An empty Type (legacy / forgot-to-set) is treated as embedded.
type TFSourceCfg struct {
	// Type selects where the Terraform comes from: "embedded" (the tree compiled
	// into this binary — the default, and the only one guaranteed to match it),
	// "github" (a released tree), or "local" (a path on disk, for testing a fork).
	Type string `yaml:"type"`

	// Repo is the owner/name to fetch from when Type is "github".
	Repo string `yaml:"repo,omitempty"`

	// Ref is the tag, branch or commit to fetch when Type is "github". Pin it to
	// a tag: a branch re-resolves on every run, so two applies days apart can
	// deploy different infrastructure from identical config.
	Ref string `yaml:"ref,omitempty"`

	// Path is the local directory holding the Terraform tree when Type is
	// "local".
	Path string `yaml:"path,omitempty"`
}

// COSCfg points roksbnkctl at the IBM Cloud Object Storage that holds the FAR
// auth key + subscription JWT (the "orchestration" COS). Empty fields fall back
// to the built-in defaults (bnk-supply-chain / bnk-artifacts /
// us-south). These are honoured BOTH by the terraform render
// (ibmcloud_cos_instance_name / ibmcloud_resources_cos_bucket /
// ibmcloud_cos_bucket_region) AND by the `registry` FAR-file resolver, so a
// customer-owned COS bucket is used consistently across both.
type COSCfg struct {
	// Instance is the IBM Cloud Object Storage service instance holding the
	// bucket.
	Instance string `yaml:"instance,omitempty"`

	// Bucket is the bucket the FAR service-account credential is read from, for
	// estates that stage it centrally rather than passing a local file.
	Bucket string `yaml:"bucket,omitempty"`

	// Region is the bucket's region, which need not match ibmcloud.region.
	Region string `yaml:"region,omitempty"`

	// Upload lists local files to place into that bucket before the phases that
	// read them run.
	Upload []COSUpload `yaml:"upload,omitempty"`
}

type COSUpload struct {
	// Source is the local file to upload.
	Source string `yaml:"source"`
	// Key is the object name it is stored under in the bucket. This is the name
	// the other settings refer to — bnk.far_auth_file names a Key, not a Source.
	Key string `yaml:"key"`
}

// ErrWorkspaceNotFound is returned by LoadWorkspace when the workspace's
// config.yaml does not exist. Callers (e.g. `roksbnkctl init`) check for this
// to distinguish "workspace doesn't exist yet" from real I/O errors.
var ErrWorkspaceNotFound = errors.New("workspace not found")

// validNameRE constrains workspace names to filesystem-safe identifiers so
// we never accidentally interpret a path traversal as a name. Names must
// start with alphanumeric (rejects ".", "..", "-leading"), be at most 64
// chars, and contain only [A-Za-z0-9_.-].
var validNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)

// ValidateName rejects empty / overlong / path-traversing workspace names.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("workspace name is empty")
	}
	if !validNameRE.MatchString(name) {
		return fmt.Errorf("workspace name %q is invalid: must be 1–64 chars, [A-Za-z0-9_.-], starting with alphanumeric", name)
	}
	return nil
}

// LoadWorkspace reads ~/.roksbnkctl/<name>/config.yaml. Returns
// ErrWorkspaceNotFound (wrapped) if the file is missing.
func LoadWorkspace(name string) (*Workspace, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	path, err := WorkspaceConfigPath(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrWorkspaceNotFound, name)
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := rejectPlaintextSecrets(b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var ws Workspace
	if err := yaml.Unmarshal(b, &ws); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	warnOnLoosePerms(name)
	return &ws, nil
}

// SaveWorkspace writes ~/.roksbnkctl/<name>/config.yaml, creating both the
// workspace dir and its state/ subdir.
func SaveWorkspace(name string, ws *Workspace) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	cfgPath, err := WorkspaceConfigPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), SecretDirMode); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(cfgPath), err)
	}
	stateDir, err := WorkspaceStateDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, SecretDirMode); err != nil {
		return fmt.Errorf("creating %s: %w", stateDir, err)
	}
	b, err := yaml.Marshal(ws)
	if err != nil {
		return fmt.Errorf("encoding workspace config: %w", err)
	}
	// Owner-only: this file can hold ibmcloud.api_key_b64 and
	// registry.generic_password_b64, which are base64 and not encrypted.
	if err := os.WriteFile(cfgPath, b, SecretFileMode); err != nil {
		return fmt.Errorf("writing %s: %w", cfgPath, err)
	}
	// WriteFile does not narrow an existing file's mode, and MkdirAll does not
	// touch an existing directory's — so a workspace written by an older build
	// stays world-readable until this runs.
	//
	// Best-effort: a filesystem that cannot hold 0600 (a WSL DrvFs mount without
	// metadata, an SMB share) must not make `init` fail outright — the workspace
	// itself was written correctly, and refusing to save it would leave the user
	// with no workspace at all rather than a workspace with loose permissions.
	// `roksbnkctl doctor` reports the tree it could not tighten.
	_, _ = SecureWorkspacePerms(name)
	return nil
}

// ListWorkspaces returns the names of every directory under BaseDir that
// looks like a workspace (contains config.yaml). Order: filesystem-natural
// (which os.ReadDir sorts alphabetically on most platforms).
func ListWorkspaces() ([]string, error) {
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cfg := filepath.Join(base, e.Name(), workspaceConfigFile)
		if _, err := os.Stat(cfg); err == nil {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// WorkspaceExists is a stat-only check.
func WorkspaceExists(name string) bool {
	if err := ValidateName(name); err != nil {
		return false
	}
	cfg, err := WorkspaceConfigPath(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(cfg)
	return err == nil
}

// DeleteWorkspace removes ~/.roksbnkctl/<name>/. Refuses to delete if the
// workspace's terraform.tfstate has resources (would orphan live infra)
// unless force is true.
func DeleteWorkspace(name string, force bool) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	dir, err := WorkspaceDir(name)
	if err != nil {
		return err
	}
	if !force {
		statePath := filepath.Join(dir, stateSubdir, "terraform.tfstate")
		if has, _ := tfstateHasResources(statePath); has {
			return fmt.Errorf("workspace %q has terraform-managed resources; pass --force to delete anyway", name)
		}
	}
	return os.RemoveAll(dir)
}

// plaintextSecretsRE matches lines that look like a credential value being
// set in YAML. Heuristic — catches the common shapes (api_key, password,
// token) without false-positiving on commented-out examples or empty values.
var plaintextSecretsRE = regexp.MustCompile(`(?m)^[\t ]*(api_key|apikey|ibmcloud_api_key|ic_api_key|password|token|secret_access_key|hmac_secret)[\t ]*:[\t ]+[^\s#\n][^\n]*`)

func rejectPlaintextSecrets(b []byte) error {
	if loc := plaintextSecretsRE.FindIndex(b); loc != nil {
		return fmt.Errorf("plaintext secret detected (offset %d): workspace config.yaml must not contain credentials — use IBMCLOUD_API_KEY env var or the OS keychain (see `roksbnkctl init`)", loc[0])
	}
	return nil
}

// AdvancedComponentCfg is one component's advanced settings. Only env is
// surfaced today; the CR's advanced block carries more, and this is the shape
// that lets those arrive without moving what already works.
type AdvancedComponentCfg struct {
	// Env sets environment variables on that component's containers, name → value.
	// Merged over what roksbnkctl renders, so a key here wins.
	Env map[string]string `yaml:"env,omitempty"`
}
