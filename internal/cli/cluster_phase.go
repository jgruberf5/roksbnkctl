package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// `roksbnkctl cluster ...` is the cluster-phase command group: durable,
// per-workspace ROKS cluster lifecycle that's decoupled from BNK trial
// runs. Subcommands write/read ~/.roksbnkctl/<workspace>/cluster-outputs.json
// so a single cluster can host many BNK trials with different tfvars
// over its lifetime.
var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "ROKS cluster lifecycle (separate from BNK trials)",
	Long: `Manage the ROKS cluster as a durable, reusable resource that
sits underneath your BNK trials.

Commands:
  roksbnkctl cluster up        Create the ROKS cluster (+ transit gateway, registry COS)
  roksbnkctl cluster down      Destroy the cluster and everything cluster-scoped
  roksbnkctl cluster register  Discover an already-existing cluster and persist its identity
  roksbnkctl cluster config    Print the recorded cluster identity from cluster-outputs.json
  roksbnkctl cluster status    Live runtime status (endpoints + node readiness)

Each ` + "`roksbnkctl up`" + ` against this workspace will reuse the registered
cluster (reading cluster-outputs.json) so multiple BNK trials can share
one cluster.`,
}

var (
	flagClusterRegisterCOSName string
	flagClusterRegisterPrompt  bool
)

var clusterRegisterCmd = &cobra.Command{
	Use:   "register [cluster-name-or-id]",
	Short: "Discover an existing ROKS cluster and persist its identity",
	Long: `Looks up an existing ROKS cluster in your IBM Cloud account,
verifies its registry COS instance exists, and writes the cluster's
identity to ~/.roksbnkctl/<workspace>/cluster-outputs.json.

Subsequent ` + "`roksbnkctl up`" + ` runs in this workspace will pick up the
registered cluster automatically — no need to repeat its identity in
trial tfvars.

The registry COS instance is found by probing the names roksbnkctl and the
upstream HCL actually use: "<prefix>-registry-cos" (what ` + "`cluster up`" + `
creates), "<cluster>-registry-cos", then the upstream fallback
"<cluster>-cos". Pass --registry-cos-name to override (e.g. if your tfvars
sets roks_cos_instance_name to something else).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runClusterRegister,
}

var clusterConfigCmd = &cobra.Command{
	Use:     "config",
	Aliases: []string{"show"}, // back-compat: `cluster show` still works
	Short:   "Print the recorded cluster identity (cluster-outputs.json)",
	Long: `Prints the cluster identity recorded at cluster up / register time
(~/.roksbnkctl/<workspace>/cluster-outputs.json): cluster ID, endpoints, VPC,
transit gateway, registry COS, and the create-time settings the cluster was
built with (network mode, and the VPC address block when this tool created the
VPC). This is the RECORDED config, and for the create-time settings it is
AUTHORITATIVE — config.yaml describes what is wanted, this describes what
exists. For live runtime state (node readiness) use ` + "`roksbnkctl cluster status`" + `.`,
	Args: cobra.NoArgs,
	RunE: runClusterShow,
}

var clusterUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Provision the ROKS cluster (and cluster-shared services) only",
	Long: `Creates the durable cluster-shared infrastructure only — the ROKS
cluster, transit gateway, and registry COS. cert-manager and the BNK trial
modules (flo, cne_instance, license) deploy in the BNK phase; the jumphosts
deploy in the testing phase. On success, writes the cluster's identity to
~/.roksbnkctl/<workspace>/cluster-outputs.json so subsequent ` + "`roksbnkctl up`" + `
runs can deploy the BNK and testing phases onto this cluster.

Uses a separate state directory (~/.roksbnkctl/<workspace>/state-cluster/)
so it doesn't tangle with BNK or testing state.`,
	Args: cobra.NoArgs,
	RunE: runClusterUp,
}

var clusterDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy the cluster phase (ROKS + cluster-shared services)",
	Long: `Tears down everything roksbnkctl cluster up created. Refuses to run
if any BNK trial state exists for this workspace — destroy those first
with ` + "`roksbnkctl down`" + ` to avoid orphaned BNK resources.`,
	Args: cobra.NoArgs,
	RunE: runClusterDown,
}

