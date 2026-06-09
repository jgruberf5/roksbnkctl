package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/naming"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// githubRepoPattern matches a GitHub-shaped "owner/repo" slug. Used by
// the init prompt to decide whether a user-typed TF source is a GitHub
// repo or a local path. Must match the full input.
var githubRepoPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*/[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// looksLikeGitHubRepo reports whether s matches the "owner/repo" pattern.
// Anything else (paths, URLs, blank) is treated as a local path.
func looksLikeGitHubRepo(s string) bool {
	return githubRepoPattern.MatchString(strings.TrimSpace(s))
}

// resolveLocalTFSource is the cli-layer thin wrapper over the single
// path-normalization chokepoint (orchestration.NormalizeLocalPath). It
// exists so the in-package tests that pin the --tf-source local
// relative-path behavior (Sprint 12 Issue 2) call a stable local
// symbol; the canonical normalization lives in orchestration and is
// applied exactly once at command entry (the root PersistentPreRunE →
// resolveInvocationContext), NOT re-derived per call site.
//
// Only reached for the local TF source form — the embedded/github
// branches (split off via the "embedded" literal and looksLikeGitHubRepo)
// never build a local Path, so there is no URL or owner/repo input to
// guard against here.
func resolveLocalTFSource(path string) (string, error) {
	return orchestration.NormalizeLocalPath(path)
}

// envHasAPIKey reports whether any of the env vars the resolution chain
// honours is set. Used by `roksbnkctl init` to decide whether to opportunistically
// persist the resolved key into the workspace — env-driven setups don't
// need persistent storage; they have it already.
func envHasAPIKey() bool {
	for _, v := range []string{"IBMCLOUD_API_KEY", "IC_API_KEY", "TF_VAR_ibmcloud_api_key", "TF_VAR_IBMCLOUD_API_KEY", "TF_VAR_IC_API_KEY"} {
		if os.Getenv(v) != "" {
			return true
		}
	}
	return false
}

const (
	// defaultTFRepo is the source roksbnkctl drives by default. Per the
	// PRD's "unified tag stream" decision, roksbnkctl pins to the latest
	// release of this repo at init time.
	defaultTFRepo = "jgruberf5/ibmcloud_terraform_bigip_next_for_kubernetes_2_3"

	// initTimeout caps the network operations init does (IAM verify,
	// resource group lookup, GitHub release resolution). Prompts run
	// outside the timeout so users can take their time typing.
	initTimeout = 60 * time.Second
)

// runInit implements `roksbnkctl init` — interactive setup that writes the
// workspace's config.yaml and (if no global pointer is set) sets the
// current_workspace pointer.
//
// Behaviours:
//   - If --upgrade-tf and the workspace exists, just bumps tf_source.ref.
//   - If the workspace exists and --upgrade-tf is not set, prompts to
//     overwrite (existing values become the default for each prompt).
//   - If --var-file <path> is supplied (Sprint 19 Issue 1), the file
//     seeds the interview-targeted config.yaml fields and is copied
//     verbatim to BOTH phase state dirs as `terraform.tfvars.user`
//     (mode 0600). Prompts the file answered are skipped; fields it
//     doesn't carry still prompt (or default) exactly as today. Without
//     --var-file, behaviour is byte-identical to pre-Sprint-19.
//   - If stdin is not a TTY, accepts every default — usable from CI as
//     long as IBMCLOUD_API_KEY and the existing config (or workspace
//     name) provide enough context.
func runInit(_ *cobra.Command, _ []string) error {
	if err := rejectOnFlag("init"); err != nil {
		return err
	}
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}

	// No -w and no current pointer (first run, or every workspace was deleted).
	// config.New no longer falls back to a phantom "default", so ask for a name
	// here — non-interactive runs take the bootstrap "default".
	if cctx.WorkspaceName == "" {
		name := promptString("Workspace name", config.DefaultWorkspace)
		if err := config.ValidateName(name); err != nil {
			return err
		}
		cctx, err = config.New(name)
		if err != nil {
			return err
		}
	}

	// --upgrade-tf is the cheap path: re-resolve TF source on existing config.
	if flagUpgradeTF {
		if cctx.Workspace == nil {
			return fmt.Errorf("workspace %q does not exist; run `roksbnkctl init` (without --upgrade-tf) to create it", cctx.WorkspaceName)
		}
		ctx, cancel := contextWithTimeout(initTimeout)
		defer cancel()
		return runUpgradeTF(ctx, cctx)
	}

	// Sprint 19 Issue 1 — `--var-file <path>` parsed up-front so a
	// missing/malformed file surfaces an actionable error BEFORE the
	// interview runs (acceptance #4, #5). Resolution to an absolute path
	// is done here at the var-file branch entry — init's single-string
	// `--var-file` is not the lifecycle's chokepoint-normalized
	// flagVarFiles array, and the chokepoint guard test pins zero
	// per-RunE re-derivations of THAT name. This is a separate flag,
	// resolved once, at its own seam.
	var seeds varFileSeeds
	varFilePath := ""
	if flagInitVarFile != "" {
		varFilePath, err = absVarFilePath(flagInitVarFile)
		if err != nil {
			return err
		}
		seeds, err = loadInitVarFile(varFilePath)
		if err != nil {
			return err
		}
	}

	// Existing workspace + interactive overwrite confirmation.
	if cctx.Workspace != nil {
		fmt.Fprintf(os.Stderr, "Workspace %q already exists.\n", cctx.WorkspaceName)
		if !promptYesNo("Overwrite config?", false) {
			return errors.New("aborted")
		}
	}

	fmt.Fprintf(os.Stderr, "Setting up workspace %q\n\n", cctx.WorkspaceName)

	// Existing values become defaults; otherwise PRD-stated defaults.
	dRegion, dRG, dCluster, dOCP, dWorkers, dCreate := initDefaults(cctx)

	// API key — env, then keychain, then prompt; offer to save on prompt.
	resolver := &cred.Resolver{Workspace: cctx.WorkspaceName}
	apiKey, err := resolver.IBMCloudAPIKey(context.Background())
	if err != nil {
		return fmt.Errorf("resolving API key: %w", err)
	}

	// Bootstrap region only to construct the client for credential verification
	// + the region/cluster listings (Verify, ListRegions, ListClusters,
	// ResolveResourceGroup are global or use a fixed host). The FINAL region is
	// chosen in the interview below — a menu when creating a cluster, the
	// chosen cluster's own region when reusing one.
	region := dRegion
	if seeds.HasRegion {
		region = seeds.Region
	}

	// Network ops below — bound to a timeout.
	ctx, cancel := contextWithTimeout(initTimeout)
	defer cancel()

	fmt.Fprintln(os.Stderr, "\n→ Verifying IBM Cloud credentials...")
	ic, err := ibm.New(apiKey, region)
	if err != nil {
		return err
	}
	id, err := ic.Verify(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ %s\n\n", id)

	// Two flows:
	//   - --var-file: non-interactive seed path (cluster from the file, every
	//     resource defaulted to "create"); region from the seed/default.
	//   - interactive: the account-aware interview — create-vs-reuse, region +
	//     existing-cluster menus pulled from the credentials, resource toggles,
	//     and the optional testing client (with its own region).
	var (
		cluster   config.ClusterCfg
		prefix    string
		resources *config.ResourcesCfg
		rgName    string
	)
	if varFilePath != "" {
		cluster, prefix, resources, err = seedVarFileInterview(seeds, dCreate, dCluster, dOCP, dWorkers, cctx.WorkspaceName)
		if err != nil {
			return err
		}
		if seeds.HasResourceGroup {
			rgName = seeds.ResourceGroup
		}
	} else {
		choices, ierr := runAccountInterview(ctx, ic, cctx, region, dOCP, dWorkers, dCreate)
		if ierr != nil {
			return ierr
		}
		region = choices.Region
		cluster = choices.Cluster
		prefix = choices.Prefix
		resources = choices.Resources
	}

	// Resource group (global; region-independent). Interactive: prompt after
	// the cluster/region choice; --var-file: the seeded group, else prompt.
	if rgName == "" {
		rgName = promptString("Resource group", dRG)
	}
	rgID, err := ic.ResolveResourceGroup(ctx, rgName)
	if err != nil {
		return fmt.Errorf("verifying resource group: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ Resource group %q (id %s)\n\n", rgName, rgID)

	tfCfg, err := promptTFSource(ctx, cctx)
	if err != nil {
		return err
	}

	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{
			Region:        region,
			ResourceGroup: rgName,
		},
		Cluster:   cluster,
		Prefix:    prefix,
		Resources: resources,
		TFSource:  tfCfg,
	}

	// BNK supply-chain knobs (Sprint 29): the manifest version + the FAR auth
	// tarball name. Asked only when BNK is being deployed; seeded with the
	// terraform-default values and stored in config.yaml so `registry` and the
	// BNK phase run from the workspace without flags.
	if resources != nil && resources.BNK.Create {
		ws.BNK.ManifestVersion = promptString("BNK manifest version", config.DefaultManifestVersion)
		ws.BNK.FarAuthFile = promptString("FAR auth file (in the orchestration COS bucket)", config.DefaultFARAuthFile)
		ws.BNK.SubscriptionJWTFile = promptString("Subscription JWT file (in the orchestration COS bucket)", config.DefaultSubscriptionJWTFile)
	}

	// Show the resolved name plan so the operator sees exactly what
	// roksbnkctl will ask IBM Cloud to create (or reuse).
	printNamePlan(os.Stderr, ws)
	if err := config.SaveWorkspace(cctx.WorkspaceName, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	cfgPath, _ := config.WorkspaceConfigPath(cctx.WorkspaceName)
	fmt.Fprintf(os.Stderr, "\n✓ Wrote %s\n", cfgPath)

	// Sprint 19 Issue 1 — `--var-file <path>`. Copy the operator's file
	// verbatim to the workspace root as `terraform.tfvars.user`
	// (mode 0600, sibling to config.yaml). This is the file the existing
	// tfws.HasUserTFVars() codepath auto-layers between the auto-rendered
	// tfvars and any caller's `--var-file <…>` flag on every subsequent
	// lifecycle op — no further code change needed. The same file serves
	// BOTH the trial and cluster phases (tf.Workspace.UserTFVarsPath
	// resolves to filepath.Dir(stateDir)/terraform.tfvars.user for either
	// phase). Pre-existing file is overwritten (acceptance #7) with a
	// brief stderr note so the operator sees what landed.
	if varFilePath != "" {
		// AC #7 — a re-init that supplies a different var-file overwrites
		// the existing terraform.tfvars.user copy; note on stderr so the
		// operator sees the replacement happened. Detection is a pre-copy
		// stat because the helper is atomic-rename (the old file vanishes
		// as the new one lands — no chance to check post-hoc).
		wsRoot, _ := config.WorkspaceDir(cctx.WorkspaceName)
		prior := filepath.Join(wsRoot, "terraform.tfvars.user")
		if _, statErr := os.Stat(prior); statErr == nil {
			fmt.Fprintf(os.Stderr, "note: replacing existing %s\n", prior)
		}
		dest, copyErr := writeUserTFVarsCopies(cctx.WorkspaceName, varFilePath)
		if copyErr != nil {
			return copyErr
		}
		fmt.Fprintf(os.Stderr, "✓ Wrote %s\n", dest)
	}

	// Persist the API key for future runs. ResolveAPIKey may have
	// already saved to the keychain during the prompt path, but if it
	// couldn't (e.g. WSL2 without libsecret) the workspace didn't yet
	// exist for the config.yaml fallback. Now it does — try again.
	if !envHasAPIKey() && !config.APIKeyInKeychain(cctx.WorkspaceName) {
		dest, perr := config.SaveAPIKeyForWorkspace(cctx.WorkspaceName, apiKey)
		if perr == nil {
			fmt.Fprintf(os.Stderr, "✓ API key persisted in %s\n", dest)
		} else {
			fmt.Fprintf(os.Stderr, "warning: could not persist API key: %v\n", perr)
			fmt.Fprintln(os.Stderr, "  set IBMCLOUD_API_KEY in a .env file or shell to skip the prompt next run")
		}
	}

	// Initialising a workspace selects it — the user's environment follows the
	// freshly-configured workspace without a separate `ws use`.
	if err := config.SetCurrent(cctx.WorkspaceName); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not set current workspace: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "✓ Current workspace: %s\n", cctx.WorkspaceName)
	}

	fmt.Fprintln(os.Stderr, "\nNext: roksbnkctl up")
	return nil
}

