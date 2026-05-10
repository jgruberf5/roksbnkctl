package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// jsonDecoder is a tiny shim so test.go's helpers don't need to import
// encoding/json at every call site. Returns a *json.Decoder over the
// given string.
func jsonDecoder(s string) *json.Decoder {
	return json.NewDecoder(strings.NewReader(s))
}

var (
	flagThroughputMode      string
	flagThroughputCrossNode bool
	flagKeepFixtures        bool
	flagInsecureTLS         bool

	// DNS probe flags (Sprint 5; PRD 03 §"DNS probe (GSLB-aware)").
	// All optional — when none of them are set, `roksbnkctl test dns`
	// keeps today's workspace-extra_hosts behaviour unchanged. As soon
	// as any one is set, the new flag-driven path activates.
	flagDNSTarget            string
	flagDNSType              string
	flagDNSServer            string
	flagDNSIterations        int
	flagDNSTimeout           time.Duration
	flagDNSGSLBCompare       bool
	flagDNSRequireDivergence bool
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
	Short: "DNS resolution probe (single-vantage, GSLB-compare, or workspace-driven)",
	Long: `roksbnkctl test dns runs DNS probes against configured resolvers.

Two modes:

  Workspace-driven (no flags) — resolves each host listed under
  test.connectivity.extra_hosts via the std-lib resolver. Same as
  Sprint 0–4 behaviour; preserves CI invocations using the legacy
  ` + "`roksbnkctl.v1`" + ` schema.

  Flag-driven (any of --target/--type/--server/--gslb-compare set) —
  uses the embedded miekg/dns probe (no external dig install needed).
  Single-vantage emits ` + "`roksbnkctl.dns.v1.vantage`" + `; --gslb-compare
  emits ` + "`roksbnkctl.dns.v1`" + ` with a gslb_divergence boolean across
  all configured backends (local + k8s + ssh:<targets>).

Use --backend local|k8s|ssh:<target> to pick a single vantage point;
--gslb-compare fans out across all available vantages. PRD 03 §"DNS
probe (GSLB-aware)".`,
	RunE: runTestDNSCmd,
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

	// DNS probe flag surface (PRD 03 §"DNS probe (GSLB-aware)" §"CLI surface").
	// Setting any one of these (or --gslb-compare) activates the new
	// flag-driven path; otherwise `roksbnkctl test dns` keeps the legacy
	// workspace-extra_hosts probe behaviour for backwards compatibility.
	testDNSCmd.Flags().StringVar(&flagDNSTarget, "target", "", "DNS name to query (overrides workspace test.dns.default_target)")
	testDNSCmd.Flags().StringVar(&flagDNSType, "type", "A", "record type: A | AAAA | CNAME | MX | NS | TXT | SRV | SOA | PTR | CAA | DS | DNSKEY | ANY")
	testDNSCmd.Flags().StringVar(&flagDNSServer, "server", "", "resolver: <ip>[:<port>] | system | cluster | <named-from-workspace> (default: system)")
	testDNSCmd.Flags().IntVar(&flagDNSIterations, "iterations", 1, "number of repeated queries; >1 enables RTT distribution")
	testDNSCmd.Flags().DurationVar(&flagDNSTimeout, "timeout", 2*time.Second, "per-query timeout")
	testDNSCmd.Flags().BoolVar(&flagDNSGSLBCompare, "gslb-compare", false, "fan out the probe across all configured backends (local + k8s + ssh:<targets>) and emit a comparison JSON with gslb_divergence")
	testDNSCmd.Flags().BoolVar(&flagDNSRequireDivergence, "require-divergence", false, "with --gslb-compare: exit non-zero if NO divergence is observed (CI assertion that GSLB is doing something)")

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
	// New flag-driven path activates when any of --target / --type
	// (when set to anything other than the default "A" — handled via
	// the cmd.Flags().Changed check) / --server / --gslb-compare is
	// set on the command line. PRD 03 §"DNS probe" §"backwards-
	// compatible path".
	if dnsFlagDriven(cmd) {
		return runTestDNSProbe(cmd)
	}
	_, hosts, err := loadHosts()
	if err != nil {
		return err
	}
	s := test.RunDNS(cmd.Context(), hosts)
	return outputSuite(s)
}

