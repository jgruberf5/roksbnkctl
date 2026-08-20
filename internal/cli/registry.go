package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cos"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/registry/mirror"
	"github.com/jgruberf5/roksbnkctl/internal/registry/ocireg"
	"github.com/jgruberf5/roksbnkctl/internal/registry/source"
)

// `roksbnkctl registry ...` is the Sprint 29 air-gap registry-mirror command
// group (PRD 11). It replicates every artifact a BNK install needs — the F5
// charts + images enumerated by the f5-bigip-k8s-manifest plus the non-F5 deps —
// from FAR (repo.f5.com) into a private target (IBM Container Registry, or a
// generic OCI registry like Artifactory), and reports on that mirror. The
// surface is CRUD-shaped, modeled on the COS client:
//
//	registry bom        Build + print the bill-of-materials (no target needed)
//	registry list       List what is currently in the mirror
//	registry diff       Show BOM vs. mirror drift (what replicate would copy)
//	registry replicate  Copy the BOM into the mirror (needs a live cluster)
//	registry verify     Confirm every BOM artifact is present + digest-matched
//	registry prune      Remove mirrored artifacts no longer in the BOM
//	registry delete     Delete ALL replicated artifacts from the target
var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Air-gap registry mirror — replicate BNK artifacts into a private registry (PRD 11)",
	Long: `Manage the air-gap registry mirror: replicate every chart + image a BNK
install needs (the f5-bigip-k8s-manifest set plus the non-F5 dependencies) from
the F5 Artifact Repository (repo.f5.com) into a private target — IBM Container
Registry (ICR) or a generic OCI registry (Artifactory / Harbor / Quay) — so an
air-gapped cluster installs BNK from a registry it controls.

Commands:
  roksbnkctl registry target     Show or set the mirror target (icr|generic)
  roksbnkctl registry bom        Build + print the bill-of-materials
  roksbnkctl registry list       List artifacts currently in the mirror
  roksbnkctl registry diff       Show what ` + "`replicate`" + ` would copy (BOM vs. mirror)
  roksbnkctl registry replicate  Copy the BOM into the mirror (registry-to-registry; no cluster)
  roksbnkctl registry verify     Confirm every BOM artifact is present + digest-matched
  roksbnkctl registry prune      Remove mirrored artifacts no longer in the BOM
  roksbnkctl registry delete     Delete ALL replicated artifacts from the target

` + "`registry bom`" + ` works entirely offline against the FAR manifest. The other verbs
need a configured registry: block and network reachability to the target registry —
` + "`replicate`" + ` also needs the FAR source (repo.f5.com). NONE require a Kubernetes
cluster: replicate copies registry-to-registry (via go-containerregistry) from wherever
roksbnkctl runs, so you can pre-seed the mirror as a standalone supply-chain step before
any cluster exists.`,
}

// registry-group flag values.
var (
	flagRegistryJSON              bool
	flagRegistryManifestVer       string
	flagRegistryFARRepo           string
	flagRegistrySAB64             string
	flagRegistryIncludeDeps       bool
	flagRegistryNoIncludeDep      bool
	flagRegistryConcurrency       int
	flagRegistryTarget            string
	flagRegistryPasswordStdin     bool
	flagRegistryForce             bool
	flagRegistryCAFile            string
	flagRegistryCAFingerprint     string
	flagRegistryInsecureCaptureCA bool
)

var registryBOMCmd = &cobra.Command{
	Use:   "bom",
	Short: "Build + print the BNK bill-of-materials from the FAR manifest",
	Long: `Pulls the f5-bigip-k8s-manifest for the configured BNK release, parses it
into the full artifact set (charts + images), unions the non-F5 dependencies,
and prints the resulting BOM. Needs no target — it only reads from FAR.`,
	Args: cobra.NoArgs,
	RunE: runRegistryBOM,
}

var registryListCmd = &cobra.Command{
	Use:   "list",
	Short: "List artifacts currently recorded in the mirror",
	Long:  `Lists the artifacts the last replicate recorded in registry-mirror.json.`,
	Args:  cobra.NoArgs,
	RunE:  runRegistryList,
}

var registryDiffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Show BOM vs. mirror drift (what `replicate` would copy)",
	Args:  cobra.NoArgs,
	RunE:  runRegistryDiff,
}

var registryReplicateCmd = &cobra.Command{
	Use:   "replicate",
	Short: "Copy the BOM into the mirror (registry-to-registry; no cluster needed)",
	Long: `Prepares the target registry (auth + repository namespace), then copies every
BOM artifact into it, idempotently. Records the result in registry-mirror.json so
the BNK install can be redirected to the mirror.

Runs entirely host-side: it pulls each artifact by digest from the FAR source
(repo.f5.com) and pushes to the target registry via go-containerregistry — no
Kubernetes cluster is involved. So you can pre-seed the mirror as a standalone
supply-chain step, before creating any cluster, from any host that can reach both
the FAR source and the target registry. Requires a configured registry: block and
the FAR source credential (registry.source_service_account_b64, the workspace FAR
auth, or --source-sa-b64).`,
	Args: cobra.NoArgs,
	RunE: runRegistryReplicate,
}

var registryVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Confirm every BOM artifact is present + digest-matched in the mirror",
	Args:  cobra.NoArgs,
	RunE:  runRegistryVerify,
}

var registryPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove mirrored artifacts no longer in the BOM",
	Args:  cobra.NoArgs,
	RunE:  runRegistryPrune,
}

var registryDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete ALL replicated artifacts from the target registry",
	Long: `Removes every artifact roksbnkctl replicated (recorded in registry-mirror.json)
from the configured target, then clears the mirror record so the BNK install
reverts to pulling from FAR. Destructive — pass --force to skip the confirmation.

Deletion is by digest where recorded (the reliable form for a registry manifest
DELETE). Artifacts that fail to delete are kept in the record so a re-run retries
them. For target=icr the API key needs Manager (delete) rights on the namespace;
for target=generic the registry must have deletes enabled.`,
	Args: cobra.NoArgs,
	RunE: runRegistryDelete,
}

