package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/test"
)

var (
	flagThroughputMode      string
	flagThroughputCrossNode bool
	flagKeepFixtures        bool
	flagInsecureTLS         bool
)

var testCmd = &cobra.Command{
	Use:   "test [suite]",
	Short: "Run deployment validation tests (default: all)",
	Long: `roksbnkctl test runs deployment validation against the current workspace.

Suites:
  connectivity   HTTP/HTTPS reachability of deployed BNK services
  dns            DNS resolution of ingress and service hostnames
  throughput     iperf3 measurements (north-south by default; v1.x)
  all            run all of the above (default if no suite is specified)

Honors -o json with the roksbnkctl.v1 schema. Exit code 0 on all-pass,
non-zero on any-fail — CI-friendly.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runTestDispatch,
}

var testConnectivityCmd = &cobra.Command{
	Use:   "connectivity",
	Short: "HTTP/HTTPS reachability against configured hosts",
	RunE:  runTestConnectivityCmd,
}

var testDNSCmd = &cobra.Command{
	Use:   "dns",
	Short: "DNS resolution of configured hosts",
	RunE:  runTestDNSCmd,
}

var testThroughputCmd = &cobra.Command{
	Use:   "throughput",
	Short: "iperf3 throughput; deploys server pod automatically (v1.x)",
	Long: `Deploys an iperf3 server in the test namespace and runs the client
either from the roksbnkctl host (--mode north-south, default) or from a second
in-cluster pod (--mode east-west).

Not yet implemented — landing in v1.x once the internal/k8s client-go
fixture lifecycle is wired.`,
	RunE: runTestThroughputCmd,
}

var testListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available test suites",
	RunE:  runTestListCmd,
}

func init() {
	testCmd.Flags().BoolVar(&flagInsecureTLS, "insecure", false, "skip TLS certificate validation (connectivity only)")

	testThroughputCmd.Flags().StringVar(&flagThroughputMode, "mode", "north-south", "throughput mode: north-south | east-west")
	testThroughputCmd.Flags().BoolVar(&flagThroughputCrossNode, "cross-node", false, "force east-west client and server onto different nodes")
	testThroughputCmd.Flags().BoolVar(&flagKeepFixtures, "keep", false, "leave the iperf3 server pod running after the test")

	testCmd.AddCommand(testConnectivityCmd, testDNSCmd, testThroughputCmd, testListCmd)
	rootCmd.AddCommand(testCmd)
}

// runTestDispatch handles `roksbnkctl test [suite]` — dispatches the bare
// suite name to the corresponding subcommand impl.
func runTestDispatch(cmd *cobra.Command, args []string) error {
	suite := "all"
	if len(args) > 0 {
		suite = args[0]
	}
	switch suite {
	case "all":
		return runTestAllCmd(cmd, nil)
	case "connectivity":
		return runTestConnectivityCmd(cmd, nil)
	case "dns":
		return runTestDNSCmd(cmd, nil)
	case "throughput":
		return runTestThroughputCmd(cmd, nil)
	case "list":
		return runTestListCmd(cmd, nil)
	default:
		return fmt.Errorf("unknown test suite %q (try connectivity, dns, throughput, all)", suite)
	}
}

func runTestAllCmd(cmd *cobra.Command, _ []string) error {
	cctx, hosts, err := loadHosts()
	if err != nil {
		return err
	}
	_ = cctx
	all := test.RunAll(cmd.Context(), hosts, flagInsecureTLS)
	return outputAll(all)
}

func runTestConnectivityCmd(cmd *cobra.Command, _ []string) error {
	_, hosts, err := loadHosts()
	if err != nil {
		return err
	}
	s := test.RunConnectivity(cmd.Context(), hosts, flagInsecureTLS)
	return outputSuite(s)
}

func runTestDNSCmd(cmd *cobra.Command, _ []string) error {
	_, hosts, err := loadHosts()
	if err != nil {
		return err
	}
	s := test.RunDNS(cmd.Context(), hosts)
	return outputSuite(s)
}

func runTestThroughputCmd(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}

	// Resolve the iperf3 client backend. Sprint 4 default is "k8s" per
	// PRD 03 §"iperf3" §"Default backend"; users can override via
	// --backend. Docker isn't useful for iperf3 — call it out before
	// the user wonders why the bandwidth numbers are funny.
	backendSpec := resolveBackendSpecWith(cctx, "iperf3", flagBackend)
	switch {
	case backendSpec == "" || backendSpec == "k8s" || backendSpec == "local":
		// supported
	case strings.HasPrefix(backendSpec, "ssh:"):
		// supported
	case backendSpec == "docker":
		return fmt.Errorf("--backend docker isn't supported for iperf3 — docker shares the host network namespace by default and gives no network-locality benefit over local. Use --backend local or --backend k8s instead")
	default:
		return fmt.Errorf("unsupported backend %q for iperf3 (want k8s|local|ssh:<target>)", backendSpec)
	}

	kc, err := k8s.NewFromDefault()
	if err != nil {
		return err
	}

	ns := k8s.Iperf3Namespace
	mode := flagThroughputMode
	if mode != "north-south" && mode != "east-west" {
		return fmt.Errorf("--mode must be north-south or east-west (got %q)", mode)
	}

	svcType := corev1.ServiceTypeClusterIP
	if mode == "north-south" {
		svcType = corev1.ServiceTypeLoadBalancer
	}

	image := cctx.Workspace.Test.Throughput.Image
	if image == "" {
		image = k8s.Iperf3DefaultImage
	}

	fmt.Fprintln(os.Stderr, "→ Deploying iperf3 fixture")
	if err := kc.DeployIperf3(cmd.Context(), k8s.Iperf3Options{
		Namespace:   ns,
		Image:       image,
		ServiceType: svcType,
	}); err != nil {
		return err
	}
	if !flagKeepFixtures {
		defer teardownIperf3Best(cmd.Context(), kc, ns)
	}

	fmt.Fprintln(os.Stderr, "→ Waiting for iperf3 server pod ready")
	if err := kc.WaitIperf3Ready(cmd.Context(), ns, 0); err != nil {
		return err
	}

	endpoint, err := resolveIperf3Endpoint(cmd.Context(), kc, ns, mode)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "✓ iperf3 endpoint: %s\n", endpoint)

	duration := cctx.Workspace.Test.Throughput.Duration
	streams := cctx.Workspace.Test.Throughput.Streams
	opts := test.ThroughputOptions{
		Mode:     mode,
		Endpoint: endpoint,
		Duration: duration,
		Streams:  streams,
	}

	// Backend dispatch for the iperf3 *client*. The server lives
	// in-cluster regardless (the deploy above). Sprint 4: --backend k8s
	// runs the client as an in-cluster Job for true pod-to-pod
	// throughput. --backend local (or empty) keeps today's host-iperf3
	// path. --backend ssh:<target> runs the client on the named SSH
	// jumphost.
	switch {
	case backendSpec == "" || backendSpec == "local":
		s := test.RunThroughput(cmd.Context(), opts)
		return outputSuite(s)
	case backendSpec == "k8s":
		s, err := runIperf3ClientK8s(cmd.Context(), kc, image, opts)
		if err != nil {
			return err
		}
		return outputSuite(s)
	case strings.HasPrefix(backendSpec, "ssh:"):
		s, err := runIperf3ClientSSH(cmd.Context(), backendSpec, opts)
		if err != nil {
			return err
		}
		return outputSuite(s)
	}
	// Unreachable — backendSpec validation above filters to the four
	// supported values. Belt-and-braces for refactor safety.
	return fmt.Errorf("internal: backend %q reached client dispatch", backendSpec)
}

// resolveIperf3Endpoint picks the address the iperf3 client connects to.
// north-south = LoadBalancer external IP/hostname (BNK data path);
// east-west   = Service ClusterIP (in-cluster client; client-from-host
// won't reach a ClusterIP, so v1 east-west still uses the host as the
// client — this means east-west measures host→ClusterIP-via-NodePort-equivalent.
// True pod-to-pod east-west lands in v1.x with an in-cluster client pod.).
func resolveIperf3Endpoint(ctx context.Context, kc *k8s.Client, ns, mode string) (string, error) {
	if mode == "north-south" {
		fmt.Fprintln(os.Stderr, "→ Waiting for LoadBalancer endpoint (can take 30–90s on IBM Cloud)")
		return kc.WaitLoadBalancerEndpoint(ctx, ns, 0)
	}
	return kc.ClusterIPEndpoint(ctx, ns)
}

// runIperf3ClientK8s spawns the iperf3 client as an in-cluster Job
// (via the K8s execution backend) and parses its JSON output. Server
// is already deployed by the caller; endpoint is the cluster-side
// address (LB IP/hostname for north-south, ClusterIP for east-west).
//
// PRD 03 §"iperf3" §"K8s shape" — server + client both in-cluster.
func runIperf3ClientK8s(ctx context.Context, kc *k8s.Client, image string, opts test.ThroughputOptions) (test.SuiteRun, error) {
	start := time.Now()
	args := []string{"-c", opts.Endpoint, "-J"}
	if opts.Duration > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", opts.Duration))
	}
	if opts.Streams > 0 {
		args = append(args, "-P", fmt.Sprintf("%d", opts.Streams))
	}

	be, err := execbackend.ResolveBackend("k8s")
	if err != nil {
		return test.SuiteRun{}, err
	}
	// Build argv: [tool, ...args]. The Job path's image lookup hits
	// toolImages["iperf3"] for the bundled image; the workspace's
	// configured override wins via opts.Image only at server-deploy time.
	argv := append([]string{"iperf3"}, args...)
	var stdout strings.Builder
	rc, runErr := be.Run(ctx, argv, execbackend.RunOpts{
		Stdout: &stdout,
		Stderr: os.Stderr,
	})
	dur := time.Since(start).Milliseconds()
	if runErr != nil && rc == 0 {
		return test.SuiteRun{}, runErr
	}

	probe := test.ProbeResult{
		Suite:      "throughput",
		Name:       fmt.Sprintf("iperf3 %s → %s (k8s)", opts.Mode, opts.Endpoint),
		DurationMS: dur,
	}
	if rc != 0 {
		probe.Status = test.StatusFail
		probe.Detail = fmt.Sprintf("iperf3 client Job exited %d", rc)
	} else {
		probe.Status = test.StatusPass
		// Parse the JSON output from the Job's stdout (collected via
		// pod log stream by the k8s backend).
		probe.Detail = strings.TrimSpace(stdout.String())
	}
	probes := []test.ProbeResult{probe}
	return test.SuiteRun{
		Schema:     test.SchemaVersion,
		Command:    "test",
		Suite:      "throughput",
		Timestamp:  time.Now(),
		DurationMS: dur,
		Results:    probes,
		Overall:    test.Aggregate(probes),
	}, nil
}

// runIperf3ClientSSH runs the iperf3 client over the SSH backend (e.g.,
// from a jumphost) and parses its JSON output.
//
// PRD 03 §"iperf3" §"SSH shape" — auto-install via apt (with
// --bootstrap), then `iperf3 -c <endpoint> -J`.
func runIperf3ClientSSH(ctx context.Context, backendSpec string, opts test.ThroughputOptions) (test.SuiteRun, error) {
	start := time.Now()
	args := []string{"-c", opts.Endpoint, "-J"}
	if opts.Duration > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", opts.Duration))
	}
	if opts.Streams > 0 {
		args = append(args, "-P", fmt.Sprintf("%d", opts.Streams))
	}

	be, err := execbackend.ResolveBackend(backendSpec)
	if err != nil {
		return test.SuiteRun{}, err
	}
	target := execbackend.SpecTarget(backendSpec)
	cctx, _, _ := workspaceEnv()
	wsName := ""
	if cctx != nil {
		wsName = cctx.WorkspaceName
	}
	execbackend.SetSSHOpts(execbackend.SSHBackendOpts{
		Workspace:       wsName,
		Bootstrap:       flagBootstrap,
		InsecureHostKey: flagInsecureHostKey,
	})
	env := []string{"ROKSBNKCTL_SSH_TARGET=" + target}
	argv := append([]string{"iperf3"}, args...)
	var stdout strings.Builder
	rc, runErr := be.Run(ctx, argv, execbackend.RunOpts{
		Env:    env,
		Stdout: &stdout,
		Stderr: os.Stderr,
	})
	dur := time.Since(start).Milliseconds()
	if runErr != nil && rc == 0 {
		return test.SuiteRun{}, runErr
	}

	probe := test.ProbeResult{
		Suite:      "throughput",
		Name:       fmt.Sprintf("iperf3 %s → %s (ssh:%s)", opts.Mode, opts.Endpoint, target),
		DurationMS: dur,
	}
	if rc != 0 {
		probe.Status = test.StatusFail
		probe.Detail = fmt.Sprintf("ssh iperf3 client exited %d", rc)
	} else {
		probe.Status = test.StatusPass
		probe.Detail = strings.TrimSpace(stdout.String())
	}
	probes := []test.ProbeResult{probe}
	return test.SuiteRun{
		Schema:     test.SchemaVersion,
		Command:    "test",
		Suite:      "throughput",
		Timestamp:  time.Now(),
		DurationMS: dur,
		Results:    probes,
		Overall:    test.Aggregate(probes),
	}, nil
}

// teardownIperf3Best is the deferred cleanup when --keep is not passed.
// Uses a fresh background context with a short timeout so a cancelled
// outer ctx doesn't skip the teardown entirely.
func teardownIperf3Best(_ context.Context, kc *k8s.Client, ns string) {
	tctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := kc.TeardownIperf3(tctx, ns); err != nil {
		fmt.Fprintf(os.Stderr, "warning: tearing down iperf3 fixture: %v\n", err)
		return
	}
	fmt.Fprintln(os.Stderr, "✓ iperf3 fixture removed")
}

func runTestListCmd(_ *cobra.Command, _ []string) error {
	suites := []struct{ Name, Desc string }{
		{"connectivity", "HTTP/HTTPS reachability of configured hosts"},
		{"dns", "DNS resolution of configured hosts"},
		{"throughput", "iperf3 throughput (v1.x)"},
		{"all", "runs connectivity + dns (throughput once available)"},
	}
	for _, s := range suites {
		fmt.Printf("  %-15s %s\n", s.Name, s.Desc)
	}
	return nil
}

// loadHosts pulls the workspace's test host list. Returns a clear error
// when nothing is configured — better than silently passing an empty
// list and returning all-skipped.
func loadHosts() (*config.Context, []string, error) {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return nil, nil, err
	}
	if cctx.Workspace == nil {
		return nil, nil, fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}
	hosts := test.HostsFromConfig(cctx.Workspace)
	if len(hosts) == 0 {
		return nil, nil, fmt.Errorf("no hosts configured to probe; add to test.connectivity.extra_hosts in config.yaml")
	}
	return cctx, hosts, nil
}

// outputSuite writes a SuiteRun in JSON (to stdout) or text (to stderr)
// per -o, then exits non-zero if the suite failed.
func outputSuite(s test.SuiteRun) error {
	if flagOutput == "json" {
		if err := test.WriteJSON(os.Stdout, s); err != nil {
			return err
		}
	} else {
		test.PrintSuiteText(os.Stderr, s)
	}
	if s.Overall == test.StatusFail {
		os.Exit(1)
	}
	return nil
}

// outputAll handles AllRun output (multi-suite). Same JSON-on-stdout vs
// text-on-stderr split, then exits non-zero on any-fail.
func outputAll(all test.AllRun) error {
	if flagOutput == "json" {
		if err := test.WriteJSON(os.Stdout, all); err != nil {
			return err
		}
	} else {
		for _, s := range all.Suites {
			test.PrintSuiteText(os.Stderr, s)
		}
		passed := 0
		for _, s := range all.Suites {
			if s.Overall == test.StatusPass {
				passed++
			}
		}
		fmt.Fprintf(os.Stderr, "\n%s overall (%d/%d suites passed)\n", all.Overall, passed, len(all.Suites))
	}
	if all.Overall == test.StatusFail {
		os.Exit(1)
	}
	return nil
}
