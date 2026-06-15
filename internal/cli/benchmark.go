package cli

// Package cli — benchmark subcommand.
//
// awsbnkctl forge benchmark
//   Runs aiperf FROM the in-network jumphost (the only host that can reach
//   the BNK VIP on the external VLAN) against the VIP, then pushes the
//   result to a running forge instance.
//
// Prerequisites on the jumphost:
//   pip install aiperf          # one-time; or pass --ensure-aiperf
//
// Example:
//   awsbnkctl forge benchmark \
//     --region ap-southeast-2 \
//     --instance-id i-0abc123 \
//     --source-ip 10.0.11.50 \
//     --vip 10.0.10.100 \
//     --model meta-llama/Llama-3.1-8B-Instruct \
//     --forge-rest-url http://localhost:8000 \
//     --run-label nightly-ci
//
// Multi-scenario example:
//   awsbnkctl forge benchmark ... --scenarios latency,throughput
//   awsbnkctl forge benchmark ... --scenarios all

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

// discoverProxiesFn is the injectable seam for forge.DiscoverProxies.
var discoverProxiesFn = forge.DiscoverProxies

// listProxyDeploymentsFn is the injectable seam for forge.ListProxyDeployments.
var listProxyDeploymentsFn = forge.ListProxyDeployments

var (
	flagBenchRegion               string
	flagBenchInstanceID           string
	flagBenchSourceIP             string
	flagBenchVIP                  string
	flagBenchModel                string
	flagBenchEndpoint             string
	flagBenchConcurrency          int
	flagBenchNumRequests          int
	flagBenchISL                  int
	flagBenchOSL                  int
	flagBenchStreaming            bool
	flagBenchTokenizer            string
	flagBenchHostHeader           string
	flagBenchRunLabel             string
	flagBenchProxy                string
	flagBenchForgeURL             string
	flagBenchForgeUser            string
	flagBenchForgePass            string
	flagBenchEnsure               bool
	flagBenchResultID             string
	flagBenchTimeout              time.Duration
	flagBenchScenarios            string
	flagBenchRegisterAccessMethod bool
	flagBenchWorkspace            string // workspace name for forge_link.json cluster_id resolution
	flagBenchScenario             string // --scenario <key>: native forge scenario (WS-C1)
	flagBenchProxies              string // --proxies <csv>: shootout front-end list (WS-D1)
	flagBenchDirectPodIP          string // --direct-pod-ip <ip>: direct nodeport baseline (WS-D1)
	flagBenchConfig               string // --config/-f <cluster.yaml>: kubeconfig source for cluster lookups (e.g. bnk resync)
	flagBenchUpstreamService      string // --upstream-service: k8s Service the proxy fronts (for NLB opt-in tag)
	flagBenchUpstreamPort         string // --upstream-port: Service port (for NLB opt-in tag)
)

var forgeBenchmarkCmd = &cobra.Command{
	Use:   "benchmark",
	Short: "Run an aiperf benchmark from the jumphost and push results to forge",
	Long: `forgeBenchmark runs aiperf against the BNK Gateway VIP from the
jumphost (the only host with in-network access to the external VLAN), then
pushes the structured result to forge's benchmark ingestion endpoint
(POST /api/benchmarks/results).

Prerequisites on the jumphost:
  pip install aiperf      # one-time; or pass --ensure-aiperf to auto-install

The jumphost is reached via EC2 Instance Connect (EICE) — the operator's
IAM principal must have ec2-instance-connect:SendSSHPublicKey permission and
the aws CLI must be on PATH.

Use --scenarios to run multiple named presets in sequence:
  --scenarios latency,throughput   run two presets
  --scenarios all                  run all four built-in presets

Available presets: latency, throughput, long-context, streaming`,
	RunE: runForgeBenchmark,
}

func init() {
	f := forgeBenchmarkCmd.Flags()

	// EICE / jumphost identity
	f.StringVar(&flagBenchRegion, "region", "", "AWS region (e.g. ap-southeast-2) [required]")
	f.StringVar(&flagBenchInstanceID, "instance-id", "", "jumphost EC2 instance ID [required]")
	f.StringVar(&flagBenchSourceIP, "source-ip", "", "jumphost BNK external ENI IP (used as --interface so traffic enters the data path)")
	f.StringVar(&flagBenchVIP, "vip", "", "BNK Gateway VIP to benchmark (e.g. 10.0.10.100) [required]")

	// Benchmark config
	f.StringVar(&flagBenchModel, "model", "", "LLM model name (e.g. meta-llama/Llama-3.1-8B-Instruct) [required]")
	f.StringVar(&flagBenchEndpoint, "endpoint", "/v1/chat/completions", "API endpoint path")
	f.IntVar(&flagBenchConcurrency, "concurrency", 1, "number of concurrent users")
	f.IntVar(&flagBenchNumRequests, "num-requests", 10, "total number of requests")
	f.IntVar(&flagBenchISL, "isl", 512, "input sequence length (tokens)")
	f.IntVar(&flagBenchOSL, "osl", 128, "output sequence length (tokens)")
	f.BoolVar(&flagBenchStreaming, "stream", true, "enable streaming mode (default true — required for TTFT/ITL metrics)")
	f.StringVar(&flagBenchTokenizer, "tokenizer", "NousResearch/Meta-Llama-3-8B-Instruct",
		"Hugging Face tokenizer repo for aiperf token counting (required by aiperf 0.10.0)")
	f.StringVar(&flagBenchHostHeader, "host-header", "",
		"HTTP Host header to inject (--header Host:<value>); required when the BNK HTTPRoute has a hostname match")
	f.DurationVar(&flagBenchTimeout, "timeout", 5*time.Minute, "maximum time for the aiperf run")
	f.BoolVar(&flagBenchEnsure, "ensure-aiperf", false, "install aiperf on the jumphost before running (python3.11 -m pip install aiperf)")

	// Multi-scenario mode (legacy smoke presets)
	f.StringVar(&flagBenchScenarios, "scenarios", "",
		`comma-separated preset names to run in sequence, or "all" for all presets.
Presets: latency, throughput, long-context, streaming.
When set, per-explicit flags (--concurrency/--num-requests/--isl/--osl/--stream) are ignored.`)

	// Native forge scenario (WS-C1): expands into N linked child runs.
	// Mutually exclusive with --scenarios.
	scenarioUsage := fmt.Sprintf(
		"native forge scenario key to run (e.g. baseline, high-concurrency, prefix-cache).\n"+
			"Expands into N ordered child aiperf runs (concurrency sweep and/or phases).\n"+
			"Mutually exclusive with --scenarios.\n"+
			"Available keys: %s.",
		strings.Join(forgeScenarioKeys(), ", "),
	)
	f.StringVar(&flagBenchScenario, "scenario", "", scenarioUsage)

	// Labeling
	f.StringVar(&flagBenchRunLabel, "run-label", "", "human-readable label for this run (stored in forge)")
	f.StringVar(&flagBenchProxy, "proxy", "f5-bnk", "proxy label forwarded to forge (e.g. f5-bnk, envoy)")
	f.StringVar(&flagBenchResultID, "result-id", "", "result UUID (auto-generated when empty)")

	// Forge REST target
	f.StringVar(&flagBenchForgeURL, "forge-rest-url", "http://localhost:8000", "forge REST base URL")
	f.StringVar(&flagBenchForgeUser, "forge-user", "", "forge username (default: admin)")
	f.StringVar(&flagBenchForgePass, "forge-pass", "", "forge password (default: changeme)")

	// Access-method registration
	f.BoolVar(&flagBenchRegisterAccessMethod, "register-access-method", true,
		"register the jumphost as a forge SSH access-method record before running (best-effort, non-fatal)")

	// Object-graph linkage
	f.StringVar(&flagBenchWorkspace, "workspace", "",
		"workspace name (e.g. ai-rig) used to read forge_link.json for cluster_id when registering a BenchmarkTarget (best-effort, non-fatal when absent)")

	// Proxy shootout (WS-D1)
	f.StringVar(&flagBenchProxies, "proxies", "",
		`comma-separated list of forge proxy types to include in a shootout
(e.g. --proxies nodeport,envoy,haproxy,f5-bnk).
Valid types: envoy, nginx, haproxy, f5-bnk, nodeport.
When set, the chosen --scenario (or single-run flags) is run once per
front-end. When empty, single/scenario/preset mode runs unchanged.`)
	f.StringVar(&flagBenchDirectPodIP, "direct-pod-ip", "",
		`IP of the vLLM pod for a nodeport direct-baseline run (WS-D1).
When set, adds a 'nodeport' front-end that benchmarks aiperf against the
pod IP instead of the BNK VIP. Distinct from any VIP nodeport in --proxies.
AWS SG ingress rule for this path is out of scope (WS-E).`)
	f.StringVarP(&flagBenchConfig, "config", "f", "",
		`path to cluster.yaml (intent file); used to resolve the kubeconfig for
cluster lookups. Accepted but no longer required for --proxies (proxy
endpoints are now read from forge's external_url field).`)

	// NLB opt-in tags (Slice 4): forwarded to the forge BenchmarkTarget when
	// a non-BNK proxy is included in --proxies (triggers data-path NLB exposure).
	f.StringVar(&flagBenchUpstreamService, "upstream-service", "vllm",
		"k8s Service the proxy fronts (sets tags[upstream_service] on the forge BenchmarkTarget when --proxies includes a non-BNK proxy)")
	f.StringVar(&flagBenchUpstreamPort, "upstream-port", "80",
		"upstream Service port (sets tags[upstream_port] on the forge BenchmarkTarget when --proxies includes a non-BNK proxy)")

	// Wire under `awsbnkctl forge`
	forgeCmd.AddCommand(forgeBenchmarkCmd)
}