func init() {
	registryBOMCmd.Flags().BoolVar(&flagRegistryJSON, "json", false, "emit the BOM as JSON (overrides --output)")
	for _, c := range []*cobra.Command{registryBOMCmd, registryDiffCmd, registryReplicateCmd, registryVerifyCmd, registryPruneCmd} {
		c.Flags().StringVar(&flagRegistryManifestVer, "manifest-version", "", "BNK manifest version (default: workspace bnk.manifest_version)")
		c.Flags().StringVar(&flagRegistryFARRepo, "far-repo-url", "", "FAR registry host (default: workspace bnk.far_repo_url, else repo.f5.com)")
		c.Flags().StringVar(&flagRegistrySAB64, "source-sa-b64", "", "FAR _json_key_base64 service account (default: workspace registry.source_service_account_b64)")
		c.Flags().BoolVar(&flagRegistryIncludeDeps, "include-deps", false, "force-include the non-F5 dependency artifacts (cert-manager, node-labeler)")
		c.Flags().BoolVar(&flagRegistryNoIncludeDep, "no-include-deps", false, "exclude the non-F5 dependency artifacts")
	}
	for _, c := range []*cobra.Command{registryReplicateCmd, registryVerifyCmd, registryPruneCmd, registryDeleteCmd} {
		c.Flags().StringVar(&flagRegistryTarget, "target", "", `mirror target backend: icr|generic (default: workspace registry.target, else "icr")`)
	}
	registryReplicateCmd.Flags().IntVar(&flagRegistryConcurrency, "concurrency", 0, "parallel copy workers (default: 4)")
	registryReplicateCmd.Flags().StringVar(&flagRegistryCAFile, "registry-ca", "", "PEM CA the mirror serves TLS with, for air-gap node trust (preferred: the file you generated; else registry.generic_ca_b64)")
	registryReplicateCmd.Flags().StringVar(&flagRegistryCAFingerprint, "registry-ca-fingerprint", "", "expected SHA-256 of the mirror CA (\"sha256:ab:cd…\" or bare hex), authenticating a captured CA out of band")
	registryReplicateCmd.Flags().BoolVar(&flagRegistryInsecureCaptureCA, "insecure-capture-ca", false, "adopt a self-signed mirror CA over an UNAUTHENTICATED connection (trust-on-first-use); prefer --registry-ca or --registry-ca-fingerprint")
	registryTargetCmd.Flags().BoolVar(&flagRegistryPasswordStdin, "password-stdin", false, "read the generic registry password from stdin (for `registry target generic_password`)")
	registryDeleteCmd.Flags().BoolVar(&flagRegistryForce, "force", false, "skip the confirmation prompt")

	registryAdoptCmd.Flags().BoolVar(&registryAdoptFlags.verifyContents, "verify-contents", false,
		"digest-check every BOM artifact before recording (needs the FAR source)")
	registryAdoptCmd.Flags().BoolVar(&registryAdoptFlags.force, "force", false,
		"record the mirror even when it holds nothing under the configured prefix")

	registryCmd.AddCommand(registryBOMCmd, registryListCmd, registryDiffCmd, registryReplicateCmd, registryAdoptCmd, registryVerifyCmd, registryPruneCmd, registryTargetCmd, registryDeleteCmd)
	rootCmd.AddCommand(registryCmd)
}

// ── shared resolution helpers ───────────────────────────────────────────────

// registryBOMInputs resolves the BOM-build inputs from flags + workspace config.
type registryBOMInputs struct {
	ManifestVersion string
	FARRepoURL      string
	SourceSAB64     string
	IncludeDeps     bool
	CertManagerVer  string
	NodeLabelerTag  string
}

// resolveBOMInputs merges the registry flags over the workspace config.
func resolveBOMInputs(ws *config.Workspace) registryBOMInputs {
	in := registryBOMInputs{
		ManifestVersion: ws.BNK.ManifestVersion,
		FARRepoURL:      ws.BNK.FARRepoURL,
		IncludeDeps:     ws.Registry.IncludeDepsOrDefault(),
		// cert-manager / node-labeler tags are the terraform-rendered defaults;
		// the BOM only needs them to tag the dep artifacts (PRD 11 §2). These
		// mirror the terraform variable defaults.
		CertManagerVer: defaultCertManagerVersion,
		NodeLabelerTag: defaultNodeLabelerTag,
	}
	if ws.Registry != nil {
		in.SourceSAB64 = ws.Registry.SourceServiceAccountB64
	}
	if flagRegistryManifestVer != "" {
		in.ManifestVersion = flagRegistryManifestVer
	}
	if flagRegistryFARRepo != "" {
		in.FARRepoURL = flagRegistryFARRepo
	}
	if flagRegistrySAB64 != "" {
		in.SourceSAB64 = flagRegistrySAB64
	}
	if flagRegistryIncludeDeps {
		in.IncludeDeps = true
	}
	if flagRegistryNoIncludeDep {
		in.IncludeDeps = false
	}
	return in
}

// defaultCertManagerVersion / defaultNodeLabelerTag mirror the terraform
// variable defaults so the BOM tags the non-F5 deps the install actually pulls.
// (terraform cert_manager_version default; the bitnami/kubectl node-labeler tag.)
const (
	defaultCertManagerVersion = "v1.17.3"
	defaultNodeLabelerTag     = "latest"
)

// FAR-auth COS coordinates — the orchestration COS instance/bucket/region that
// holds the FAR auth tarball + the license JWT. Centralised in internal/config
// so the terraform render, the init supply-chain provisioning, and this
// resolver all share one source of truth.
const (
	farOrchestrationCOSInstance = config.DefaultCOSInstance
	farResourcesBucket          = config.DefaultCOSBucket
	farCOSBucketRegion          = config.DefaultCOSRegion
)

// resolveFARServiceAccount downloads the workspace's FAR auth tarball
// (bnk.far_auth_file, default f5-far-auth-key.tgz) from the orchestration COS
// using the workspace API key, and extracts the _json_key_base64 service account.
// This is the flag-free path: with it, `registry` authenticates to FAR straight
// from the workspace config (no --source-sa-b64).
func resolveFARServiceAccount(ctx context.Context, name string, ws *config.Workspace) (string, error) {
	farAuthFile := ws.BNK.FarAuthFile
	if farAuthFile == "" {
		farAuthFile = config.DefaultFARAuthFile
	}
	apiKey, err := (&cred.Resolver{Workspace: name}).IBMCloudAPIKey(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving the workspace API key: %w", err)
	}
	ic, err := ibm.New(apiKey, ws.IBMCloud.Region)
	if err != nil {
		return "", err
	}
	// Honour a customer-owned orchestration COS from config.yaml (cos.instance /
	// cos.bucket / cos.region), falling back to the built-in defaults. The
	// terraform render reads the SAME cos block (ibmcloud_cos_* tfvars), so both
	// paths resolve FAR files from the same place.
	cosInstance, cosBucket, cosRegion := farOrchestrationCOSInstance, farResourcesBucket, farCOSBucketRegion
	if ws.COS != nil {
		if ws.COS.Instance != "" {
			cosInstance = ws.COS.Instance
		}
		if ws.COS.Bucket != "" {
			cosBucket = ws.COS.Bucket
		}
		if ws.COS.Region != "" {
			cosRegion = ws.COS.Region
		}
	}
	inst, err := ic.GetCOSInstanceByName(ctx, cosInstance)
	if err != nil {
		return "", fmt.Errorf("finding the %q COS instance: %w", cosInstance, err)
	}
	cosClient, err := cos.New(apiKey, cosRegion, inst.CRN)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "far-auth-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	tgz := filepath.Join(tmp, "far-auth.tgz")
	if err := cosClient.GetObjectToFile(ctx, cosBucket, farAuthFile, tgz); err != nil {
		return "", fmt.Errorf("downloading %s from COS %s/%s: %w", farAuthFile, cosInstance, cosBucket, err)
	}
	return source.ExtractServiceAccountFromTarball(tgz)
}

