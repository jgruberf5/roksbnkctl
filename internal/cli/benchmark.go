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
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

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
	f.BoolVar(&flagBenchStreaming, "stream", false, "enable streaming mode")
	f.DurationVar(&flagBenchTimeout, "timeout", 5*time.Minute, "maximum time for the aiperf run")
	f.BoolVar(&flagBenchEnsure, "ensure-aiperf", false, "install aiperf on the jumphost before running (pip install aiperf)")

	// Multi-scenario mode
	f.StringVar(&flagBenchScenarios, "scenarios", "",
		`comma-separated preset names to run in sequence, or "all" for all presets.
Presets: latency, throughput, long-context, streaming.
When set, per-explicit flags (--concurrency/--num-requests/--isl/--osl/--stream) are ignored.`)

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

	// Wire under `awsbnkctl forge`
	forgeCmd.AddCommand(forgeBenchmarkCmd)
}

// runAiperfFn is the injectable seam for RunAiperf — allows CLI tests to stub
// out the real SSH call without replacing the jumphost package's internal seam.
var runAiperfFn = jumphost.RunAiperf

// pushBenchmarkResultFn is the injectable seam for PushBenchmarkResult.
var pushBenchmarkResultFn = forge.PushBenchmarkResult

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

	// Step 0: optionally register jumphost as forge SSH access-method.
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

	// ── Multi-scenario mode ──────────────────────────────────────────────────
	if len(presets) > 0 {
		return runBenchmarkScenarios(cmd, probOpts, forgeCreds, accessMethodName, presets)
	}

	// ── Single-run mode (original behaviour, unchanged) ──────────────────────
	return runBenchmarkSingle(cmd, probOpts, forgeCreds, accessMethodName)
}