func init() {
	clusterRegisterCmd.Flags().StringVar(&flagClusterRegisterCOSName, "registry-cos-name", "",
		`registry COS instance name (default: probe "<prefix>-registry-cos", "<cluster>-registry-cos", "<cluster>-cos")`)
	clusterRegisterCmd.Flags().BoolVar(&flagClusterRegisterPrompt, "prompt", false,
		"prompt for the cluster name even if one is given as an argument")

	// up/down share the same lifecycle flags as `roksbnkctl up`/`down` so users
	// only have one mental model. Reuses the package-level flag vars.
	clusterUpCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt before apply")
	clusterUpCmd.Flags().BoolVar(&flagNoKubeconfig, "no-kubeconfig", false, "skip the post-apply admin kubeconfig fetch")
	clusterUpCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")
	clusterDownCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")
	clusterDownCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")

	clusterConfigCmd.Flags().BoolVar(&flagStatusJSON, "json", false, "output JSON (CI-friendly)")
	clusterCmd.AddCommand(clusterRegisterCmd, clusterConfigCmd, clusterUpCmd, clusterDownCmd)
	rootCmd.AddCommand(clusterCmd)
}

func runClusterRegister(cmd *cobra.Command, args []string) error {
	cctx, ic, err := openIBMClient()
	if err != nil {
		return err
	}

	clusterArg := ""
	if len(args) == 1 {
		clusterArg = args[0]
	}
	if flagClusterRegisterPrompt || clusterArg == "" {
		clusterArg, err = promptForCluster(clusterArg)
		if err != nil {
			return err
		}
	}
	if clusterArg == "" {
		return errors.New("cluster name/id is required")
	}

	ctx := cmd.Context()

	// 1. Cluster discovery via container service (same endpoint
	//    `ibmcloud ks cluster get` uses). 404 → ErrClusterNotFound.
	fmt.Fprintf(os.Stderr, "→ Looking up cluster %q\n", clusterArg)
	info, err := ic.GetCluster(ctx, clusterArg)
	if err != nil {
		if errors.Is(err, ibm.ErrClusterNotFound) {
			return fmt.Errorf("no cluster named %q in this account (region %s) — check the name/ID and that your API key has access",
				clusterArg, ic.Region())
		}
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ Cluster %s (%s) — state: %s, masters: %s\n",
		info.Name, info.ID, info.State, info.MasterKubeVersion)
	vpc := info.VPCID()
	if vpc == "" {
		return fmt.Errorf("cluster %q has no VPC — roksbnkctl only supports vpc-gen2 clusters", info.Name)
	}
	fmt.Fprintf(os.Stderr, "✓ VPC %s (resource group %s)\n", vpc, info.ResourceGroupName)

	// 2. Registry COS instance verification. There is no single name to guess:
	//    roksbnkctl's OWN terraform names it "<prefix>-registry-cos"
	//    (internal/naming), while the upstream HCL fallback is "<cluster>-cos".
	//    Probing only the latter meant `cluster register` could never adopt a
	//    cluster roksbnkctl itself had created — the common case. Try each
	//    convention, and let --registry-cos-name override everything.
	var cosCandidates []string
	if flagClusterRegisterCOSName != "" {
		cosCandidates = []string{flagClusterRegisterCOSName}
	} else {
		cosCandidates = []string{
			cctx.Workspace.Prefix + "-registry-cos", // roksbnkctl convention
			info.Name + "-registry-cos",             // ditto, when prefix != cluster name
			info.Name + "-cos",                      // upstream HCL fallback
		}
	}
	var cos *ibm.COSInstance
	for _, name := range cosCandidates {
		if name == "-registry-cos" {
			continue // empty prefix
		}
		fmt.Fprintf(os.Stderr, "→ Verifying registry COS instance %q\n", name)
		c, cerr := ic.GetCOSInstanceByName(ctx, name)
		if cerr == nil {
			cos = c
			break
		}
		err = cerr
	}
	if cos == nil {
		return fmt.Errorf("no registry COS instance found in account (tried %s): %w\n  Either run `roksbnkctl cluster up` to create it, or pass --registry-cos-name <name> if your tfvars uses a different roks_cos_instance_name",
			strings.Join(cosCandidates, ", "), err)
	}
	fmt.Fprintf(os.Stderr, "✓ COS instance %s (%s)\n", cos.Name, cos.GUID)

	// 3. Persist. Source = "cluster-register" so consumers can tell
	//    discovered-vs-created.
	out := &config.ClusterOutputs{
		ClusterName:      info.Name,
		ClusterID:        info.ID,
		Region:           info.Region,
		ResourceGroupID:  info.ResourceGroupID,
		VPCID:            vpc,
		RegistryCOSCRN:   cos.CRN,
		RegistryCOSName:  cos.Name,
		MasterURL:        info.MasterURL,
		OpenShiftVersion: info.MasterKubeVersion,
		Source:           "cluster-register",
		NetworkMode:      networkModeFor(cctx),
	}
	if err := config.WriteClusterOutputs(cctx.WorkspaceName, out); err != nil {
		return fmt.Errorf("writing cluster-outputs.json: %w", err)
	}
	p, _ := config.WorkspaceClusterOutputsPath(cctx.WorkspaceName)
	fmt.Fprintf(os.Stderr, "✓ Wrote %s\n", p)
	// If the config names an existing Transit Gateway, attach the registered
	// cluster to it now — the registered-cluster path to a shared gateway.
	tryAutoConnectTGW(cmd, cctx)
	return nil
}

