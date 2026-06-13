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
	flagBenchRegion      string
	flagBenchInstanceID  string
	flagBenchSourceIP    string
	flagBenchVIP         string
	flagBenchModel       string
	flagBenchEndpoint    string
	flagBenchConcurrency int
	flagBenchNumRequests int
	flagBenchISL         int
	flagBenchOSL         int
	flagBenchStreaming   bool
	flagBenchRunLabel    string
	flagBenchProxy       string
	flagBenchForgeURL    string
	flagBenchForgeUser   string
	flagBenchForgePass   string
	flagBenchEnsure      bool
	flagBenchResultID    string
	flagBenchTimeout     time.Duration
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
the aws CLI must be on PATH.`,
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

	// Labeling
	f.StringVar(&flagBenchRunLabel, "run-label", "", "human-readable label for this run (stored in forge)")
	f.StringVar(&flagBenchProxy, "proxy", "f5-bnk", "proxy label forwarded to forge (e.g. f5-bnk, envoy)")
	f.StringVar(&flagBenchResultID, "result-id", "", "result UUID (auto-generated when empty)")

	// Forge REST target
	f.StringVar(&flagBenchForgeURL, "forge-rest-url", "http://localhost:8000", "forge REST base URL")
	f.StringVar(&flagBenchForgeUser, "forge-user", "", "forge username (default: admin)")
	f.StringVar(&flagBenchForgePass, "forge-pass", "", "forge password (default: changeme)")

	// Wire under `awsbnkctl forge`
	forgeCmd.AddCommand(forgeBenchmarkCmd)
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

	probOpts := jumphost.ProbeOptions{
		Region:     flagBenchRegion,
		InstanceID: flagBenchInstanceID,
		SourceIP:   flagBenchSourceIP,
		VIP:        flagBenchVIP,
		User:       "ec2-user",
	}

	// Step 1: optionally ensure aiperf is installed.
	if flagBenchEnsure {
		fmt.Fprintln(os.Stderr, "→ Ensuring aiperf is installed on jumphost")
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
		defer cancel()
		if err := jumphost.EnsureAiperf(ctx, probOpts); err != nil {
			return fmt.Errorf("ensure aiperf: %w", err)
		}
		fmt.Fprintln(os.Stderr, "✓ aiperf ready")
	}

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

	result, err := jumphost.RunAiperf(cmd.Context(), runOpts)
	if err != nil {
		return fmt.Errorf("aiperf run: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d requests succeeded (%.1fs)\n",
		result.Successful, result.TotalRequests, result.DurationSeconds)

	// Step 3: push to forge.
	fmt.Fprintf(os.Stderr, "→ Pushing result to forge at %s\n", flagBenchForgeURL)

	pushOpts := forge.BenchmarkPushOptions{
		RestURL: flagBenchForgeURL,
		Creds: forge.RestCreds{
			Username: flagBenchForgeUser,
			Password: flagBenchForgePass,
		},
		ResultID:      flagBenchResultID,
		RunLabel:      flagBenchRunLabel,
		Proxy:         flagBenchProxy,
		AgentName:     "awsbnkctl-jumphost",
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

	forgeResp, err := forge.PushBenchmarkResult(cmd.Context(), result, pushOpts)
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