// allCreateResources returns a ResourcesCfg with every toggle set to
// create + no existing refs — the default the non-interactive (--var-file
// and re-init-without-answers) flows land so the generated base is
// collision-safe.
func allCreateResources() *config.ResourcesCfg {
	return &config.ResourcesCfg{
		TransitGateway:   config.ResourceToggle{Create: true},
		RegistryCOS:      config.ResourceToggle{Create: true},
		CertManager:      config.ResourceToggle{Create: true},
		BNK:              config.ResourceToggle{Create: true},
		TGWJumphost:      config.ResourceToggle{Create: true},
		ClusterJumphosts: config.ResourceToggle{Create: false},
		ClientVPC:        config.ResourceToggle{Create: true},
	}
}

// seedVarFileInterview builds the cluster + prefix + resources for the
// --var-file flow. The cluster block keeps the Sprint 19 seed-driven
// behaviour; the prefix is derived non-interactively (the file's
// openshift_cluster_name sanitized, else the sanitized workspace name) and
// every resource defaults to create. The generated base is collision-safe;
// the operator's verbatim terraform.tfvars.user still overrides it via
// terraform's var-file layering.
func seedVarFileInterview(seeds varFileSeeds, dCreate bool, dCluster, dOCP string, dWorkers int, workspaceName string) (config.ClusterCfg, string, *config.ResourcesCfg, error) {
	create := dCreate
	if seeds.HasCreateCluster {
		create = seeds.CreateCluster
	}
	cluster := config.ClusterCfg{Create: create}
	if create {
		if seeds.HasClusterName {
			cluster.Name = seeds.ClusterName
		} else {
			cluster.Name = dCluster
		}
		if seeds.HasOCPVersion {
			cluster.OpenShiftVersion = seeds.OCPVersion
		} else {
			cluster.OpenShiftVersion = dOCP
		}
		if seeds.HasWorkersPerZone {
			cluster.WorkersPerZone = seeds.WorkersPerZone
		} else {
			cluster.WorkersPerZone = dWorkers
		}
	} else {
		if seeds.HasClusterName {
			cluster.Name = seeds.ClusterName
		} else {
			cluster.Name = dCluster
		}
		if cluster.Name == "" {
			return config.ClusterCfg{}, "", nil, errors.New("existing cluster name is required when not creating")
		}
	}

	// Prefix: prefer the file's cluster name, else the workspace name.
	seedName := workspaceName
	if seeds.HasClusterName && seeds.ClusterName != "" {
		seedName = seeds.ClusterName
	}
	prefix := naming.SanitizeToPrefix(seedName)
	return cluster, prefix, allCreateResources(), nil
}

