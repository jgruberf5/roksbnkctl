package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/config"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s"
)

var (
	flagFollow        bool
	flagLogsNamespace string
	flagLogsContainer string
	flagLogsPrevious  bool
	flagLogsSince     string
	flagLogsTailLines int64

	// flagStatusConfig points status at a cluster.yaml so it can locate
	// the AWS-SDK phased path's state.env IDs cache
	// (.awsbnkctl/<metadata.name>/state.env). When unset, status falls
	// back to the current workspace's cluster name for a best-effort
	// lookup under the working directory.
	flagStatusConfig string
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Summary of the workspace: cluster, components, deploy state",
	Long: `awsbnkctl status reports a quick read of the workspace:

  - workspace name + region
  - configured cluster name
  - deploy state read from the AWS-SDK phased path's state.env IDs cache
    (.awsbnkctl/<cluster>/state.env): VPC, EKS cluster, node group,
    TMM node / jumphost, BNK activation, forge link, last phase applied
  - kubeconfig path (if any)
  - cluster reachability (node count + ready count)

Pass --config <cluster.yaml> to point status at a specific cluster's
state.env. Without it, status uses the current workspace's cluster name
and looks for .awsbnkctl/<name>/state.env under the working directory.

Every section is best-effort: a missing or unreadable state.env degrades
to "not deployed" rather than failing the command.`,
	RunE: runStatus,
}