// runAiperfFn is the injectable seam for RunAiperf — allows CLI tests to stub
// out the real SSH call without replacing the jumphost package's internal seam.
var runAiperfFn = jumphost.RunAiperf

// checkServedModelFn is the injectable seam for CheckServedModel — allows CLI
// tests to stub out the EICE/SSH preflight without network access.
var checkServedModelFn = jumphost.CheckServedModel

// pushBenchmarkResultFn is the injectable seam for PushBenchmarkResult.
// Kept for backward-compat; primary push path is now pushRawAiperfResultFn.
var pushBenchmarkResultFn = forge.PushBenchmarkResult

// pushRawAiperfResultFn is the injectable seam for PushRawAiperfResult.
var pushRawAiperfResultFn = forge.PushRawAiperfResult

// registerBenchmarkAgentFn is the injectable seam for RegisterBenchmarkAgent.
var registerBenchmarkAgentFn = forge.RegisterBenchmarkAgent

// registerBenchmarkTargetFn is the injectable seam for RegisterBenchmarkTarget.
var registerBenchmarkTargetFn = forge.RegisterBenchmarkTarget

// registerBenchmarkConfigFn is the injectable seam for RegisterBenchmarkConfig.
var registerBenchmarkConfigFn = forge.RegisterBenchmarkConfig

// forgeGraph holds the resolved forge object IDs for linking runs.
// All fields are optional — zero means unset and will be omitted from pushes.
type forgeGraph struct {
	agentID           int // BenchmarkAgent.id (resolved from agent name)
	targetID          int // BenchmarkTarget.id
	configID          int // BenchmarkConfig.id (preset-specific, set per-run)
	proxyDeploymentID int // ProxyDeployment.id (set per-front-end in shootout mode)
}

// hasNonBNKProxy reports whether the comma-separated proxy CSV contains at
// least one entry that is not "f5-bnk". Blank entries are ignored.
// Returns false when csv is empty.
func hasNonBNKProxy(csv string) bool {
	for _, raw := range strings.Split(csv, ",") {
		t := strings.TrimSpace(raw)
		if t != "" && t != "f5-bnk" {
			return true
		}
	}
	return false
}

// nlbOptInTags returns the three opt-in internal-NLB tags required by forge
// PR #325 when the run is a non-BNK proxy shootout; nil otherwise.
// Tags are forwarded to the forge BenchmarkTarget so forge exposes the proxy
// front-end via an internal AWS NLB (Slice-3 contract).
func nlbOptInTags(proxiesCSV, upstreamService, upstreamPort string) map[string]string {
	if !hasNonBNKProxy(proxiesCSV) {
		return nil
	}
	return map[string]string{
		"proxy_expose":     "internal-nlb",
		"upstream_service": upstreamService,
		"upstream_port":    upstreamPort,
	}
}

// resolveForgeGraph registers the agent + target (best-effort, non-fatal) and
// returns the resolved IDs for threading into push options.
//
// agent_name: the name to register the jumphost under AND to pass as agent_name
// on every run push (forge resolves agent_id from agent_name server-side).
//
// cluster_id: resolved from forge_link.json in the workspace directory if
// --workspace is set; otherwise Target registration is skipped gracefully.
func resolveForgeGraph(ctx context.Context, creds forge.RestCreds, agentName string) forgeGraph {
	var g forgeGraph

	// ── Step A: Register BenchmarkAgent ─────────────────────────────────────
	agentResp, agentErr := registerBenchmarkAgentFn(ctx, forge.BenchmarkAgentOptions{
		RestURL:      flagBenchForgeURL,
		Creds:        creds,
		Name:         agentName,
		Hostname:     flagBenchInstanceID,
		IPAddress:    flagBenchSourceIP,
		Capabilities: []string{"aiperf"},
	})
	if agentErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ forge agent registration failed (non-fatal): %v\n", agentErr)
	} else {
		g.agentID = agentResp.ID
		fmt.Fprintf(os.Stderr, "✓ forge agent registered: id=%d name=%s\n", agentResp.ID, agentResp.Name)
	}

	// ── Step B: Resolve cluster_id from forge_link.json ──────────────────────
	clusterID := 0
	if flagBenchWorkspace != "" {
		wsDir := fmt.Sprintf(".awsbnkctl/%s", flagBenchWorkspace)
		if link, lerr := forge.ReadLink(wsDir); lerr == nil && link != nil {
			clusterID = link.ClusterID
			fmt.Fprintf(os.Stderr, "✓ resolved cluster_id=%d from workspace %q forge link\n", clusterID, flagBenchWorkspace)
		} else {
			fmt.Fprintf(os.Stderr, "⚠ could not read forge_link.json for workspace %q (non-fatal): %v\n", flagBenchWorkspace, lerr)
		}
	}

	// ── Step C: Register BenchmarkTarget ─────────────────────────────────────
	targetName := fmt.Sprintf("awsbnkctl-%s-%s", flagBenchWorkspace, flagBenchModel)
	if flagBenchWorkspace == "" {
		targetName = fmt.Sprintf("awsbnkctl-%s-%s", flagBenchInstanceID, flagBenchModel)
	}
	targetResp, targetErr := registerBenchmarkTargetFn(ctx, forge.BenchmarkTargetOptions{
		RestURL:    flagBenchForgeURL,
		Creds:      creds,
		Name:       targetName,
		ClusterID:  clusterID,
		LLMBaseURL: fmt.Sprintf("http://%s", flagBenchVIP),
		LLMModel:   flagBenchModel,
		Tags:       nlbOptInTags(flagBenchProxies, flagBenchUpstreamService, flagBenchUpstreamPort),
	})
	if targetErr != nil {
		if errors.Is(targetErr, forge.ErrTargetNoClusterID) {
			fmt.Fprintln(os.Stderr, "⚠ skipping forge target registration: no cluster_id (pass --workspace to enable)")
		} else {
			fmt.Fprintf(os.Stderr, "⚠ forge target registration failed (non-fatal): %v\n", targetErr)
		}
	} else {
		g.targetID = targetResp.ID
		fmt.Fprintf(os.Stderr, "✓ forge target registered: id=%d name=%s\n", targetResp.ID, targetResp.Name)
	}

	return g
}