// buildBOM pulls the FAR manifest and assembles the BOM. workspaceScratch is the
// dir helm pulls into (the workspace state/scratch tree); "" → a temp dir. When
// no source service account is configured, it is resolved from the workspace's
// FAR auth tarball in COS (bnk.far_auth_file) so the command runs flag-free.
func buildBOM(ctx context.Context, name string, ws *config.Workspace, in *registryBOMInputs, workspaceScratch string) (*bnkbom.BOM, error) {
	if in.ManifestVersion == "" {
		// Fall back to the tfvar/init default so `registry` runs out-of-box;
		// bnk.manifest_version (init) or --manifest-version override it.
		in.ManifestVersion = config.DefaultManifestVersion
	}
	if in.SourceSAB64 == "" {
		sa, err := resolveFARServiceAccount(ctx, name, ws)
		if err != nil {
			return nil, fmt.Errorf("resolving the FAR service account from COS (or set registry.source_service_account_b64 / --source-sa-b64): %w", err)
		}
		in.SourceSAB64 = sa
	}
	manifest, err := source.FetchManifest(ctx, in.FARRepoURL, in.ManifestVersion, "", workspaceScratch, in.SourceSAB64)
	if err != nil {
		return nil, fmt.Errorf("fetching f5-bigip-k8s-manifest %s: %w", in.ManifestVersion, err)
	}
	return bnkbom.Build(manifest, bnkbom.Options{
		ManifestVersion:     in.ManifestVersion,
		IncludeDeps:         in.IncludeDeps,
		CertManagerVersion:  in.CertManagerVer,
		NodeLabelerImageTag: in.NodeLabelerTag,
	})
}

// loadRegistryWorkspace resolves the workspace name + loads its config, the same
// path the lifecycle commands use.
func loadRegistryWorkspace() (string, *config.Workspace, error) {
	name := resolvedWorkspaceName()
	if name == "" {
		return "", nil, fmt.Errorf("no workspace selected (use -w <name> or `roksbnkctl init`)")
	}
	ws, err := config.LoadWorkspace(name)
	if err != nil {
		return "", nil, err
	}
	return name, ws, nil
}

// registryScratchDir is the helm-pull scratch dir under the workspace state
// tree, mirroring where the FLO module pulls the manifest.
func registryScratchDir(name string) string {
	dir, err := config.WorkspaceStateDir(name)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "scratch", "registry")
}

// mirrorTarget is the registry-target contract the CLI consumes: the engine's
// push side (mirror.Target) plus the pull-side endpoints the install redirect
// reads. *ocireg.Target (ICR + generic OCI) satisfies it. (Prepare is NOT on the
// interface — its signature differs per impl, so buildTarget prepares each kind
// inline.)
type mirrorTarget interface {
	mirror.Target
	ImagePullRef(bnkbom.Artifact) string
	ChartPullRef(bnkbom.Artifact) string
	ImageHostPath() string
	ChartHostPath() string
	// MirrorNamespace is the repo prefix recorded in registry-mirror.json
	// (the ICR/generic repo prefix).
	MirrorNamespace() string
}

// registryTargetKind resolves the active target backend: --target flag >
// registry.target > "icr" (the default).
func registryTargetKind(ws *config.Workspace) string {
	kind := "icr"
	if ws.Registry != nil && ws.Registry.Target != "" {
		kind = ws.Registry.Target
	}
	if flagRegistryTarget != "" {
		kind = flagRegistryTarget
	}
	return kind
}

// buildTarget resolves the configured registry target and prepares it.
// "icr"/"generic" build a static nesting OCI target.
func buildTarget(ctx context.Context, name string, ws *config.Workspace) (mirrorTarget, error) {
	switch kind := registryTargetKind(ws); kind {
	case "icr":
		return buildICRTarget(ctx, name, ws)
	case "generic":
		return buildGenericTarget(ws)
	default:
		return nil, fmt.Errorf("unsupported registry target %q (expected icr or generic)", kind)
	}
}

// icrHostForRegion maps an IBM Cloud region to its regional ICR registry host.
var icrHostForRegion = map[string]string{
	"us-south": "us.icr.io", "us-east": "us.icr.io",
	"eu-de": "de.icr.io", "eu-gb": "uk.icr.io", "eu-es": "es.icr.io",
	"jp-tok": "jp.icr.io", "jp-osa": "jp2.icr.io",
	"au-syd": "au.icr.io", "ca-tor": "ca.icr.io", "br-sao": "br.icr.io",
}

// buildICRTarget builds the IBM Container Registry target: host from
// registry.icr_host (else derived from ibmcloud.region), namespace from
// registry.icr_namespace (else the workspace prefix), and iamapikey auth using
// the workspace's resolved IBM Cloud API key.
func buildICRTarget(ctx context.Context, name string, ws *config.Workspace) (mirrorTarget, error) {
	reg := ws.Registry
	if reg == nil {
		reg = &config.RegistryCfg{}
	}
	host := reg.ICRHost
	if host == "" {
		h, ok := icrHostForRegion[ws.IBMCloud.Region]
		if !ok {
			return nil, fmt.Errorf("registry target icr: cannot derive an ICR host for region %q — set registry.icr_host (e.g. de.icr.io)", ws.IBMCloud.Region)
		}
		host = h
	}
	ns := reg.ICRNamespace
	if ns == "" {
		ns = ws.Prefix
	}
	if ns == "" {
		return nil, fmt.Errorf("registry target icr: set registry.icr_namespace (or a workspace prefix) for the ICR namespace")
	}
	apiKey, err := (&cred.Resolver{Workspace: name}).IBMCloudAPIKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("registry target icr: resolving API key: %w", err)
	}
	return &ocireg.Target{
		Host:      host,
		Namespace: ns,
		Auth:      &authn.Basic{Username: "iamapikey", Password: apiKey},
	}, nil
}

// buildGenericTarget builds a generic OCI target (Artifactory / Harbor /
// registry:2) from registry.generic_*. Anonymous when no credential is set.
func buildGenericTarget(ws *config.Workspace) (mirrorTarget, error) {
	reg := ws.Registry
	if reg == nil || reg.GenericHost == "" {
		return nil, fmt.Errorf("registry target generic: set registry.generic_host (the OCI registry host)")
	}
	var auth authn.Authenticator = authn.Anonymous
	if reg.GenericUsername != "" || reg.GenericPasswordB64 != "" {
		pw, derr := base64.StdEncoding.DecodeString(reg.GenericPasswordB64)
		if derr != nil {
			return nil, fmt.Errorf("registry target generic: decoding generic_password_b64: %w", derr)
		}
		auth = &authn.Basic{Username: reg.GenericUsername, Password: string(pw)}
	}
	return &ocireg.Target{
		Host:      reg.GenericHost,
		Namespace: reg.GenericRepoPrefix,
		Auth:      auth,
	}, nil
}

// ── registry target (CLI-driven mirror configuration) ───────────────────────