// accountInterview is the result of the interactive, account-aware init
// interview: the chosen region, the cluster (created or reused), the validated
// prefix, and the populated resources block (which carries the testing
// client's region when one was added).
type accountInterview struct {
	Region    string
	Cluster   config.ClusterCfg
	Prefix    string
	Resources *config.ResourcesCfg
}

// runAccountInterview drives the post-credential interview. It first asks
// whether to create a new ROKS cluster:
//
//   - create: pick a region from the account's available regions, choose a
//     prefix, OpenShift version, and workers-per-zone (floored at 1 — a ROKS
//     cluster spans all 3 AZs, so the minimum is 3 workers total).
//   - reuse: pick from the account's running OpenShift clusters; the region and
//     name come from the chosen cluster, and cluster-outputs.json is populated
//     so the BNK/Testing phases reuse it.
//
// Then the resource toggles, and an optional testing client whose region is
// chosen from the same region list. ic is used only for the (TTY-only) region
// and cluster menus, so a non-interactive run never dials the network here.
func runAccountInterview(ctx context.Context, ic *ibm.Client, cctx *config.Context, dRegion, dOCP string, dWorkers int, dCreate bool) (*accountInterview, error) {
	res := &config.ResourcesCfg{}
	out := &accountInterview{Resources: res, Region: dRegion}

	if promptYesNo("Create a new ROKS cluster?", dCreate) {
		out.Region = pickRegion(ctx, ic, "Region for the new cluster", dRegion)

		prefix, err := promptPrefix(cctx)
		if err != nil {
			return nil, err
		}
		out.Prefix = prefix

		// A ROKS cluster spans all three AZs, so the minimum is one worker per
		// zone (3 total). Clamp anything lower.
		workers := promptInt("Workers per zone (min 1; cluster spans all 3 AZs)", dWorkers)
		if workers < 1 {
			fmt.Fprintln(os.Stderr, "  (minimum is 1 worker per zone — a ROKS cluster spans 3 AZs, so 3 workers total)")
			workers = 1
		}
		out.Cluster = config.ClusterCfg{
			Create:           true,
			Name:             naming.Derive(prefix).ClusterName,
			OpenShiftVersion: promptString("OpenShift version", dOCP),
			WorkersPerZone:   workers,
		}
		res.RegistryCOS.Create = promptYesNo("Create registry COS instance?", true)
		if !res.RegistryCOS.Create {
			res.RegistryCOS.Existing = promptString("Existing COS instance name", "")
		}
	} else {
		fmt.Fprintln(os.Stderr, "\n→ Listing running OpenShift clusters...")
		clusters, err := ic.ListClusters(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing clusters: %w", err)
		}
		if len(clusters) == 0 {
			return nil, errors.New("no running OpenShift (ROKS) clusters found in this account — create one instead, or check that your API key has access")
		}
		labels := make([]string, len(clusters))
		for i, cl := range clusters {
			labels[i] = fmt.Sprintf("%s  (%s, %s)", cl.Name, cl.Region, cl.MasterKubeVersion)
		}
		chosen := clusters[promptSelect("Choose an existing cluster", labels, 0)]
		out.Region = chosen.Region
		out.Cluster = config.ClusterCfg{Create: false, Name: chosen.Name}

		prefix, err := prefixLoop(naming.SanitizeToPrefix(chosen.Name))
		if err != nil {
			return nil, err
		}
		out.Prefix = prefix
		res.RegistryCOS.Create = false

		// Record the cluster so BNK/Testing reuse it (mirrors `cluster register`).
		if err := registerReusedCluster(ctx, ic, cctx.WorkspaceName, chosen.Name); err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not record cluster details (%v)\n  run `roksbnkctl cluster register %s` before `roksbnkctl up`\n", err, chosen.Name)
		}
	}

	// Transit gateway. When declined, an enabled TGW jumphost needs the
	// existing gateway's name — captured below once the jumphost is decided.
	res.TransitGateway.Create = promptYesNo("Create Transit Gateway?", true)

	// In-cluster services.
	res.CertManager.Create = promptYesNo("Install cert-manager?", true)
	res.BNK.Create = promptYesNo("Deploy BIG-IP Next (BNK)?", true)

	// Optional testing client (TGW jumphost + client VPC), in a region of its
	// own — prompted again from the account's region list.
	if promptYesNo("Add a testing client?", false) {
		res.TGWJumphost.Create = true
		res.ClientRegion = pickRegion(ctx, ic, "Region to install the test client in", out.Region)
		res.ClientVPC.Create = promptYesNo("Create a new client VPC for it?", true)
		if !res.ClientVPC.Create {
			res.ClientVPC.Existing = promptString("Existing client VPC name", "")
		}
		if !res.TransitGateway.Create {
			res.TransitGateway.Existing = promptString("Existing Transit Gateway name", "")
		}
	} else {
		res.TGWJumphost.Create = false
		res.ClientVPC.Create = false
	}

	// Per-zone cluster jumphosts (default off).
	res.ClusterJumphosts.Create = promptYesNo("Create per-zone cluster jumphosts?", false)

	return out, nil
}