func runClusterShow(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	out, err := config.ReadClusterOutputs(cctx.WorkspaceName)
	if err != nil {
		return err
	}

	if flagStatusJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	// Stable, easy-to-grep key:value layout — same shape regardless of
	// terminal width or color settings.
	fmt.Printf("workspace:        %s\n", cctx.WorkspaceName)
	fmt.Printf("source:           %s\n", out.Source)
	fmt.Printf("recorded_at:      %s\n", out.RecordedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Println()
	fmt.Printf("cluster_name:     %s\n", out.ClusterName)
	fmt.Printf("cluster_id:       %s\n", out.ClusterID)
	fmt.Printf("region:           %s\n", out.Region)
	fmt.Printf("resource_group:   %s\n", out.ResourceGroupID)
	fmt.Printf("openshift:        %s\n", out.OpenShiftVersion)
	fmt.Printf("master_url:       %s\n", out.MasterURL)
	fmt.Println()
	fmt.Printf("vpc_id:           %s\n", out.VPCID)
	if out.VPCName != "" {
		fmt.Printf("vpc_name:         %s\n", out.VPCName)
	}
	if out.TransitGatewayID != "" {
		fmt.Printf("transit_gateway:  %s\n", out.TransitGatewayID)
	}
	if out.TransitGatewayName != "" {
		fmt.Printf("transit_gw_name:  %s\n", out.TransitGatewayName)
	}
	fmt.Printf("registry_cos:     %s\n", out.RegistryCOSName)
	fmt.Printf("registry_cos_crn: %s\n", out.RegistryCOSCRN)

	// Transit Gateway connection (the `tgw connect` phase). Shown for BOTH created
	// and registered clusters — this is where a shared/adopted gateway shows up,
	// with its live connection state.
	if tgw, terr := config.ReadTGWOutputs(cctx.WorkspaceName); terr == nil {
		fmt.Println()
		fmt.Printf("tgw_connected:    %s\n", liveTGWConnectionState(ctxWithCmd(cmd), tgw))
		fmt.Printf("tgw_gateway_id:   %s\n", tgw.GatewayID)
		fmt.Printf("tgw_gateway_name: %s\n", tgw.GatewayName)
		fmt.Printf("tgw_connection:   %s\n", tgw.ConnectionName)
	}
	return nil
}

// clusterPhaseOverrideContent — extra tfvars layered on top of the
// user's tfvars during cluster up/down. Forces the BNK trial modules
// off so cluster phase is genuinely "cluster + cluster-shared services
// only," regardless of what the user's tfvars say about deploy_bnk.
const clusterPhaseOverrideContent = `# Generated by roksbnkctl. Do not edit by hand.
# Cluster-phase override: BNK trial modules (flo / cne_instance /
# license) AND cert-manager are skipped here. cert-manager moved to the
# BNK phase (Sprint 27): it is now provider-based (helm_release /
# kubectl_manifest), and those providers cannot obtain cluster credentials
# during cluster CREATION (the cluster doesn't exist yet at plan time, so
# data.ibm_container_cluster_config is count=0 → empty host → the k8s
# resources dial localhost).
#
# Sprint 28 three-phase split: the testing jumphosts ALSO leave the cluster
# phase — they now own their own Testing phase (state-testing/), provisioned
# by "roksbnkctl testing up". Forcing testing_create_* = false here means the
# cluster phase owns ONLY the cluster + cluster VPC + transit gateway +
# registry COS; the shared jumphost SSH key (count-gated on the testing
# toggles) drops to count=0 in the cluster state and lives only in
# state-testing/.
deploy_bnk = false
deploy_cert_manager = false
testing_create_tgw_jumphost = false
testing_create_cluster_jumphosts = false
testing_create_client_vpc = false
`

const clusterPhaseOverrideFile = "cluster-phase-override.tfvars"

// openClusterTF mirrors openTF but uses WorkspaceClusterStateDir and
// emits the cluster-phase override tfvars in that state dir. The
// returned varFiles must be passed to plan/apply/destroy so the
// override actually lands.
func openClusterTF(ctx context.Context) (*config.Context, *tf.Workspace, []string, error) {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return nil, nil, nil, err
	}
	if cctx.Workspace == nil {
		return nil, nil, nil, config.WorkspaceNotReady(cctx.WorkspaceName)
	}
	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolving API key: %w", err)
	}
	stateDir, err := config.WorkspaceClusterStateDir(cctx.WorkspaceName)
	if err != nil {
		return nil, nil, nil, err
	}
	tfws, err := tf.Open(ctx, cctx.WorkspaceName, cctx.Workspace, stateDir, apiKey, os.Stdout, os.Stderr)
	if err != nil {
		return nil, nil, nil, err
	}

	// Write the cluster-phase override tfvars and append it to the
	// var-file chain *after* user var-files so it wins. tf.Workspace's
	// internal varFiles helper already chains config.yaml-tfvars + any
	// terraform.tfvars.user; extraVarFiles are appended after.
	overridePath := filepath.Join(stateDir, clusterPhaseOverrideFile)
	if err := os.WriteFile(overridePath, []byte(clusterPhaseOverrideContent), 0o644); err != nil {
		return nil, nil, nil, fmt.Errorf("writing cluster-phase override: %w", err)
	}
	varFiles := append([]string{}, flagVarFiles...)
	varFiles = append(varFiles, overridePath)
	return cctx, tfws, varFiles, nil
}

