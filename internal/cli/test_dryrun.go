package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/JLCode-tech/awsbnkctl/internal/config"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s"
	"github.com/JLCode-tech/awsbnkctl/internal/test"
)

// `awsbnkctl test {connectivity,dns,throughput,all} --dry-run`
// surface. Plans the probe by resolving the workspace-derived targets
// (region, cluster, namespace, host list, DNS resolvers, iperf3 mode)
// without executing any probe. Lets operators see "what would I be
// measuring" without paying the LB / DNS / iperf3 setup cost.
//
// Mirrors the spike-deferral pattern from `awsbnkctl up --dry-run`:
// the phased path prints its plan without mutating AWS; test --dry-run
// plans without probing. Brief §3 + §"Verification".

// testPlan is the structured shape rendered when --dry-run is set.
// Public fields so the JSON output mode (-o json) can serialise it
// without an extra mapping layer.
type testPlan struct {
	Suite     string            `json:"suite"`
	Workspace string            `json:"workspace"`
	Region    string            `json:"region,omitempty"`
	Cluster   string            `json:"cluster,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
	Targets   []string          `json:"targets,omitempty"`
	Resolvers map[string]string `json:"resolvers,omitempty"`
	Mode      string            `json:"mode,omitempty"`
	Backend   string            `json:"backend,omitempty"`
	Note      string            `json:"note,omitempty"`
}

// workspacePlanContext returns the workspace-derived defaults the test
// commands plumb into probes. Empty Workspace is acceptable — fields
// return zero-value strings/slices so the dry-run rendering still works
// and surfaces "(unset)" markers to operators.
//
// Region resolves from the workspace `aws.region`. Cluster from
// `cluster.name`. Namespace defaults to FLO's namespace
// (`aws.supply_chain.flo_namespace`) for connectivity / DNS probes,
// falling back to `k8s.Iperf3Namespace` ("awsbnkctl-test") for the
// iperf3 fixture path.
func workspacePlanContext(ws *config.Workspace) (region, cluster, ns string) {
	if ws == nil {
		return "", "", k8s.Iperf3Namespace
	}
	region = ws.AWS.Region
	cluster = ws.Cluster.Name
	ns = ws.AWS.SupplyChain.FLONamespace
	if ns == "" {
		ns = k8s.Iperf3Namespace
	}
	return
}

// printTestPlan renders a testPlan in either JSON (to stdout) or
// human-readable text (to stderr) per the -o / flagOutput selection.
// Returns nil on success — the verb's caller exits 0 after this.
func printTestPlan(out, errW io.Writer, plan testPlan) error {
	if flagOutput == "json" {
		return test.WriteJSON(out, plan)
	}
	fmt.Fprintf(errW, "## test %s — dry-run plan (workspace=%s)\n", plan.Suite, valueOr(plan.Workspace, "(none)"))
	if plan.Region != "" {
		fmt.Fprintf(errW, "  region:    %s\n", plan.Region)
	} else {
		fmt.Fprintf(errW, "  region:    (unset — set aws.region in workspace or AWS_REGION)\n")
	}
	if plan.Cluster != "" {
		fmt.Fprintf(errW, "  cluster:   %s\n", plan.Cluster)
	}
	if plan.Namespace != "" {
		fmt.Fprintf(errW, "  namespace: %s\n", plan.Namespace)
	}
	if plan.Mode != "" {
		fmt.Fprintf(errW, "  mode:      %s\n", plan.Mode)
	}
	if plan.Backend != "" {
		fmt.Fprintf(errW, "  backend:   %s\n", plan.Backend)
	}
	if len(plan.Targets) > 0 {
		fmt.Fprintf(errW, "  targets (%d):\n", len(plan.Targets))
		for _, t := range plan.Targets {
			fmt.Fprintf(errW, "    - %s\n", t)
		}
	}
	if len(plan.Resolvers) > 0 {
		keys := make([]string, 0, len(plan.Resolvers))
		for k := range plan.Resolvers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(errW, "  resolvers (%d):\n", len(plan.Resolvers))
		for _, k := range keys {
			fmt.Fprintf(errW, "    %s -> %s\n", k, plan.Resolvers[k])
		}
	}
	if plan.Note != "" {
		fmt.Fprintf(errW, "  note:      %s\n", plan.Note)
	}
	fmt.Fprintln(errW, "✓ dry-run: plan resolved (no probe executed)")
	return nil
}

func valueOr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// planConnectivity builds the dry-run plan for `test connectivity`.
// Resolves the workspace-derived host list (test.connectivity.extra_hosts)
// without making any HTTP request.
func planConnectivity(cctx *config.Context) testPlan {
	region, cluster, ns := workspacePlanContext(workspaceFrom(cctx))
	hosts := []string{}
	if ws := workspaceFrom(cctx); ws != nil {
		hosts = test.HostsFromConfig(ws)
	}
	note := ""
	if len(hosts) == 0 {
		note = "no test.connectivity.extra_hosts configured; live run would error"
	}
	return testPlan{
		Suite:     "connectivity",
		Workspace: workspaceName(cctx),
		Region:    region,
		Cluster:   cluster,
		Namespace: ns,
		Targets:   hosts,
		Note:      note,
	}
}

// planDNS builds the dry-run plan for `test dns` — workspace-driven
// (no flags) renders the extra_hosts list; flag-driven renders the
// --target + --server pair plus any named resolvers.
func planDNS(cctx *config.Context, flagDriven bool) testPlan {
	region, cluster, ns := workspacePlanContext(workspaceFrom(cctx))
	ws := workspaceFrom(cctx)
	plan := testPlan{
		Suite:     "dns",
		Workspace: workspaceName(cctx),
		Region:    region,
		Cluster:   cluster,
		Namespace: ns,
	}
	if flagDriven {
		target := flagDNSTarget
		if target == "" && ws != nil {
			target = ws.Test.DNS.DefaultTarget
		}
		server := flagDNSServer
		if server == "" {
			server = "system"
		}
		// Map named resolvers from workspace if --server names one.
		if ws != nil && ws.Test.DNS.Resolvers != nil {
			if mapped, ok := ws.Test.DNS.Resolvers[server]; ok {
				server = mapped
			}
			plan.Resolvers = ws.Test.DNS.Resolvers
		}
		plan.Targets = []string{target + " (" + flagDNSType + " @ " + server + ")"}
		plan.Backend = flagBackend
		if plan.Backend == "" {
			plan.Backend = "local"
		}
		if flagDNSGSLBCompare {
			plan.Note = "gslb-compare: would fan out across local + k8s + ssh:<targets>"
		}
		return plan
	}
	hosts := []string{}
	if ws != nil {
		hosts = test.HostsFromConfig(ws)
	}
	plan.Targets = hosts
	if len(hosts) == 0 {
		plan.Note = "no test.connectivity.extra_hosts configured; live run would error"
	}
	return plan
}

// planThroughput builds the dry-run plan for `test throughput`.
// Surfaces the mode, the iperf3 fixture namespace, the image, and the
// resolved client backend. Does not deploy the server pod.
func planThroughput(cctx *config.Context) testPlan {
	region, cluster, _ := workspacePlanContext(workspaceFrom(cctx))
	// throughput always lands in the dedicated iperf3 namespace
	// (k8s.Iperf3Namespace) — the FLO namespace defaulting above is
	// for connectivity/DNS probes against in-cluster services.
	ns := k8s.Iperf3Namespace
	plan := testPlan{
		Suite:     "throughput",
		Workspace: workspaceName(cctx),
		Region:    region,
		Cluster:   cluster,
		Namespace: ns,
		Mode:      flagThroughputMode,
		Backend:   flagBackend,
	}
	if plan.Backend == "" {
		plan.Backend = "k8s"
	}
	if cctx != nil && cctx.Workspace != nil {
		img := cctx.Workspace.Test.Throughput.Image
		if img == "" {
			img = k8s.Iperf3DefaultImage
		}
		plan.Targets = []string{"iperf3 server image: " + img}
	}
	return plan
}

// planAll composes connectivity + dns + throughput plans into a single
// `test all` document. Mirrors test.AllRun's shape (multi-suite envelope).
func planAll(cctx *config.Context) []testPlan {
	return []testPlan{
		planConnectivity(cctx),
		planDNS(cctx, false),
		planThroughput(cctx),
	}
}

// printTestPlans renders a multi-suite plan list (test all --dry-run).
func printTestPlans(out, errW io.Writer, plans []testPlan) error {
	if flagOutput == "json" {
		return test.WriteJSON(out, plans)
	}
	for _, p := range plans {
		if err := printTestPlan(out, errW, p); err != nil {
			return err
		}
		fmt.Fprintln(errW)
	}
	return nil
}

// workspaceFrom is a small accessor that survives a nil *config.Context.
func workspaceFrom(cctx *config.Context) *config.Workspace {
	if cctx == nil {
		return nil
	}
	return cctx.Workspace
}

// workspaceName returns the workspace name from a *config.Context, or
// empty when the context is nil.
func workspaceName(cctx *config.Context) string {
	if cctx == nil {
		return ""
	}
	return cctx.WorkspaceName
}