// pickRegion shows the account's available regions as a menu and returns the
// chosen name, defaulting to def. TTY-only: a non-interactive run returns def
// without dialing the API (keeps init scriptable). On a list error it falls
// back to the built-in region list so init never hard-fails offline.
func pickRegion(ctx context.Context, ic *ibm.Client, label, def string) string {
	if !isTTY() {
		return def
	}
	regions, err := ic.ListRegions(ctx)
	if err != nil || len(regions) == 0 {
		if err != nil {
			fmt.Fprintf(os.Stderr, "  (could not list regions: %v; using the built-in list)\n", err)
		}
		regions = ibm.CuratedRegions()
	}
	labels := make([]string, len(regions))
	defIdx := 0
	for i, r := range regions {
		labels[i] = r.Name
		if r.Name == def {
			defIdx = i
		}
	}
	return regions[promptSelect(label, labels, defIdx)].Name
}

// registerReusedCluster populates cluster-outputs.json for a reused cluster so
// the BNK/Testing phases find it (mirrors `roksbnkctl cluster register`). The
// registry COS is recorded best-effort — a hand-built cluster may not have the
// roksbnkctl-convention `<cluster>-cos` instance.
func registerReusedCluster(ctx context.Context, ic *ibm.Client, workspace, clusterArg string) error {
	info, err := ic.GetCluster(ctx, clusterArg)
	if err != nil {
		return err
	}
	vpc := info.VPCID()
	if vpc == "" {
		return fmt.Errorf("cluster %q has no VPC (vpc-gen2 only)", info.Name)
	}
	out := &config.ClusterOutputs{
		ClusterName:      info.Name,
		ClusterID:        info.ID,
		Region:           info.Region,
		ResourceGroupID:  info.ResourceGroupID,
		VPCID:            vpc,
		MasterURL:        info.MasterURL,
		OpenShiftVersion: info.MasterKubeVersion,
		Source:           "cluster-register",
	}
	if cos, err := ic.GetCOSInstanceByName(ctx, info.Name+"-cos"); err == nil {
		out.RegistryCOSCRN = cos.CRN
		out.RegistryCOSName = cos.Name
	}
	return config.WriteClusterOutputs(workspace, out)
}

