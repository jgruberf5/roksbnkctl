package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Workspace is ~/.awsbnkctl/<name>/config.yaml.
//
// There is no `api_key` field — secrets live in env vars or the OS
// keychain, never in this struct. Plaintext keys in the YAML are
// rejected at load time by rejectPlaintextSecrets.
//
// The `aws:` block is the only first-class cloud shape. AWS credentials
// resolve via the SDK chain (env / shared config / profile / instance
// role / SSO) — never written to the workspace file. Legacy on-disk
// workspaces with a v0.x cloud-cred block load cleanly because YAML
// ignores unknown keys at unmarshal time; the region/profile/etc values
// formerly carried under that block are no longer consulted. Operators
// upgrading from a v0.1 workspace must re-run `awsbnkctl init` to
// populate the `aws:` block.
type Workspace struct {
	// AWS is the only first-class cloud block; new workspaces written
	// by `awsbnkctl init` populate this. Doctor + tf vars renderer
	// + inspect / workspaces.go all read from here. Region empty
	// means "let the SDK chain resolve AWS_REGION".
	AWS AWSCfg `yaml:"aws,omitempty"`

	Cluster ClusterCfg           `yaml:"cluster"`
	BNK     BNKCfg               `yaml:"bnk,omitempty"`
	Test    TestCfg              `yaml:"test,omitempty"`
	COS     *COSCfg              `yaml:"cos,omitempty"`
	Targets map[string]TargetCfg `yaml:"targets,omitempty"`

	// Exec is the per-tool execution-backend config block. Maps a tool
	// name (`iperf3`, `kubectl`) to its preferred backend (`local`,
	// `docker`, `k8s`, or `ssh:<target>`). Per-invocation `--backend`
	// flag wins over the workspace config; missing entries default to
	// `local`.
	//
	// Example:
	//
	//   exec:
	//     iperf3: { backend: k8s }
	Exec map[string]ExecToolCfg `yaml:"exec,omitempty"`
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

// AWSCfg is the AWS-shaped workspace block. Mirrors the inputs
// `awsbnkctl init` (AWS path) collects and threads into the AWS-SDK
// provisioning phases.
//
// No credential fields appear here: AWS credentials resolve via the
// SDK's default chain (env / shared config / profile / instance role /
// SSO) — never written to config.yaml. The Profile field below is a
// chain hint, not a secret.
type AWSCfg struct {
	// Region is the AWS region (e.g. "us-east-1"). Threaded into the
	// AWS client Options used by the provisioning phases.
	Region string `yaml:"region,omitempty"`

	// Profile is an optional AWS_PROFILE override pinned per workspace.
	// Empty = use the standard chain's default. The doctor's
	// credentials-configured probe reads this when constructing
	// aws.Clients.
	Profile string `yaml:"profile,omitempty"`

	// VPCID is the VPC hosting the cluster. Empty = `awsbnkctl init`
	// will offer the "create new VPC" path (v1.x requires an existing
	// VPC ID).
	VPCID string `yaml:"vpc_id,omitempty"`

	// SubnetIDs is the list of private subnets passed through to the
	// EKS cluster module. >=3 AZs are required for HA; the init wizard
	// enforces this.
	SubnetIDs []string `yaml:"subnet_ids,omitempty"`

	// SupplyChain captures the local FAR archive + JWT paths the init
	// wizard collected. These are uploaded to the S3 supply-chain
	// bucket via `internal/aws/s3.go` at workspace save time (or via
	// the S3 upload phase on `awsbnkctl up`). The fields are local
	// paths, not secrets — empty when the operator skipped the wizard's
	// supply-chain step (e.g. `--dry-run`).
	SupplyChain SupplyChainCfg `yaml:"supply_chain,omitempty"`
}

// SupplyChainCfg is the local-path manifest the init wizard collects.
// Lives under AWS.SupplyChain. Empty for `--dry-run` invocations or
// when the operator deferred supply-chain bootstrap to a later
// `awsbnkctl init --supply-chain` flow.
type SupplyChainCfg struct {
	// FARArchivePath is the local filesystem path to the
	// `f5cne-far-auth-*.tar.gz` archive supplied by F5.
	FARArchivePath string `yaml:"far_archive_path,omitempty"`

	// JWTPath is the local filesystem path to the
	// `f5cne-subscription-*.jwt` licence file.
	JWTPath string `yaml:"jwt_path,omitempty"`

	// BucketName overrides the auto-generated supply-chain bucket
	// name (`awsbnkctl-<workspace>-<random>` is the default).
	// Empty = let the provisioner pick.
	BucketName string `yaml:"bucket_name,omitempty"`

	// KMSKeyARN pins an existing CMK ARN; empty = the
	// s3_supply_chain module creates one.
	KMSKeyARN string `yaml:"kms_key_arn,omitempty"`

	// FLONamespace is the Kubernetes namespace the FLO service
	// account lives in. Defaults to "flo-system".
	FLONamespace string `yaml:"flo_namespace,omitempty"`

	// EnableECRMirror gates the optional ECR mirror module (v1.0
	// stretch). Default false; v1.x promotes to first-class.
	EnableECRMirror bool `yaml:"enable_ecr_mirror,omitempty"`
}

type ClusterCfg struct {
	Create           bool   `yaml:"create"`
	Name             string `yaml:"name"`
	OpenShiftVersion string `yaml:"openshift_version,omitempty"`
	WorkersPerZone   int    `yaml:"workers_per_zone,omitempty"`
}

type BNKCfg struct {
	CNEInstanceSize string `yaml:"cneinstance_size,omitempty"`
	FARRepoURL      string `yaml:"far_repo_url,omitempty"`
	ManifestVersion string `yaml:"manifest_version,omitempty"`
}

type TestCfg struct {
	Throughput   ThroughputCfg   `yaml:"throughput,omitempty"`
	Connectivity ConnectivityCfg `yaml:"connectivity,omitempty"`
	DNS          DNSCfg          `yaml:"dns,omitempty"`
}

// DNSCfg drives the flag-driven DNS probe. The map's keys are the names
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

type COSCfg struct {
	Instance string      `yaml:"instance,omitempty"`
	Bucket   string      `yaml:"bucket,omitempty"`
	Upload   []COSUpload `yaml:"upload,omitempty"`
}

type COSUpload struct {
	Source string `yaml:"source"`
	Key    string `yaml:"key"`
}

// ErrWorkspaceNotFound is returned by LoadWorkspace when the workspace's
// config.yaml does not exist. Callers (e.g. `awsbnkctl init`) check for this
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

// LoadWorkspace reads ~/.awsbnkctl/<name>/config.yaml. Returns
// ErrWorkspaceNotFound (wrapped) if the file is missing.
func LoadWorkspace(name string) (*Workspace, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	path, err := WorkspaceConfigPath(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path) // #nosec G304 -- path is derived from ValidateName(name) + WorkspaceConfigPath layout, not user-tainted
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

// SaveWorkspace writes ~/.awsbnkctl/<name>/config.yaml, creating both the
// workspace dir and its state/ subdir.
func SaveWorkspace(name string, ws *Workspace) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	cfgPath, err := WorkspaceConfigPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(cfgPath), err)
	}
	stateDir, err := WorkspaceStateDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", stateDir, err)
	}
	b, err := yaml.Marshal(ws)
	if err != nil {
		return fmt.Errorf("encoding workspace config: %w", err)
	}
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
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