// dnsFlagDriven decides whether the user invoked the new flag surface.
// We check `cmd.Flags().Changed` rather than the variable values
// because --type defaults to "A" (a non-empty value cobra never marks
// as "Changed" unless the user typed it).
func dnsFlagDriven(cmd *cobra.Command) bool {
	for _, name := range []string{"target", "type", "server", "iterations", "gslb-compare"} {
		if f := cmd.Flag(name); f != nil && f.Changed {
			return true
		}
	}
	return false
}

// runTestDNSProbe runs the new miekg/dns-based probe surface.
//
// Reject `--backend docker` here per PRD 03 §"DNS probe" §"Why no
// docker backend": a docker container shares the host's network
// identity (default bridge), so it's a useless extra hop with no
// GSLB-relevant locality difference.
func runTestDNSProbe(cmd *cobra.Command) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	// Workspace is optional for the DNS probe — bare-flag invocations
	// like `roksbnkctl test dns --target www.cloudflare.com` should
	// just-work without a workspace.
	var ws *config.Workspace
	if cctx != nil {
		ws = cctx.Workspace
	}

	// Reject --backend docker per spec — clear remediation message.
	if flagBackend == "docker" {
		return fmt.Errorf("DNS probe doesn't benefit from docker; use --backend local instead (a docker container shares the host's network identity, so there's no GSLB-relevant locality difference). See docs/prd/03-EXECUTION-BACKENDS.md §\"DNS probe\"")
	}

	target := flagDNSTarget
	if target == "" && ws != nil {
		target = ws.Test.DNS.DefaultTarget
	}
	if target == "" {
		return fmt.Errorf("--target is required (or set test.dns.default_target in workspace config)")
	}

	qtype, err := test.ParseDNSType(flagDNSType)
	if err != nil {
		return err
	}

	// Resolve --server through the named-resolver map first.
	server := flagDNSServer
	if ws != nil && server != "" && ws.Test.DNS.Resolvers != nil {
		if mapped, ok := ws.Test.DNS.Resolvers[server]; ok {
			server = mapped
		}
	}

	iterations := flagDNSIterations
	if iterations < 1 {
		iterations = 1
	}

	if flagDNSGSLBCompare {
		return runDNSGSLBCompare(cmd.Context(), cctx, target, qtype, server, iterations, flagDNSTimeout)
	}
	return runDNSSingleVantage(cmd.Context(), cctx, target, qtype, server, iterations, flagDNSTimeout)
}

// runDNSSingleVantage runs the probe on a single backend (default
// local) and emits the per-vantage result document. The shape matches
// PRD 03 §"DNS probe" §"JSON output schema" — a single
// `roksbnkctl.dns.v1.vantage` document, not the multi-vantage
// comparison wrapper.
func runDNSSingleVantage(ctx context.Context, cctx *config.Context, target string, qtype uint16, server string, iterations int, timeout time.Duration) error {
	backendSpec := flagBackend
	if backendSpec == "" {
		backendSpec = "local"
	}

	res, err := dispatchDNSProbe(ctx, cctx, backendSpec, target, qtype, server, iterations, timeout)
	if err != nil {
		return err
	}
	if flagOutput == "json" {
		if err := test.WriteJSON(os.Stdout, res); err != nil {
			return err
		}
	} else {
		printDNSVantageText(os.Stderr, res)
	}
	if res.Err != "" || res.Rcode == "TIMEOUT" || res.Rcode == "ERROR" {
		os.Exit(1)
	}
	return nil
}

