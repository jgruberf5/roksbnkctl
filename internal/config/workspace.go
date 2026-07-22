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
	IBMCloud IBMCloudCfg          `yaml:"ibmcloud"`
	Cluster  ClusterCfg           `yaml:"cluster"`
	BNK      BNKCfg               `yaml:"bnk,omitempty"`
	Gateway  GatewayCfg           `yaml:"gateway,omitempty"`
	Registry *RegistryCfg         `yaml:"registry,omitempty"`
	Test     TestCfg              `yaml:"test,omitempty"`
	TFSource TFSourceCfg          `yaml:"tf_source"`
	COS      *COSCfg              `yaml:"cos,omitempty"`
	Targets  map[string]TargetCfg `yaml:"targets,omitempty"`
	State    StateCfg             `yaml:"state,omitempty"`
	BNKForge *BNKForgeCfg         `yaml:"bnkforge,omitempty"`
	Agent    *AgentCfg            `yaml:"agent,omitempty"`

	// Prefix is the workspace's account-scoped resource-name base
	// (Sprint 26, issues/issue_sprint26_staff.md). When non-empty, the
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
	// Insecure skips TLS verification against the Forge server (self-signed
	// certs, common in lab installs). Also settable via --insecure.
	Insecure bool `yaml:"insecure,omitempty"`
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
	Host      string `yaml:"host"`
	Port      int    `yaml:"port,omitempty"` // default 22
	User      string `yaml:"user"`
	KeyPath   string `yaml:"key_path,omitempty"`   // file path (PEM)
	KeySource string `yaml:"key_source,omitempty"` // "agent" | "tf-output:<name>"
}

type IBMCloudCfg struct {
	Region        string `yaml:"region"`
	ResourceGroup string `yaml:"resource_group"`
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
	Create           bool   `yaml:"create"`
	Name             string `yaml:"name"`
	OpenShiftVersion string `yaml:"openshift_version,omitempty"`
	WorkersPerZone   int    `yaml:"workers_per_zone,omitempty"`

	// MinWorkerVCPUCount / MinWorkerMemoryGB drive the worker-flavor auto-select
	// (the cluster module picks the smallest bx2 profile meeting both minimums).
	// Rendered as roks_min_worker_vcpu_count / roks_min_worker_memory_gb; 0 (unset)
	// leaves the terraform defaults (16 vCPU / 64 GB). Only meaningful when Create.
	MinWorkerVCPUCount int `yaml:"min_worker_vcpu_count,omitempty"`
	MinWorkerMemoryGB  int `yaml:"min_worker_memory_gb,omitempty"`
}

