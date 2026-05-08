package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksctl/internal/config"
	"github.com/jgruberf5/roksctl/internal/ibm"
	"github.com/jgruberf5/roksctl/internal/tf"
)

// `roksctl cluster ...` is the cluster-phase command group: durable,
// per-workspace ROKS cluster lifecycle that's decoupled from BNK trial
// runs. Subcommands write/read ~/.roksctl/<workspace>/cluster-outputs.json
// so a single cluster can host many BNK trials with different tfvars
// over its lifetime.
var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "ROKS cluster lifecycle (separate from BNK trials)",
	Long: `Manage the ROKS cluster as a durable, reusable resource that
sits underneath your BNK trials.

Commands:
  roksctl cluster up        Create the ROKS cluster (+ transit gateway, registry COS, cert-manager, jumphost)
  roksctl cluster down      Destroy the cluster and everything cluster-scoped
  roksctl cluster register  Discover an already-existing cluster and persist its identity
  roksctl cluster show      Print the registered cluster from cluster-outputs.json

Each ` + "`roksctl up`" + ` against this workspace will reuse the registered
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
identity to ~/.roksctl/<workspace>/cluster-outputs.json.

Subsequent ` + "`roksctl up`" + ` runs in this workspace will pick up the
registered cluster automatically — no need to repeat its identity in
trial tfvars.

By default the registry COS instance name follows the upstream HCL
fallback formula "<cluster-name>-cos". Pass --registry-cos-name to
override (e.g. if your tfvars sets roks_cos_instance_name to a different
value).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runClusterRegister,
}

var clusterShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the registered cluster (cluster-outputs.json)",
	RunE:  runClusterShow,
}

var clusterUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Provision the ROKS cluster (and cluster-shared services) only",
	Long: `Runs terraform apply with deploy_bnk=false forced — creates the
ROKS cluster, transit gateway, registry COS, cert-manager, and the test
jumphost, but skips the BNK trial modules (flo, cne_instance, license).
On success, writes the cluster's identity to
~/.roksctl/<workspace>/cluster-outputs.json so subsequent ` + "`roksctl up`" + `
runs can deploy BNK trials onto this cluster.

Uses a separate state directory (~/.roksctl/<workspace>/state-cluster/)
so it doesn't tangle with BNK-trial state.`,
	RunE: runClusterUp,
}

var clusterDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy the cluster phase (ROKS + cluster-shared services)",
	Long: `Tears down everything roksctl cluster up created. Refuses to run
if any BNK trial state exists for this workspace — destroy those first
with ` + "`roksctl down`" + ` to avoid orphaned BNK resources.`,
	RunE: runClusterDown,
}

func init() {
	clusterRegisterCmd.Flags().StringVar(&flagClusterRegisterCOSName, "registry-cos-name", "",
		`expected registry COS instance name (default "<cluster>-cos" — matches the upstream HCL fallback)`)
	clusterRegisterCmd.Flags().BoolVar(&flagClusterRegisterPrompt, "prompt", false,
		"prompt for the cluster name even if one is given as an argument")

	// up/down share the same lifecycle flags as `roksctl up`/`down` so users
	// only have one mental model. Reuses the package-level flag vars.
	clusterUpCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt before apply")
	clusterUpCmd.Flags().BoolVar(&flagNoKubeconfig, "no-kubeconfig", false, "skip the post-apply admin kubeconfig fetch")
	clusterUpCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")
	clusterDownCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")
	clusterDownCmd.Flags().StringArrayVar(&flagVarFiles, "var-file", nil, "extra TF var-file (repeatable; later files override earlier)")

	clusterCmd.AddCommand(clusterRegisterCmd, clusterShowCmd, clusterUpCmd, clusterDownCmd)
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
		return fmt.Errorf("cluster %q has no VPC — roksctl only supports vpc-gen2 clusters", info.Name)
	}
	fmt.Fprintf(os.Stderr, "✓ VPC %s (resource group %s)\n", vpc, info.ResourceGroupName)

	// 2. Registry COS instance verification. Default name follows the
	//    upstream HCL fallback (`<cluster>-cos`); user can override via
	//    --registry-cos-name to match a tfvars override of
	//    roks_cos_instance_name.
	cosName := flagClusterRegisterCOSName
	if cosName == "" {
		cosName = info.Name + "-cos"
	}
	fmt.Fprintf(os.Stderr, "→ Verifying registry COS instance %q\n", cosName)
	cos, err := ic.GetCOSInstanceByName(ctx, cosName)
	if err != nil {
		return fmt.Errorf("registry COS instance %q not found in account: %w\n  Either run `roksctl cluster up` to create it, or pass --registry-cos-name <name> if your tfvars uses a different roks_cos_instance_name",
			cosName, err)
	}
	fmt.Fprintf(os.Stderr, "✓ COS instance %s (%s)\n", cos.Name, cos.GUID)

	// 3. Persist. Source = "cluster-register" so consumers can tell
	//    discovered-vs-created.
	out := &config.ClusterOutputs{
		ClusterName:       info.Name,
		ClusterID:         info.ID,
		Region:            info.Region,
		ResourceGroupID:   info.ResourceGroupID,
		VPCID:             vpc,
		RegistryCOSCRN:    cos.CRN,
		RegistryCOSName:   cos.Name,
		MasterURL:         info.MasterURL,
		OpenShiftVersion:  info.MasterKubeVersion,
		Source:            "cluster-register",
	}
	if err := config.WriteClusterOutputs(cctx.WorkspaceName, out); err != nil {
		return fmt.Errorf("writing cluster-outputs.json: %w", err)
	}
	p, _ := config.WorkspaceClusterOutputsPath(cctx.WorkspaceName)
	fmt.Fprintf(os.Stderr, "✓ Wrote %s\n", p)
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
	fmt.Printf("registry_cos:     %s\n", out.RegistryCOSName)
	fmt.Printf("registry_cos_crn: %s\n", out.RegistryCOSCRN)
	return nil
}