var registryTargetCmd = &cobra.Command{
	Use:   "target [icr|generic | <field> <value>]",
	Short: "Show or set the registry mirror target and its fields",
	Long: `Configure the registry replication target without hand-editing config.yaml.

With no arguments, prints the current target + configured fields. Otherwise the
first argument is either a backend KIND (sets registry.target) or a FIELD name
(set with a following value):

  Kinds:  icr | generic
  Fields: icr_host  icr_namespace
          generic_host  generic_repo_prefix  generic_username  generic_password
          generic_ca (a PEM file)  generic_ca_sha256

Examples:
  roksbnkctl registry target                          # show current config
  roksbnkctl registry target icr                      # use IBM Container Registry
  roksbnkctl registry target icr_namespace bnk-test
  roksbnkctl registry target generic                  # use a generic OCI registry
  roksbnkctl registry target generic_host art.example.com
  roksbnkctl registry target generic_repo_prefix bnk
  roksbnkctl registry target generic_username ci-bot
  echo "$TOKEN" | roksbnkctl registry target generic_password --password-stdin
  roksbnkctl registry target generic_ca /opt/harbor/certs/harbor.crt`,
	Args: cobra.MaximumNArgs(2),
	RunE: runRegistryTarget,
}

// registryTargetKinds are the backend selectors `registry target <kind>` accepts.
var registryTargetKinds = map[string]bool{"icr": true, "generic": true}

func runRegistryTarget(_ *cobra.Command, args []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}

	// No arguments (and no stdin flag) → show the current config.
	if len(args) == 0 && !flagRegistryPasswordStdin {
		printRegistryTarget(ws)
		return nil
	}

	if ws.Registry == nil {
		ws.Registry = &config.RegistryCfg{}
	}
	reg := ws.Registry

	// --password-stdin sets generic_password from stdin, keeping the token out
	// of argv + shell history.
	if flagRegistryPasswordStdin {
		field := "generic_password"
		if len(args) >= 1 {
			field = args[0]
		}
		if field != "generic_password" {
			return fmt.Errorf("--password-stdin only applies to generic_password")
		}
		raw, rerr := io.ReadAll(os.Stdin)
		if rerr != nil {
			return fmt.Errorf("reading password from stdin: %w", rerr)
		}
		raw = bytes.TrimRight(raw, "\r\n")
		if len(raw) == 0 {
			return fmt.Errorf("no password read from stdin")
		}
		reg.GenericPasswordB64 = base64.StdEncoding.EncodeToString(raw)
		return saveRegistryTarget(name, ws, "generic_password")
	}

	first := args[0]
	if registryTargetKinds[first] {
		reg.Target = first
		return saveRegistryTarget(name, ws, "target = "+first)
	}

	// Otherwise a field name, which needs a value.
	if len(args) != 2 {
		return fmt.Errorf("setting %q needs a value: roksbnkctl registry target %s <value>", first, first)
	}
	val := args[1]
	switch first {
	case "icr_host":
		reg.ICRHost = val
	case "icr_namespace":
		reg.ICRNamespace = val
	case "generic_host":
		reg.GenericHost = val
	case "generic_repo_prefix":
		reg.GenericRepoPrefix = val
	case "generic_username":
		reg.GenericUsername = val
	case "generic_ca":
		// Takes a FILE, not a literal: the CA is what you generated, so it is read
		// from disk and its fingerprint recorded alongside it. Recording both means a
		// later capture (if the PEM is ever cleared) is still authenticated by the pin.
		pemBytes, rerr := os.ReadFile(val)
		if rerr != nil {
			return fmt.Errorf("reading generic_ca %s: %w", val, rerr)
		}
		trimmed := strings.TrimSpace(string(pemBytes))
		if !strings.Contains(trimmed, "BEGIN CERTIFICATE") {
			return fmt.Errorf("%s does not look like a PEM certificate", val)
		}
		reg.GenericCAB64 = base64.StdEncoding.EncodeToString([]byte(trimmed))
		if fp, ferr := pemRootFingerprint(trimmed); ferr == nil {
			reg.GenericCASHA256 = fp
			fmt.Fprintf(os.Stderr, "  pinned SHA-256 %s\n", fp)
		}
	case "generic_ca_sha256":
		fp := normalizeCAPin(val)
		if len(fp) != 64 {
			return fmt.Errorf("generic_ca_sha256 %q is not a SHA-256 hex digest (64 hex chars)", val)
		}
		reg.GenericCASHA256 = fp
	case "generic_password":
		reg.GenericPasswordB64 = base64.StdEncoding.EncodeToString([]byte(val))
	default:
		return fmt.Errorf("unknown registry target arg %q\n  kinds:  icr|generic\n  fields: icr_host icr_namespace generic_host generic_repo_prefix generic_username generic_password generic_ca generic_ca_sha256", first)
	}
	return saveRegistryTarget(name, ws, first)
}

func saveRegistryTarget(name string, ws *config.Workspace, what string) error {
	if err := config.SaveWorkspace(name, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ registry %s\n", what)
	return nil
}

// printRegistryTarget shows the effective target kind + the configured fields
// (the generic password is redacted).
func printRegistryTarget(ws *config.Workspace) {
	fmt.Printf("target: %s\n", registryTargetKind(ws))
	reg := ws.Registry
	if reg == nil {
		return
	}
	show := func(label, val string) {
		if val != "" {
			fmt.Printf("  %s: %s\n", label, val)
		}
	}
	show("icr_host", reg.ICRHost)
	show("icr_namespace", reg.ICRNamespace)
	show("generic_host", reg.GenericHost)
	show("generic_repo_prefix", reg.GenericRepoPrefix)
	show("generic_username", reg.GenericUsername)
	if reg.GenericPasswordB64 != "" {
		fmt.Println("  generic_password: (set)")
	}
}

// ── registry delete (wipe all replicated artifacts) ─────────────────────────

func runRegistryDelete(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	rec, err := config.ReadRegistryMirror(name)
	if err != nil {
		if errors.Is(err, config.ErrNoRegistryMirror) {
			fmt.Fprintln(os.Stderr, "no mirror recorded — nothing to delete")
			return nil
		}
		return err
	}
	// REFUSE, not discard. delete takes its artifact list from the RECORD and
	// removes it from the CONFIGURED target, so a record describing another
	// mirror deletes one registry's contents out of a different one — and the
	// prompt below names the record's host, so it would state the wrong
	// destination while doing it. diff can afford to shrug this off and report
	// everything missing; an unrecoverable delete cannot (#109).
	if why := mirrorRecordMismatch(cmd.Context(), name, ws, rec); why != "" {
		return fmt.Errorf("refusing to delete: the recorded mirror does not describe the configured target — %s.\n"+
			"  Deleting would remove that mirror's artifact list from THIS one.\n"+
			"  Point the workspace back at the recorded mirror, or clear the record with `registry adopt`", why)
	}
	if len(rec.Artifacts) == 0 {
		fmt.Fprintln(os.Stderr, "mirror is empty — nothing to delete")
		return nil
	}
	if !flagRegistryForce {
		if !promptYesNo(fmt.Sprintf("Delete all %d replicated artifact(s) from the %s target (%s)?", len(rec.Artifacts), rec.Target, rec.ImageHost), false) {
			return errors.New("aborted")
		}
	}
	target, err := buildTarget(cmd.Context(), name, ws)
	if err != nil {
		return err
	}
	arts := make([]bnkbom.Artifact, len(rec.Artifacts))
	for i, ma := range rec.Artifacts {
		arts[i] = bnkbom.Artifact{Name: ma.Name, Tag: ma.Tag, Digest: ma.Digest}
	}
	// Delete talks to the mirror too, so it needs the same CA the record carries.
	// The recorded CACert is authoritative here: it is what replicate/adopt already
	// established for this mirror, so no rediscovery (or pin prompt) is needed.
	delCA := rec.CACert
	if delCA == "" {
		delCA, _ = resolveMirrorCA(name, ws, registryHostFromPath(target.ImageHostPath()))
	}
	results := registryEngine(target, resolveBOMInputs(ws), delCA).Delete(cmd.Context(), arts)

	var deleted, failed int
	var remaining []config.MirrorArtifact
	for i, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "  FAIL %s:%s — %v\n", r.Artifact.Name, r.Artifact.Tag, r.Err)
			remaining = append(remaining, rec.Artifacts[i])
			continue
		}
		deleted++
		if !flagQuiet {
			fmt.Fprintf(os.Stderr, "  deleted %s:%s\n", r.Artifact.Name, r.Artifact.Tag)
		}
	}
	fmt.Fprintf(os.Stderr, "✓ deleted %d artifact(s)\n", deleted)

	// Drop the record when the mirror is empty; otherwise keep the artifacts
	// that failed so a re-run retries exactly those.
	if len(remaining) == 0 {
		if derr := config.DeleteRegistryMirror(name); derr != nil {
			return derr
		}
	} else {
		rec.Artifacts = remaining
		if werr := config.WriteRegistryMirror(name, rec); werr != nil {
			return werr
		}
	}
	if failed > 0 {
		return fmt.Errorf("delete: %d artifact(s) could not be removed", failed)
	}
	return nil
}

