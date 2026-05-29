package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	awspkg "github.com/JLCode-tech/awsbnkctl/internal/aws"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/phases"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/config"
	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	"github.com/JLCode-tech/awsbnkctl/internal/ui"
)

var (
	flagAuto         bool
	flagNoKubeconfig bool

	// flagUpDryRun is bound ONLY to upCmd's --dry-run.
	flagUpDryRun bool

	// flagDownDryRun is bound ONLY to downCmd's --dry-run.
	flagDownDryRun bool

	// flagRegisterWithForge wires the P2 auto-handoff: after a
	// successful `awsbnkctl up`, register the resulting EKS cluster
	// with a running bnk-forge instance over MCP. No-op in dry-run.
	// Equivalent to running `awsbnkctl forge register` post-apply,
	// but bundled so operators don't have to remember the second
	// command.
	flagRegisterWithForge bool

	// flagConfig activates the Go-SDK phased path when set.
	// When empty, up/down return a clear error directing the user to
	// supply --config <cluster.yaml>.
	flagConfig string

	// flagYes skips the interactive "type 'destroy' to proceed" prompt
	// in `awsbnkctl down --config <file>`. Equivalent to the --yes/-y
	// flag in aws-gpu-setup/down.sh.
	flagYes bool

	// flagKeepForgeLink is bound ONLY to downCmd (single-owner per the
	// cobra shared-flag-variable anti-pattern rules above). When true,
	// Phase09ForgeRegisterDown skips forge unregister and preserves
	// forge-link.json so the operator can manage the forge project manually.
	// Default false (unregister on down).
	flagKeepForgeLink bool

	// flagKeepIRSA is bound ONLY to downCmd. When true, Phase18IrsaOidcDown
	// skips deletion of the OIDC provider and retains the IRSA role ARN in
	// state so a subsequent `up` can reuse the same OIDC provider. The IRSA
	// role itself is always deleted (it must be recreated with the new cluster's
	// federated trust policy). Default false (delete OIDC provider on down).
	flagKeepIRSA bool

	// flagSkipActivationPoll is bound ONLY to upCmd. When true, Phase25 returns
	// immediately after logging intent, skipping the 20-min CNEInstance+License
	// poll. Designed for reviewers who need to re-run up without re-burning a
	// real F5 license activation each round. Default false (always poll).
	flagSkipActivationPoll bool

	// flagDemo is bound ONLY to upCmd. When true, the cluster is marked as a demo
	// deployment: DEMO_MODE, DEMO_STAGED_AT, and DEMO_EXPIRY are written to
	// state.env before the provisioning phase graph. Sugar for demo.enabled: true
	// in cluster.yaml — the provisioning phases are unchanged. Requires
	// testing.jumphost.enabled: true (validated in runPhasedUp).
	flagDemo bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive AWS setup; collects region + VPC + subnets + FAR archive + JWT, writes the workspace config (PRD 08).",
	Long: `awsbnkctl init walks through the AWS-shaped prompts (region, VPC, subnets,
cluster name, FAR archive path, subscription JWT path, FLO namespace) and writes
the workspace config.yaml under ~/.awsbnkctl/<workspace>/. The supply-chain
artefacts are uploaded to S3 by 'awsbnkctl up', not by init directly — see
PRD 08 § "Open questions" for the rationale.

Use --dry-run to walk the wizard offline (no AWS API calls; useful for
populating a workspace ahead of a real apply).`,
	RunE: runInit,
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Provision the EKS cluster + BNK stack via Go-SDK phased path (requires --config <cluster.yaml>)",
	Long: `awsbnkctl up drives the full end-to-end provisioning graph via the
Go-SDK phased path. Requires --config <cluster.yaml>.

  awsbnkctl up --config cluster.yaml [--dry-run]

When --dry-run is set, the phase functions print planned actions but make
zero AWS API mutations.`,
	RunE: runUp,
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Destroy everything provisioned by 'awsbnkctl up' (requires --config <cluster.yaml>)",
	RunE:  runDown,
}