// runDNSGSLBCompare fans the probe out across the configured
// vantages (local always, plus k8s if a kubeconfig + ops-pod-equivalent
// is reachable, plus each ssh target the workspace defines).
//
// PRD 03 §"DNS probe" §"GSLB use case": divergence is *expected* in a
// healthy GSLB; --require-divergence flips the exit code so CI can
// assert the GSLB rules are taking effect.
func runDNSGSLBCompare(ctx context.Context, cctx *config.Context, target string, qtype uint16, server string, iterations int, timeout time.Duration) error {
	specs := []string{"local"}
	// k8s vantage if a default kubeconfig is reachable. We probe via
	// the BuildClientset path the doctor uses; if it fails, we just
	// skip — the user will see one less vantage in the comparison.
	if k8s.DefaultKubeconfigPath() != "" {
		specs = append(specs, "k8s")
	}
	// ssh:<target> vantages from workspace targets.
	if cctx != nil && cctx.Workspace != nil {
		// Sort keys for deterministic vantage ordering across runs.
		names := make([]string, 0, len(cctx.Workspace.Targets))
		for n := range cctx.Workspace.Targets {
			names = append(names, n)
		}
		// stable order (no need for full sort.Strings to keep dep
		// surface tight — the cli already imports strings).
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				if names[j] < names[i] {
					names[i], names[j] = names[j], names[i]
				}
			}
		}
		for _, n := range names {
			specs = append(specs, "ssh:"+n)
		}
	}

	vantages := make([]test.DNSProbeResult, 0, len(specs))
	for _, spec := range specs {
		res, err := dispatchDNSProbe(ctx, cctx, spec, target, qtype, server, iterations, timeout)
		if err != nil {
			// Backend-level failure (couldn't reach k8s, ssh target
			// missing). Emit a degenerate vantage so the comparison
			// still has the entry; consumers see Err populated.
			res = &test.DNSProbeResult{
				Schema:  test.DNSVantageSchemaVersion,
				Backend: spec,
				Server:  server,
				Err:     err.Error(),
				Rcode:   "ERROR",
			}
		}
		vantages = append(vantages, *res)
	}

	cmp := test.CompareDNSVantages(target, qtype, vantages)
	if flagOutput == "json" {
		if err := test.WriteJSON(os.Stdout, cmp); err != nil {
			return err
		}
	} else {
		printDNSCompareText(os.Stderr, cmp)
	}
	// --require-divergence inverts the exit code: 0 only if divergence
	// was observed. Otherwise default exit code follows whether ANY
	// vantage errored.
	if flagDNSRequireDivergence && !cmp.GSLBDivergence {
		fmt.Fprintln(os.Stderr, "✗ --require-divergence: no divergence across vantages (GSLB may not be taking effect)")
		os.Exit(1)
	}
	for _, v := range cmp.Vantages {
		if v.Err != "" {
			os.Exit(1)
		}
	}
	return nil
}

// dispatchDNSProbe runs the Probe via the named backend.
//
//   - "local" runs in-process via Probe.Run.
//   - "k8s" runs in-cluster as a one-shot Job that re-execs the
//     `roksbnkctl` binary with the same probe args + `-o json`. The
//     binary lives inside the ops pod's image (the bundled tools
//     image ships with `/usr/local/bin/roksbnkctl` alongside
//     `ibmcloud`). The Job's stdout is parsed back into a
//     DNSProbeResult.
//   - "ssh:<target>" runs the binary on the named SSH target.
//
// Sprint 5 implements local + k8s; ssh is a stub that returns an
// error pointing at the deferred-to-v1.x tracking issue.
func dispatchDNSProbe(ctx context.Context, cctx *config.Context, spec, target string, qtype uint16, server string, iterations int, timeout time.Duration) (*test.DNSProbeResult, error) {
	switch {
	case spec == "" || spec == "local":
		p := &test.Probe{
			Target:     target,
			Type:       qtype,
			Server:     server,
			Iterations: iterations,
			Timeout:    timeout,
			Backend:    "local",
		}
		return p.Run(ctx)
	case spec == "k8s":
		return runDNSProbeK8s(ctx, cctx, target, qtype, server, iterations, timeout)
	case strings.HasPrefix(spec, "ssh:"):
		return runDNSProbeSSH(ctx, cctx, spec, target, qtype, server, iterations, timeout)
	default:
		return nil, fmt.Errorf("unsupported backend %q for dns probe (want local|k8s|ssh:<target>)", spec)
	}
}