// clusterPhaseOverrideContent — extra tfvars layered on top of the
// user's tfvars during cluster up/down. Forces the BNK trial modules
// off so cluster phase is genuinely "cluster + cluster-shared services
// only," regardless of what the user's tfvars say about deploy_bnk.
const clusterPhaseOverrideContent = `# Generated by roksctl. Do not edit by hand.
# Cluster-phase override: BNK trial modules (flo / cne_instance /
# license) are skipped. cert-manager and the testing jumphost still run
# — they're cluster-shared singletons that belong with the cluster.
deploy_bnk = false
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
		return nil, nil, nil, fmt.Errorf("workspace %q is not initialised; run `roksctl init` first", cctx.WorkspaceName)
	}
	apiKey, err := config.ResolveAPIKey(cctx.WorkspaceName, cctx.Workspace.IBMCloud.APIKeySource)
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

func runClusterUp(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	cctx, tfws, varFiles, err := openClusterTF(ctx)
	if err != nil {
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
		fmt.Fprintln(os.Stderr, "         (run `roksctl cluster register <name>` to populate it manually)")
	}
	tryAutoKubeconfig(ctx, cctx, tfws)
	return nil
}

func runClusterDown(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	cctx, tfws, varFiles, err := openClusterTF(ctx)
	if err != nil {
		return err
	}
	if !flagAuto {
		fmt.Fprintf(os.Stderr, "This will destroy the cluster phase for workspace %q (ROKS + transit gateway + registry COS + cert-manager + jumphost).\n", cctx.WorkspaceName)
		fmt.Fprintln(os.Stderr, "Any BNK trial state on top of this cluster will be orphaned — run `roksctl down` first if needed.")
		if !promptYesNo("Continue?", false) {
			return errors.New("aborted")
		}
	}
	if err := writeAndInit(ctx, tfws, cctx.Workspace); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "→ terraform destroy (cluster phase)")
	if err := tfws.Destroy(ctx, varFiles...); err != nil {
		return err
	}
	if err := config.DeleteClusterOutputs(cctx.WorkspaceName); err != nil {
		fmt.Fprintf(os.Stderr, "warning: destroy succeeded but cluster-outputs.json removal failed: %v\n", err)
	}
	return nil
}

// persistClusterOutputs reads relevant terraform outputs after a
// cluster apply and writes ~/.roksctl/<workspace>/cluster-outputs.json.
// Falls back to the IBM SDK for fields the upstream root doesn't emit
// (VPC ID, registry COS CRN) — same path roksctl cluster register uses.
func persistClusterOutputs(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, source string) error {
	outputs, err := tfws.Output(ctx)
	if err != nil {
		return fmt.Errorf("reading terraform outputs: %w", err)
	}
	clusterName := stringOutput(outputs, "roks_cluster_name")
	clusterID := stringOutput(outputs, "roks_cluster_id")
	if clusterName == "" && clusterID == "" {
		return errors.New("terraform outputs have neither roks_cluster_name nor roks_cluster_id (cluster module skipped?)")
	}
	identity := clusterID
	if identity == "" {
		identity = clusterName
	}

	apiKey, err := config.ResolveAPIKey(cctx.WorkspaceName, cctx.Workspace.IBMCloud.APIKeySource)
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
		ClusterName:      info.Name,
		ClusterID:        info.ID,
		Region:           info.Region,
		ResourceGroupID:  info.ResourceGroupID,
		VPCID:            info.VPCID(),
		MasterURL:        info.MasterURL,
		OpenShiftVersion: info.MasterKubeVersion,
		Source:           source,
	}
	// Registry COS lookup uses the same name-derivation rule the user's
	// tfvars implies: try config.Workspace's cluster.name + "-cos" or
	// "-cos-instance" (the two patterns we see in practice). Best-effort —
	// failure here doesn't fail the parent up.
	cosCandidates := []string{
		info.Name + "-cos-instance",
		info.Name + "-cos",
	}
	for _, n := range cosCandidates {
		if cos, lookupErr := ic.GetCOSInstanceByName(ctx, n); lookupErr == nil {
			out.RegistryCOSName = cos.Name
			out.RegistryCOSCRN = cos.CRN
			break
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