func runForgeBenchmark(cmd *cobra.Command, _ []string) error {
	// Validate required flags.
	switch {
	case flagBenchRegion == "":
		return fmt.Errorf("--region is required")
	case flagBenchInstanceID == "":
		return fmt.Errorf("--instance-id is required")
	case flagBenchVIP == "":
		return fmt.Errorf("--vip is required")
	case flagBenchModel == "":
		return fmt.Errorf("--model is required")
	}

	// --scenario and --scenarios are mutually exclusive.
	if flagBenchScenario != "" && flagBenchScenarios != "" {
		return fmt.Errorf("--scenario and --scenarios are mutually exclusive; use one or the other")
	}

	probOpts := jumphost.ProbeOptions{
		Region:     flagBenchRegion,
		InstanceID: flagBenchInstanceID,
		SourceIP:   flagBenchSourceIP,
		VIP:        flagBenchVIP,
		User:       "ec2-user",
	}

	forgeCreds := forge.RestCreds{
		Username: flagBenchForgeUser,
		Password: flagBenchForgePass,
	}

	// Step 0a: optionally register jumphost as forge SSH access-method.
	accessMethodName := fmt.Sprintf("awsbnkctl-jumphost-%s", flagBenchInstanceID)
	if flagBenchRegisterAccessMethod {
		fmt.Fprintln(os.Stderr, "→ Registering jumphost as forge SSH access-method (record-only / EICE)")
		amResp, amErr := forge.RegisterJumphostAccessMethod(cmd.Context(), forge.AccessMethodOptions{
			RestURL:    flagBenchForgeURL,
			Creds:      forgeCreds,
			Name:       accessMethodName,
			Host:       flagBenchInstanceID,
			Region:     flagBenchRegion,
			InstanceID: flagBenchInstanceID,
		})
		if amErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ forge access-method registration failed (non-fatal): %v\n", amErr)
		} else {
			fmt.Fprintf(os.Stderr, "✓ forge access-method registered: id=%d name=%s\n", amResp.ID, amResp.Name)
		}
	}

	// Step 0b: register forge object graph (agent + target) — best-effort, non-fatal.
	fmt.Fprintln(os.Stderr, "→ Registering forge object graph (agent, target)")
	graph := resolveForgeGraph(cmd.Context(), forgeCreds, accessMethodName)

	// Parse --scenarios flag.
	presets, err := resolveBenchmarkScenarios(flagBenchScenarios)
	if err != nil {
		return err
	}

	// Step 1: optionally ensure aiperf is installed (once, regardless of scenario count).
	if flagBenchEnsure {
		fmt.Fprintln(os.Stderr, "→ Ensuring aiperf is installed on jumphost")
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
		defer cancel()
		if err := jumphost.EnsureAiperf(ctx, probOpts); err != nil {
			return fmt.Errorf("ensure aiperf: %w", err)
		}
		fmt.Fprintln(os.Stderr, "✓ aiperf ready")
	}

	// Step 1b: preflight — verify that --model is actually served by the endpoint.
	// This catches the common footgun where vLLM's --served-model-name differs from
	// the HF repo path (e.g. "llama3" vs "meta-llama/Meta-Llama-3-8B-Instruct").
	// Non-fatal on transport errors; fatal when the preflight succeeds but the model
	// is absent (avoids a silent all-failed aiperf run).
	//
	// In proxy shootout mode each front-end may have a different VIP — the preflight
	// is skipped here and run per-front-end in runShootoutFrontEnd instead.
	if flagBenchProxies == "" && flagBenchDirectPodIP == "" {
		fmt.Fprintf(os.Stderr, "→ Preflight: checking that model %q is served at %s\n", flagBenchModel, flagBenchVIP)
		if pfErr := checkServedModelFn(cmd.Context(), probOpts, flagBenchModel); pfErr != nil {
			return fmt.Errorf("served-model preflight: %w", pfErr)
		}
	}

	// ── Proxy shootout mode (WS-D1) ─────────────────────────────────────────
	if flagBenchProxies != "" || flagBenchDirectPodIP != "" {
		return runProxyShootout(cmd, probOpts, forgeCreds, accessMethodName, graph, presets)
	}

	// ── Native forge scenario mode (WS-C1) ──────────────────────────────────
	if flagBenchScenario != "" {
		return runNativeScenario(cmd, probOpts, forgeCreds, accessMethodName, graph, flagBenchScenario)
	}

	// ── Legacy smoke-preset mode ─────────────────────────────────────────────
	if len(presets) > 0 {
		return runBenchmarkScenarios(cmd, probOpts, forgeCreds, accessMethodName, graph, presets)
	}

	// ── Single-run mode ──────────────────────────────────────────────────────
	return runBenchmarkSingle(cmd, probOpts, forgeCreds, accessMethodName, graph)
}