// networkModeFor is the mode to RECORD for a cluster being created or adopted.
//
// Always a concrete value, never empty: the contract's reader defaults absence to
// single-nic, but writing the value explicitly means a file this binary produced
// says what it means rather than relying on a default that a later reader might
// change.
// vpcCIDRFor is the address block to RECORD, which is not always the one
// configured: on the adopt path (resources.cluster_vpc.create=false) the setting
// is ignored entirely, and recording it would record a value that never applied
// — then later disagree with itself and warn about a change nobody made.
func vpcCIDRFor(cctx *config.Context) string {
	if cctx == nil || cctx.Workspace == nil {
		return ""
	}
	if r := cctx.Workspace.Resources; r != nil && !r.ClusterVPC.Create {
		return ""
	}
	return strings.TrimSpace(cctx.Workspace.Cluster.VPCCIDR)
}

func networkModeFor(cctx *config.Context) string {
	if cctx == nil || cctx.Workspace == nil {
		return config.NetworkModeSingleNIC
	}
	return cctx.Workspace.ClusterNetworkMode()
}

func runClusterUp(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	// The network mode is fixed at creation. Validate it here, before either the
	// attach path or the terraform create — an unknown value should not reach
	// terraform, and a value that contradicts an existing cluster must not be
	// planned, because terraform would plan a REPLACEMENT of a running cluster
	// rather than a change to it.
	if c, cerr := config.New(flagWorkspace); cerr == nil && c.Workspace != nil {
		out, _ := config.ReadClusterOutputs(c.WorkspaceName) // nil is "no cluster yet"
		if err := config.CheckNetworkMode(c.Workspace, out); err != nil {
			return err
		}
	}

	// Create-or-attach: when the workspace targets an EXISTING cluster
	// (cluster.create=false), `cluster up` ATTACHES to it — the same discovery +
	// cluster-outputs.json write as `cluster register` — instead of running the
	// terraform create. So one command (and one blueprint/CI toggle) covers both
	// "make a new cluster" and "use the one named in config".
	if c, cerr := config.New(flagWorkspace); cerr == nil && c.Workspace != nil && !c.Workspace.Cluster.Create {
		name := c.Workspace.Cluster.Name
		if name == "" {
			return fmt.Errorf("cluster.create is false but cluster.name is empty — set the existing cluster's name/ID (config.yaml cluster.name or ROKSBNKCTL_CLUSTER_NAME)")
		}
		fmt.Fprintf(os.Stderr, "→ cluster.create=false — attaching to existing cluster %q\n", name)
		return runClusterRegister(cmd, []string{name})
	}

	// flagVarFiles is already chokepoint-normalized to absolute paths
	// against the invocation CWD (root PersistentPreRunE →
	// resolveInvocationContext) before openClusterTF folds them into the
	// varFiles slice. No per-RunE re-derivation (Sprint 12 Issue 1).

	cctx, tfws, varFiles, err := openClusterTF(ctx)
	if err != nil {
		return err
	}
	// Refuse BEFORE terraform touches anything if this VPC's address prefixes would
	// overlap one already on the transit gateway it is joining. Cheap to check, and
	// the alternative is expensive: the gateway cannot route to two overlapping VPCs,
	// so it silently blackholes one — presenting as intermittent image-pull timeouts
	// while every security group and ACL in the path allows the traffic (issue #46).
	if err := guardVPCPrefixOverlap(ctx, cctx); err != nil {
		return err
	}
	if err := writeAndInit(ctx, tfws, cctx.Workspace); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "→ terraform plan (cluster phase: deploy_bnk=false forced)")
	changes, err := tfws.Plan(ctx, varFiles...)
	if err != nil {
		return err
	}
	if !changes {
		fmt.Fprintln(os.Stderr, "✓ no changes")
		// Still refresh cluster-outputs.json so a no-op cluster up
		// updates the recorded_at timestamp + catches any drift in
		// cluster identity. Best-effort.
		_ = persistClusterOutputs(ctx, cctx, tfws, "cluster-up")
		tryAutoKubeconfig(ctx, cctx, tfws)
		tryRegisterBNKForge(ctx, cctx)
		tryAutoConnectTGW(cmd, cctx)
		return nil
	}
	if !flagAuto && !promptYesNo("Apply this plan?", false) {
		return errors.New("aborted")
	}

	fmt.Fprintln(os.Stderr, "→ terraform apply")
	if err := applyWithRetry(ctx, tfws, varFiles); err != nil {
		return err
	}

	if err := persistClusterOutputs(ctx, cctx, tfws, "cluster-up"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: apply succeeded but cluster-outputs.json write failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "         (run `roksbnkctl cluster register <name>` to populate it manually)")
	}
	tryAutoKubeconfig(ctx, cctx, tfws)
	tryRegisterBNKForge(ctx, cctx)
	tryAutoConnectTGW(cmd, cctx)
	return nil
}