// registryEngine builds the copy engine. registryCA is a REQUIRED parameter rather
// than a field callers may remember to set: every verb here talks to the mirror over
// crane, and craneOpts only installs a private CA when Engine.RegistryCA is set — so
// omitting it silently breaks every self-signed (i.e. air-gapped) mirror, which is
// the case this whole subsystem exists for. Pass "" only when there is genuinely no
// private CA (a public target); resolveMirrorCA returns exactly that.
func registryEngine(t mirror.Target, in registryBOMInputs, registryCA string) *mirror.Engine {
	return &mirror.Engine{
		Target:      t,
		SourceAuth:  source.SourceAuth(in.FARRepoURL, in.SourceSAB64),
		Concurrency: flagRegistryConcurrency,
		RegistryCA:  registryCA,
	}
}

// ── bom ─────────────────────────────────────────────────────────────────────

func runRegistryBOM(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmd.Context(), name, ws, &in, registryScratchDir(name))
	if err != nil {
		return err
	}
	if flagRegistryJSON || flagOutput == "json" {
		return json.NewEncoder(os.Stdout).Encode(bom)
	}
	printBOMTable(bom)
	return nil
}

func printBOMTable(bom *bnkbom.BOM) {
	charts, images := bom.Counts()
	fmt.Fprintf(os.Stderr, "BNK manifest %s — %d charts, %d images (%d total)\n",
		bom.ManifestVersion, charts, images, len(bom.Artifacts))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tORIGIN\tSOURCE\tNAME\tTAG")
	for _, a := range bom.Artifacts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Kind, a.Origin, a.SourceHost, a.Name, a.Tag)
	}
	tw.Flush()
}

// ── list ────────────────────────────────────────────────────────────────────

func runRegistryList(_ *cobra.Command, _ []string) error {
	name, _, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	rec, err := config.ReadRegistryMirror(name)
	if err != nil {
		return err
	}
	if flagOutput == "json" {
		return json.NewEncoder(os.Stdout).Encode(rec)
	}
	fmt.Fprintf(os.Stderr, "mirror %s/%s (manifest %s, recorded %s) — %d artifacts\n",
		rec.Target, rec.Namespace, rec.ManifestVersion, rec.RecordedAt.Format(time.RFC3339), len(rec.Artifacts))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAME\tTAG\tDIGEST")
	for _, a := range rec.Artifacts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", a.Kind, a.Name, a.Tag, a.Digest)
	}
	tw.Flush()
	return nil
}

// ── diff ────────────────────────────────────────────────────────────────────

// mirrorRecordMismatch reports why the recorded mirror does not describe the
// CONFIGURED target, or "" when it does (or when we cannot tell).
//
// Cannot-tell is deliberately treated as a match. If the target will not build —
// missing credentials, an unreachable ICR region — we know less than the record
// does, and discarding it on that basis would turn a diff into a full re-replicate
// for a reason that has nothing to do with the mirror's contents.
func mirrorRecordMismatch(ctx context.Context, name string, ws *config.Workspace, rec *config.RegistryMirror) string {
	if rec == nil {
		return ""
	}
	if kind := registryTargetKind(ws); rec.Target != "" && rec.Target != kind {
		return fmt.Sprintf("it was written for target %q, the configured target is %q", rec.Target, kind)
	}
	target, err := buildTarget(ctx, name, ws)
	if err != nil {
		return "" // cannot resolve the target — say nothing rather than guess
	}
	if ns := target.MirrorNamespace(); rec.Namespace != "" && ns != "" && rec.Namespace != ns {
		return fmt.Sprintf("it was written for repository %q, the configured repository is %q", rec.Namespace, ns)
	}
	if h := target.ImageHostPath(); rec.ImageHost != "" && h != "" && rec.ImageHost != h {
		return fmt.Sprintf("it was written for host %q, the configured host is %q", rec.ImageHost, h)
	}
	return ""
}

