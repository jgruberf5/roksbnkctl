package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cos"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/registry/mirror"
	"github.com/jgruberf5/roksbnkctl/internal/registry/openshift"
	"github.com/jgruberf5/roksbnkctl/internal/registry/source"
)

// `roksbnkctl registry ...` is the Sprint 29 air-gap registry-mirror command
// group (PRD 11). It replicates every artifact a BNK install needs — the F5
// charts + images enumerated by the f5-bigip-k8s-manifest plus the non-F5 deps —
// from FAR (repo.f5.com) into a private target (the cluster's own OpenShift
// internal registry), and reports on that mirror. The surface is CRUD-shaped,
// modeled on the COS client:
//
//	registry bom        Build + print the bill-of-materials (no target needed)
//	registry list       List what is currently in the mirror
//	registry diff       Show BOM vs. mirror drift (what replicate would copy)
//	registry replicate  Copy the BOM into the mirror (needs a live cluster)
//	registry verify     Confirm every BOM artifact is present + digest-matched
//	registry prune      Remove mirrored artifacts no longer in the BOM
var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Air-gap registry mirror — replicate BNK artifacts into a private registry (PRD 11)",
	Long: `Manage the air-gap registry mirror: replicate every chart + image a BNK
install needs (the f5-bigip-k8s-manifest set plus the non-F5 dependencies) from
the F5 Artifact Repository (repo.f5.com) into a private target — the cluster's
own OpenShift internal registry — so an air-gapped cluster installs BNK from
images it hosts itself.

Commands:
  roksbnkctl registry bom        Build + print the bill-of-materials
  roksbnkctl registry list       List artifacts currently in the mirror
  roksbnkctl registry diff       Show what ` + "`replicate`" + ` would copy (BOM vs. mirror)
  roksbnkctl registry replicate  Copy the BOM into the mirror (needs a live cluster)
  roksbnkctl registry verify     Confirm every BOM artifact is present + digest-matched
  roksbnkctl registry prune      Remove mirrored artifacts no longer in the BOM

` + "`registry bom`" + ` works offline against the FAR manifest; the cluster-touching
verbs (replicate/list/diff/verify/prune) need a reachable cluster + a configured
registry: block in the workspace config.`,
}

