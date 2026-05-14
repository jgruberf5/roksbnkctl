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

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

var (
	flagFollow        bool
	flagLogsNamespace string
	flagLogsContainer string
	flagLogsPrevious  bool
	flagLogsSince     string
	flagLogsTailLines int64
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Summary of the workspace: cluster, components, last apply",
	Long: `roksbnkctl status reports a quick read of the workspace:

  - workspace name + region
  - configured cluster name
  - pinned Terraform source
  - per-phase deployment status (cluster phase + BNK trial)
  - v1.0.x ` + "`Last apply`" + ` line preserved for legacy single-state workspaces
  - kubeconfig path (if any)
  - cluster reachability (node count + ready count)

v1.x will add per-BNK-component readiness (flo, cis, cert-manager,
cneinstance) once the component-discovery shape is finalised.`,
	RunE: runStatus,
}

var logsCmd = &cobra.Command{
	Use:   "logs <component>",
	Short: "Tail logs for a BNK component (flo, cis, cert-manager, cneinstance)",
	Long: `Looks up the named BNK component, finds its pod(s) by label, and
streams logs to stdout. With --follow, streams live. With multiple
matching pods, tails the first and prints a hint about using
roksbnkctl kubectl for per-pod selection.

The component → namespace/selector map is hardcoded for v1 against the
upstream TF chart's default labels; if your install renamed namespaces
or relabelled, fall back to:

  roksbnkctl kubectl logs -n <ns> <pod>`,
	Args: cobra.ExactArgs(1),
	RunE: runLogs,
}

func init() {
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
		fmt.Fprintln(tw, "Status:\t(not initialised — run `roksbnkctl init`)")
		return nil
	}

	fmt.Fprintf(tw, "Region:\t%s\n", or(cctx.Workspace.IBMCloud.Region, "(unset)"))
	fmt.Fprintf(tw, "Resource group:\t%s\n", or(cctx.Workspace.IBMCloud.ResourceGroup, "(unset)"))
	fmt.Fprintf(tw, "Cluster:\t%s\t%s\n", or(cctx.Workspace.Cluster.Name, "(unset)"), createOrAttach(cctx.Workspace.Cluster.Create))
	fmt.Fprintf(tw, "TF source:\t%s\n", tfSourceDescription(cctx.Workspace.TFSource))

	// PRD 06 §"`status` command integration" (Sprint 10): consume
	// `config.DetectShape` and emit per-phase deployment lines for non-
	// Legacy shapes. Legacy preserves the v1.0.x `Last apply` line
	// verbatim plus a one-line shape callout for script-compat. Best-
	// effort by convention — a DetectShape error or unreadable state
	// file degrades to "not deployed" rather than failing the command.
	writeStatusPhaseLines(tw, cctx.WorkspaceName)

	// Kubeconfig + cluster reachability.
	kcPath := k8s.DefaultKubeconfigPath()
	if kcPath == "" {
		fmt.Fprintln(tw, "Kubeconfig:\t(none — run `roksbnkctl kubeconfig --download`)")
		return nil
	}
	fmt.Fprintf(tw, "Kubeconfig:\t%s\n", kcPath)

	// Flush so the cluster check can stream its own line cleanly after.
	tw.Flush()

	clusterStatus := probeCluster(cmd.Context(), kcPath)
	fmt.Fprintf(os.Stdout, "Cluster:        %s\n", clusterStatus)
	return nil
}