func runRegistryDiff(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmd.Context(), name, ws, &in, registryScratchDir(name))
	if err != nil {
		return err
	}
	rec, err := config.ReadRegistryMirror(name)
	have := map[string]bool{}
	if err == nil {
		// The record describes the mirror it was WRITTEN against, which is not
		// necessarily the one configured now. Re-point a workspace at a
		// different registry — or rebuild the one it names — and the record
		// still lists 89 artifacts that are not there, so diff reports "in
		// sync" against an EMPTY registry (#109). verify catches it because it
		// probes; diff does not probe at all.
		//
		// A record for a different mirror tells us nothing about this one, so it
		// is discarded rather than trusted. Everything then reads as missing,
		// which is the safe direction: it prompts a replicate, and replicate is
		// idempotent — an artifact already present at the right digest is
		// skipped.
		if why := mirrorRecordMismatch(cmd.Context(), name, ws, rec); why != "" {
			fmt.Fprintf(os.Stderr, "→ ignoring the recorded mirror: %s\n", why)
			fmt.Fprintln(os.Stderr, "  It describes a different mirror, so it says nothing about this one.")
		} else {
			for _, a := range rec.Artifacts {
				have[a.Kind+"|"+a.Name+":"+a.Tag] = true
			}
		}
	} else if err != config.ErrNoRegistryMirror {
		return err
	}

	var missing []bnkbom.Artifact
	for _, a := range bom.Artifacts {
		if !have[string(a.Kind)+"|"+a.Name+":"+a.Tag] {
			missing = append(missing, a)
		}
	}
	if flagOutput == "json" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"missing": missing, "bom_total": len(bom.Artifacts)})
	}
	if len(missing) == 0 {
		// Say what this is based on. diff reads the RECORD; it never contacts
		// the registry, so "in sync" means "nothing left to replicate according
		// to what was last replicated" — not "every artifact is present". Only
		// verify establishes the latter, and the difference matters when the
		// registry has been emptied or rebuilt underneath the record.
		fmt.Fprintln(os.Stderr, "mirror is in sync with the BOM — nothing to replicate")
		fmt.Fprintln(os.Stderr, "  (from the recorded mirror; `registry verify` probes the registry itself)")
		return nil
	}
	fmt.Fprintf(os.Stderr, "%d of %d BOM artifacts not yet in the mirror:\n", len(missing), len(bom.Artifacts))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAME\tTAG")
	for _, a := range missing {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", a.Kind, a.Name, a.Tag)
	}
	tw.Flush()
	return nil
}

// ── replicate ───────────────────────────────────────────────────────────────

// resolveMirrorCA returns the mirror's CA PEM used both to trust the replicate
// push TLS and to record for air-gap node trust, in DESCENDING ORDER OF AUTHORITY:
//
//  1. --registry-ca <file>        explicit, per-invocation; fatal if unreadable
//  2. registry.generic_ca_b64     the workspace's recorded copy, taken from the
//     file that generated it — no network involved
//  3. registry-mirror.json        a CA a previous replicate already established
//  4. a PINNED capture from host  authenticated against generic_ca_sha256 or
//     --registry-ca-fingerprint
//  5. an UNPINNED capture         refused unless --insecure-capture-ca
//
// 1–3 never touch the network for trust, and are the intended path for a mirror
// you built yourself. A public target still yields "" (no error): there is no
// private CA to adopt, so nothing needs authenticating.
//
// A capture REFUSAL is fatal, unlike a transport failure. Silently degrading to ""
// would be worse than failing: the node-trust step would no-op and `bnk up` would
// fail much later with an opaque x509 error from a pod pull.
func resolveMirrorCA(name string, ws *config.Workspace, host string) (string, error) {
	if flagRegistryCAFile != "" {
		pemBytes, err := os.ReadFile(flagRegistryCAFile)
		if err != nil {
			return "", fmt.Errorf("reading --registry-ca %s: %w", flagRegistryCAFile, err)
		}
		return strings.TrimSpace(string(pemBytes)), nil
	}
	if ws != nil && ws.Registry != nil && ws.Registry.GenericCAB64 != "" {
		der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ws.Registry.GenericCAB64))
		if err != nil {
			return "", fmt.Errorf("decoding registry.generic_ca_b64: %w", err)
		}
		return strings.TrimSpace(string(der)), nil
	}
	if rec, err := config.ReadRegistryMirror(name); err == nil && rec != nil && rec.CACert != "" {
		return strings.TrimSpace(rec.CACert), nil
	}
	if host == "" {
		return "", nil
	}
	caPEM, err := captureRegistryCA(host, caCaptureOpts{
		PinSHA256:     mirrorCAPin(ws),
		AllowUnpinned: flagRegistryInsecureCaptureCA,
	})
	if err != nil {
		// A refused/mismatched CA is a policy decision the operator must see.
		if errors.Is(err, errUnpinnedPrivateCA) || errors.Is(err, errCAPinMismatch) {
			return "", err
		}
		return "", nil // best-effort: unreachable/public host is non-fatal
	}
	return caPEM, nil
}

// mirrorCAPin is the configured out-of-band fingerprint: the flag wins over the
// workspace so a one-off invocation can pin without editing config.
func mirrorCAPin(ws *config.Workspace) string {
	if flagRegistryCAFingerprint != "" {
		return flagRegistryCAFingerprint
	}
	if ws != nil && ws.Registry != nil {
		return ws.Registry.GenericCASHA256
	}
	return ""
}

// ensureMirrorCATrust makes the operator's terraform/helm CHILD processes trust a
// private mirror's recorded CA before `bnk up` pulls charts from it. `bnk up`'s
// terraform helm provider runs as a subprocess that inherits os.Environ(), and a
// container operator (the roksbnkctl-tools-runner) has NO OS trust for a co-located
// self-signed Harbor — so those chart pulls fail `x509: unknown authority` without
// this. It is the operator-side complement to the node CA-trust installer: nodes get
// the CA via the DaemonSet, the operator gets it here. It appends the recorded mirror
// CA to the existing trust bundle and points SSL_CERT_FILE at the result (so public
// source pulls keep working too). No-op when the workspace recorded no private CA (a
// public mirror), and never fatal — trust setup is best-effort.
func ensureMirrorCATrust(workspace string) {
	rec, err := config.ReadRegistryMirror(workspace)
	if err != nil || rec == nil || strings.TrimSpace(rec.CACert) == "" {
		return
	}
	base, err := config.BaseDir()
	if err != nil {
		return
	}
	sys := os.Getenv("SSL_CERT_FILE")
	if sys == "" {
		for _, p := range []string{
			"/etc/ssl/certs/ca-certificates.crt", // debian / ubuntu (the runner image)
			"/etc/pki/tls/certs/ca-bundle.crt",   // rhel / fedora
			"/etc/ssl/cert.pem",                  // alpine / macos
		} {
			if _, statErr := os.Stat(p); statErr == nil {
				sys = p
				break
			}
		}
	}
	var buf bytes.Buffer
	if sys != "" {
		if b, rerr := os.ReadFile(sys); rerr == nil {
			buf.Write(b)
			buf.WriteByte('\n')
		}
	}
	buf.WriteString(strings.TrimSpace(rec.CACert))
	buf.WriteByte('\n')
	bundle := filepath.Join(base, "mirror-ca-bundle.crt")
	if werr := os.WriteFile(bundle, buf.Bytes(), 0o644); werr != nil {
		return
	}
	_ = os.Setenv("SSL_CERT_FILE", bundle)
	fmt.Fprintln(os.Stderr, "→ trusting the mirror's private CA for the install (operator SSL_CERT_FILE)")
}