// runBenchmarkSingle executes one benchmark run from the explicit CLI flags.
// Primary push path: RunAiperf → PushRawAiperfResult (rich ingest endpoint).
func runBenchmarkSingle(cmd *cobra.Command, probOpts jumphost.ProbeOptions, creds forge.RestCreds, agentName string, graph forgeGraph) error {
	// Step 2: run aiperf.
	fmt.Fprintf(os.Stderr, "→ Running aiperf (concurrency=%d requests=%d stream=%v) against %s\n",
		flagBenchConcurrency, flagBenchNumRequests, flagBenchStreaming, flagBenchVIP)

	runOpts := jumphost.AiperfRunOptions{
		ProbeOptions: probOpts,
		Config: jumphost.AiperfConfig{
			Model:        flagBenchModel,
			EndpointPath: flagBenchEndpoint,
			Concurrency:  flagBenchConcurrency,
			NumRequests:  flagBenchNumRequests,
			ISL:          flagBenchISL,
			OSL:          flagBenchOSL,
			Streaming:    flagBenchStreaming,
			Tokenizer:    flagBenchTokenizer,
			HostHeader:   flagBenchHostHeader,
			Timeout:      flagBenchTimeout,
		},
		RunLabel: flagBenchRunLabel,
		ResultID: flagBenchResultID,
	}

	result, err := runAiperfFn(cmd.Context(), runOpts)
	if err != nil {
		return fmt.Errorf("aiperf run: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d requests succeeded (%.1fs rps=%.3f)\n",
		result.Successful, result.TotalRequests, result.DurationSeconds, result.RequestThroughput)

	// Step 3: push raw JSON to forge rich-ingest endpoint.
	fmt.Fprintf(os.Stderr, "→ Pushing raw aiperf result to forge at %s\n", flagBenchForgeURL)

	rawPushOpts := forge.RawAiperfPushOptions{
		RestURL:           flagBenchForgeURL,
		Creds:             creds,
		RawJSON:           []byte(result.RawJSON),
		Proxy:             flagBenchProxy,
		Model:             flagBenchModel,
		URL:               fmt.Sprintf("http://%s", flagBenchVIP),
		AgentName:         agentName,
		RunLabel:          flagBenchRunLabel,
		TargetID:          graph.targetID,
		ConfigID:          graph.configID,
		ProxyDeploymentID: graph.proxyDeploymentID,
	}

	rawResp, rawErr := pushRawAiperfResultFn(cmd.Context(), rawPushOpts)
	if rawErr != nil {
		// Fall back to the structured push path so a missing/old forge endpoint
		// doesn't abort a successful benchmark run.
		fmt.Fprintf(os.Stderr, "⚠ raw aiperf push failed (%v); falling back to structured push\n", rawErr)
		pushOpts := forge.BenchmarkPushOptions{
			RestURL:           flagBenchForgeURL,
			Creds:             creds,
			ResultID:          flagBenchResultID,
			RunLabel:          flagBenchRunLabel,
			Proxy:             flagBenchProxy,
			AgentName:         agentName,
			AgentHostname:     flagBenchInstanceID,
			TargetID:          graph.targetID,
			ConfigID:          graph.configID,
			ProxyDeploymentID: graph.proxyDeploymentID,
			AiperfConfig: map[string]any{
				"model":        flagBenchModel,
				"endpoint":     flagBenchEndpoint,
				"concurrency":  flagBenchConcurrency,
				"num_requests": flagBenchNumRequests,
				"isl":          flagBenchISL,
				"osl":          flagBenchOSL,
				"streaming":    flagBenchStreaming,
			},
		}
		structResp, structErr := pushBenchmarkResultFn(cmd.Context(), result, pushOpts)
		if structErr != nil {
			return fmt.Errorf("forge push (raw+structured both failed): raw=%v structured=%w", rawErr, structErr)
		}
		fmt.Fprintf(os.Stderr, "✓ pushed to forge (structured fallback): run_id=%d proxy=%s model=%s status=%s\n",
			structResp.RunID, structResp.Proxy, structResp.Model, structResp.Status)
		// Surface the structured response for output below.
		rawResp = forge.RawAiperfPushResponse{
			ID:    structResp.ID,
			RunID: structResp.RunID,
			Proxy: structResp.Proxy,
			Model: structResp.Model,
		}
	} else {
		fmt.Fprintf(os.Stderr, "✓ pushed to forge (raw): run_id=%d proxy=%s model=%s status=%s\n",
			rawResp.RunID, rawResp.Proxy, rawResp.Model, rawResp.Status)
	}

	// Emit a JSON summary on stdout when -o json, otherwise a brief text line.
	if flagOutput == "json" {
		summary := map[string]any{
			"schema":   "awsbnkctl.benchmark.v1",
			"run_id":   rawResp.RunID,
			"proxy":    rawResp.Proxy,
			"model":    rawResp.Model,
			"status":   rawResp.Status,
			"forge":    flagBenchForgeURL,
			"vip":      flagBenchVIP,
			"requests": result.TotalRequests,
			"success":  result.Successful,
			"p50_ms":   result.RequestLatency.P50,
			"p99_ms":   result.RequestLatency.P99,
			"rps":      result.RequestThroughput,
			"tps":      result.OutputTokenThroughput,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summary)
	}

	successRatePct := 0.0
	if result.TotalRequests > 0 {
		successRatePct = float64(result.Successful) / float64(result.TotalRequests) * 100.0
	}
	fmt.Printf("benchmark run_id=%d p50=%.1fms p99=%.1fms rps=%.3f tps=%.1f success_rate=%.1f%%\n",
		rawResp.RunID,
		result.RequestLatency.P50,
		result.RequestLatency.P99,
		result.RequestThroughput,
		result.OutputTokenThroughput,
		successRatePct,
	)
	return nil
}

// scenarioOutcome records the result of a single preset run.
type scenarioOutcome struct {
	Preset   string
	RunID    int
	ResultID string
	Status   string
	Err      error
}

// runBenchmarkScenariosCollect runs each preset in sequence and returns the
// raw outcomes without printing a summary. One preset failing does NOT abort
// the others. Used by runBenchmarkScenarios (prints its own summary) and by
// runProxyShootout (aggregates across front-ends).
//
// proxy is the front-end label forwarded to forge (defaults to flagBenchProxy
// when empty). vip is the effective LLM host (defaults to flagBenchVIP when
// empty). Both are threaded through to runOnePreset → pushAiperfResult.
func runBenchmarkScenariosCollect(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	presets []benchmarkPreset,
	proxy, vip string,
) []scenarioOutcome {
	outcomes := make([]scenarioOutcome, 0, len(presets))
	for _, preset := range presets {
		outcome := runOnePreset(cmd, probOpts, creds, agentName, graph, preset, proxy, vip)
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

// runBenchmarkScenarios runs each preset in sequence, collecting outcomes.
// One preset failing does NOT abort the others. A summary table is printed
// at the end. Each preset is optionally registered as a forge BenchmarkConfig.
func runBenchmarkScenarios(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	presets []benchmarkPreset,
) error {
	// Non-shootout path: proxy and vip default to globals (empty strings).
	outcomes := runBenchmarkScenariosCollect(cmd, probOpts, creds, agentName, graph, presets, "", "")

	// Print summary table.
	fmt.Println()
	fmt.Printf("%-16s  %-10s  %-8s  %s\n", "PRESET", "STATUS", "RUN_ID", "NOTE")
	fmt.Printf("%-16s  %-10s  %-8s  %s\n", "------", "------", "------", "----")
	for _, o := range outcomes {
		note := ""
		if o.Err != nil {
			note = o.Err.Error()
			if len(note) > 60 {
				note = note[:57] + "..."
			}
		}
		fmt.Printf("%-16s  %-10s  %-8d  %s\n", o.Preset, o.Status, o.RunID, note)
	}

	// Return an error only if ALL presets failed.
	allFailed := true
	for _, o := range outcomes {
		if o.Err == nil {
			allFailed = false
			break
		}
	}
	if allFailed && len(outcomes) > 0 {
		return fmt.Errorf("all %d preset(s) failed", len(outcomes))
	}
	return nil
}

// runOnePreset executes a single preset: optionally registers the config,
// runs aiperf, and pushes the result to forge via the raw-JSON ingest path.
//
// proxy is the front-end label forwarded to forge (defaults to flagBenchProxy
// when empty). vip is the effective LLM host (defaults to flagBenchVIP when
// empty). Both are forwarded to pushAiperfResult so forge records the correct
// metadata for each front-end in a shootout.
func runOnePreset(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	preset benchmarkPreset,
	proxy, vip string,
) scenarioOutcome {
	label := presetRunLabel(flagBenchRunLabel, preset.Name)
	fmt.Fprintf(os.Stderr, "\n── preset: %s (%s) label=%s ──\n", preset.Name, preset.Description, label)

	// Effective VIP for config registration metadata (falls back to flagBenchVIP).
	effectiveVIP := vip
	if effectiveVIP == "" {
		effectiveVIP = flagBenchVIP
	}

	// Best-effort: register the preset as a forge BenchmarkConfig (idempotent).
	configJSON := map[string]any{
		"url":           fmt.Sprintf("http://%s", effectiveVIP),
		"model":         flagBenchModel,
		"endpoint":      flagBenchEndpoint,
		"concurrency":   preset.Config.Concurrency,
		"request_count": preset.Config.NumRequests,
		"isl":           preset.Config.ISL,
		"osl":           preset.Config.OSL,
		"streaming":     preset.Config.Streaming,
	}
	cfgResp, cfgErr := registerBenchmarkConfigFn(cmd.Context(), forge.BenchmarkConfigOptions{
		RestURL:     flagBenchForgeURL,
		Creds:       creds,
		Name:        fmt.Sprintf("awsbnkctl-%s", preset.Name),
		Description: preset.Description,
		ConfigJSON:  configJSON,
	})
	presetConfigID := graph.configID // inherit from graph (0 if unset)
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ forge config registration failed (non-fatal): %v\n", cfgErr)
	} else {
		fmt.Fprintf(os.Stderr, "✓ forge config registered: id=%d name=%s\n", cfgResp.ID, cfgResp.Name)
		presetConfigID = cfgResp.ID
	}

	// Build AiperfRunOptions from the preset, inheriting shared flags.
	cfg := preset.Config
	cfg.Model = flagBenchModel
	cfg.EndpointPath = flagBenchEndpoint
	cfg.Tokenizer = flagBenchTokenizer
	cfg.HostHeader = flagBenchHostHeader
	cfg.Timeout = flagBenchTimeout

	runOpts := jumphost.AiperfRunOptions{
		ProbeOptions: probOpts,
		Config:       cfg,
		RunLabel:     label,
	}

	fmt.Fprintf(os.Stderr, "→ Running aiperf (concurrency=%d requests=%d isl=%d osl=%d stream=%v)\n",
		cfg.Concurrency, cfg.NumRequests, cfg.ISL, cfg.OSL, cfg.Streaming)

	result, err := runAiperfFn(cmd.Context(), runOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ preset %s aiperf failed: %v\n", preset.Name, err)
		return scenarioOutcome{Preset: preset.Name, Status: "FAILED", Err: err}
	}
	fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d succeeded (%.1fs rps=%.3f)\n",
		result.Successful, result.TotalRequests, result.DurationSeconds, result.RequestThroughput)

	// Primary push path: raw JSON to the rich-ingest endpoint.
	// Pass proxy and vip so forge records the correct front-end label and URL.
	runID, pushErr := pushAiperfResult(cmd, result, creds, agentName, label, presetConfigID, graph.targetID, graph.proxyDeploymentID, proxy, vip, "", configJSON)
	if pushErr != nil {
		fmt.Fprintf(os.Stderr, "✗ preset %s forge push failed: %v\n", preset.Name, pushErr)
		return scenarioOutcome{Preset: preset.Name, Status: "PUSH_FAILED", Err: pushErr}
	}
	return scenarioOutcome{
		Preset:   preset.Name,
		RunID:    runID,
		ResultID: result.BaseURL,
		Status:   "OK",
	}
}

// pushAiperfResult is the shared raw-push + structured-fallback helper used by
// runOnePreset and runNativeScenario. It returns the forge run_id on success.
//
// proxyDeploymentID is stamped on both push paths (0 = unlinked / omitted).
// proxy is the label forwarded to forge (defaults to flagBenchProxy when empty).
// vip is the LLM base URL host; when empty flagBenchVIP is used.
// datasetName is optional — when non-empty it is forwarded as DatasetName on
// the raw push (used by the native scenario path to stamp <scenario key>).
// fallbackConfig is the map forwarded to the structured push path as AiperfConfig.
func pushAiperfResult(
	cmd *cobra.Command,
	result *jumphost.AiperfResult,
	creds forge.RestCreds,
	agentName, label string,
	configID, targetID, proxyDeploymentID int,
	proxy, vip string,
	datasetName string,
	fallbackConfig map[string]any,
) (int, error) {
	effectiveProxy := proxy
	if effectiveProxy == "" {
		effectiveProxy = flagBenchProxy
	}
	effectiveVIP := vip
	if effectiveVIP == "" {
		effectiveVIP = flagBenchVIP
	}

	rawPushOpts := forge.RawAiperfPushOptions{
		RestURL:           flagBenchForgeURL,
		Creds:             creds,
		RawJSON:           []byte(result.RawJSON),
		Proxy:             effectiveProxy,
		Model:             flagBenchModel,
		URL:               fmt.Sprintf("http://%s", effectiveVIP),
		AgentName:         agentName,
		RunLabel:          label,
		TargetID:          targetID,
		ConfigID:          configID,
		ProxyDeploymentID: proxyDeploymentID,
		DatasetName:       datasetName,
	}

	rawResp, pushErr := pushRawAiperfResultFn(cmd.Context(), rawPushOpts)
	if pushErr != nil {
		// Fall back to structured push so a missing/old forge endpoint does not
		// abort a successful benchmark run.
		fmt.Fprintf(os.Stderr, "⚠ raw push failed (%v); falling back to structured push\n", pushErr)
		pushOpts := forge.BenchmarkPushOptions{
			RestURL:           flagBenchForgeURL,
			Creds:             creds,
			RunLabel:          label,
			Proxy:             effectiveProxy,
			AgentName:         agentName,
			AgentHostname:     flagBenchInstanceID,
			TargetID:          targetID,
			ConfigID:          configID,
			ProxyDeploymentID: proxyDeploymentID,
			AiperfConfig:      fallbackConfig,
		}
		structResp, structErr := pushBenchmarkResultFn(cmd.Context(), result, pushOpts)
		if structErr != nil {
			return 0, fmt.Errorf("raw+structured both failed: raw=%v structured=%w", pushErr, structErr)
		}
		fmt.Fprintf(os.Stderr, "✓ pushed (structured fallback): run_id=%d status=%s\n", structResp.RunID, structResp.Status)
		return structResp.RunID, nil
	}

	fmt.Fprintf(os.Stderr, "✓ pushed (raw): run_id=%d status=%s\n", rawResp.RunID, rawResp.Status)
	return rawResp.RunID, nil
}

// nativeScenarioOutcome records the result of a single child run within a
// native forge scenario.
type nativeScenarioOutcome struct {
	VariantLabel string
	RunID        int
	Status       string
	Err          error
}

// runNativeScenarioCollect expands a forge-native scenario key into its child
// runs, executes each, and returns the raw outcomes without printing a summary.
// Used by runNativeScenario (prints its own summary) and runProxyShootout
// (aggregates across front-ends).
//
// proxy is the front-end label forwarded to forge (defaults to flagBenchProxy
// when empty). vip is the effective LLM host (defaults to flagBenchVIP when
// empty). Both are forwarded to pushAiperfResult so forge records the correct
// metadata for each front-end in a shootout.
func runNativeScenarioCollect(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	scenarioKey string,
	scenarioConfigID int,
	proxy, vip string,
) ([]nativeScenarioOutcome, error) {
	baseCfg := jumphost.AiperfConfig{
		Model:        flagBenchModel,
		EndpointPath: flagBenchEndpoint,
		Tokenizer:    flagBenchTokenizer,
		HostHeader:   flagBenchHostHeader,
		Timeout:      flagBenchTimeout,
	}

	children, err := expandForgeScenario(scenarioKey, baseCfg)
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "→ native scenario %q: %d child run(s)\n", scenarioKey, len(children))

	outcomes := make([]nativeScenarioOutcome, 0, len(children))

	for _, child := range children {
		label := presetRunLabel(flagBenchRunLabel, scenarioKey+"-"+child.VariantLabel)
		fmt.Fprintf(os.Stderr, "\n── child: %s label=%s ──\n", child.VariantLabel, label)

		cfg := child.Config
		if cfg.SeqDist != "" {
			fmt.Fprintf(os.Stderr, "→ Running aiperf (concurrency=%d requests=%d seq-dist=%q stream=%v)\n",
				cfg.Concurrency, cfg.NumRequests, cfg.SeqDist, cfg.Streaming)
		} else {
			fmt.Fprintf(os.Stderr, "→ Running aiperf (concurrency=%d requests=%d isl=%d osl=%d stream=%v)\n",
				cfg.Concurrency, cfg.NumRequests, cfg.ISL, cfg.OSL, cfg.Streaming)
		}

		runOpts := jumphost.AiperfRunOptions{
			ProbeOptions: probOpts,
			Config:       cfg,
			RunLabel:     label,
		}

		result, aiperfErr := runAiperfFn(cmd.Context(), runOpts)
		if aiperfErr != nil {
			fmt.Fprintf(os.Stderr, "✗ child %s aiperf failed: %v\n", child.VariantLabel, aiperfErr)
			outcomes = append(outcomes, nativeScenarioOutcome{
				VariantLabel: child.VariantLabel,
				Status:       "FAILED",
				Err:          aiperfErr,
			})
			continue
		}
		fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d succeeded (%.1fs rps=%.3f)\n",
			result.Successful, result.TotalRequests, result.DurationSeconds, result.RequestThroughput)

		// Effective VIP for fallback config metadata (falls back to flagBenchVIP).
		effectiveVIP := vip
		if effectiveVIP == "" {
			effectiveVIP = flagBenchVIP
		}

		fallbackCfg := map[string]any{
			"scenario_key":  scenarioKey,
			"variant_label": child.VariantLabel,
			"url":           fmt.Sprintf("http://%s", effectiveVIP),
			"model":         flagBenchModel,
			"concurrency":   cfg.Concurrency,
			"request_count": cfg.NumRequests,
			"streaming":     cfg.Streaming,
		}

		datasetName := scenarioKey
		pushLabel := label
		if scenarioKey == "mooncake" {
			datasetName = "mooncake-toolagent"
			if !strings.Contains(pushLabel, "tokenizer_substituted") {
				pushLabel = pushLabel + "+tokenizer_substituted"
			}
		}

		// Pass proxy and vip so forge records the correct front-end label and URL.
		runID, pushErr := pushAiperfResult(cmd, result, creds, agentName, pushLabel, scenarioConfigID, graph.targetID, graph.proxyDeploymentID, proxy, vip, datasetName, fallbackCfg)
		if pushErr != nil {
			fmt.Fprintf(os.Stderr, "✗ child %s push failed: %v\n", child.VariantLabel, pushErr)
			outcomes = append(outcomes, nativeScenarioOutcome{
				VariantLabel: child.VariantLabel,
				Status:       "PUSH_FAILED",
				Err:          pushErr,
			})
			continue
		}
		outcomes = append(outcomes, nativeScenarioOutcome{
			VariantLabel: child.VariantLabel,
			RunID:        runID,
			Status:       "OK",
		})
	}
	return outcomes, nil
}

