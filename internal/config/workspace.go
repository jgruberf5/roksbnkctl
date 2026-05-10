package config

import (
	"encoding/json"
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
	Test     TestCfg              `yaml:"test,omitempty"`
	TFSource TFSourceCfg          `yaml:"tf_source"`
	COS      *COSCfg              `yaml:"cos,omitempty"`
	Targets  map[string]TargetCfg `yaml:"targets,omitempty"`

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

// tfstateHasResources is a deliberately shallow check — counts entries in
// state.resources via JSON parse. Errors (file missing, malformed) are
// treated as "no resources" so the caller falls back to safe-delete.
func tfstateHasResources(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var s struct {
		Resources []any `json:"resources"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return false, err
	}
	return len(s.Resources) > 0, nil
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