// registry-group flag values.
var (
	flagRegistryJSON         bool
	flagRegistryManifestVer  string
	flagRegistryFARRepo      string
	flagRegistrySAB64        string
	flagRegistryIncludeDeps  bool
	flagRegistryNoIncludeDep bool
	flagRegistryKubeconfig   string
	flagRegistryConcurrency  int
	flagRegistryTarget       string
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
	Short: "Copy the BOM into the mirror (needs a live cluster)",
	Long: `Prepares the target registry (enables the route, mints a push token, binds
pull RBAC), then copies every BOM artifact into it, idempotently. Records the
result in registry-mirror.json so the BNK install can be redirected to the mirror.`,
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

func init() {
	registryBOMCmd.Flags().BoolVar(&flagRegistryJSON, "json", false, "emit the BOM as JSON (overrides --output)")
	for _, c := range []*cobra.Command{registryBOMCmd, registryDiffCmd, registryReplicateCmd, registryVerifyCmd, registryPruneCmd} {
		c.Flags().StringVar(&flagRegistryManifestVer, "manifest-version", "", "BNK manifest version (default: workspace bnk.manifest_version)")
		c.Flags().StringVar(&flagRegistryFARRepo, "far-repo-url", "", "FAR registry host (default: workspace bnk.far_repo_url, else repo.f5.com)")
		c.Flags().StringVar(&flagRegistrySAB64, "source-sa-b64", "", "FAR _json_key_base64 service account (default: workspace registry.source_service_account_b64)")
		c.Flags().BoolVar(&flagRegistryIncludeDeps, "include-deps", false, "force-include the non-F5 dependency artifacts (cert-manager, node-labeler)")
		c.Flags().BoolVar(&flagRegistryNoIncludeDep, "no-include-deps", false, "exclude the non-F5 dependency artifacts")
	}
	for _, c := range []*cobra.Command{registryReplicateCmd, registryVerifyCmd, registryPruneCmd} {
		c.Flags().StringVar(&flagRegistryKubeconfig, "kubeconfig", "", "kubeconfig path (default: workspace/cluster default)")
		c.Flags().StringVar(&flagRegistryTarget, "target", "", `mirror target backend (default: workspace registry.target, else "openshift")`)
	}
	registryReplicateCmd.Flags().IntVar(&flagRegistryConcurrency, "concurrency", 0, "parallel copy workers (default: 4)")

	registryCmd.AddCommand(registryBOMCmd, registryListCmd, registryDiffCmd, registryReplicateCmd, registryVerifyCmd, registryPruneCmd)
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
// holds the FAR auth tarball + the license JWT. These mirror the terraform
// ibmcloud_cos_instance_name / ibmcloud_resources_cos_bucket /
// ibmcloud_cos_bucket_region defaults.
const (
	farOrchestrationCOSInstance = "bnk-orchestration"
	farResourcesBucket          = "bnk-schematics-resources"
	farCOSBucketRegion          = "us-south"
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
	inst, err := ic.GetCOSInstanceByName(ctx, farOrchestrationCOSInstance)
	if err != nil {
		return "", fmt.Errorf("finding the %q COS instance: %w", farOrchestrationCOSInstance, err)
	}
	cosClient, err := cos.New(apiKey, farCOSBucketRegion, inst.CRN)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "far-auth-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	tgz := filepath.Join(tmp, "far-auth.tgz")
	if err := cosClient.GetObjectToFile(ctx, farResourcesBucket, farAuthFile, tgz); err != nil {
		return "", fmt.Errorf("downloading %s from COS %s/%s: %w", farAuthFile, farOrchestrationCOSInstance, farResourcesBucket, err)
	}
	return source.ExtractServiceAccountFromTarball(tgz)
}

// buildBOM pulls the FAR manifest and assembles the BOM. workspaceScratch is the
// dir helm pulls into (the workspace state/scratch tree); "" → a temp dir. When
// no source service account is configured, it is resolved from the workspace's
// FAR auth tarball in COS (bnk.far_auth_file) so the command runs flag-free.
func buildBOM(ctx context.Context, name string, ws *config.Workspace, in registryBOMInputs, workspaceScratch string) (*bnkbom.BOM, error) {
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

// buildTarget constructs + prepares the OpenShift mirror target against the live
// cluster. Used by the cluster-touching verbs.
func buildTarget(ctx context.Context, ws *config.Workspace) (*openshift.Target, error) {
	kind := "openshift"
	if ws.Registry != nil && ws.Registry.Target != "" {
		kind = ws.Registry.Target
	}
	if flagRegistryTarget != "" { // --target overrides the workspace config
		kind = flagRegistryTarget
	}
	if kind != "openshift" {
		return nil, fmt.Errorf("unsupported registry target %q (only \"openshift\" is implemented)", kind)
	}
	cfg, err := k8s.BuildRESTConfig(flagRegistryKubeconfig)
	if err != nil {
		return nil, err
	}
	t := &openshift.Target{Namespace: ws.Registry.MirrorNamespace()}
	if err := t.Prepare(ctx, cfg); err != nil {
		return nil, fmt.Errorf("preparing mirror target: %w", err)
	}
	return t, nil
}

func registryEngine(t mirror.Target, in registryBOMInputs) *mirror.Engine {
	return &mirror.Engine{
		Target:      t,
		SourceAuth:  source.SourceAuth(in.FARRepoURL, in.SourceSAB64),
		Concurrency: flagRegistryConcurrency,
	}
}

// ── bom ─────────────────────────────────────────────────────────────────────

func runRegistryBOM(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmd.Context(), name, ws, in, registryScratchDir(name))
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

func runRegistryDiff(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmd.Context(), name, ws, in, registryScratchDir(name))
	if err != nil {
		return err
	}
	rec, err := config.ReadRegistryMirror(name)
	have := map[string]bool{}
	if err == nil {
		for _, a := range rec.Artifacts {
			have[a.Kind+"|"+a.Name+":"+a.Tag] = true
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
		fmt.Fprintln(os.Stderr, "mirror is in sync with the BOM — nothing to replicate")
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

func runRegistryReplicate(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmd.Context(), name, ws, in, registryScratchDir(name))
	if err != nil {
		return err
	}
	target, err := buildTarget(cmd.Context(), ws)
	if err != nil {
		return err
	}
	eng := registryEngine(target, in)
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
		Target:          "openshift",
		Namespace:       target.Namespace,
		ChartHost:       target.ChartHostPath(),
		ImageHost:       target.ImageHostPath(),
		ManifestVersion: bom.ManifestVersion,
		Artifacts:       mirrored,
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

// ── verify ──────────────────────────────────────────────────────────────────

func runRegistryVerify(cmd *cobra.Command, _ []string) error {
	name, ws, err := loadRegistryWorkspace()
	if err != nil {
		return err
	}
	in := resolveBOMInputs(ws)
	bom, err := buildBOM(cmd.Context(), name, ws, in, registryScratchDir(name))
	if err != nil {
		return err
	}
	target, err := buildTarget(cmd.Context(), ws)
	if err != nil {
		return err
	}
	bad := registryEngine(target, in).Verify(cmd.Context(), bom)
	if flagOutput == "json" {
		out := make([]map[string]string, 0, len(bad))
		for _, b := range bad {
			out = append(out, map[string]string{"name": b.Artifact.Name, "tag": b.Artifact.Tag, "error": b.Err.Error()})
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"bad": out, "bom_total": len(bom.Artifacts)})
	}
	if len(bad) == 0 {
		fmt.Fprintf(os.Stderr, "✓ all %d BOM artifacts present + digest-matched in the mirror\n", len(bom.Artifacts))
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
	bom, err := buildBOM(cmd.Context(), name, ws, in, registryScratchDir(name))
	if err != nil {
		return err
	}
	rec, err := config.ReadRegistryMirror(name)
	if err != nil {
		return err
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
	// Pruning the OpenShift internal registry is a per-image-stream delete that
	// the Stage-6 gated-live pass exercises against a real cluster; here we
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