func init() {
	upCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the confirmation prompt before apply")
	upCmd.Flags().BoolVar(&flagNoKubeconfig, "no-kubeconfig", false, "skip the post-apply admin kubeconfig fetch")
	upCmd.Flags().BoolVar(&flagUpDryRun, "dry-run", false, "print the phased plan and exit 0 with no AWS mutations")
	upCmd.Flags().BoolVar(&flagRegisterWithForge, "register-with-forge", false, "after a successful apply, register the EKS cluster with bnk-forge over MCP (no-op in --dry-run)")
	upCmd.Flags().StringVarP(&flagConfig, "config", "f", "", "path to cluster.yaml (required)")
	upCmd.Flags().BoolVar(&flagSkipActivationPoll, "skip-activation-poll", false, "skip the 20-min CNEInstance+License activation poll (for reviewer re-runs that must not re-burn the F5 license)")
	upCmd.Flags().BoolVar(&flagDemo, "demo", false, "provision the same cluster, marked as a demo (writes DEMO_MODE, requires testing.jumphost.enabled)")

	downCmd.Flags().BoolVar(&flagAuto, "auto", false, "skip the destroy confirmation")
	downCmd.Flags().BoolVar(&flagDownDryRun, "dry-run", false, "print what would be destroyed, make no AWS mutations")
	downCmd.Flags().StringVarP(&flagConfig, "config", "f", "", "path to cluster.yaml (required)")
	downCmd.Flags().BoolVar(&flagYes, "yes", false, "skip the interactive destroy confirmation (required with --config)")
	downCmd.Flags().BoolVar(&flagKeepForgeLink, "keep-forge-link", false, "preserve forge-link.json on down (skips Phase 09 forge unregister)")
	downCmd.Flags().BoolVar(&flagKeepIRSA, "keep-irsa", false, "retain the OIDC provider and IRSA role on down (both are kept for reuse across cluster iterations)")

	rootCmd.AddCommand(initCmd, upCmd, downCmd)
}

// runUp wires `awsbnkctl up` — requires --config <cluster.yaml>.
func runUp(cmd *cobra.Command, _ []string) error {
	if flagConfig == "" {
		return errors.New("awsbnkctl up requires --config <cluster.yaml>")
	}
	return runPhasedUp(cmd.Context(), flagConfig, flagUpDryRun, flagSkipActivationPoll, flagDemo)
}

// registerWithForgePostApply runs the same flow as `awsbnkctl forge
// register` — used by `awsbnkctl up --register-with-forge`. Pulled out
// of runUp so the dry-run / live-apply branching stays readable, and
// so it can be unit-tested independently of the lifecycle path.
func registerWithForgePostApply(ctx context.Context) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return fmt.Errorf("forge register after apply: %w", err)
	}
	wsDir, err := config.WorkspaceDir(cctx.WorkspaceName)
	if err != nil {
		return fmt.Errorf("forge register after apply: resolving workspace dir: %w", err)
	}

	clusterName := cctx.Workspace.Cluster.Name
	if clusterName == "" {
		return fmt.Errorf("forge register after apply: workspace cluster.name is empty")
	}
	region := cctx.Workspace.AWS.Region
	if region == "" {
		return fmt.Errorf("forge register after apply: workspace AWS.region is empty")
	}

	// Generate the kubeconfig in-process via the EKS presigned-URL
	// flow. Matches what `awsbnkctl forge register` does without a
	// --kubeconfig override.
	clients, err := awspkg.NewClients(ctx, awspkg.Options{
		Region:  region,
		Profile: cctx.Workspace.AWS.Profile,
	})
	if err != nil {
		return fmt.Errorf("forge register after apply: aws clients: %w", err)
	}
	ci, err := clients.DescribeCluster(ctx, clusterName)
	if err != nil {
		return fmt.Errorf("forge register after apply: eks describe-cluster %s: %w", clusterName, err)
	}
	yaml, err := clients.KubeconfigFromCluster(ci)
	if err != nil {
		return fmt.Errorf("forge register after apply: generate kubeconfig: %w", err)
	}

	fc := forge.NewClient("")
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "→ forge MCP: %s\n", fc.URL())
	}

	regCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	res, err := forge.Register(regCtx, fc, forge.RegisterRequest{
		WorkspaceName: cctx.WorkspaceName,
		WorkspaceDir:  wsDir,
		ClusterName:   clusterName,
		Region:        region,
		Kubeconfig:    []byte(yaml),
	})
	if err != nil {
		return fmt.Errorf("forge register after apply: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ registered with forge (project_id=%d cluster_id=%d)\n",
		res.Link.ProjectID, res.Link.ClusterID)
	return nil
}