// writeStatusPhaseLines emits the per-shape deployment lines for the
// `status` command per PRD 06 §"`status` command integration"
// (Sprint 10 scope addition). Output by shape:
//
//	ShapeEmpty        — "Cluster phase: not deployed" + "BNK trial: not deployed"
//	ShapeClusterOnly  — cluster phase with mtime; trial "not deployed"
//	ShapeSplit        — both phases with their own mtimes
//	ShapeLegacySingle — one-line shape callout + the v1.0.x "Last apply"
//	                    line verbatim from `state/terraform.tfstate` mtime
//	                    (script-compat for v1.0.x parsers)
//	ShapeUnknown      — falls back to the v1.0.x "Last apply" line so a
//	                    DetectShape error never blocks status output
//
// All filesystem failures are silenced — every section of `runStatus`
// is best-effort.
func writeStatusPhaseLines(tw io.Writer, workspace string) {
	shape, err := config.DetectShape(workspace)
	if err != nil {
		// Malformed state files etc. — fall through to v1.0.x line so
		// the user gets *some* signal rather than a hard failure here.
		writeLegacyLastApply(tw, workspace)
		return
	}

	trialDir, _ := config.WorkspaceStateDir(workspace)
	clusterDir, _ := config.WorkspaceClusterStateDir(workspace)
	trialState := filepath.Join(trialDir, "terraform.tfstate")
	clusterState := filepath.Join(clusterDir, "terraform.tfstate")

	switch shape {
	case config.ShapeEmpty:
		fmt.Fprintln(tw, "Cluster phase:\tnot deployed")
		fmt.Fprintln(tw, "BNK trial:\tnot deployed")

	case config.ShapeClusterOnly:
		fmt.Fprintf(tw, "Cluster phase:\t%s\n", deployedLine(clusterState))
		fmt.Fprintln(tw, "BNK trial:\tnot deployed")

	case config.ShapeSplit:
		fmt.Fprintf(tw, "Cluster phase:\t%s\n", deployedLine(clusterState))
		fmt.Fprintf(tw, "BNK trial:\t%s\n", deployedLine(trialState))

	case config.ShapeLegacySingle:
		// One-line callout so the reader sees "legacy" at a glance,
		// plus the verbatim v1.0.x `Last apply` line for script-compat.
		fmt.Fprintln(tw, "Shape:\tlegacy single-state (cluster + trial in one tfstate)")
		writeLegacyLastApply(tw, workspace)

	default:
		// ShapeUnknown should be unreachable on a successful DetectShape
		// but handle defensively: surface the v1.0.x shape so nothing
		// downstream parses a missing line.
		writeLegacyLastApply(tw, workspace)
	}
}

// deployedLine returns the `deployed (last apply <timestamp>)` shape
// for a per-phase line, reading the mtime of `statePath`. Falls back
// to `not deployed` when the file isn't readable — keeps the per-shape
// output honest in the face of partial state.
func deployedLine(statePath string) string {
	info, err := os.Stat(statePath)
	if err != nil {
		return "not deployed"
	}
	return fmt.Sprintf("deployed (last apply %s)", info.ModTime().Format("2006-01-02 15:04:05 MST"))
}

// writeLegacyLastApply emits the verbatim v1.0.x `Last apply` line from
// `state/terraform.tfstate` mtime. Used both for `ShapeLegacySingle`
// (per PRD 06 §"`status` command integration" — script-compat preservation)
// and as a defensive fallback for `ShapeUnknown` / `DetectShape` errors.
func writeLegacyLastApply(tw io.Writer, workspace string) {
	stateDir, _ := config.WorkspaceStateDir(workspace)
	statePath := filepath.Join(stateDir, "terraform.tfstate")
	if info, err := os.Stat(statePath); err == nil {
		age := time.Since(info.ModTime()).Round(time.Second)
		fmt.Fprintf(tw, "Last apply:\t%s\t(%s ago)\n", info.ModTime().Format("2006-01-02 15:04:05 MST"), age)
	} else {
		fmt.Fprintln(tw, "Last apply:\t(no state — run `roksbnkctl up`)")
	}
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

func tfSourceDescription(s config.TFSourceCfg) string {
	switch s.Type {
	case "github":
		return fmt.Sprintf("%s@%s", s.Repo, s.Ref)
	case "local":
		return "local:" + s.Path
	default:
		return "(unset)"
	}
}

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
		// (kubectl-style) — same as `roksbnkctl k logs <pod>`. This
		// is the v0.8 shortcut so `roksbnkctl logs my-pod` works
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
		return fmt.Errorf("no %s pods found in namespace %s with selector %s — chart label may have changed; try `roksbnkctl k get pods -A | grep <name>`",
			component, ns, comp.Selector)
	}
	pod := &pods.Items[0]
	if len(pods.Items) > 1 {
		fmt.Fprintf(os.Stderr, "→ %d %s pods found; tailing %s (use `roksbnkctl k logs -n %s <pod>` for a specific one)\n",
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