func runRegistryReplicate(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmd.Context(), name, ws, &in, registryScratchDir(name))
	if err != nil {
		return err
	}
	target, err := buildTarget(cmd.Context(), name, ws)
	if err != nil {
		return err
	}
	// Resolve the mirror's CA up front (before any copy): the file/config copy wins,
	// else an OUT-OF-BAND-PINNED capture from the push host (refused when unpinned).
	// A private/self-signed mirror (co-located Harbor by private IP) returns its CA
	// here; a public target returns "". When
	// set, the engine trusts it for the push TLS so a container operator with no OS
	// trust for the mirror can still replicate — and it is also recorded below for
	// air-gap node trust, so the same CA drives both the operator and the nodes.
	pushHost := registryHostFromPath(target.ImageHostPath())
	mirrorCA, caErr := resolveMirrorCA(name, ws, pushHost)
	if caErr != nil {
		return caErr // an explicit --registry-ca that can't be read is fatal
	}
	eng := registryEngine(target, in, mirrorCA)
	// Check the push credential once up front. Without this a wrong password is
	// retried against every artifact in the BOM (401 is retryable — Harbor's token
	// service genuinely flakes), so the command grinds for minutes and then reports
	// ~100 failures instead of one clear "the mirror rejected the credential".
	if err := eng.PreflightAuth(cmd.Context(), bom); err != nil {
		return err
	}
	results := eng.Replicate(cmd.Context(), bom)

	var failed int
	mirrored := make([]config.MirrorArtifact, 0, len(results))
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "  FAIL %s/%s:%s — %v\n", r.Artifact.Kind, r.Artifact.Name, r.Artifact.Tag, r.Err)
			continue
		}
		verb := "copied"
		if r.Skipped {
			verb = "skipped"
		}
		if !flagQuiet {
			fmt.Fprintf(os.Stderr, "  %s %s/%s:%s %s\n", verb, r.Artifact.Kind, r.Artifact.Name, r.Artifact.Tag, r.Digest)
		}
		mirrored = append(mirrored, config.MirrorArtifact{
			Kind: string(r.Artifact.Kind), Name: r.Artifact.Name, Tag: r.Artifact.Tag, Digest: r.Digest,
		})
	}

	rec := &config.RegistryMirror{
		Target:          registryTargetKind(ws),
		Namespace:       target.MirrorNamespace(),
		ChartHost:       target.ChartHostPath(),
		ImageHost:       target.ImageHostPath(),
		ManifestVersion: bom.ManifestVersion,
		Artifacts:       mirrored,
	}
	// Air-gap node trust: record the bare pull host and the CA it serves so
	// `bnk up` installs that CA on every node before pulling. The authoritative
	// copy (--registry-ca / registry.generic_ca_b64) wins; a captured CA must be
	// pinned. A public or unreachable host records no CA and node-trust no-ops.
	rec.RegistryHost = pushHost
	// mirrorCA was resolved (and trusted for the push) before the copy above.
	if mirrorCA != "" {
		rec.CACert = mirrorCA
		fmt.Fprintf(os.Stderr, "  ✓ trusted + recorded the mirror CA from %s (the push trusts it; nodes install it before pulling)\n", pushHost)
	} else if pushHost != "" {
		fmt.Fprintf(os.Stderr, "  ⚠ no private CA captured from %s — if it is a self-signed mirror, re-run with --registry-ca <file>\n", pushHost)
	}
	if err := config.WriteRegistryMirror(name, rec); err != nil {
		return fmt.Errorf("recording mirror: %w", err)
	}
	if failed > 0 {
		return fmt.Errorf("replicate: %d of %d artifacts failed", failed, len(results))
	}
	fmt.Fprintf(os.Stderr, "✓ mirrored %d artifacts into %s\n", len(mirrored), target.ChartHostPath())
	return nil
}

var registryAdoptFlags struct {
	verifyContents bool
	force          bool
}

var registryAdoptCmd = &cobra.Command{
	Use:   "adopt",
	Short: "Record a mirror this workspace did not populate, so `bnk up` can use it",
	Long: `Writes registry-mirror.json for a mirror that already exists.

` + "`bnk up`" + ` refuses to render against a mirror the workspace has no record of —
otherwise BNK would be pointed at far_repo_url, which an air-gapped cluster cannot
reach. Until now only ` + "`registry replicate`" + ` wrote that record, which means a
workspace could only use a mirror it had populated itself.

That is the wrong constraint for how mirrors are actually used. The registry is
filled once, as a supply-chain step, and then many installs pull from it — often
from a different workspace, a different host, or a different team. Those installs
were forced to re-run replicate purely to re-derive a record, which needs the FAR
source reachable at install time. An air-gapped operator frequently does not have
that, and it is the whole point of having mirrored in the first place.

adopt derives the record from the configured registry target: the chart and image
hosts, the repo namespace, the manifest version, and the mirror CA all come from
the workspace config, so no source access is needed. It then asks the MIRROR what
it holds under the configured prefix — a sanity check that catches a typo in the
prefix or an empty registry, without pretending to prove the contents are correct.

Pass --verify-contents when the source IS reachable and you want proof rather than
assertion: it builds the BOM and digest-checks every artifact before recording,
and the record then carries the full artifact inventory.`,
	Args: cobra.NoArgs,
	RunE: runRegistryAdopt,
}