// runNativeScenario expands a forge-native scenario key into its child runs,
// executes each as a separate aiperf invocation, and pushes every child to
// forge via the raw /aiperf path with DatasetName=<scenarioKey>.
//
// One child failing does NOT abort the rest. A summary table is printed at the
// end. Returns an error only when all children fail.
func runNativeScenario(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	scenarioKey string,
) error {
	// Look up the scenario's human-readable description for the forge config record.
	var scenarioDescription string
	if forgeScn, ok := forgeScenarioByKey(scenarioKey); ok {
		scenarioDescription = forgeScn.Description
	}

	// Register one BenchmarkConfig for the whole scenario (idempotent).
	scenarioConfigJSON := forgeScenarioConfigJSON(scenarioKey, flagBenchModel, flagBenchVIP)
	scenarioConfigName := fmt.Sprintf("awsbnkctl-scenario-%s", scenarioKey)
	cfgResp, cfgErr := registerBenchmarkConfigFn(cmd.Context(), forge.BenchmarkConfigOptions{
		RestURL:     flagBenchForgeURL,
		Creds:       creds,
		Name:        scenarioConfigName,
		Description: scenarioDescription,
		ConfigJSON:  scenarioConfigJSON,
	})
	scenarioConfigID := graph.configID // inherit from graph (0 if unset)
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ forge config registration failed (non-fatal): %v\n", cfgErr)
	} else {
		fmt.Fprintf(os.Stderr, "✓ forge config registered: id=%d name=%s\n", cfgResp.ID, cfgResp.Name)
		scenarioConfigID = cfgResp.ID
	}

	// Non-shootout path: proxy and vip default to globals (empty strings).
	outcomes, err := runNativeScenarioCollect(cmd, probOpts, creds, agentName, graph, scenarioKey, scenarioConfigID, "", "")
	if err != nil {
		return err
	}

	// Summary table.
	fmt.Println()
	fmt.Printf("scenario: %s\n", scenarioKey)
	fmt.Printf("%-30s  %-10s  %-8s  %s\n", "VARIANT", "STATUS", "RUN_ID", "NOTE")
	fmt.Printf("%-30s  %-10s  %-8s  %s\n", "-------", "------", "------", "----")
	for _, o := range outcomes {
		note := ""
		if o.Err != nil {
			note = o.Err.Error()
			if len(note) > 50 {
				note = note[:47] + "..."
			}
		}
		fmt.Printf("%-30s  %-10s  %-8d  %s\n", o.VariantLabel, o.Status, o.RunID, note)
	}

	// Fail only when all children failed.
	allFailed := true
	for _, o := range outcomes {
		if o.Err == nil {
			allFailed = false
			break
		}
	}
	if allFailed && len(outcomes) > 0 {
		return fmt.Errorf("all %d child run(s) failed for scenario %q", len(outcomes), scenarioKey)
	}
	return nil
}