// DeleteWorkspace removes ~/.awsbnkctl/<name>/. Refuses to delete if the
// workspace's IDs cache (state/state.env) still lists provisioned AWS
// resources (deleting would orphan live infra) unless force is true.
//
// Per D-001…D-007 the provisioning state is the shell-sourceable
// state.env IDs cache (internal/aws/state), not an HCL state file. The
// guard reads state/state.env under the workspace dir and refuses
// deletion when any of the load-bearing resource-ID keys is populated. A
// missing / unreadable state.env reads as "no resources" so a
// never-applied (or already torn-down) workspace deletes cleanly.
func DeleteWorkspace(name string, force bool) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	dir, err := WorkspaceDir(name)
	if err != nil {
		return err
	}
	if !force {
		stateEnvPath := filepath.Join(dir, stateSubdir, "state.env")
		if stateEnvHasResources(stateEnvPath) {
			return fmt.Errorf("workspace %q has provisioned AWS resources recorded in state.env; pass --force to delete anyway", name)
		}
	}
	return os.RemoveAll(dir)
}

// stateEnvResourceKeys is the set of state.env keys whose presence (a
// non-empty value) means the workspace still tracks live AWS infra. Kept
// deliberately narrow to the top-level resources whose orphaning would
// cost money / leak: the VPC, the EKS cluster, the default node group,
// and the two BNK-path instances. Phase code clears these on `down`, so a
// populated value here is a strong "live infra" signal.
var stateEnvResourceKeys = []string{
	"VPC_ID",
	"EKS_CLUSTER_NAME",
	"EKS_CLUSTER_ARN",
	"NODEGROUP_DEFAULT_NAME",
	"TMM_INSTANCE_ID",
	"JUMPHOST_INSTANCE_ID",
}

// stateEnvHasResources reports whether the state.env at path has any of
// the load-bearing resource-ID keys populated. The file is the
// shell-sourceable KEY=VALUE IDs cache (see internal/aws/state). A
// missing/unreadable file or a parse error reads as "no resources"
// (false) — the delete guard is best-effort and must not block on a
// malformed cache (the operator can always pass --force).
func stateEnvHasResources(path string) bool {
	b, err := os.ReadFile(path) // #nosec G304 -- path is <workspace>/state/state.env (config-managed layout), not user-tainted
	if err != nil {
		return false
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx < 1 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if val == "" {
			continue
		}
		for _, want := range stateEnvResourceKeys {
			if key == want {
				return true
			}
		}
	}
	return false
}

// plaintextSecretsRE matches lines that look like a credential value being
// set in YAML. Heuristic — catches the common shapes (api_key, password,
// token, AWS secret_access_key) without false-positiving on commented-out
// examples or empty values.
var plaintextSecretsRE = regexp.MustCompile(`(?m)^[\t ]*(api_key|apikey|password|token|secret_access_key|aws_secret_access_key|hmac_secret)[\t ]*:[\t ]+[^\s#\n][^\n]*`)

func rejectPlaintextSecrets(b []byte) error {
	if loc := plaintextSecretsRE.FindIndex(b); loc != nil {
		return fmt.Errorf("plaintext secret detected (offset %d): workspace config.yaml must not contain credentials — use AWS SDK chain env vars (AWS_PROFILE / AWS_ACCESS_KEY_ID) or the OS keychain", loc[0])
	}
	return nil
}
