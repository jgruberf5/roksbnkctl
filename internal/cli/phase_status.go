package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

// flagStatusJSON backs the --json flag shared by every phase `status` command
// (only one runs per invocation, so a single var is fine). With it set, each
// status command emits the phaseStatus envelope as indented JSON, making the
// commands usable as CI stage gates.
var flagStatusJSON bool

// phaseStatus is the CI-friendly envelope every `<phase> status` emits. Outputs
// come from the phase's terraform.tfstate (sensitive values redacted); Probe is
// a best-effort live readiness check.
type phaseStatus struct {
	Phase    string            `json:"phase"`
	Deployed bool              `json:"deployed"`
	Outputs  map[string]any    `json:"outputs,omitempty"`
	Probe    map[string]string `json:"probe,omitempty"`
}

// phaseProbe is a per-phase live readiness check. Always best-effort: it returns
// a status map (e.g. {"nodes":"3/3 ready"}), never an error.
type phaseProbe func(ctx context.Context, outs map[string]config.StateOutput) map[string]string

// runPhaseStatus is the shared body of the four `<phase> status` commands: read
// the phase's tfstate outputs, select the relevant keys, run the live probe, and
// render (human or --json). A missing state file → deployed:false, exit 0.
func runPhaseStatus(cmd *cobra.Command, phase string, stateDir func(string) (string, error), keys []string, probe phaseProbe) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return config.WorkspaceNotReady(cctx.WorkspaceName)
	}
	dir, err := stateDir(cctx.WorkspaceName)
	if err != nil {
		return err
	}

	ps := phaseStatus{Phase: phase}
	outs, err := config.ReadStateOutputs(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return renderPhaseStatus(cmd, ps) // not deployed
		}
		return err
	}
	ps.Deployed = true
	ps.Outputs = selectOutputs(outs, keys)
	if probe != nil {
		if p := probe(cmdContext(cmd), outs); len(p) > 0 {
			ps.Probe = p
		}
	}
	return renderPhaseStatus(cmd, ps)
}