var logsCmd = &cobra.Command{
	Use:   "logs <component>",
	Short: "Tail logs for a BNK component (flo, cis, cert-manager, cneinstance)",
	Long: `Looks up the named BNK component, finds its pod(s) by label, and
streams logs to stdout. With --follow, streams live. With multiple
matching pods, tails the first and prints a hint about using
awsbnkctl kubectl for per-pod selection.

The component → namespace/selector map is hardcoded for v1 against the
upstream TF chart's default labels; if your install renamed namespaces
or relabelled, fall back to:

  awsbnkctl kubectl logs -n <ns> <pod>`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

func init() {
	statusCmd.Flags().StringVarP(&flagStatusConfig, "config", "f", "", "path to cluster.yaml (locates the phased path's state.env; defaults to the workspace cluster name)")
	logsCmd.Flags().BoolVarP(&flagFollow, "follow", "f", false, "follow log output")
	logsCmd.Flags().StringVarP(&flagLogsNamespace, "namespace", "n", "", "override the component's default namespace")
	logsCmd.Flags().StringVarP(&flagLogsContainer, "container", "c", "", "container name in a multi-container pod")
	logsCmd.Flags().BoolVar(&flagLogsPrevious, "previous", false, "fetch logs from the previous container instance")
	logsCmd.Flags().StringVar(&flagLogsSince, "since", "", "only return logs newer than this duration (e.g. 5s, 2m, 1h)")
	logsCmd.Flags().Int64Var(&flagLogsTailLines, "tail", -1, "tail the last N lines (-1 = full log)")
	rootCmd.AddCommand(statusCmd, logsCmd)
}

// runStatus prints a human-readable workspace summary. Always best-effort
// — every section reports its own missing pieces so a partial state
// (no cluster reachable, no state file yet, etc.) still produces useful
// output rather than a hard error.
func runStatus(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer tw.Flush()

	fmt.Fprintf(tw, "Workspace:\t%s\n", cctx.WorkspaceName)
	if cctx.Workspace == nil {
		fmt.Fprintln(tw, "Status:\t(not initialised — run `awsbnkctl init`)")
		return nil
	}

	// AWS is the only first-class cloud block (PRD 04 retarget).
	region := cctx.Workspace.AWS.Region
	fmt.Fprintf(tw, "Region:\t%s\n", or(region, "(unset)"))
	if cctx.Workspace.AWS.Profile != "" {
		fmt.Fprintf(tw, "AWS profile:\t%s\n", cctx.Workspace.AWS.Profile)
	}
	fmt.Fprintf(tw, "Cluster:\t%s\t%s\n", or(cctx.Workspace.Cluster.Name, "(unset)"), createOrAttach(cctx.Workspace.Cluster.Create))

	// Deploy state, read from the AWS-SDK phased path's state.env IDs
	// cache (per D-001…D-007). Locate it via the
	// cluster name — from --config <cluster.yaml> if given, else from the
	// workspace's configured cluster name — and read
	// .awsbnkctl/<name>/state.env under the working directory. Best-effort
	// by convention: a missing / unreadable / empty state.env degrades to
	// "not deployed" rather than failing the command.
	st, _ := loadStatusState(flagStatusConfig, cctx.Workspace.Cluster.Name)
	writeStatusDeployStateFromState(tw, st)

	// Kubeconfig + cluster reachability.
	// Prefer KUBECONFIG_PATH from state.env (written by Phase 11 during `up`)
	// over the host's default kubeconfig, so `status --config <cluster.yaml>`
	// always reports the targeted cluster rather than whatever kube-context
	// happens to be active on the host.
	kcPath := resolveKubeconfigForStatus(st)
	if kcPath == "" {
		fmt.Fprintln(tw, "Kubeconfig:\t(none — run `awsbnkctl kubeconfig --download`)")
		return nil
	}
	fmt.Fprintf(tw, "Kubeconfig:\t%s\n", kcPath)

	// Flush so the cluster check can stream its own line cleanly after.
	// Flush returns os.Stderr/Stdout-style errors which we can't usefully
	// recover from here; ignore so the cluster check still runs.
	_ = tw.Flush()

	clusterStatus := probeCluster(cmd.Context(), kcPath)
	fmt.Fprintf(os.Stdout, "Cluster:        %s\n", clusterStatus)
	return nil
}

// writeStatusDeployStateFromState emits the deploy-state lines for `status`
// from an already-loaded *state.State. When st is nil (state not found or
// unreadable) it emits a single "no state" hint line.
//
// Best-effort by convention — every section of `runStatus` is best-effort.
// A missing cluster name, an unloadable cluster.yaml, or an unreadable /
// malformed state.env all degrade to a single "not deployed" line rather than
// a hard error. The state.env is shell-sourceable KEY=VALUE
// (see internal/aws/state); empty values mean "not provisioned" because the
// phase code clears IDs on `down`.
func writeStatusDeployStateFromState(tw io.Writer, st *state.State) {
	if st == nil {
		fmt.Fprintln(tw, "Deploy state:\t(no state — run `awsbnkctl up --config <cluster.yaml>`)")
		return
	}

	fmt.Fprintf(tw, "VPC:\t%s\n", orNotProvisioned(st.Get("VPC_ID")))
	fmt.Fprintf(tw, "EKS cluster:\t%s\n", orNotProvisioned(st.Get("EKS_CLUSTER_NAME")))
	fmt.Fprintf(tw, "Node group:\t%s\n", orNotProvisioned(st.Get("NODEGROUP_DEFAULT_NAME")))
	fmt.Fprintf(tw, "TMM node:\t%s\n", tmmNodeLine(st))
	fmt.Fprintf(tw, "Jumphost:\t%s\n", orNotProvisioned(st.Get("JUMPHOST_INSTANCE_ID")))
	fmt.Fprintf(tw, "BNK activation:\t%s\n", bnkActivationLine(st))
	fmt.Fprintf(tw, "Forge:\t%s\n", forgeLine(st))
	fmt.Fprintf(tw, "Last phase applied:\t%s\n", lastPhaseAppliedLine(st))
}

// resolveKubeconfigForStatus returns the kubeconfig path to use for the status
// command's cluster probe. It prefers KUBECONFIG_PATH from the loaded state
// (written by Phase 11 during `awsbnkctl up`) so that `status --config
// <cluster.yaml>` always probes the targeted cluster, not the host's current
// kube-context. Falls back to k8s.DefaultKubeconfigPath() when state is nil
// or KUBECONFIG_PATH is absent/stale.
//
// The path stored in state.env is cwd-relative (written relative to where
// `up` ran). If the stored path doesn't exist as-is, it is also tried
// relative to the state directory.
func resolveKubeconfigForStatus(st *state.State) string {
	if st != nil {
		if stored := st.Get("KUBECONFIG_PATH"); stored != "" {
			if _, err := os.Stat(stored); err == nil {
				return stored
			}
			// Try relative to the state dir (e.g. .awsbnkctl/<name>/kubeconfig).
			candidate := filepath.Join(st.Dir(), filepath.Base(stored))
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return k8s.DefaultKubeconfigPath()
}

// resolveKubeconfigFromConfig derives a kubeconfig path from a cluster.yaml
// path by loading the cluster's state.env and reading KUBECONFIG_PATH.
// Returns ("", nil) when configPath is empty. Returns an error only when the
// config file itself cannot be loaded; a missing/absent KUBECONFIG_PATH in
// state is not an error — the caller falls back to its own default.
//
// This is the shared helper used by both `status` (Bug 1) and `bnk resync
// --config` (Bug 2) so both commands resolve the kubeconfig the same way.
func resolveKubeconfigFromConfig(configPath string) (string, error) {
	if configPath == "" {
		return "", nil
	}
	cl, err := intent.Load(configPath)
	if err != nil {
		return "", fmt.Errorf("loading --config %s: %w", configPath, err)
	}
	st, err := state.Load(cl.StateDir())
	if err != nil {
		// state.Load returns an error only on malformed lines; treat as empty.
		return "", nil
	}
	stored := st.Get("KUBECONFIG_PATH")
	if stored == "" {
		return "", nil
	}
	if _, err := os.Stat(stored); err == nil {
		return stored, nil
	}
	// Try relative to the state dir.
	candidate := filepath.Join(cl.StateDir(), filepath.Base(stored))
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", nil
}

// loadStatusState resolves the cluster name then loads its state.env.
// Returns (state, true) only when a non-empty state.env was found; a
// missing file, missing cluster name, or unloadable cluster.yaml return
// (nil, false) so the caller emits the "no state" line.
func loadStatusState(configPath, fallbackClusterName string) (*state.State, bool) {
	var stateDir string
	switch {
	case configPath != "":
		cl, err := intent.Load(configPath)
		if err != nil || cl.Metadata.Name == "" {
			return nil, false
		}
		stateDir = cl.StateDir()
	case fallbackClusterName != "":
		// Mirror intent.Cluster.StateDir()'s layout
		// (.awsbnkctl/<name>) relative to the working directory.
		stateDir = filepath.Join(".awsbnkctl", fallbackClusterName)
	default:
		return nil, false
	}

	// state.Load returns an empty State (no error) when the file is
	// absent, and an error only on a malformed line. Treat both the
	// error case and the "file absent / nothing populated" case as
	// "not deployed".
	st, err := state.Load(stateDir)
	if err != nil {
		return nil, false
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, "state.env")); statErr != nil {
		return nil, false
	}
	return st, true
}

// tmmNodeLine reports the TMM data-plane node: prefer the node name,
// fall back to the instance ID, else "not provisioned".
func tmmNodeLine(st *state.State) string {
	if name := st.Get("TMM_NODE_NAME"); name != "" {
		return name
	}
	return orNotProvisioned(st.Get("TMM_INSTANCE_ID"))
}

// bnkActivationLine reports BNK activation. "Active" once the CNEInstance
// reached Ready and the license CR landed; "activating" when the license
// is applied but the instance hasn't reported Ready yet; else
// "not activated".
func bnkActivationLine(st *state.State) string {
	ready := st.Get("CNEINSTANCE_READY_AT")
	licenseApplied := st.Get("LICENSE_APPLIED_AT") != "" || st.Get("LICENSE_NAME") != ""
	switch {
	case ready != "" && licenseApplied:
		return fmt.Sprintf("Active (license %s, ready %s)", orUnnamed(st.Get("LICENSE_NAME")), ready)
	case ready != "":
		return fmt.Sprintf("Active (ready %s)", ready)
	case licenseApplied:
		return fmt.Sprintf("activating (license %s applied, CNEInstance not yet Ready)", orUnnamed(st.Get("LICENSE_NAME")))
	default:
		return "not activated"
	}
}

// forgeLine reports the forge link from FORGE_STATUS / FORGE_PROJECT_ID.
func forgeLine(st *state.State) string {
	status := st.Get("FORGE_STATUS")
	project := st.Get("FORGE_PROJECT_ID")
	switch {
	case status == "" && project == "":
		return "not linked"
	case project != "":
		return fmt.Sprintf("%s (project %s)", orUnknown(status), project)
	default:
		return orUnknown(status)
	}
}

// statusTimestampKeys are the state.env keys that record a phase
// timestamp. lastPhaseAppliedLine scans these for the most recent
// RFC3339 value and reports it as the deploy "high-water mark".
var statusTimestampKeys = []string{
	"CLOUD_NETWORK_MAPPING_APPLIED_AT",
	"IRSA_SA_APPLIED_AT",
	"NADS_APPLIED_AT",
	"IFACE_DISCOVERY_AT",
	"F5SPKVLAN_APPLIED_AT",
	"FLO_INSTALLED_AT",
	"HUGEPAGES_DS_INSTALLED_AT",
	"DSSM_INSECURE_OVERLAY_APPLIED_AT",
	"LICENSE_CRD_READY_AT",
	"LICENSE_APPLIED_AT",
	"CNEINSTANCE_APPLIED_AT",
	"CNEINSTANCE_RECONCILE_STARTED_AT",
	"CNEINSTANCE_READY_AT",
}

// lastPhaseAppliedLine finds the most-recent parseable RFC3339 timestamp
// across statusTimestampKeys and reports it as "<KEY> at <time>". Values
// that aren't RFC3339 (e.g. the "dry-run" sentinel) are skipped. When no
// timestamp is present it reports the "no state" hint.
func lastPhaseAppliedLine(st *state.State) string {
	var bestKey string
	var bestTime time.Time
	for _, k := range statusTimestampKeys {
		v := st.Get(k)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			continue
		}
		if t.After(bestTime) {
			bestTime = t
			bestKey = k
		}
	}
	if bestKey == "" {
		return "(no phase timestamps yet — run `awsbnkctl up --config <cluster.yaml>`)"
	}
	return fmt.Sprintf("%s at %s", bestKey, bestTime.Format("2006-01-02 15:04:05 MST"))
}

// probeCluster does a single timed call to list nodes and summarises
// what it found. Never returns an error — a failed probe shows up as
// the cluster-status string.
func probeCluster(ctx context.Context, kubeconfigPath string) string {
	kc, err := k8s.NewFromKubeconfigFile(kubeconfigPath)
	if err != nil {
		return fmt.Sprintf("(unreachable: %v)", err)
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	nodes, err := kc.Clientset().CoreV1().Nodes().List(tctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Sprintf("(unreachable: %v)", err)
	}
	ready := 0
	for i := range nodes.Items {
		if nodeReady(&nodes.Items[i]) {
			ready++
		}
	}
	if len(nodes.Items) == 0 {
		return "0 nodes (unusual — check cluster)"
	}
	return fmt.Sprintf("%d/%d nodes ready", ready, len(nodes.Items))
}

func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// ── small formatters ─────────────────────────────────────────────────

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func createOrAttach(create bool) string {
	if create {
		return "(create new)"
	}
	return "(attach existing)"
}

func orNotProvisioned(s string) string { return or(s, "not provisioned") }

func orUnnamed(s string) string { return or(s, "unnamed") }

func orUnknown(s string) string { return or(s, "unknown") }

// ── logs ─────────────────────────────────────────────────────────────

// bnkComponent is one row of the hardcoded component → namespace/selector
// map. Hardcoded for v1 to match the upstream TF chart's default labels.
// When BNK chart releases rename labels these need updating.
type bnkComponent struct {
	Name     string
	Desc     string
	Ns       string
	Selector string
}

var bnkComponents = []bnkComponent{
	{"flo", "F5 Lifecycle Operator", "f5-bnk", "app.kubernetes.io/name=f5-lifecycle-operator"},
	{"cis", "F5 BNK CIS controller", "f5-bnk", "app=f5-bnk-cis"},
	{"cert-manager", "cert-manager", "cert-manager", "app.kubernetes.io/instance=cert-manager"},
	{"cneinstance", "BIG-IP TMM data plane (CNEInstance pods)", "f5-bnk", "app.kubernetes.io/component=tmm"},
}

func runLogs(cmd *cobra.Command, args []string) error {
	component := args[0]
	comp := lookupComponent(component)
	if comp == nil {
		// Not a known component. Fall through to the raw pod-name path
		// (kubectl-style) — same as `awsbnkctl k logs <pod>`. This
		// is the v0.8 shortcut so `awsbnkctl logs my-pod` works
		// without users having to know the `k` prefix.
		since, err := k8s.ParseSinceDuration(flagLogsSince)
		if err != nil {
			return err
		}
		opts := &k8s.LogsOptions{
			PodName:      component,
			Namespace:    flagLogsNamespace,
			Container:    flagLogsContainer,
			Follow:       flagFollow,
			Previous:     flagLogsPrevious,
			SinceSeconds: since,
			TailLines:    flagLogsTailLines,
			IOStreams: genericiooptions.IOStreams{
				In:     os.Stdin,
				Out:    os.Stdout,
				ErrOut: os.Stderr,
			},
		}
		if err := opts.Run(cmd.Context()); err != nil {
			// If the pod-name path also fails with NotFound, surface a
			// clearer "not a component AND not a pod" message that
			// nudges toward `-A` or the component list.
			names := make([]string, 0, len(bnkComponents))
			for _, c := range bnkComponents {
				names = append(names, c.Name)
			}
			return fmt.Errorf("%w (also not a known BNK component: %s)",
				err, strings.Join(names, ", "))
		}
		return nil
	}

	kc, err := k8s.NewFromDefault()
	if err != nil {
		return err
	}

	ns := comp.Ns
	if flagLogsNamespace != "" {
		ns = flagLogsNamespace
	}

	ctx := cmd.Context()
	pods, err := kc.Clientset().CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: comp.Selector,
	})
	if err != nil {
		return fmt.Errorf("listing %s pods in %s: %w", component, ns, err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no %s pods found in namespace %s with selector %s — chart label may have changed; try `awsbnkctl k get pods -A | grep <name>`",
			component, ns, comp.Selector)
	}
	pod := &pods.Items[0]
	if len(pods.Items) > 1 {
		fmt.Fprintf(os.Stderr, "→ %d %s pods found; tailing %s (use `awsbnkctl k logs -n %s <pod>` for a specific one)\n",
			len(pods.Items), component, pod.Name, pod.Namespace)
	} else {
		fmt.Fprintf(os.Stderr, "→ Tailing logs from %s/%s\n", pod.Namespace, pod.Name)
	}

	since, err := k8s.ParseSinceDuration(flagLogsSince)
	if err != nil {
		return err
	}
	logOpts := &corev1.PodLogOptions{
		Container: flagLogsContainer,
		Follow:    flagFollow,
		Previous:  flagLogsPrevious,
	}
	if since > 0 {
		logOpts.SinceSeconds = &since
	}
	if flagLogsTailLines >= 0 {
		t := flagLogsTailLines
		logOpts.TailLines = &t
	}

	req := kc.Clientset().CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, logOpts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("opening log stream: %w", err)
	}
	defer stream.Close()
	_, err = io.Copy(os.Stdout, stream)
	return err
}

func lookupComponent(name string) *bnkComponent {
	for i := range bnkComponents {
		if bnkComponents[i].Name == name {
			return &bnkComponents[i]
		}
	}
	return nil
}