// tryAutoConnectTGW attaches the cluster VPC to an existing Transit Gateway when
// the config names one (resources.transit_gateway.create=false + existing set) —
// so declining to create a gateway in the interview and pointing at an existing
// one "just connects" on `cluster up` / `cluster register`, no separate command.
// Best-effort: a failure prints how to retry with `tgw connect` and does NOT fail
// the cluster operation, which already succeeded.
func tryAutoConnectTGW(cmd *cobra.Command, cctx *config.Context) {
	ws := cctx.Workspace
	if ws.Resources == nil || ws.Resources.TransitGateway.Create || ws.Resources.TransitGateway.Existing == "" {
		return // creating a gateway, or none named — nothing to attach to
	}
	target := ws.Resources.TransitGateway.Existing
	fmt.Fprintf(os.Stderr, "→ Attaching the cluster VPC to Transit Gateway %q (resources.transit_gateway.existing)\n", target)
	in := lifecycleInputs()
	in.Auto = true // no prompt inside cluster up
	if err := orchestration.RunTGWConnect(ctxWithCmd(cmd), in); err != nil {
		fmt.Fprintf(os.Stderr, "⚠ could not attach to Transit Gateway %q: %v\n", target, err)
		fmt.Fprintf(os.Stderr, "  the cluster is up; retry the attach with `roksbnkctl -w %s tgw connect %s`\n", cctx.WorkspaceName, target)
	}
}