// runRegistryAdopt records an existing mirror without replicating into it.
//
// The record it writes is identical to replicate's in every field the rest of the
// tool reads, with one deliberate exception: Artifacts is empty unless
// --verify-contents was passed, because without a BOM there is no way to know what
// the mirror holds. That matters for `registry delete`, which walks Artifacts — an
// adopted record cannot drive a delete, and adopt says so rather than leaving a
// later delete to silently remove nothing.
func runRegistryAdopt(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	target, err := buildTarget(cmd.Context(), name, ws)
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)

	// The CA is resolved the same way replicate resolves it — config/file first,
	// else an out-of-band-PINNED capture. Adoption does not relax that: an
	// unpinned capture is refused here exactly as it is there, because the CA
	// ends up in every node's trust store either way.
	pushHost := registryHostFromPath(target.ImageHostPath())
	mirrorCA, caErr := resolveMirrorCA(name, ws, pushHost)
	if caErr != nil {
		return caErr
	}

	// The engine must TRUST that CA, not merely record it: both things adopt does
	// over the network — ProbeNamespace's catalog listing and --verify-contents'
	// digest checks — go through crane. Without it a self-signed mirror (the entire
	// target case for adoption) fails x509: the probe degrades to a warning and
	// silently loses its only validation, and --verify-contents reports every
	// artifact as missing.
	eng := registryEngine(target, in, mirrorCA)

	var artifacts []config.MirrorArtifact
	manifestVersion := in.ManifestVersion

	if registryAdoptFlags.verifyContents {
		bom, berr := buildBOM(cmd.Context(), name, ws, &in, registryScratchDir(name))
		if berr != nil {
			return fmt.Errorf("--verify-contents needs the FAR source to build the BOM: %w", berr)
		}
		manifestVersion = bom.ManifestVersion
		// VerifyAll, not Verify: it returns every artifact with its resolved TARGET
		// digest, so the recorded inventory can carry digests. An inventory without
		// them drives a tag-based `registry delete` rather than the digest-based
		// form, which is the reliable one for a registry manifest DELETE.
		results := eng.VerifyAll(cmd.Context(), bom)
		var bad []mirror.Result
		for _, r := range results {
			if r.Err != nil {
				bad = append(bad, r)
			}
		}
		if len(bad) > 0 {
			for _, r := range bad {
				fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", r.Artifact.Name, r.Err)
			}
			return fmt.Errorf("adopt --verify-contents: %d of %d artifacts are missing or digest-mismatched",
				len(bad), len(bom.Artifacts))
		}
		for _, r := range results {
			artifacts = append(artifacts, config.MirrorArtifact{
				Kind: string(r.Artifact.Kind), Name: r.Artifact.Name, Tag: r.Artifact.Tag, Digest: r.Digest,
			})
		}
		fmt.Fprintf(os.Stderr, "✓ verified %d artifacts against the source\n", len(bom.Artifacts))
	} else {
		// Source-free sanity check: does the mirror hold anything under the prefix?
		n, perr := eng.ProbeNamespace(cmd.Context(), target.MirrorNamespace())
		switch {
		case perr != nil:
			// Not every registry exposes _catalog. Being unable to look is not the
			// same as looking and finding nothing, so this warns rather than fails.
			fmt.Fprintf(os.Stderr, "  ⚠ could not list %s to sanity-check the mirror: %v\n", pushHost, perr)
		case n == 0 && !registryAdoptFlags.force:
			return fmt.Errorf("the mirror at %s holds no repositories under %q — "+
				"check registry.generic_repo_prefix, or pass --force to record it anyway",
				pushHost, target.MirrorNamespace())
		case n == 0:
			fmt.Fprintf(os.Stderr, "  ⚠ %s holds no repositories under %q — recording anyway (--force)\n",
				pushHost, target.MirrorNamespace())
		default:
			suffix := "ies"
			if n == 1 {
				suffix = "y"
			}
			fmt.Fprintf(os.Stderr, "  ✓ %s holds %d repositor%s under %q\n",
				pushHost, n, suffix, target.MirrorNamespace())
		}
	}

	rec := &config.RegistryMirror{
		Target:          registryTargetKind(ws),
		Namespace:       target.MirrorNamespace(),
		ChartHost:       target.ChartHostPath(),
		ImageHost:       target.ImageHostPath(),
		ManifestVersion: manifestVersion,
		Artifacts:       artifacts,
		RegistryHost:    pushHost,
	}
	if mirrorCA != "" {
		rec.CACert = mirrorCA
		fmt.Fprintf(os.Stderr, "  ✓ recorded the mirror CA from %s (nodes install it before pulling)\n", pushHost)
	} else if pushHost != "" {
		fmt.Fprintf(os.Stderr, "  ⚠ no CA recorded for %s — if it is a self-signed mirror, re-run with --registry-ca <file>\n", pushHost)
	}
	if err := config.WriteRegistryMirror(name, rec); err != nil {
		return fmt.Errorf("recording mirror: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ adopted the mirror at %s — `bnk up` will render against it\n", target.ChartHostPath())
	if len(artifacts) == 0 {
		fmt.Fprintln(os.Stderr, "  note: no artifact inventory was recorded, so `registry delete` has nothing to "+
			"remove for this workspace. Re-run with --verify-contents (needs the FAR source) to record one.")
	} else {
		fmt.Fprintf(os.Stderr, "  ✓ recorded %d artifacts with digests — `registry delete` can drive from this record\n", len(artifacts))
	}
	return nil
}

// ── verify ──────────────────────────────────────────────────────────────────

func runRegistryVerify(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmd.Context(), name, ws, &in, registryScratchDir(name))
	if err != nil {
		return err
	}
	target, err := buildTarget(cmd.Context(), name, ws)
	if err != nil {
		return err
	}
	// Trust a private/self-signed mirror's CA for the verify HEAD checks too — the
	// crane digest probes fail x509 from a container operator otherwise, exactly as
	// the replicate push does. Best-effort capture (public targets return "").
	verifyCA, _ := resolveMirrorCA(name, ws, registryHostFromPath(target.ImageHostPath()))
	eng := registryEngine(target, in, verifyCA)
	bad := eng.Verify(cmd.Context(), bom)
	if flagOutput == "json" {
		out := make([]map[string]string, 0, len(bad))
		for _, b := range bad {
			out = append(out, map[string]string{"name": b.Artifact.Name, "tag": b.Artifact.Tag, "error": b.Err.Error()})
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"bad": out, "bom_total": len(bom.Artifacts)})
	}
	if len(bad) == 0 {
		fmt.Fprintf(os.Stderr, "✓ all %d BOM artifacts present + digest-matched in the mirror\n", len(bom.Artifacts))
		// Verify stays read-only. It does NOT write registry-mirror.json — a verb
		// that promises inspection should not change what a later `bnk up` does,
		// and two commands writing the record would drift over what they put in it
		// (replicate and adopt --verify-contents record an artifact inventory; a
		// bare adopt cannot). It does say what to run, so a mirror proven good is
		// one obvious command away from being usable.
		if _, rerr := config.ReadRegistryMirror(name); errors.Is(rerr, config.ErrNoRegistryMirror) {
			fmt.Fprintln(os.Stderr, "  note: this workspace has no mirror record, so `bnk up` will refuse to "+
				"use it. Run `roksbnkctl registry adopt` to record it.")
		}
		return nil
	}
	for _, b := range bad {
		fmt.Fprintf(os.Stderr, "  BAD %s/%s:%s — %v\n", b.Artifact.Kind, b.Artifact.Name, b.Artifact.Tag, b.Err)
	}
	return fmt.Errorf("verify: %d of %d artifacts missing or mismatched", len(bad), len(bom.Artifacts))
}

// ── prune ───────────────────────────────────────────────────────────────────

func runRegistryPrune(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmd.Context(), name, ws, &in, registryScratchDir(name))
	if err != nil {
		return err
	}
	rec, err := config.ReadRegistryMirror(name)
	if err != nil {
		return err
	}

	// Same refusal as delete: prune computes what to REMOVE from the record and
	// removes it from the configured target. A record for another mirror makes
	// that a delete against the wrong registry (#109).
	if why := mirrorRecordMismatch(cmd.Context(), name, ws, rec); why != "" {
		return fmt.Errorf("refusing to prune: the recorded mirror does not describe the configured target — %s", why)
	}
	inBOM := map[string]bool{}
	for _, a := range bom.Artifacts {
		inBOM[string(a.Kind)+"|"+a.Name+":"+a.Tag] = true
	}
	var stale []config.MirrorArtifact
	for _, a := range rec.Artifacts {
		if !inBOM[a.Kind+"|"+a.Name+":"+a.Tag] {
			stale = append(stale, a)
		}
	}
	if flagOutput == "json" {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"stale": stale})
	}
	if len(stale) == 0 {
		fmt.Fprintln(os.Stderr, "no stale artifacts in the mirror — nothing to prune")
		return nil
	}
	// Pruning a registry target is a per-artifact manifest delete; here we
	// report the stale set so an operator can act. Removing them from the record
	// keeps the mirror record honest about the intended set.
	fmt.Fprintf(os.Stderr, "%d stale artifacts (no longer in the BOM):\n", len(stale))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tNAME\tTAG")
	for _, a := range stale {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", a.Kind, a.Name, a.Tag)
	}
	tw.Flush()
	return nil
}