// promptPrefix runs the prefix loop: default = the existing workspace's
// prefix (re-init) else SanitizeToPrefix(workspaceName); prompt →
// ValidatePrefix; on failure print the actionable message + re-prompt. In a
// non-TTY run it validates the default and hard-errors if it is invalid
// (mirroring the non-interactive cred-resolver pattern). Loops a bounded
// number of times so a pathological TTY can't spin forever.
func promptPrefix(cctx *config.Context) (string, error) {
	def := naming.SanitizeToPrefix(cctx.WorkspaceName)
	if cctx.Workspace != nil && cctx.Workspace.Prefix != "" {
		def = cctx.Workspace.Prefix
	}
	return prefixLoop(def)
}

// prefixLoop prompts for a workspace prefix with the given default, validating
// via naming.ValidatePrefix and re-prompting on failure. Non-TTY validates the
// default and hard-errors if invalid. Shared by promptPrefix (create path) and
// the reuse path (default = sanitized existing-cluster name).
func prefixLoop(def string) (string, error) {
	label := fmt.Sprintf("Workspace prefix (≤ %d chars)", naming.MaxPrefixLen())

	if !isTTY() {
		if err := naming.ValidatePrefix(def); err != nil {
			return "", fmt.Errorf("non-interactive init: default prefix %q is invalid: %w", def, err)
		}
		return def, nil
	}

	for attempt := 0; attempt < 100; attempt++ {
		candidate := promptString(label, def)
		if err := naming.ValidatePrefix(candidate); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %v\n", err)
			continue
		}
		return candidate, nil
	}
	return "", errors.New("too many invalid prefix attempts")
}