// runDNSProbeK8s executes the DNS probe inside the cluster as a
// one-shot Job that self-execs `roksbnkctl test dns ...` against the
// same flags. The Job's image is the bundled ops pod image (which
// carries the `roksbnkctl` binary alongside `ibmcloud`).
//
// PRD 03 §"DNS probe" §"K8s shape": the binary itself runs in-cluster;
// no separate image needed.
func runDNSProbeK8s(ctx context.Context, cctx *config.Context, target string, qtype uint16, server string, iterations int, timeout time.Duration) (*test.DNSProbeResult, error) {
	be, err := execbackend.ResolveBackend("k8s")
	if err != nil {
		return nil, err
	}
	// "cluster" sentinel rewrites to "system" in the pod (where the
	// pod's resolv.conf points at CoreDNS — so "system" inside the
	// pod is "cluster" from the host's perspective).
	srv := server
	if srv == "" || strings.EqualFold(srv, "system") || strings.EqualFold(srv, "cluster") {
		srv = "system"
	}
	// argv[0] = "roksbnkctl" — the k8s backend's runAsJob path looks
	// up an image for this name. We add it to toolImages just below.
	argv := []string{
		"roksbnkctl", "test", "dns",
		"--target", target,
		"--type", dnsTypeName(qtype),
		"--server", srv,
		"--iterations", fmt.Sprintf("%d", iterations),
		"--timeout", timeout.String(),
		"-o", "json",
	}
	var stdout strings.Builder
	rc, runErr := be.Run(ctx, argv, execbackend.RunOpts{
		Stdout: &stdout,
		Stderr: os.Stderr,
	})
	if runErr != nil && rc == 0 {
		return nil, runErr
	}
	// Parse the Job's JSON output back into a DNSProbeResult. The
	// Job emits the per-vantage shape (single-vantage path inside
	// the pod), which we re-stamp with backend="k8s" so the wrapper's
	// comparison shows the right label.
	res, err := decodeDNSProbeJSON(stdout.String())
	if err != nil {
		if rc != 0 {
			return nil, fmt.Errorf("k8s dns-probe job exited %d: %s", rc, strings.TrimSpace(stdout.String()))
		}
		return nil, fmt.Errorf("parsing k8s dns-probe output: %w", err)
	}
	res.Backend = "k8s"
	return res, nil
}