func runClusterDown(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	// flagVarFiles is already chokepoint-normalized (PersistentPreRunE).

	// Presence gating (Sprint 28 §3c): `cluster down` operates strictly on
	// the cluster phase. Hard-refuse while the BNK, Testing, OR Gateway phase
	// still has resources — all reference the cluster VPC/TGW, so destroying
	// the cluster underneath them would orphan them. The guard is a
	// correctness check, NOT a prompt, so --auto does not bypass it.
	cctx0, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	pres, err := config.DetectPresence(cctx0.WorkspaceName)
	if err != nil {
		return fmt.Errorf("detecting workspace presence: %w", err)
	}
	if pres.BNK || pres.Testing || pres.Gateway || pres.FLP || pres.TGW {
		var present []string
		var verbs []string
		if pres.Gateway {
			present = append(present, "Gateway")
			verbs = append(verbs, "`roksbnkctl gateway down`")
		}
		if pres.FLP {
			present = append(present, "FLP")
			verbs = append(verbs, "`roksbnkctl flp down`")
		}
		if pres.TGW {
			present = append(present, "Transit Gateway connection")
			verbs = append(verbs, "`roksbnkctl tgw disconnect`")
		}
		if pres.BNK {
			present = append(present, "BNK")
			verbs = append(verbs, "`roksbnkctl bnk down`")
		}
		if pres.Testing {
			present = append(present, "Testing")
			verbs = append(verbs, "`roksbnkctl testing down`")
		}
		return fmt.Errorf("%s state exists in this workspace; run %s first (or `roksbnkctl down` to tear down all phases)",
			strings.Join(present, " and "), strings.Join(verbs, " and "))
	}
	// Resumable: proceed if the cluster resource is present OR the phase has
	// residual managed resources (network left by a partially-failed destroy).
	if !pres.Cluster && !pres.ClusterResidual {
		return errors.New("nothing to destroy in this workspace")
	}

	// Shared-VPC guard: if this workspace OWNS the cluster VPC and other clusters'
	// subnets still live in it, refuse — the owner must be destroyed LAST, else the
	// VPC delete fails and the shared public gateways are pulled out from under the
	// adopters. Correctness check, not a prompt, so --auto does not bypass it.
	if err := guardSharedVPCTeardown(ctx, cctx0.Workspace, cctx0.WorkspaceName); err != nil {
		return err
	}

	cctx, tfws, varFiles, err := openClusterTF(ctx)
	if err != nil {
		return err
	}
	if !flagAuto {
		fmt.Fprintf(os.Stderr, "This will destroy the cluster phase for workspace %q (ROKS + transit gateway + registry COS + cert-manager + jumphost).\n", cctx.WorkspaceName)
		if !promptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
	}
	if err := writeAndInit(ctx, tfws, cctx.Workspace); err != nil {
		return err
	}

	// Auto-layer the cluster phase's applied-tfvars replay (validator
	// Issue 3, round-3) as the lowest-precedence var-file so bare
	// `cluster down -w <ws>` (no --var-file) destroys cleanly. Returns
	// nil when no snapshot exists → caller behaviour unchanged.
	appliedVF := orchestration.LayerAppliedTFVars(cctx.WorkspaceName, "cluster")
	// Option (b): no snapshot AND no --var-file → actionable error
	// instead of bubbling up terraform's raw missing-required-var lines.
	// `varFiles` here is the cluster-phase chain assembled by
	// openClusterTF (flagVarFiles + cluster-phase-override.tfvars).
	// Strip the architectural override before the gate so the user's
	// explicit --var-file is what counts as "supplied inputs".
	userVF := make([]string, 0, len(varFiles))
	for _, vf := range varFiles {
		if filepath.Base(vf) != "cluster-phase-override.tfvars" {
			userVF = append(userVF, vf)
		}
	}
	if err := orchestration.RequireSnapshotOrVarFile(appliedVF, userVF, tfws.HasUserTFVars(), cctx.Workspace.Prefix != "", "cluster", "cluster down"); err != nil {
		return err
	}
	varFiles = append(append([]string{}, appliedVF...), varFiles...)
	fmt.Fprintln(os.Stderr, "→ terraform destroy (cluster phase)")
	if err := orchestration.DestroyWithRetry(ctx, tfws, varFiles); err != nil {
		return err
	}
	if err := config.DeleteClusterOutputs(cctx.WorkspaceName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: destroy succeeded but cluster-outputs.json removal failed: %v\n", err)
	}
	return nil
}