// printNamePlan writes the resolved naming.Plan to w so the operator sees
// the generated (or reused) resource names before the config is saved. A
// legacy empty-prefix workspace prints nothing (the upstream module
// defaults still apply).
func printNamePlan(w io.Writer, ws *config.Workspace) {
	if ws.Prefix == "" {
		return
	}
	plan := naming.Derive(ws.Prefix)
	res := ws.Resources
	if res == nil {
		res = allCreateResources()
	}

	fmt.Fprintf(w, "\nResolved resource names for prefix %q:\n", ws.Prefix)
	clusterName := plan.ClusterName
	if !ws.Cluster.Create {
		clusterName = ws.Cluster.Name + "  (existing)"
	}
	fmt.Fprintf(w, "  cluster                %s\n", clusterName)
	fmt.Fprintf(w, "  cluster VPC            %s\n", plan.ClusterVPCName)
	fmt.Fprintf(w, "  registry COS instance  %s\n", planNameOrExisting(res.RegistryCOS, plan.COSInstanceName))
	fmt.Fprintf(w, "  transit gateway        %s\n", planNameOrExisting(res.TransitGateway, plan.TransitGatewayName))
	if res.TGWJumphost.Create {
		fmt.Fprintf(w, "  TGW jumphost           %s\n", plan.TGWJumphostName)
		fmt.Fprintf(w, "  client VPC             %s\n", planNameOrExisting(res.ClientVPC, plan.ClientVPCName))
	}
	if res.ClusterJumphosts.Create {
		fmt.Fprintf(w, "  cluster jumphosts      %s-<zone>\n", plan.ClusterJumphostPrefix)
	}
}

