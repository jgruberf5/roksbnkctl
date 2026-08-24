package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

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
  roksbnkctl registry list       List the artifacts the last replicate recorded
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

	// GatewayAPIBundleVersion is non-empty only when this workspace needs the
	// upstream Gateway API bundle (2.4 + bnk.gateway_api_mtls, #185), and
	// GatewayAPIBundleURL optionally moves where it is fetched from.
	//
	// Resolved from the WORKSPACE rather than offered as a flag. The condition is
	// a property of the install, not of the operator's mood: a mirror replicated
	// without the bundle is a mirror an mTLS install cannot install from, and one
	// replicated with it for a 2.3 workspace carries a megabyte of CRDs that
	// nothing will ever pull.
	GatewayAPIBundleVersion string
	GatewayAPIBundleURL     string
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
	if ws.GatewayAPIBundleNeeded() {
		in.GatewayAPIBundleVersion = ws.GatewayAPIBundleVersion()
		in.GatewayAPIBundleURL = ws.BNK.GatewayAPIBundleURL
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
	return bnkbom.Build(manifest, bomOptions(in))
}

// bomOptions maps the resolved inputs onto the BOM builder's options.
//
// Split out of buildBOM so it is reachable without a FAR manifest pull: buildBOM
// itself needs the network and a credential, so a field mapped to the wrong
// option there could only be caught by running against FAR — which no test does.
func bomOptions(in *registryBOMInputs) bnkbom.Options {
	return bnkbom.Options{
		ManifestVersion:         in.ManifestVersion,
		IncludeDeps:             in.IncludeDeps,
		CertManagerVersion:      in.CertManagerVer,
		NodeLabelerImageTag:     in.NodeLabelerTag,
		GatewayAPIBundleVersion: in.GatewayAPIBundleVersion,
		GatewayAPIBundleURL:     in.GatewayAPIBundleURL,
	}
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
	return config.MirrorTargetKind(ws, flagRegistryTarget)
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
		h, ok := config.ICRHostForRegion[ws.IBMCloud.Region]
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
		pw, derr := config.DecodeB64Field("registry.generic_password_b64", reg.GenericPasswordB64)
		if derr != nil {
			return nil, fmt.Errorf("registry target generic: %w", derr)
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

func printBOMTable(bom *bnkbom.BOM) {
	charts, images, files := bom.Counts()
	// Only mention files when there are any, so the common BNK BOM reads exactly
	// as it did before — but never omit them when present, or the parts stop
	// summing to the total shown beside them.
	if files > 0 {
		fmt.Fprintf(os.Stderr, "BNK manifest %s — %d charts, %d images, %d files (%d total)\n",
			bom.ManifestVersion, charts, images, files, len(bom.Artifacts))
	} else {
		fmt.Fprintf(os.Stderr, "BNK manifest %s — %d charts, %d images (%d total)\n",
			bom.ManifestVersion, charts, images, len(bom.Artifacts))
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KIND\tORIGIN\tSOURCE\tNAME\tTAG")
	for _, a := range bom.Artifacts {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Kind, a.Origin, a.SourceHost, a.Name, a.Tag)
	}
	tw.Flush()
}

// ── list ────────────────────────────────────────────────────────────────────

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
		der, err := config.DecodeB64Field("registry.generic_ca_b64", ws.Registry.GenericCAB64)
		if err != nil {
			return "", err
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