// selectOutputs picks the named outputs, redacting sensitive ones to
// "<sensitive>" (so --json is safe to log in CI).
func selectOutputs(outs map[string]config.StateOutput, keys []string) map[string]any {
	m := make(map[string]any, len(keys))
	for _, k := range keys {
		o, ok := outs[k]
		if !ok {
			continue
		}
		if o.Sensitive {
			m[k] = "<sensitive>"
		} else {
			m[k] = o.Value
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func renderPhaseStatus(cmd *cobra.Command, ps phaseStatus) error {
	w := cmd.OutOrStdout()
	if flagStatusJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(ps)
	}
	if !ps.Deployed {
		fmt.Fprintf(w, "%s: not deployed\n", ps.Phase)
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Phase:\t%s (deployed)\n", ps.Phase)
	for _, k := range sortedKeys(ps.Outputs) {
		fmt.Fprintf(tw, "%s:\t%s\n", k, formatOutputValue(ps.Outputs[k]))
	}
	for _, k := range sortedKeys(ps.Probe) {
		fmt.Fprintf(tw, "probe.%s:\t%s\n", k, ps.Probe[k])
	}
	return tw.Flush()
}

// formatOutputValue renders a decoded tf output value for the human layout:
// strings verbatim, lists comma-joined, maps as sorted key=val pairs.
func formatOutputValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = fmt.Sprintf("%v", e)
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		ks := make([]string, 0, len(x))
		for k := range x {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		parts := make([]string, 0, len(x))
		for _, k := range ks {
			parts = append(parts, fmt.Sprintf("%s=%v", k, x[k]))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", x)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// ── per-phase live probes ────────────────────────────────────────────────────

func clusterProbe(ctx context.Context, _ map[string]config.StateOutput) map[string]string {
	kcPath := k8s.DefaultKubeconfigPath()
	if kcPath == "" {
		return map[string]string{"nodes": "(no kubeconfig)"}
	}
	return map[string]string{"nodes": probeCluster(ctx, kcPath)}
}

// bnkProbe reports ready/total for each known BNK component (reuses the
// inspect.go bnkComponents selector map).
func bnkProbe(ctx context.Context, _ map[string]config.StateOutput) map[string]string {
	kcPath := k8s.DefaultKubeconfigPath()
	if kcPath == "" {
		return map[string]string{"cluster": "(no kubeconfig)"}
	}
	kc, err := k8s.NewFromKubeconfigFile(kcPath)
	if err != nil {
		return map[string]string{"cluster": fmt.Sprintf("(unreachable: %v)", err)}
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res := make(map[string]string, len(bnkComponents))
	for _, c := range bnkComponents {
		pods, err := kc.Clientset().CoreV1().Pods(c.Ns).List(tctx, metav1.ListOptions{LabelSelector: c.Selector})
		if err != nil {
			res[c.Name] = fmt.Sprintf("(error: %v)", err)
			continue
		}
		if len(pods.Items) == 0 {
			res[c.Name] = "0 pods"
			continue
		}
		ready := 0
		for i := range pods.Items {
			if podReady(&pods.Items[i]) {
				ready++
			}
		}
		res[c.Name] = fmt.Sprintf("%d/%d ready", ready, len(pods.Items))
	}
	return res
}

// testingProbe TCP-dials port 22 on each jumphost IP from the outputs.
func testingProbe(ctx context.Context, outs map[string]config.StateOutput) map[string]string {
	res := map[string]string{}
	if o, ok := outs["testing_tgw_jumphost_ip"]; ok {
		if s, _ := o.Value.(string); s != "" && s != "TGW jumphost not created" {
			res[s] = dialSSHPort(ctx, s)
		}
	}
	if o, ok := outs["testing_cluster_jumphost_ips"]; ok {
		if list, _ := o.Value.([]any); list != nil {
			for _, e := range list {
				if s, _ := e.(string); s != "" {
					res[s] = dialSSHPort(ctx, s)
				}
			}
		}
	}
	if len(res) == 0 {
		return nil
	}
	return res
}

func dialSSHPort(ctx context.Context, ip string) string {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, "22"))
	if err != nil {
		return "unreachable"
	}
	_ = conn.Close()
	return "reachable"
}

// gatewayProbe reads the live Gateway-API + F5 CRs the gateway phase created and
// reports what terraform state cannot: the controller-assigned Gateway address
// and CR readiness conditions. Best-effort — returns notes, never errors.
func gatewayProbe(ctx context.Context, outs map[string]config.StateOutput) map[string]string {
	gwName := outString(outs, "gateway_name")
	if gwName == "" {
		return map[string]string{"gateway": "(not deployed)"}
	}
	kcPath := k8s.DefaultKubeconfigPath()
	if kcPath == "" {
		return map[string]string{"gateway": "(no kubeconfig)"}
	}
	dyn, err := k8s.BuildDynamicClient(kcPath)
	if err != nil {
		return map[string]string{"gateway": fmt.Sprintf("(unreachable: %v)", err)}
	}
	mapper, err := k8s.BuildRESTMapper(kcPath)
	if err != nil {
		return map[string]string{"gateway": fmt.Sprintf("(unreachable: %v)", err)}
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	res := map[string]string{}

	// GatewayClass first, because everything below it is downstream of this one
	// condition and its failure is otherwise invisible. A controllerName that no
	// controller matches leaves Accepted absent — terraform still reports
	// success, the Gateway simply never programs, and nothing says why. Reporting
	// the class's Accepted condition next to the controllerName that produced it
	// turns that into a one-line diagnosis.
	if className := outString(outs, "gateway_class_name"); className != "" {
		gc, err := getCR(tctx, dyn, mapper, "gateway.networking.k8s.io", "GatewayClass", "", className)
		if err != nil {
			res["gatewayclass"] = fmt.Sprintf("(error: %v)", err)
		} else {
			res["gatewayclass"] = crConditionSummary(gc, "Accepted")
			if cn, found, _ := unstructured.NestedString(gc.Object, "spec", "controllerName"); found && cn != "" {
				res["gatewayclass_controller"] = cn
			}
		}
	}

	gw, err := getCR(tctx, dyn, mapper, "gateway.networking.k8s.io", "Gateway", outString(outs, "gateway_app_namespace"), gwName)
	if err != nil {
		res["gateway"] = fmt.Sprintf("(error: %v)", err)
	} else {
		if addr := crGatewayAddress(gw); addr != "" {
			res["gateway_address"] = addr
		}
		res["gateway"] = crConditionSummary(gw, "Programmed")
	}

	if bnkGwName := outString(outs, "gateway_bnkgateway_name"); bnkGwName != "" {
		bg, err := getCR(tctx, dyn, mapper, "k8s.f5net.com", "F5BnkGateway", outString(outs, "gateway_flo_namespace"), bnkGwName)
		if err != nil {
			res["f5bnkgateway"] = fmt.Sprintf("(error: %v)", err)
		} else {
			res["f5bnkgateway"] = crBestEffortState(bg)
		}
	}
	return res
}

// outString returns a string-typed, non-empty terraform output, else "".
func outString(outs map[string]config.StateOutput, key string) string {
	if o, ok := outs[key]; ok {
		if s, _ := o.Value.(string); s != "" {
			return s
		}
	}
	return ""
}

// getCR fetches a single CR by GroupKind (preferred version) + name, resolving
// the plural via the discovery RESTMapper so no plural is hardcoded.
func getCR(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, group, kind, ns, name string) (*unstructured.Unstructured, error) {
	mapping, err := mapper.RESTMapping(schema.GroupKind{Group: group, Kind: kind})
	if err != nil {
		return nil, err
	}
	ri := dyn.Resource(mapping.Resource)
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		return ri.Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	}
	return ri.Get(ctx, name, metav1.GetOptions{})
}

// crGatewayAddress returns the first controller-assigned status.addresses[].value.
func crGatewayAddress(u *unstructured.Unstructured) string {
	addrs, found, _ := unstructured.NestedSlice(u.Object, "status", "addresses")
	if !found {
		return ""
	}
	for _, a := range addrs {
		if m, ok := a.(map[string]any); ok {
			if v, _ := m["value"].(string); v != "" {
				return v
			}
		}
	}
	return ""
}

// crConditionSummary reports "<type>=<status> (<reason>)" for the named status
// condition, or a fallback when it isn't present yet.
func crConditionSummary(u *unstructured.Unstructured, condType string) string {
	conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if !found {
		return "present (no conditions yet)"
	}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == condType {
			st, _ := m["status"].(string)
			if reason, _ := m["reason"].(string); reason != "" {
				return fmt.Sprintf("%s=%s (%s)", condType, st, reason)
			}
			return fmt.Sprintf("%s=%s", condType, st)
		}
	}
	return "present (" + condType + " pending)"
}

// crBestEffortState summarizes a CR that may not use Gateway-API conditions:
// a Ready/Programmed/Accepted condition if any, then status.state, else "present".
func crBestEffortState(u *unstructured.Unstructured) string {
	conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if found {
		for _, want := range []string{"Ready", "Programmed", "Accepted"} {
			for _, c := range conds {
				if m, ok := c.(map[string]any); ok {
					if ct, _ := m["type"].(string); ct == want {
						st, _ := m["status"].(string)
						return fmt.Sprintf("%s=%s", want, st)
					}
				}
			}
		}
	}
	if s, found, _ := unstructured.NestedString(u.Object, "status", "state"); found && s != "" {
		return s
	}
	return "present"
}

// ── commands ─────────────────────────────────────────────────────────────────

var clusterStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Live runtime status of the Cluster phase (endpoints + node readiness)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runPhaseStatus(cmd, "cluster", config.WorkspaceClusterStateDir,
			[]string{"openshift_cluster_public_endpoint", "openshift_cluster_private_endpoint", "roks_transit_gateway_name"},
			clusterProbe)
	},
}

var bnkStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Live runtime status of the BNK trial phase (namespaces + component readiness)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runPhaseStatus(cmd, "bnk", config.WorkspaceStateDir,
			[]string{"flo_namespace", "flo_utils_namespace", "flo_trusted_profile_id"},
			bnkProbe)
	},
}

var testingStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Live runtime status of the Testing phase (jumphost IPs + SSH reachability)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runPhaseStatus(cmd, "testing", config.WorkspaceTestingStateDir,
			[]string{
				"testing_ssh_key_name",
				"testing_tgw_jumphost_ip", "testing_tgw_jumphost_ssh_command",
				"testing_cluster_jumphost_ips", "testing_cluster_jumphost_ssh_commands",
				"testing_tgw_jumphost_subnet_cidr", "testing_cluster_jumphost_subnet_cidrs",
				"jumphost_shared_key",
			},
			testingProbe)
	},
}

var gatewayStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Runtime status of the Gateway phase (CR readiness + assigned address)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runPhaseStatus(cmd, "gateway", config.WorkspaceGatewayStateDir,
			[]string{"gateway_enabled", "gateway_name", "gateway_class_name", "gateway_controller_name", "gateway_bnkgateway_name", "gateway_app_namespace", "gateway_listener_networks", "gateway_egress_mode"},
			gatewayProbe)
	},
}

func init() {
	for _, c := range []*cobra.Command{clusterStatusCmd, bnkStatusCmd, testingStatusCmd, gatewayStatusCmd} {
		c.Flags().BoolVar(&flagStatusJSON, "json", false, "output JSON (CI-friendly)")
	}
	clusterCmd.AddCommand(clusterStatusCmd)
	bnkCmd.AddCommand(bnkStatusCmd)
	testingCmd.AddCommand(testingStatusCmd)
	gatewayCmd.AddCommand(gatewayStatusCmd)
}