// planNameOrExisting renders the derived name when the toggle creates the
// resource, or the operator's existing name (annotated) when it reuses one.
func planNameOrExisting(t config.ResourceToggle, derived string) string {
	if t.Create {
		return derived
	}
	if t.Existing != "" {
		return t.Existing + "  (existing)"
	}
	return "(not managed)"
}

// runUpgradeTF re-resolves the TF source ref against the workspace's
// existing repo (or accepts --tf-source for a local override) and
// rewrites the workspace config. No prompts.
//
// For embedded sources there's nothing to upgrade — the TF version is
// whatever the binary ships, so update via `roksbnkctl self update` (or
// reinstall) rather than --upgrade-tf.
func runUpgradeTF(ctx context.Context, cctx *config.Context) error {
	if flagTFSource != "" {
		// Local-path override. flagTFSource is already normalized to an
		// absolute path by the single chokepoint (root PersistentPreRunE
		// → resolveInvocationContext) so the value pinned into
		// config.yaml stays stable regardless of the per-phase terraform
		// state dir CWD it's later used from (Sprint 12 Issue 2, retired
		// as a class). No per-call-site re-derivation.
		tfCfg := config.TFSourceCfg{Type: "local", Path: flagTFSource}
		return saveTFSourceUpdate(cctx, tfCfg)
	}
	switch cctx.Workspace.TFSource.Type {
	case "", "embedded":
		fmt.Fprintln(os.Stderr, "TF source is embedded — its version is tied to the roksbnkctl binary.")
		fmt.Fprintln(os.Stderr, "Update via `roksbnkctl self update` (or reinstall) to pick up newer HCL.")
		return nil
	case "github":
		repo := cctx.Workspace.TFSource.Repo
		if repo == "" {
			repo = defaultTFRepo
		}
		tfCfg, err := resolveLatestRelease(ctx, repo)
		if err != nil {
			return err
		}
		return saveTFSourceUpdate(cctx, tfCfg)
	case "local":
		fmt.Fprintln(os.Stderr, "TF source is a local path — nothing to re-resolve. Pass --tf-source <path> to change it.")
		return nil
	default:
		return fmt.Errorf("unknown TF source type %q in workspace config", cctx.Workspace.TFSource.Type)
	}
}

// saveTFSourceUpdate writes a new TF source into the workspace config,
// or no-ops if it matches what's already there. Used by --upgrade-tf.
func saveTFSourceUpdate(cctx *config.Context, tfCfg config.TFSourceCfg) error {
	if cctx.Workspace.TFSource == tfCfg {
		fmt.Fprintf(os.Stderr, "TF source already at %s\n", refDescription(tfCfg))
		return nil
	}
	prev := cctx.Workspace.TFSource
	cctx.Workspace.TFSource = tfCfg
	if err := config.SaveWorkspace(cctx.WorkspaceName, cctx.Workspace); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ TF source updated %s → %s\n", refDescription(prev), refDescription(tfCfg))
	return nil
}