// ── Proxy shootout (WS-D1) ──────────────────────────────────────────────────

// shootoutFrontEnd describes one front-end in a proxy shootout.
type shootoutFrontEnd struct {
	// ProxyType is the forge canonical proxy type label (e.g. "envoy", "f5-bnk").
	ProxyType string
	// VIP overrides flagBenchVIP when non-empty (used by --direct-pod-ip and envoy NodePort).
	VIP string
	// ProxyDeploymentID is the resolved forge id (0 = unlinked).
	ProxyDeploymentID int
	// ResolveErr is set when endpoint resolution failed for this front-end.
	// runProxyShootout marks the leg RESOLVE_FAILED and continues other front-ends.
	ResolveErr error
}

// shootoutOutcome records the aggregated result for one front-end.
type shootoutOutcome struct {
	ProxyType         string
	ProxyDeploymentID int
	RunIDs            []int
	Status            string
	Err               error
}

// resolveShootoutFrontEnds builds the ordered list of front-ends from the
// --proxies CSV flag and --direct-pod-ip. It runs discover + list against
// forge to resolve ProxyDeploymentIDs (best-effort; 0 when unavailable).
//
// Returns an error immediately when any --proxies entry is not a recognised
// forge proxy type (e.g. a typo). Valid-but-undiscovered types (cluster has
// no running proxy of that kind) still proceed as unlinked front-ends.
//
// For non-f5-bnk proxy types: reads the front-end's reachable address from
// the discovered ProxyDeployment's external_url field (forge's "NodePort/LB
// URL for external agents", jumphost-reachable). When external_url is empty
// (discovery not yet populated, or LB ingress not provisioned) the front-end
// is returned with ResolveErr set — the caller marks it RESOLVE_FAILED. This
// is fail-closed: NEVER silently fall back to the global --vip (that would
// reintroduce the "silent BNK bake-off" bug). Other front-ends still run.
//
// f5-bnk: has no proxy Service so external_url is null by design. VIP stays
// empty and runProxyShootout falls back to the global --vip flag.
//
// The direct-pod-ip nodeport front-end is always appended last and is kept
// distinct from any VIP nodeport in --proxies.
func resolveShootoutFrontEnds(ctx context.Context, creds forge.RestCreds, targetID int) ([]shootoutFrontEnd, error) {
	var frontEnds []shootoutFrontEnd

	// Parse --proxies CSV.
	var proxyTypes []string
	for _, raw := range strings.Split(flagBenchProxies, ",") {
		t := strings.TrimSpace(raw)
		if t != "" {
			proxyTypes = append(proxyTypes, t)
		}
	}

	// Fail fast on unknown proxy type names (typo guard).
	for _, pt := range proxyTypes {
		if !forge.IsValidProxyType(pt) {
			return nil, fmt.Errorf("--proxies: unknown proxy type %q; valid types: envoy, f5-bnk, haproxy, nginx, nodeport", pt)
		}
	}

	// Best-effort: discover + list proxies from forge when we have a target.
	// Run when either --proxies is non-empty OR --direct-pod-ip is set so that
	// a nodeport-only direct-pod-ip run can still resolve its ProxyDeploymentID.
	var knownProxies []forge.ProxyDeployment
	if targetID != 0 && (len(proxyTypes) > 0 || flagBenchDirectPodIP != "") {
		discOpts := forge.ProxyDiscoverOptions{
			RestURL:  flagBenchForgeURL,
			Creds:    creds,
			TargetID: targetID,
		}
		if _, discErr := discoverProxiesFn(ctx, discOpts); discErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ forge discover-proxies failed (non-fatal, running unlinked): %v\n", discErr)
		}
		listed, listErr := listProxyDeploymentsFn(ctx, discOpts)
		if listErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ forge list-proxies failed (non-fatal, running unlinked): %v\n", listErr)
		} else {
			knownProxies = listed
		}
	}

	// Build VIP front-ends.
	for _, pt := range proxyTypes {
		dep := forge.FindProxyDeployment(knownProxies, pt)
		id := 0
		if dep != nil {
			id = dep.ID
		}
		fe := shootoutFrontEnd{
			ProxyType:         pt,
			ProxyDeploymentID: id,
		}

		// f5-bnk has no proxy Service → external_url is null by design.
		// Leave VIP empty; runProxyShootout falls back to the global --vip flag.
		//
		// All other proxy types read their jumphost-reachable address from
		// external_url. Fail-closed: an empty external_url (discovery not yet
		// run or LB ingress not provisioned) marks the leg RESOLVE_FAILED.
		// NEVER fall back to the global --vip (that silently benchmarks the BNK VIP).
		if pt != "f5-bnk" {
			if dep == nil || dep.ExternalURL == "" {
				if dep == nil {
					fe.ResolveErr = fmt.Errorf("%s front-end: no ProxyDeployment found in forge (run discover-proxies first)", pt)
				} else {
					fe.ResolveErr = fmt.Errorf("%s front-end: external_url is empty (forge discovery has not populated it yet; check Service type and LB ingress)", pt)
				}
				fmt.Fprintf(os.Stderr, "⚠ %v\n", fe.ResolveErr)
			} else {
				fe.VIP = dep.ExternalURL
				fmt.Fprintf(os.Stderr, "✓ %s endpoint resolved from forge external_url: %s\n", pt, fe.VIP)
			}
		}

		frontEnds = append(frontEnds, fe)
	}

	// Append direct-pod-ip nodeport front-end (always last, always distinct).
	if flagBenchDirectPodIP != "" {
		// Resolve the proxy_deployment_id for "nodeport" from the same list
		// (best-effort; may be different from any VIP nodeport front-end above,
		// or the same id if there is only one nodeport record).
		id := forge.ResolveProxyDeploymentID(knownProxies, "nodeport")
		frontEnds = append(frontEnds, shootoutFrontEnd{
			ProxyType:         "nodeport",
			VIP:               flagBenchDirectPodIP,
			ProxyDeploymentID: id,
		})
	}

	return frontEnds, nil
}