func runDown(cmd *cobra.Command, _ []string) error {
	if flagConfig == "" {
		return errors.New("awsbnkctl down requires --config <cluster.yaml>")
	}
	return runPhasedDown(cmd.Context(), flagConfig, flagYes, flagDownDryRun)
}

// runPhasedUp is the Go-SDK phased provisioning path activated by
// `awsbnkctl up --config <file>`. It reads the cluster.yaml intent,
// constructs AWS clients with the SSO sentinel middleware, then runs
// phases 00 → 25 in order.
//
// When dryRun is true the phase functions print planned actions but make
// zero AWS API mutations.
// When skipActivationPoll is true, Phase 25 returns immediately (for reviewers
// who must not re-burn a real F5 license each round).
// When demo is true (or demo.enabled: true in cluster.yaml), the cluster is
// marked as a demo: DEMO_MODE, DEMO_STAGED_AT, and DEMO_EXPIRY are written to
// state.env before the phase graph. The provisioning phases are unchanged.
func runPhasedUp(ctx context.Context, configPath string, dryRun bool, skipActivationPoll bool, demo bool) error {
	cl, err := intent.Load(configPath)
	if err != nil {
		return fmt.Errorf("up: %w", err)
	}

	// --demo flag forces demo.enabled=true even when the demo: block is absent.
	// EnableDemo is idempotent: safe to call when already enabled by cluster.yaml.
	if demo {
		cl.EnableDemo()
	}
	// For the flag path, ValidateDemo was NOT called inside intent.Load (the flag
	// is not visible to validate()). Call it now — both paths enforce the same rules.
	if cl.DemoEnabled() {
		if err := intent.ValidateDemo(cl); err != nil {
			return fmt.Errorf("up: %w", err)
		}
	}

	clients, err := phases.NewClients(ctx, cl.Metadata.Region, "")
	if err != nil {
		return fmt.Errorf("up: aws clients: %w", err)
	}
	// Attach forge client when forge is enabled in cluster.yaml.
	if cl.Forge != nil {
		clients.AttachForgeClient(cl.Forge.Enabled, cl.Forge.MCPURL)
	}

	stateDir := cl.StateDir()
	st, err := state.Load(stateDir)
	if err != nil {
		return fmt.Errorf("up: loading state: %w", err)
	}

	if dryRun {
		// Read-only state: phases Set placeholder IDs in-memory for their plan
		// output, but nothing persists to the real state.env on disk. Prevents
		// dry-run placeholders ("dry-run-subnet-...") from polluting a real
		// cluster's state and later breaking `down`. See state.MarkReadOnly.
		st.MarkReadOnly()
		fmt.Fprintln(os.Stderr, "→ dry-run: printing plan, no AWS mutations will be made")
	}

	// Demo markers: write DEMO_MODE, DEMO_STAGED_AT, DEMO_EXPIRY early (before the
	// phase graph) so a partially-failed up still records the cluster as a demo.
	// A normal (non-demo) up writes none of these keys and is byte-for-byte unchanged.
	if cl.DemoEnabled() {
		ttl, _ := time.ParseDuration(cl.Demo.TTL) // already validated above
		now := time.Now().UTC()
		st.Set("DEMO_MODE", "true")
		st.Set("DEMO_STAGED_AT", now.Format(time.RFC3339))
		st.Set("DEMO_EXPIRY", now.Add(ttl).Format(time.RFC3339))
		// Inject demo tags into cl.Tags so every phase's tags.Merge carries
		// awsbnkctl:demo=true and awsbnkctl:demo-expiry=<RFC3339> onto every
		// created AWS resource. The expiry value matches DEMO_EXPIRY above.
		cl.SetDemoTags(now.Add(ttl))
		if !dryRun {
			if err := st.Save(); err != nil {
				return fmt.Errorf("up: writing demo markers to state: %w", err)
			}
		}
		fmt.Fprintf(os.Stderr, "→ demo mode: DEMO_MODE=true TTL=%s DEMO_EXPIRY=%s\n",
			cl.Demo.TTL, st.Get("DEMO_EXPIRY"))
	}

	// Construct the launch renderer. RocketRenderer is returned only when
	// demo && IsTerminal(stderr) && !noColor; otherwise PlainRenderer (no-op).
	rdr := ui.NewRenderer(os.Stderr, cl.Metadata.Name, cl.DemoEnabled(), flagNoColor)
	rdr.Start([]ui.Stage{
		{Num: 1, Label: "VPC · subnets · IGW · NAT", PhaseRange: "[Phase 00–07]"},
		{Num: 2, Label: "EKS control plane", PhaseRange: "[Phase 08–08b]"},
		{Num: 3, Label: "Nodes · kubeconfig · ENIs · jumphost", PhaseRange: "[Phase 10–18]"},
		{Num: 4, Label: "BNK supply chain · activation", PhaseRange: "[Phase 11b–25]"},
	})

	// stage wraps a single phase call with PhaseBegin/PhaseEnd events and
	// preserves the existing fmt.Errorf("up: %w", err) wrapping on failure.
	stage := func(num int, name string, fn func() error) error {
		rdr.PhaseBegin(num, name)
		err := fn()
		rdr.PhaseEnd(num, name, err)
		if err != nil {
			rdr.Finish(err)
			return fmt.Errorf("up: %w", err)
		}
		return nil
	}

	if err := stage(1, "preflight", func() error {
		return phases.Phase00Preflight(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(1, "vpc", func() error {
		return phases.Phase02VPC(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(1, "subnets", func() error {
		return phases.Phase03Subnets(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(1, "igw", func() error {
		return phases.Phase04IGW(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(1, "nat", func() error {
		return phases.Phase05NAT(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(1, "route-tables", func() error {
		return phases.Phase06RouteTables(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(1, "iam", func() error {
		return phases.Phase07IAM(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(2, "eks-cluster", func() error {
		return phases.Phase08EKSCluster(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(2, "forge-register", func() error {
		return phases.Phase09ForgeRegister(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 08b: vpc-cni prefix delegation BEFORE the node group so nodes boot in
	// prefix mode (CNI stays on the primary ENI; no secondary ENI → no cross-node
	// asymmetric-drop on a secondary ENI, which previously hung BNK licensing).
	if err := stage(2, "vpc-cni-prefix", func() error {
		return phases.Phase08bVPCCNIPrefix(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(3, "node-group", func() error {
		return phases.Phase10NodeGroup(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(3, "kubeconfig", func() error {
		return phases.Phase11Kubeconfig(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}

	// Phase 16: label the TMM-target node + resolve EC2 instance ID.
	// Requires K8s client (attached below for dryRun=false); dry-run
	// path is handled inside Phase16TMMNodeLabel.
	if !dryRun {
		kubeconfigPath := st.Get("KUBECONFIG_PATH")
		if kubeconfigPath == "" {
			return fmt.Errorf("up: KUBECONFIG_PATH not in state after phase 11")
		}
		if err := clients.AttachK8s(kubeconfigPath); err != nil {
			return fmt.Errorf("up: attaching k8s clients: %w", err)
		}
	}
	if err := stage(3, "tmm-node-label", func() error {
		return phases.Phase16TMMNodeLabel(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(3, "secondary-enis", func() error {
		return phases.Phase17SecondaryENIs(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(3, "jumphost", func() error {
		return phases.Phase17bJumphost(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(3, "iface-discovery", func() error {
		return phases.Phase17cIfaceDiscovery(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 17d: demo client staging — pre-stages grpcurl + diameter assets on the
	// jumphost over EICE. Gated on DemoEnabled(); normal/CI up is byte-for-byte
	// unchanged. Runs after 17c so the 10.0.10.x data-path ENI is attached + up.
	if cl.DemoEnabled() {
		if err := stage(3, "demo-stage", func() error {
			return phases.Phase17dDemoStage(ctx, cl, st, clients, dryRun)
		}); err != nil {
			return err
		}
	}
	if err := stage(3, "irsa-oidc", func() error {
		return phases.Phase18IRSAOIDC(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}

	// Phase 11b (slice 8): EBS CSI managed addon + gp3 StorageClass + hugepages-ds.
	// Runs after Phase 18 (IRSA) so it has node-role IAM in place AND k8s clients
	// attached, but BEFORE Phase 12 (k8s foundation) since cert-manager etc. don't
	// depend on CSI/hugepages. Naming "11b" preserves slice-7 numbering identity.
	if err := stage(4, "ebs-csi-hugepages", func() error {
		return phases.Phase11bEBSCSIHugepages(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}

	if err := stage(4, "k8s-foundation", func() error {
		return phases.Phase12K8sFoundation(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(4, "flo-helm", func() error {
		return phases.Phase14FLOHelm(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	if err := stage(4, "otel-certs", func() error {
		return phases.Phase15OTELCerts(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 19: cloud-network-mapping ConfigMap (required by cne-controller pre-CNEInstance).
	if err := stage(4, "cloud-network-mapping", func() error {
		return phases.Phase19CloudNetworkMapping(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 20: host-device NADs in f5-cne-system + default (required by CNEInstance webhook).
	if err := stage(4, "nads", func() error {
		return phases.Phase20NADs(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 21: IRSA ServiceAccount pre-creation with eks.amazonaws.com/role-arn annotation.
	if err := stage(4, "irsa-sa", func() error {
		return phases.Phase21IRSASA(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 22: CNEInstance CR apply + reconcile-started gate (2 min).
	if err := stage(4, "cne-instance", func() error {
		return phases.Phase22CNEInstance(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 23: License CRD wait + License CR apply.
	if err := stage(4, "license", func() error {
		return phases.Phase23License(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 23b: F5SPKVlan + GatewayClass for host-device pattern.
	// Skipped silently when pattern != host-device. Completes TMM data-plane
	// plumbing — binds trunks 1.1 / 1.2 to ext-vlan / int-vlan inside the
	// TMM pod netns and announces SelfIPs assigned by Phase 17.
	if err := stage(4, "spk-vlan-gateway-class", func() error {
		return phases.Phase23bSPKVlanGatewayClass(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 24: CWC DNS-warmup heal (best-effort; never returns error).
	if err := stage(4, "cwc-heal", func() error {
		return phases.Phase24CWCHeal(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 24b: DSSM --insecure readiness probe overlay (host-device only).
	// Patches the FLO-created f5-dssm ConfigMap to add --insecure to redis-cli
	// --tls invocations, then bounces dssm pods. Fixes dssm-db-1 replica startup
	// probe failure (redis-cli 8.6.0 strict TLS hostname check vs 127.0.0.1 probe).
	// Mirrors aws-gpu-setup/deploy-bnk.sh:263-282. Idempotent.
	if err := stage(4, "dssm-overlay", func() error {
		return phases.Phase24bDSSMInsecureOverlay(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 24c: f5-tmm-pod-manager cold-start race heal (best-effort).
	// Targets Finding #4 from docs/audits/2026-05-24-live-e2e-round-2-findings.md:
	// pod-manager v1.6.x times out hitting the EKS API ClusterIP before
	// kube-proxy converges on a cold node; restart-once breaks the loop.
	if err := stage(4, "pod-manager-heal", func() error {
		return phases.Phase24cPodManagerHeal(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	// Phase 25: Activation poll — CNEInstance + License status (up to 20 min).
	// skipActivationPoll is set by --skip-activation-poll for reviewer re-runs.
	if err := stage(4, "activation-poll", func() error {
		return phases.Phase25ActivationPoll(ctx, cl, st, clients, dryRun, skipActivationPoll)
	}); err != nil {
		return err
	}
	// Phase 13 postflight runs LAST so it can verify FLO + OTEL + activation state.
	if err := stage(4, "postflight", func() error {
		return phases.Phase13Postflight(ctx, cl, st, clients, dryRun)
	}); err != nil {
		return err
	}
	rdr.Finish(nil)

	if dryRun {
		fmt.Fprintln(os.Stderr, "→ dry-run complete")
	} else {
		fmt.Fprintf(os.Stderr, "✓ up complete: cluster=%s state=%s\n", cl.Metadata.Name, stateDir)
	}

	// P2: auto-register with forge over MCP after a successful apply.
	// Dry-run skips because forge.Register needs a real EKS cluster to
	// describe + generate a kubeconfig for — and there isn't one yet.
	if flagRegisterWithForge && !dryRun {
		return registerWithForgePostApply(ctx)
	}
	return nil
}

// runPhasedDown is the Go-SDK phased destroy path activated by
// `awsbnkctl down --config <file>`. It reads the cluster.yaml intent,
// loads the IDs cache (with tag-discovery fallback), then destroys
// resources in reverse phase order.
//
// When yes is false the operator is prompted to type 'destroy' to proceed.
//
// When dryRun is true the destroy plan is printed from state and the
// function returns WITHOUT invoking any Phase*Down — guaranteeing zero AWS
// mutations. This is a single leak-proof guard rather than per-phase dryRun
// branching: down phases issue Delete calls with varied control flow, so
// gating each one independently risks one slipping through and deleting for
// real (which is exactly the bug this guard replaces). The confirmation
// prompt is also skipped because a dry-run changes nothing to confirm.
func runPhasedDown(ctx context.Context, configPath string, yes bool, dryRun bool) error {
	cl, err := intent.Load(configPath)
	if err != nil {
		return fmt.Errorf("down: %w", err)
	}

	clients, err := phases.NewClients(ctx, cl.Metadata.Region, "")
	if err != nil {
		return fmt.Errorf("down: aws clients: %w", err)
	}
	// Attach forge client when forge is enabled in cluster.yaml.
	if cl.Forge != nil {
		clients.AttachForgeClient(cl.Forge.Enabled, cl.Forge.MCPURL)
	}

	stateDir := cl.StateDir()
	st, err := state.Load(stateDir)
	if err != nil {
		return fmt.Errorf("down: loading state: %w", err)
	}

	if dryRun {
		printDownPlan(os.Stderr, cl, st, flagKeepIRSA, flagKeepForgeLink)
		return nil
	}

	if !yes {
		fmt.Fprintf(os.Stderr, "About to DESTROY cluster %q in %s.\n", cl.Metadata.Name, cl.Metadata.Region)
		fmt.Fprintln(os.Stderr, "Type 'destroy' to proceed:")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() && scanner.Text() != "destroy" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	// Reverse phase order: 15 → 14 → 12 → 11 → 10 → 09 → 08 → 07 → 06 → 05 → 04 → 03 → 02.
	// Phase 15/14/12 k8s teardown runs FIRST (while kubeconfig is still valid).
	// Attach k8s clients using the kubeconfig path from state before k8s down phases.
	kubeconfigPath := st.Get("KUBECONFIG_PATH")
	if kubeconfigPath != "" {
		if err := clients.AttachK8s(kubeconfigPath); err != nil {
			// Log and continue — kubeconfig may be absent if phase 11 never ran.
			fmt.Fprintf(os.Stderr, "down: warning: could not attach k8s clients (%v) — phase 12/14/15 down will log warning and skip\n", err)
		}
	}
	// Demo use-case teardown — runs before infra teardown while kubeconfig is
	// still valid. Gated on DEMO_MODE=true in state (not cl.DemoEnabled() —
	// the --demo flag is not persisted to cluster.yaml so cl.Demo is nil on down).
	// See runDemoCleanDown for idempotency contract.
	if err := runDemoCleanDown(ctx, cl, st); err != nil {
		// runDemoCleanDown already logs per-use-case errors and returns nil;
		// this branch is a safety net for unexpected errors.
		fmt.Fprintf(os.Stderr, "down: demo-clean: unexpected error: %v\n", err)
	}
	if err := phases.Phase15OTELCertsDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	// Phase 25/24c/24b/24/23b/23/22: activation teardown (reverse of up order).
	if err := phases.Phase25ActivationPollDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase24cPodManagerHealDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase24bDSSMInsecureOverlayDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase24CWCHealDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase23bSPKVlanGatewayClassDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase23LicenseDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase22CNEInstanceDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	// Phase 21/20/19: k8s BNK prerequisites teardown (reverse of up order).
	if err := phases.Phase21IRSASADown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase20NADsDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase19CloudNetworkMappingDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase14FLOHelmDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase12K8sFoundationDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	// Phase 11b (slice 8): EBS CSI addon + gp3 SC + hugepages-ds teardown.
	// Runs after Phase 12 down (cert-manager + multus removed) but before
	// Phase 11 kubeconfig down (still needs k8s client + EKS API access).
	if err := phases.Phase11bEBSCSIHugepagesDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase11KubeconfigDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	// Phase 18/17/16: AWS-side BNK teardown (ENIs, OIDC/IRSA, node label).
	// Runs after kubeconfig down (TMM node already gone with node group teardown).
	if err := phases.Phase18IrsaOidcDown(ctx, cl, st, clients, flagKeepIRSA); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase17cIfaceDiscoveryDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase17bJumphostDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase17SecondaryENIsDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase16TMMNodeLabelDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase10NodeGroupDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase09ForgeRegisterDown(ctx, cl, st, clients, flagKeepForgeLink); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase08bVPCCNIPrefixDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase08EKSClusterDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase07IAMDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase06RouteTablesDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase05NATDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase04IGWDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase03SubnetsDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	if err := phases.Phase02VPCDown(ctx, cl, st, clients); err != nil {
		return fmt.Errorf("down: %w", err)
	}

	fmt.Fprintf(os.Stderr, "✓ down complete: cluster=%s\n", cl.Metadata.Name)
	return nil
}

// printDownPlan renders the destroy plan for `awsbnkctl down --dry-run`.
// It enumerates the resources recorded in state, in the same reverse-phase
// order runPhasedDown tears them down, printing only the entries that are
// actually present. It makes NO AWS calls and NO state writes — it is the
// read-only half of the down flow.
//
// keepIRSA / keepForgeLink mirror the live-down flags so the plan reflects
// what would actually be retained.
func printDownPlan(w io.Writer, cl *intent.Cluster, st *state.State, keepIRSA, keepForgeLink bool) {
	fmt.Fprintf(w, "→ dry-run: destroy plan for cluster %q in %s (no AWS mutations)\n",
		cl.Metadata.Name, cl.Metadata.Region)

	type entry struct {
		label string
		key   string // state key; value printed when non-empty
	}
	// Reverse-phase order, matching runPhasedDown's teardown sequence.
	entries := []entry{
		{label: "demo use-cases", key: "DEMO_MODE"},
		{label: "OTEL server cert", key: "OTEL_SVR_CERT_NAME"},
		{label: "OTEL f5ing cert", key: "OTEL_F5ING_CERT_NAME"},
		{label: "GatewayClass + F5SPKVlan", key: "GATEWAYCLASS_NAME"},
		{label: "License CR", key: "LICENSE_NAME"},
		{label: "CNEInstance CR", key: "CNEINSTANCE_NAME"},
		{label: "IRSA ServiceAccount", key: "CNE_SA_NAME"},
		{label: "internal NAD", key: "INTERNAL_NAD"},
		{label: "external NAD", key: "EXTERNAL_NAD"},
		{label: "FLO helm release", key: "FLO_RELEASE_NAME"},
		{label: "EBS CSI addon", key: "EBS_CSI_ADDON_STATUS"},
		{label: "kubeconfig (local file)", key: "KUBECONFIG_PATH"},
		{label: "OIDC provider", key: "OIDC_PROVIDER_ARN"},
		{label: "CNE IRSA role", key: "CNE_IRSA_ROLE_NAME"},
		{label: "jumphost instance", key: "JUMPHOST_INSTANCE_ID"},
		{label: "jumphost IAM role", key: "JUMPHOST_ROLE_NAME"},
		{label: "jumphost instance profile", key: "JUMPHOST_INSTANCE_PROFILE_NAME"},
		{label: "jumphost EICE", key: "JUMPHOST_EICE_ID"},
		{label: "jumphost SG", key: "JUMPHOST_SG_ID"},
		{label: "jumphost EICE SG", key: "JUMPHOST_EICE_SG_ID"},
		{label: "jumphost mgmt ENI", key: "JUMPHOST_MGMT_ENI_ID"},
		{label: "jumphost data-path ENI", key: "JUMPHOST_BNK_EXT_ENI_ID"},
		{label: "external secondary ENI", key: "EXTERNAL_ENI"},
		{label: "internal secondary ENI", key: "INTERNAL_ENI"},
		{label: "node group", key: "NODEGROUP_DEFAULT_NAME"},
		{label: "launch template", key: "LT_ID"},
		{label: "forge registration", key: "FORGE_CLUSTER_ID"},
		{label: "EKS cluster", key: "EKS_CLUSTER_NAME"},
		{label: "EKS node role", key: "EKS_NODE_ROLE_ARN"},
		{label: "EKS cluster role", key: "EKS_CLUSTER_ROLE_ARN"},
		{label: "node instance profile", key: "NODE_INSTANCE_PROFILE_NAME"},
		{label: "BNK data SG", key: "SG_BNK_DATA"},
		{label: "public route table", key: "PUBLIC_RTB"},
		{label: "private route table", key: "PRIVATE_RTB"},
		{label: "NAT gateway", key: "NAT_GW_ID"},
		{label: "NAT Elastic IP", key: "NAT_EIP_ALLOC"},
		{label: "internet gateway", key: "IGW_ID"},
		{label: "public subnets", key: "PUBLIC_SUBNETS"},
		{label: "private subnets", key: "PRIVATE_SUBNETS"},
		{label: "BNK external subnet", key: "BNK_EXT_SUBNET"},
		{label: "BNK internal subnet", key: "BNK_INT_SUBNET"},
		{label: "VPC", key: "VPC_ID"},
	}

	n := 0
	for _, e := range entries {
		val := st.Get(e.key)
		if val == "" {
			continue
		}
		// Retention flags: show the resource is kept, not deleted.
		if keepIRSA && (e.key == "OIDC_PROVIDER_ARN" || e.key == "CNE_IRSA_ROLE_NAME") {
			fmt.Fprintf(w, "  • retain  %-28s %s (--keep-irsa)\n", e.label, val)
			continue
		}
		if keepForgeLink && e.key == "FORGE_CLUSTER_ID" {
			fmt.Fprintf(w, "  • retain  %-28s id=%s (--keep-forge-link)\n", e.label, val)
			continue
		}
		fmt.Fprintf(w, "  • delete  %-28s %s\n", e.label, val)
		n++
	}

	if n == 0 {
		fmt.Fprintln(w, "  (state records no live resources — nothing to destroy)")
	}
	fmt.Fprintf(w, "→ dry-run complete: %d resource(s) would be destroyed\n", n)
}

// runDemoCleanDown invokes the Cleanup hook for every registered demo use-case.
// It is called during runPhasedDown BEFORE infra teardown, while the kubeconfig
// is still valid. Three idempotency guards ensure AC #6 is satisfied:
//
//  1. DEMO_MODE gate — skips the whole function when the cluster is not a demo.
//     (Uses state, not cl.DemoEnabled() — the --demo flag is not persisted to
//     cluster.yaml, so cl.Demo is nil on the down path.)
//  2. Zero-use-case early return — C0 ships no real use-cases; must succeed.
//  3. NewContext / Cleanup failures are logged and tolerated — kubeconfig may
//     already be gone on a partial teardown; absent namespaces are not errors
//     (scenarios.Cleanup contract: missing namespace == no-op).
func runDemoCleanDown(ctx context.Context, cl *intent.Cluster, st *state.State) error {
	if st.Get("DEMO_MODE") != "true" {
		return nil // not a demo cluster; nothing to clean
	}
	ucs := demo.All()
	if len(ucs) == 0 {
		return nil // C0: no use-cases registered — clean success
	}

	kube := cl.StateDir() + "/kubeconfig"
	sctx, err := scenarios.NewContext(ctx, kube, cl, st, os.Stderr, false, nil)
	if err != nil {
		// kubeconfig may be absent on a re-run / partial teardown — warn, don't fail.
		fmt.Fprintf(os.Stderr, "down: demo-clean: skipping (no kube context: %v)\n", err)
		return nil
	}

	for _, uc := range ucs {
		fmt.Fprintf(os.Stderr, "down: demo-clean: %s\n", uc.Name())
		if err := scenarios.Cleanup(sctx, uc); err != nil {
			// One use-case's error must NOT abort the rest or the down sequence.
			fmt.Fprintf(os.Stderr, "down: demo-clean: %s: warn: %v\n", uc.Name(), err)
		}
	}
	return nil
}