// promptTFSource asks the user where Terraform should pull from. Accepts
// either a GitHub `owner/repo` slug (resolves to that repo's latest
// release) or any other input (treated as a local filesystem path).
//
// --tf-source short-circuits the prompt with a local override, matching
// the existing flag's behaviour.
//
// On re-init, the existing workspace's TF source is the default — users
// just press Enter to keep it.
//
// Default for fresh workspaces is "embedded" — the HCL bundled into the
// roksbnkctl binary. Most users want this; install one binary, get matched
// CLI + TF together with no separate fetch step.
func promptTFSource(ctx context.Context, cctx *config.Context) (config.TFSourceCfg, error) {
	if flagTFSource != "" {
		// flagTFSource is already normalized to an absolute path by the
		// single chokepoint (root PersistentPreRunE →
		// resolveInvocationContext) so the value pinned into config.yaml
		// stays stable regardless of the per-phase terraform state dir
		// CWD (Sprint 12 Issue 2). No per-call-site re-derivation.
		src := flagTFSource
		cfg := config.TFSourceCfg{Type: "local", Path: src}
		fmt.Fprintf(os.Stderr, "✓ TF source: local path %s\n", src)
		return cfg, nil
	}

	// Compute the prompt default. Existing workspace's setting wins,
	// otherwise "embedded".
	def := "embedded"
	if cctx.Workspace != nil {
		switch cctx.Workspace.TFSource.Type {
		case "github":
			if cctx.Workspace.TFSource.Repo != "" {
				def = cctx.Workspace.TFSource.Repo
			}
		case "local":
			if cctx.Workspace.TFSource.Path != "" {
				def = cctx.Workspace.TFSource.Path
			}
		}
	}

	fmt.Fprintln(os.Stderr, "\nTerraform source — leave as 'embedded' to use the HCL bundled in roksbnkctl,")
	fmt.Fprintln(os.Stderr, "or supply owner/repo for a GitHub release, or a path for a local checkout.")
	input := promptString("TF source", def)

	if input == "" || input == "embedded" {
		fmt.Fprintln(os.Stderr, "✓ TF source: embedded (bundled with roksbnkctl)")
		return config.TFSourceCfg{Type: "embedded"}, nil
	}

	if looksLikeGitHubRepo(input) {
		cfg, err := resolveLatestRelease(ctx, input)
		if err != nil {
			return config.TFSourceCfg{}, err
		}
		return cfg, nil
	}

	// Anything that's not "embedded" or GitHub-shaped is treated as a local path.
	fmt.Fprintf(os.Stderr, "✓ TF source: local path %s\n", input)
	return config.TFSourceCfg{Type: "local", Path: input}, nil
}

// resolveLatestRelease queries GitHub for the latest release of repo and
// returns a fully-formed TFSourceCfg pinned to that tag.
func resolveLatestRelease(ctx context.Context, repo string) (config.TFSourceCfg, error) {
	fmt.Fprintf(os.Stderr, "→ Resolving latest release of %s...\n", repo)
	ref, err := tf.ResolveLatestRelease(ctx, repo)
	if err != nil {
		return config.TFSourceCfg{}, fmt.Errorf("resolving TF source from GitHub: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ TF source: %s@%s\n", repo, ref)
	return config.TFSourceCfg{Type: "github", Repo: repo, Ref: ref}, nil
}

// initDefaults returns prompt defaults: existing workspace values first,
// PRD-stated defaults second. Workspace may be nil (fresh init).
func initDefaults(cctx *config.Context) (region, rg, cluster, ocp string, workers int, create bool) {
	region, rg, cluster, ocp = "ca-tor", "default", "bnk-demo", "4.18"
	workers, create = 1, true
	if cctx.Workspace == nil {
		return
	}
	if v := cctx.Workspace.IBMCloud.Region; v != "" {
		region = v
	}
	if v := cctx.Workspace.IBMCloud.ResourceGroup; v != "" {
		rg = v
	}
	if v := cctx.Workspace.Cluster.Name; v != "" {
		cluster = v
	}
	if v := cctx.Workspace.Cluster.OpenShiftVersion; v != "" {
		ocp = v
	}
	if v := cctx.Workspace.Cluster.WorkersPerZone; v != 0 {
		workers = v
	}
	create = cctx.Workspace.Cluster.Create
	return
}

// refDescription renders a TFSourceCfg for log output.
func refDescription(c config.TFSourceCfg) string {
	switch c.Type {
	case "", "embedded":
		return "embedded"
	case "github":
		return fmt.Sprintf("%s@%s", c.Repo, c.Ref)
	case "local":
		return fmt.Sprintf("local:%s", c.Path)
	default:
		return "<unknown>"
	}
}

// contextWithTimeout returns a child of context.Background with the
// given timeout. Used to keep init's network ops bounded.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