// runProxyShootout orchestrates the proxy shootout: runs the chosen
// --scenario (or presets / single-run) once per front-end, stamping
// the per-front-end proxy_deployment_id on every push, then prints a
// summary table with real run_ids from each front-end.
//
// One front-end failing does NOT abort the others (mirrors runBenchmarkScenarios).
func runProxyShootout(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	presets []benchmarkPreset,
) error {
	frontEnds, err := resolveShootoutFrontEnds(cmd.Context(), creds, graph.targetID)
	if err != nil {
		return err
	}
	if len(frontEnds) == 0 {
		return fmt.Errorf("--proxies is empty and --direct-pod-ip is not set; nothing to shoot out")
	}

	fmt.Fprintf(os.Stderr, "→ proxy shootout: %d front-end(s)\n", len(frontEnds))

	outcomes := make([]shootoutOutcome, 0, len(frontEnds))

	for _, fe := range frontEnds {
		// Fail-closed: a front-end whose endpoint resolution failed is marked
		// RESOLVE_FAILED immediately — never runs, never touches the default VIP.
		if fe.ResolveErr != nil {
			outcomes = append(outcomes, shootoutOutcome{
				ProxyType:         fe.ProxyType,
				ProxyDeploymentID: fe.ProxyDeploymentID,
				Status:            "RESOLVE_FAILED",
				Err:               fe.ResolveErr,
			})
			continue
		}

		// Copy graph with the per-front-end proxy id; never mutate globals.
		g := graph
		g.proxyDeploymentID = fe.ProxyDeploymentID

		// When the front-end has a specific VIP (direct-pod-ip or external_url),
		// override the probe VIP for this front-end's runs.
		feProbeOpts := probOpts
		if fe.VIP != "" {
			feProbeOpts.VIP = fe.VIP
		}

		linked := "unlinked"
		if fe.ProxyDeploymentID != 0 {
			linked = fmt.Sprintf("id=%d", fe.ProxyDeploymentID)
		}
		fmt.Fprintf(os.Stderr, "\n══ front-end: %s (%s) vip=%s ══\n",
			fe.ProxyType, linked, feProbeOpts.VIP)

		oc := runShootoutFrontEnd(cmd, feProbeOpts, creds, agentName, g, fe.ProxyType, fe.VIP, presets)
		outcomes = append(outcomes, oc)
	}

	// Outer summary table.
	fmt.Println()
	fmt.Printf("proxy shootout summary\n")
	fmt.Printf("%-14s  %-12s  %-8s  %-8s  %s\n", "PROXY", "PROXY_DEP_ID", "STATUS", "RUN_IDS", "NOTE")
	fmt.Printf("%-14s  %-12s  %-8s  %-8s  %s\n", "-----", "------------", "------", "-------", "----")
	for _, o := range outcomes {
		depID := "unlinked"
		if o.ProxyDeploymentID != 0 {
			depID = fmt.Sprintf("%d", o.ProxyDeploymentID)
		}
		runIDs := formatRunIDs(o.RunIDs)
		note := ""
		if o.Err != nil {
			note = o.Err.Error()
			if len(note) > 50 {
				note = note[:47] + "..."
			}
		}
		fmt.Printf("%-14s  %-12s  %-8s  %-8s  %s\n", o.ProxyType, depID, o.Status, runIDs, note)
	}

	// Fail only when all front-ends failed.
	allFailed := true
	for _, o := range outcomes {
		if o.Err == nil {
			allFailed = false
			break
		}
	}
	if allFailed && len(outcomes) > 0 {
		return fmt.Errorf("all %d front-end(s) failed in proxy shootout", len(outcomes))
	}
	return nil
}