// persistClusterOutputs reads relevant terraform outputs after a
// cluster apply and writes ~/.roksbnkctl/<workspace>/cluster-outputs.json.
// Falls back to the IBM SDK for fields the upstream root doesn't emit
// (VPC ID, registry COS CRN) — same path roksbnkctl cluster register uses.
func persistClusterOutputs(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, source string) error {
	outputs, err := tfws.Output(ctx)
	if err != nil {
		return fmt.Errorf("reading terraform outputs: %w", err)
	}
	clusterName := stringOutput(outputs, "roks_cluster_name")
	clusterID := stringOutput(outputs, "roks_cluster_id")
	tgwName := stringOutput(outputs, "roks_transit_gateway_name")
	if clusterName == "" && clusterID == "" {
		return errors.New("terraform outputs have neither roks_cluster_name nor roks_cluster_id (cluster module skipped?)")
	}
	identity := clusterID
	if identity == "" {
		identity = clusterName
	}

	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return err
	}
	ic, err := ibm.New(apiKey, cctx.Workspace.IBMCloud.Region)
	if err != nil {
		return err
	}
	info, err := ic.GetCluster(ctx, identity)
	if err != nil {
		return err
	}
	out := &config.ClusterOutputs{
		ClusterName:     info.Name,
		ClusterID:       info.ID,
		Region:          info.Region,
		ResourceGroupID: info.ResourceGroupID,
		VPCID:           info.VPCID(),
		// Sprint 28: the Testing phase looks the TGW up by NAME, so carry
		// it in the handoff (roks_transit_gateway_name root output). Empty
		// on flows that don't emit it (the testing phase then falls back to
		// the config.yaml-rendered testing_transit_gateway_name).
		TransitGatewayName: tgwName,
		MasterURL:          info.MasterURL,
		OpenShiftVersion:   info.MasterKubeVersion,
		Source:             source,
		// The mode the cluster was BUILT with. Recorded here because this is the
		// only moment it is knowable and settled — afterwards the contract is the
		// authority, and the config is just a request that has to agree with it.
		NetworkMode: networkModeFor(cctx),
		VPCCIDR:     vpcCIDRFor(cctx),
	}
	// Registry COS: prefer the terraform outputs (deterministic when this phase
	// created the instance) and fall back to a name-guess SDK lookup for an
	// existing/reused COS. Best-effort — a miss doesn't fail the parent up.
	out.RegistryCOSName = stringOutput(outputs, "registry_cos_name")
	out.RegistryCOSCRN = stringOutput(outputs, "registry_cos_crn")
	if out.RegistryCOSCRN == "" {
		for _, n := range []string{info.Name + "-cos-instance", info.Name + "-cos"} {
			if cos, lookupErr := ic.GetCOSInstanceByName(ctx, n); lookupErr == nil {
				out.RegistryCOSName = cos.Name
				out.RegistryCOSCRN = cos.CRN
				break
			}
		}
	}
	return config.WriteClusterOutputs(cctx.WorkspaceName, out)
}

// stringOutput extracts a string-typed terraform output. Returns "" for
// missing or non-string values — callers shape behavior on emptiness.
func stringOutput(outputs map[string]tfexec.OutputMeta, key string) string {
	v, ok := outputs[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v.Value, &s); err != nil {
		return ""
	}
	return s
}

// mapOutput extracts a string-keyed, string-valued terraform output
// (e.g. {zone => fip}). Returns nil for a missing output, an unmarshal
// failure, or the `[]`-default JSON terraform emits for an empty map
// (`value = try(..., [])` renders `[]`, not `{}`, when the resource set
// is empty). Callers treat a nil/empty map as "nothing to do" — the
// same emptiness-shaping contract as stringOutput. Mirrors the
// json.Unmarshal model used by stringOutput / resolveClusterIdentity.
func mapOutput(outputs map[string]tfexec.OutputMeta, key string) map[string]string {
	v, ok := outputs[key]
	if !ok || len(v.Value) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(v.Value, &m); err != nil {
		// `[]` (terraform's empty-map default here) and any non-object
		// JSON fail to unmarshal into map[string]string — treat as
		// "no entries", exactly like the `ip == ""` guard.
		return nil
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// promptForCluster asks the user for a cluster name/id when one wasn't
// passed on the command line. Returns def unchanged if stdin isn't a
// TTY — non-interactive invocations without an arg surface as a clean
// "required" error in the caller rather than hanging.
func promptForCluster(def string) (string, error) {
	if !isTTY() {
		if def != "" {
			return def, nil
		}
		return "", errors.New("cluster name/id required (positional arg) — stdin is not a terminal so we can't prompt")
	}
	val := promptString("Cluster name or ID", def)
	if val == "" {
		return "", errors.New("no cluster name/id provided")
	}
	return val, nil
}