// runBenchmarkSingle executes one benchmark run from the explicit CLI flags.
// This is the original three-step flow: RunAiperf → PushBenchmarkResult.
func runBenchmarkSingle(cmd *cobra.Command, probOpts jumphost.ProbeOptions, creds forge.RestCreds, agentName string) error {
	// Step 2: run aiperf.
	fmt.Fprintf(os.Stderr, "→ Running aiperf (concurrency=%d requests=%d) against %s\n",
		flagBenchConcurrency, flagBenchNumRequests, flagBenchVIP)

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
			Timeout:      flagBenchTimeout,
		},
		RunLabel: flagBenchRunLabel,
		ResultID: flagBenchResultID,
	}

	result, err := runAiperfFn(cmd.Context(), runOpts)
	if err != nil {
		return fmt.Errorf("aiperf run: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d requests succeeded (%.1fs)\n",
		result.Successful, result.TotalRequests, result.DurationSeconds)

	// Step 3: push to forge.
	fmt.Fprintf(os.Stderr, "→ Pushing result to forge at %s\n", flagBenchForgeURL)

	pushOpts := forge.BenchmarkPushOptions{
		RestURL:       flagBenchForgeURL,
		Creds:         creds,
		ResultID:      flagBenchResultID,
		RunLabel:      flagBenchRunLabel,
		Proxy:         flagBenchProxy,
		AgentName:     agentName,
		AgentHostname: flagBenchInstanceID,
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

	forgeResp, err := pushBenchmarkResultFn(cmd.Context(), result, pushOpts)
	if err != nil {
		return fmt.Errorf("forge push: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ pushed to forge: run_id=%d proxy=%s model=%s status=%s\n",
		forgeResp.RunID, forgeResp.Proxy, forgeResp.Model, forgeResp.Status)

	// Emit a JSON summary on stdout when -o json, otherwise a brief text line.
	if flagOutput == "json" {
		summary := map[string]any{
			"schema":   "awsbnkctl.benchmark.v1",
			"run_id":   forgeResp.RunID,
			"proxy":    forgeResp.Proxy,
			"model":    forgeResp.Model,
			"status":   forgeResp.Status,
			"forge":    flagBenchForgeURL,
			"vip":      flagBenchVIP,
			"requests": result.TotalRequests,
			"success":  result.Successful,
			"p50_s":    result.Latency.P50,
			"p99_s":    result.Latency.P99,
			"tps":      result.Throughput.TokensPerSec,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summary)
	}

	fmt.Printf("benchmark run_id=%d p50=%.3fs p99=%.3fs tps=%.1f success_rate=%.1f%%\n",
		forgeResp.RunID,
		result.Latency.P50,
		result.Latency.P99,
		result.Throughput.TokensPerSec,
		result.SuccessRatePct,
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

// runBenchmarkScenarios runs each preset in sequence, collecting outcomes.
// One preset failing does NOT abort the others. A summary table is printed
// at the end. Each preset is optionally registered as a forge BenchmarkConfig.
func runBenchmarkScenarios(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	presets []benchmarkPreset,
) error {
	outcomes := make([]scenarioOutcome, 0, len(presets))

	for _, preset := range presets {
		outcome := runOnePreset(cmd, probOpts, creds, agentName, preset)
		outcomes = append(outcomes, outcome)
	}

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
// runs aiperf, and pushes the result to forge.
func runOnePreset(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	preset benchmarkPreset,
) scenarioOutcome {
	label := presetRunLabel(flagBenchRunLabel, preset.Name)
	fmt.Fprintf(os.Stderr, "\n── preset: %s (%s) label=%s ──\n", preset.Name, preset.Description, label)

	// Best-effort: register the preset as a forge BenchmarkConfig.
	configJSON := map[string]any{
		"url":           fmt.Sprintf("http://%s", flagBenchVIP),
		"model":         flagBenchModel,
		"endpoint":      flagBenchEndpoint,
		"concurrency":   preset.Config.Concurrency,
		"request_count": preset.Config.NumRequests,
		"isl":           preset.Config.ISL,
		"osl":           preset.Config.OSL,
		"streaming":     preset.Config.Streaming,
	}
	cfgResp, cfgErr := forge.RegisterBenchmarkConfig(cmd.Context(), forge.BenchmarkConfigOptions{
		RestURL:     flagBenchForgeURL,
		Creds:       creds,
		Name:        fmt.Sprintf("awsbnkctl-%s", preset.Name),
		Description: preset.Description,
		ConfigJSON:  configJSON,
	})
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ forge config registration failed (non-fatal): %v\n", cfgErr)
	} else {
		fmt.Fprintf(os.Stderr, "✓ forge config registered: id=%d name=%s\n", cfgResp.ID, cfgResp.Name)
	}

	// Build AiperfRunOptions from the preset, inheriting shared flags.
	cfg := preset.Config
	cfg.Model = flagBenchModel
	cfg.EndpointPath = flagBenchEndpoint
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
	fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d succeeded (%.1fs)\n",
		result.Successful, result.TotalRequests, result.DurationSeconds)

	pushOpts := forge.BenchmarkPushOptions{
		RestURL:       flagBenchForgeURL,
		Creds:         creds,
		RunLabel:      label,
		Proxy:         flagBenchProxy,
		AgentName:     agentName,
		AgentHostname: flagBenchInstanceID,
		AiperfConfig:  configJSON,
	}

	forgeResp, pushErr := pushBenchmarkResultFn(cmd.Context(), result, pushOpts)
	if pushErr != nil {
		fmt.Fprintf(os.Stderr, "✗ preset %s forge push failed: %v\n", preset.Name, pushErr)
		return scenarioOutcome{Preset: preset.Name, Status: "PUSH_FAILED", Err: pushErr}
	}
	fmt.Fprintf(os.Stderr, "✓ pushed: run_id=%d status=%s\n", forgeResp.RunID, forgeResp.Status)

	return scenarioOutcome{
		Preset:   preset.Name,
		RunID:    forgeResp.RunID,
		ResultID: result.BaseURL, // carry forward for json output if needed
		Status:   "OK",
	}
}