// ResourcesCfg holds the per-resource create toggles for a prefix-driven
// workspace (Sprint 26). The cluster itself is NOT here — it reuses the
// existing ClusterCfg.Create / ClusterCfg.Name (Name doubles as the
// existing id/name when Create=false, as today). Each toggle carries an
// Existing name/ID used when Create=false and a live dependent still needs
// to reference the resource by name.
type ResourcesCfg struct {
	TransitGateway   ResourceToggle `yaml:"transit_gateway"`
	RegistryCOS      ResourceToggle `yaml:"registry_cos"`
	CertManager      ResourceToggle `yaml:"cert_manager"`
	BNK              ResourceToggle `yaml:"bnk"`
	TGWJumphost      ResourceToggle `yaml:"tgw_jumphost"`
	ClusterJumphosts ResourceToggle `yaml:"cluster_jumphosts"`
	ClientVPC        ResourceToggle `yaml:"client_vpc"`
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
	TestingClientVPCName string `yaml:"testing_client_vpc_name,omitempty"`
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
	TestingMinVCPUCount    int    `yaml:"testing_min_vcpu_count,omitempty"`
	TestingMinMemoryGB     int    `yaml:"testing_min_memory_gb,omitempty"`
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
func DefaultResources() *ResourcesCfg {
	return &ResourcesCfg{
		TransitGateway:   ResourceToggle{Create: true},
		RegistryCOS:      ResourceToggle{Create: true},
		CertManager:      ResourceToggle{Create: true},
		BNK:              ResourceToggle{Create: true},
		TGWJumphost:      ResourceToggle{Create: true},
		ClusterJumphosts: ResourceToggle{Create: false},
		ClientVPC:        ResourceToggle{Create: true},
		ClusterVPC:       ResourceToggle{Create: true},
	}
}

// ResourceToggle is one create/reuse decision: Create=true provisions the
// resource (under its prefix-derived name); Create=false reuses an existing
// one named by Existing (when a live dependent consumes it).
type ResourceToggle struct {
	Create   bool   `yaml:"create"`
	Existing string `yaml:"existing,omitempty"` // existing name/ID when Create=false
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
	DefaultSubscriptionJWTFile = "trial.jwt"
	// DefaultLicenseMode is the terraform License CR operationMode default; an
	// empty bnk.license_mode leaves it unset (terraform defaults to "connected"),
	// so JWT/connected licensing is unchanged unless FLP is opted into.
	DefaultLicenseMode = "connected"
	// DefaultFLPNamespace is where the `flp` phase installs the F5 License Proxy.
	DefaultFLPNamespace = "f5-license-proxy"
)

type BNKCfg struct {
	CNEInstanceSize string `yaml:"cneinstance_size,omitempty"`
	FARRepoURL      string `yaml:"far_repo_url,omitempty"`
	ManifestVersion string `yaml:"manifest_version,omitempty"`
	// FarAuthFile is the FAR auth tarball's filename in the orchestration COS
	// bucket; rendered as the f5_cne_far_auth_file tfvar + used by `registry`
	// to resolve the FAR _json_key_base64 service account.
	FarAuthFile string `yaml:"far_auth_file,omitempty"`
	// SubscriptionJWTFile is the subscription/license JWT's filename in the
	// orchestration COS bucket; rendered as the f5_cne_subscription_jwt_file tfvar.
	SubscriptionJWTFile string `yaml:"subscription_jwt_file,omitempty"`

	// CRMode selects the BNK custom-resource install mechanism rendered as
	// the bnk_cr_mode tfvar (Sprint 27). "" / "kubectl" → the terraform-native
	// helm_release + alekc/kubectl kubectl_manifest + wait_for path (default);
	// "legacy_curl" → the null_resource/curl/time_sleep baseline kept behind
	// the flag for the validator's benchmark. The `--legacy-bnk` flag sets
	// "legacy_curl" at runtime, overriding this config value.
	CRMode string `yaml:"cr_mode,omitempty"`

	// FLONamespace / FLOUtilsNamespace override the namespaces the F5 Lifecycle
	// Operator and its utility components install into (rendered as flo_namespace /
	// flo_utils_namespace). Empty → the terraform defaults (f5-bnk / f5-utils). Set
	// these for multi-tenant clusters or to avoid namespace collisions.
	FLONamespace      string `yaml:"flo_namespace,omitempty"`
	FLOUtilsNamespace string `yaml:"flo_utils_namespace,omitempty"`

	// GSLBDatacenterName sets the optional CNEInstance GSLB datacenter name
	// (rendered as cneinstance_gslb_datacenter_name). Empty → the terraform default
	// (unset).
	GSLBDatacenterName string `yaml:"gslb_datacenter_name,omitempty"`

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
	LicenseMode string `yaml:"license_mode,omitempty"`

	// FLP holds settings for the optional F5 License Proxy phase. nil → FLP is not
	// deployed (and license_mode must not be f5licenseproxy). The proxy's root CA
	// and service endpoint are NOT config — they are produced by `flp up` and read
	// from flp-outputs.json when `bnk up` runs in FLP mode.
	FLP *BNKFLPCfg `yaml:"flp,omitempty"`
}

// BNKFLPCfg configures the F5 License Proxy (FLP) phase deployment. All optional;
// nil block means FLP is off. It never carries secrets — the FLP generates its own
// certs, and its subscription JWT is the same one resolved from COS.
type BNKFLPCfg struct {
	// Namespace the FLP is installed into. Empty → DefaultFLPNamespace.
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
// BigIPPasswordB64 stores the password base64-encoded (obfuscation, NOT
// encryption — like ibmcloud.api_key_b64); the raw value is rendered to
// terraform.tfvars as bigip_password at apply time.
type BNKCISCfg struct {
	BigIPURL         string `yaml:"bigip_url,omitempty"`
	BigIPUsername    string `yaml:"bigip_username,omitempty"`
	BigIPPasswordB64 string `yaml:"bigip_password_b64,omitempty"`
}

// BNKNetworkCfg is the optional cloud-network-mapping / VLAN zone data.
type BNKNetworkCfg struct {
	Zones []BNKZoneCfg `yaml:"zones,omitempty"`
}

// GatewayCfg carries optional overrides for the Gateway phase (the BNK
// data-plane config). Every field is optional — unset values fall back to the
// terraform gateway module's BNK install-guide defaults. Rendered as gateway_*
// tfvars. The phase itself is driven by `roksbnkctl gateway up/down`, not a
// toggle here.
// StateCfg selects where terraform state lives (PRD 16). Backend "" or
// "local" (the default) keeps per-phase local tfstate under the workspace
// dir — byte-identical to pre-Sprint-31. "s3" stores each phase's state in
// an S3-compatible bucket (IBM COS), so a stateless runner / parallel CI
// needs no shared volume, with native lockfile locking (terraform >= 1.10).
// Additive + omitempty — an absent `state:` block loads as the local default.
type StateCfg struct {
	Backend string      `yaml:"backend,omitempty"` // "" | "local" | "s3"
	S3      *StateS3Cfg `yaml:"s3,omitempty"`
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
	AppNamespace       string   `yaml:"app_namespace,omitempty"`
	BackendService     string   `yaml:"backend_service,omitempty"`
	BackendPort        int      `yaml:"backend_port,omitempty"`
	EgressMode         string   `yaml:"egress_mode,omitempty"` // snatpool | automap | both
	ClientSubnetLocal  []string `yaml:"client_subnet_local,omitempty"`
	ClientSubnetRemote []string `yaml:"client_subnet_remote,omitempty"`
	VXLANPort          int      `yaml:"vxlan_port,omitempty"`
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
	GenericUsername    string `yaml:"generic_username,omitempty"`
	GenericPasswordB64 string `yaml:"generic_password_b64,omitempty"`

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
	ExtVLANCIDR    string `yaml:"ext_vlan_cidr"`
	IntVLANCIDR    string `yaml:"int_vlan_cidr"`
	IntSNATCIDR    string `yaml:"int_snat_cidr"`
	IntVIPCIDR     string `yaml:"int_vip_cidr"`
	ExternalSelfIP string `yaml:"external_selfip"`
	InternalSelfIP string `yaml:"internal_selfip"`
}

type TestCfg struct {
	Throughput   ThroughputCfg   `yaml:"throughput,omitempty"`
	Connectivity ConnectivityCfg `yaml:"connectivity,omitempty"`
	DNS          DNSCfg          `yaml:"dns,omitempty"`
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
	Resolvers     map[string]string `yaml:"resolvers,omitempty"`
	DefaultTarget string            `yaml:"default_target,omitempty"`
}

type ThroughputCfg struct {
	Image       string `yaml:"image,omitempty"`        // default: networkstatic/iperf3:latest
	Duration    int    `yaml:"duration,omitempty"`     // seconds; default 30
	Streams     int    `yaml:"streams,omitempty"`      // parallel; default 8
	DefaultMode string `yaml:"default_mode,omitempty"` // north-south | east-west
}

type ConnectivityCfg struct {
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
	Type string `yaml:"type"` // embedded | github | local
	Repo string `yaml:"repo,omitempty"`
	Ref  string `yaml:"ref,omitempty"`
	Path string `yaml:"path,omitempty"` // populated for type=local
}

// COSCfg points roksbnkctl at the IBM Cloud Object Storage that holds the FAR
// auth key + subscription JWT (the "orchestration" COS). Empty fields fall back
// to the built-in defaults (bnk-orchestration / bnk-schematics-resources /
// us-south). These are honoured BOTH by the terraform render
// (ibmcloud_cos_instance_name / ibmcloud_resources_cos_bucket /
// ibmcloud_cos_bucket_region) AND by the `registry` FAR-file resolver, so a
// customer-owned COS bucket is used consistently across both.
type COSCfg struct {
	Instance string      `yaml:"instance,omitempty"`
	Bucket   string      `yaml:"bucket,omitempty"`
	Region   string      `yaml:"region,omitempty"`
	Upload   []COSUpload `yaml:"upload,omitempty"`
}

type COSUpload struct {
	Source string `yaml:"source"`
	Key    string `yaml:"key"`
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
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(cfgPath), err)
	}
	stateDir, err := WorkspaceStateDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", stateDir, err)
	}
	b, err := yaml.Marshal(ws)
	if err != nil {
		return fmt.Errorf("encoding workspace config: %w", err)
	}
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", cfgPath, err)
	}
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