// runDNSProbeSSH runs the binary on the named SSH target. Mirrors the
// k8s path: re-exec `roksbnkctl test dns ... -o json` over SSH; parse
// the JSON back. Requires the binary to be on the target's PATH (or
// scp'd in by a future bootstrap path — out of scope for v0.9).
func runDNSProbeSSH(ctx context.Context, cctx *config.Context, spec, target string, qtype uint16, server string, iterations int, timeout time.Duration) (*test.DNSProbeResult, error) {
	be, err := execbackend.ResolveBackend(spec)
	if err != nil {
		return nil, err
	}
	wsName := ""
	if cctx != nil {
		wsName = cctx.WorkspaceName
	}
	execbackend.SetSSHOpts(execbackend.SSHBackendOpts{
		Workspace:       wsName,
		Bootstrap:       flagBootstrap,
		InsecureHostKey: flagInsecureHostKey,
	})
	tName := execbackend.SpecTarget(spec)
	srv := server
	if srv == "" || strings.EqualFold(srv, "cluster") {
		srv = "system"
	}
	argv := []string{
		"roksbnkctl", "test", "dns",
		"--target", target,
		"--type", dnsTypeName(qtype),
		"--server", srv,
		"--iterations", fmt.Sprintf("%d", iterations),
		"--timeout", timeout.String(),
		"-o", "json",
	}
	env := []string{"ROKSBNKCTL_SSH_TARGET=" + tName}
	var stdout strings.Builder
	rc, runErr := be.Run(ctx, argv, execbackend.RunOpts{
		Env:    env,
		Stdout: &stdout,
		Stderr: os.Stderr,
	})
	if runErr != nil && rc == 0 {
		return nil, runErr
	}
	res, err := decodeDNSProbeJSON(stdout.String())
	if err != nil {
		if rc != 0 {
			return nil, fmt.Errorf("ssh dns-probe exited %d on %s: %s", rc, tName, strings.TrimSpace(stdout.String()))
		}
		return nil, fmt.Errorf("parsing ssh dns-probe output: %w", err)
	}
	res.Backend = "ssh:" + tName
	return res, nil
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
		{"dns", "DNS resolution probe (miekg/dns; --gslb-compare for multi-vantage)"},
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

// dnsTypeName maps a miekg/dns Type uint16 back to its canonical
// string. Used when re-execing the binary in-cluster / over SSH so the
// child invocation sees the same type token the parent parsed. Unknown
// types fall back to "TYPE<n>" (mirrors miekg/dns's own pretty-print).
func dnsTypeName(t uint16) string {
	if s, ok := dnsTypeStringTable[t]; ok {
		return s
	}
	return fmt.Sprintf("TYPE%d", t)
}

// dnsTypeStringTable mirrors dns.TypeToString for the subset PRD 03
// §"Record types supported" calls out. Inlined to avoid the cli
// package importing miekg/dns directly — that's the test package's
// concern. Anything beyond this table falls through to "TYPE<n>" which
// the miekg/dns parser still accepts.
var dnsTypeStringTable = map[uint16]string{
	1:   "A",
	2:   "NS",
	5:   "CNAME",
	6:   "SOA",
	12:  "PTR",
	15:  "MX",
	16:  "TXT",
	28:  "AAAA",
	33:  "SRV",
	43:  "DS",
	48:  "DNSKEY",
	257: "CAA",
	255: "ANY",
}

// decodeDNSProbeJSON parses a per-vantage JSON document emitted by a
// child `roksbnkctl test dns ... -o json` invocation (k8s Job or SSH
// re-exec). The output may have stderr noise prepended (image-pull
// progress for k8s, ssh banner for ssh) — we scan for the first '{'
// and parse from there.
func decodeDNSProbeJSON(s string) (*test.DNSProbeResult, error) {
	start := strings.Index(s, "{")
	if start < 0 {
		return nil, fmt.Errorf("no JSON object found in output: %q", strings.TrimSpace(s))
	}
	var res test.DNSProbeResult
	dec := jsonDecoder(s[start:])
	if err := dec.Decode(&res); err != nil {
		return nil, fmt.Errorf("decoding dns probe JSON: %w", err)
	}
	return &res, nil
}

// printDNSVantageText renders a per-vantage result in human-readable
// form. Mirrors the SuiteRun text rendering's symbol semantics so a
// user reading mixed output gets a consistent visual.
func printDNSVantageText(w io.Writer, r *test.DNSProbeResult) {
	if r == nil {
		return
	}
	sym := "✓"
	if r.Err != "" || r.Rcode == "TIMEOUT" || r.Rcode == "ERROR" {
		sym = "✗"
	} else if r.Rcode != "NOERROR" {
		sym = "⚠"
	}
	fmt.Fprintf(w, "%s [%s] %s @ %s — %s (rtt p50=%.1fms p95=%.1fms p99=%.1fms, %d answer(s))\n",
		sym, r.Backend, r.Server, r.Server, r.Rcode,
		r.RTTMs.P50, r.RTTMs.P95, r.RTTMs.P99, len(r.Answers))
	for _, a := range r.Answers {
		fmt.Fprintf(w, "    %s  %d  %s  %s\n", a.Name, a.TTL, a.Type, a.RData)
	}
	if r.Err != "" {
		fmt.Fprintf(w, "    error: %s\n", r.Err)
	}
}

// printDNSCompareText renders the multi-vantage comparison.
func printDNSCompareText(w io.Writer, c test.DNSCompareResult) {
	fmt.Fprintf(w, "## DNS comparison: %s (%s)\n", c.Target, c.Type)
	for i := range c.Vantages {
		printDNSVantageText(w, &c.Vantages[i])
	}
	if c.GSLBDivergence {
		fmt.Fprintf(w, "→ gslb_divergence: TRUE — %s\n", c.GSLBDivergenceSummary)
	} else {
		fmt.Fprintln(w, "→ gslb_divergence: false (all vantages returned identical answer sets)")
	}
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