// runShootoutFrontEnd runs one front-end's workload (native scenario, presets,
// or single run) and returns a shootoutOutcome with the collected run_ids.
// The proxy label and VIP are taken from proxyType and vip (not from globals).
func runShootoutFrontEnd(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	proxyType, vip string,
	presets []benchmarkPreset,
) shootoutOutcome {
	base := shootoutOutcome{ProxyType: proxyType, ProxyDeploymentID: graph.proxyDeploymentID}

	// Per-front-end served-model preflight.  Each front-end may target a
	// different VIP (e.g. nodeport direct-pod-ip), so we preflight here with
	// the effective per-front-end probOpts (which already has VIP set by the
	// caller for direct-pod-ip front-ends).
	fmt.Fprintf(os.Stderr, "→ Preflight: checking model %q at vip=%s (front-end=%s)\n",
		flagBenchModel, probOpts.VIP, proxyType)
	if pfErr := checkServedModelFn(cmd.Context(), probOpts, flagBenchModel); pfErr != nil {
		base.Status = "PREFLIGHT_FAILED"
		base.Err = fmt.Errorf("served-model preflight: %w", pfErr)
		return base
	}

	switch {
	case flagBenchScenario != "":
		// Native scenario mode: register config, collect child outcomes.
		var scenarioDescription string
		if forgeScn, ok := forgeScenarioByKey(flagBenchScenario); ok {
			scenarioDescription = forgeScn.Description
		}
		scenarioConfigJSON := forgeScenarioConfigJSON(flagBenchScenario, flagBenchModel, flagBenchVIP)
		scenarioConfigName := fmt.Sprintf("awsbnkctl-scenario-%s", flagBenchScenario)
		cfgResp, cfgErr := registerBenchmarkConfigFn(cmd.Context(), forge.BenchmarkConfigOptions{
			RestURL:     flagBenchForgeURL,
			Creds:       creds,
			Name:        scenarioConfigName,
			Description: scenarioDescription,
			ConfigJSON:  scenarioConfigJSON,
		})
		scenarioConfigID := graph.configID
		if cfgErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ forge config registration failed (non-fatal): %v\n", cfgErr)
		} else {
			fmt.Fprintf(os.Stderr, "✓ forge config registered: id=%d name=%s\n", cfgResp.ID, cfgResp.Name)
			scenarioConfigID = cfgResp.ID
		}
		g := graph
		g.configID = scenarioConfigID
		// Pass proxyType and vip so forge records correct per-front-end metadata.
		children, collectErr := runNativeScenarioCollect(cmd, probOpts, creds, agentName, g, flagBenchScenario, scenarioConfigID, proxyType, vip)
		if collectErr != nil {
			base.Status = "FAILED"
			base.Err = collectErr
			return base
		}
		var runIDs []int
		allFailed := true
		for _, ch := range children {
			if ch.Err == nil {
				allFailed = false
				if ch.RunID != 0 {
					runIDs = append(runIDs, ch.RunID)
				}
			}
		}
		base.RunIDs = runIDs
		if allFailed && len(children) > 0 {
			base.Status = "FAILED"
			base.Err = fmt.Errorf("all %d child run(s) failed", len(children))
		} else {
			base.Status = "OK"
		}
		return base

	case len(presets) > 0:
		// Preset mode: collect per-preset outcomes.
		// Pass proxyType and vip so forge records correct per-front-end metadata.
		presOutcomes := runBenchmarkScenariosCollect(cmd, probOpts, creds, agentName, graph, presets, proxyType, vip)
		var runIDs []int
		allFailed := true
		for _, o := range presOutcomes {
			if o.Err == nil {
				allFailed = false
				if o.RunID != 0 {
					runIDs = append(runIDs, o.RunID)
				}
			}
		}
		base.RunIDs = runIDs
		if allFailed && len(presOutcomes) > 0 {
			base.Status = "FAILED"
			base.Err = fmt.Errorf("all %d preset(s) failed", len(presOutcomes))
		} else {
			base.Status = "OK"
		}
		return base

	default:
		// Single-run mode: run aiperf and push.
		effectiveVIP := vip
		if effectiveVIP == "" {
			effectiveVIP = flagBenchVIP
		}
		runOpts := jumphost.AiperfRunOptions{
			ProbeOptions: probOpts,
			Config: jumphost.AiperfConfig{
				Model:        flagBenchModel,
				EndpointPath: flagBenchEndpoint,
				Concurrency:  flagBenchConcurrency,
				NumRequests:  flagBenchNumRequests,
				ISL:          flagBenchISL,
				OSL:          flagBenchOSL,
				Streaming:    flagBenchStreaming,
				Tokenizer:    flagBenchTokenizer,
				HostHeader:   flagBenchHostHeader,
				Timeout:      flagBenchTimeout,
			},
			RunLabel: flagBenchRunLabel,
			ResultID: flagBenchResultID,
		}
		result, aiperfErr := runAiperfFn(cmd.Context(), runOpts)
		if aiperfErr != nil {
			base.Status = "FAILED"
			base.Err = aiperfErr
			return base
		}
		fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d succeeded (%.1fs rps=%.3f)\n",
			result.Successful, result.TotalRequests, result.DurationSeconds, result.RequestThroughput)
		runID, pushErr := pushAiperfResult(
			cmd, result, creds, agentName, flagBenchRunLabel,
			graph.configID, graph.targetID, graph.proxyDeploymentID,
			proxyType, effectiveVIP,
			"", nil,
		)
		if pushErr != nil {
			base.Status = "PUSH_FAILED"
			base.Err = pushErr
			return base
		}
		base.RunIDs = []int{runID}
		base.Status = "OK"
		return base
	}
}

// formatRunIDs formats a slice of run ids for display (e.g. "42,43,44").
// Returns "-" when the slice is empty.
func formatRunIDs(ids []int) string {
	if len(ids) == 0 {
		return "-"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, ",")
}
