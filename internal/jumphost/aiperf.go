package jumphost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AiperfConfig holds the parameters for a single aiperf benchmark run.
// aiperf is assumed to be pre-installed on the jumphost (pip install aiperf).
// All fields except Model and VIP have sensible defaults.
type AiperfConfig struct {
	// Model is the LLM model name (e.g. "meta-llama/Llama-3.1-8B-Instruct").
	Model string
	// EndpointPath is the HTTP path (e.g. "/v1/chat/completions"). Default: "/v1/chat/completions".
	EndpointPath string
	// Concurrency is the number of concurrent users. Default: 1.
	Concurrency int
	// NumRequests is the total number of requests to send. Default: 10.
	NumRequests int
	// ISL is the input sequence length (tokens). Default: 512.
	ISL int
	// OSL is the output sequence length (tokens). Default: 128.
	OSL int
	// Streaming enables streaming mode (--stream). Default: false.
	Streaming bool
	// Timeout is the per-run total timeout passed to the runner. Default: 5 minutes.
	Timeout time.Duration
}

// AiperfResult is the Go representation of the aiperf JSON output we consume.
// aiperf --output-format json emits a flat top-level object; we capture the
// fields we forward to forge.  Unknown extra fields are silently ignored.
type AiperfResult struct {
	// Identity — forwarded from config
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	Endpoint string `json:"endpoint"`

	// Timing
	RunStart        string  `json:"run_start"`
	RunEnd          string  `json:"run_end"`
	DurationSeconds float64 `json:"duration_seconds"`
	DurationMinutes float64 `json:"duration_minutes"`

	// Request counts
	TotalRequests  int     `json:"total_requests"`
	Successful     int     `json:"successful"`
	Failed         int     `json:"failed"`
	SuccessRatePct float64 `json:"success_rate_pct"`

	// Token counts
	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
	AvgInputTokens    float64 `json:"avg_input_tokens"`
	AvgOutputTokens   float64 `json:"avg_output_tokens"`

	// Latency (aiperf nests these under a "latency" key)
	Latency LatencyStats `json:"latency"`

	// Throughput
	Throughput ThroughputStats `json:"throughput"`

	// Per-phase breakdown (forwarded verbatim)
	Phases map[string]any `json:"phases"`

	// Optional timeline (can be very large; forwarded as-is)
	Timeline []any `json:"timeline,omitempty"`
}

// LatencyStats mirrors aiperf's nested latency object.
type LatencyStats struct {
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Mean float64 `json:"mean"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`

	// aiperf-specific token-level metrics (zero-valued when not available)
	TTFT LatencyDistribution `json:"ttft,omitempty"`
	ITL  LatencyDistribution `json:"itl,omitempty"`
}

// LatencyDistribution is a named per-metric distribution inside aiperf latency.
type LatencyDistribution struct {
	Mean float64 `json:"mean"`
	P50  float64 `json:"p50"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
}

// ThroughputStats mirrors aiperf's throughput object.
type ThroughputStats struct {
	OverallRPS float64 `json:"overall_rps"`
	PeakRPS    float64 `json:"peak_rps"`
	// Tokens per second across all users
	TokensPerSec float64 `json:"tokens_per_sec"`
}

// sshExecFn is the injectable seam for running a remote command over
// EICE-SSH and capturing its stdout.  Default: SSHRunViaEICE.
// Tests replace this via aiperfSSHExecFn (exposed in aiperf_export_test.go).
var aiperfSSHExecFn = func(ctx context.Context, region, instanceID, keyPath, remoteCmd string) (string, error) {
	return SSHRunViaEICE(ctx, region, instanceID, keyPath, remoteCmd)
}

// AiperfRunOptions is the full set of parameters RunAiperf needs.
type AiperfRunOptions struct {
	// ProbeOptions carries the EICE / jumphost identity.
	ProbeOptions ProbeOptions
	// Config is the benchmark configuration.
	Config AiperfConfig
	// RunLabel is a human-readable label stored in the result labels.
	RunLabel string
	// ResultID is a caller-supplied unique ID (UUID or timestamp string).
	// When empty, aiperf generates its own; we read it back from the JSON.
	ResultID string
}

// buildAiperfCmd constructs the remote shell command that runs aiperf on the
// jumphost and emits JSON to stdout. The VIP is taken from opts.ProbeOptions.VIP
// and combined with cfg.EndpointPath to form the full base_url.
//
// aiperf prereq: the jumphost must have aiperf installed (pip install aiperf).
// Install is NOT performed here — see RunAiperf docstring.
func buildAiperfCmd(opts AiperfRunOptions) string {
	cfg := opts.Config
	if cfg.EndpointPath == "" {
		cfg.EndpointPath = "/v1/chat/completions"
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}
	if cfg.NumRequests <= 0 {
		cfg.NumRequests = 10
	}
	if cfg.ISL <= 0 {
		cfg.ISL = 512
	}
	if cfg.OSL <= 0 {
		cfg.OSL = 128
	}

	vip := opts.ProbeOptions.VIP
	// Use the external-VLAN source IP as the interface so traffic
	// follows the real BNK data path (mirrors SSHCurlViaEICE behaviour).
	srcIP := opts.ProbeOptions.SourceIP

	// Build the aiperf command. We use --output-format json so we can
	// parse the structured output back over SSH stdout.
	args := []string{
		"aiperf",
		"--base-url", fmt.Sprintf("http://%s", vip),
		"--model", shellSingleQuote(cfg.Model),
		"--endpoint", shellSingleQuote(cfg.EndpointPath),
		"--num-users", fmt.Sprintf("%d", cfg.Concurrency),
		"--num-requests", fmt.Sprintf("%d", cfg.NumRequests),
		"--input-len", fmt.Sprintf("%d", cfg.ISL),
		"--output-len", fmt.Sprintf("%d", cfg.OSL),
		"--output-format", "json",
	}
	if cfg.Streaming {
		args = append(args, "--stream")
	}
	if srcIP != "" {
		// bind the HTTP client to the BNK external ENI so traffic enters
		// via the external VLAN (same pattern as --interface in curl).
		args = append(args, "--interface", srcIP)
	}
	if opts.RunLabel != "" {
		args = append(args, "--run-label", shellSingleQuote(opts.RunLabel))
	}

	return strings.Join(args, " ")
}

// RunAiperf mints an ephemeral EICE key, SSHes to the jumphost, runs
// aiperf with the given configuration, and parses the JSON result.
//
// Prerequisites on the jumphost:
//
//	pip install aiperf
//
// The operator must install aiperf before calling RunAiperf — this function
// does NOT install it (install is ~5 s; the caller decides when to do it).
// To check or install aiperf call EnsureAiperf first.
//
// The aiperf command runs synchronously over the SSH session; the session
// lifetime is bounded by opts.Config.Timeout (default 5 min).
func RunAiperf(ctx context.Context, opts AiperfRunOptions) (*AiperfResult, error) {
	if opts.Config.Timeout <= 0 {
		opts.Config.Timeout = 5 * time.Minute
	}
	if opts.ProbeOptions.User == "" {
		opts.ProbeOptions.User = "ec2-user"
	}

	keyPath, pubKeyPath, cleanup, err := prepareEICEKeyFn(ctx, opts.ProbeOptions.Region, opts.ProbeOptions.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("aiperf: prepare EICE key: %w", err)
	}
	defer cleanup()

	// Re-push public key to reset the ~60 s EICE TTL before the
	// long-running aiperf session starts.
	_ = pushSSHPublicKeyFn(ctx, opts.ProbeOptions.Region, opts.ProbeOptions.InstanceID, pubKeyPath)

	remoteCmd := buildAiperfCmd(opts)

	// Run with a bounded context so a hung aiperf doesn't block forever.
	runCtx, cancel := context.WithTimeout(ctx, opts.Config.Timeout)
	defer cancel()

	stdout, err := aiperfSSHExecFn(runCtx, opts.ProbeOptions.Region, opts.ProbeOptions.InstanceID, keyPath, remoteCmd)
	if err != nil {
		return nil, fmt.Errorf("aiperf: remote exec: %w", err)
	}

	result, err := parseAiperfJSON(stdout)
	if err != nil {
		return nil, fmt.Errorf("aiperf: parse output: %w (raw: %.500s)", err, stdout)
	}

	// Backfill identity fields from config when aiperf doesn't embed them.
	if result.Model == "" {
		result.Model = opts.Config.Model
	}
	if result.Endpoint == "" {
		result.Endpoint = opts.Config.EndpointPath
	}
	if result.BaseURL == "" {
		result.BaseURL = fmt.Sprintf("http://%s", opts.ProbeOptions.VIP)
	}

	return result, nil
}

// EnsureAiperf checks whether aiperf is available on the jumphost and installs
// it via pip if not.  It is a guarded step — it only runs pip install when
// `aiperf --version` returns a non-zero exit code.
func EnsureAiperf(ctx context.Context, probOpts ProbeOptions) error {
	if probOpts.User == "" {
		probOpts.User = "ec2-user"
	}

	keyPath, pubKeyPath, cleanup, err := prepareEICEKeyFn(ctx, probOpts.Region, probOpts.InstanceID)
	if err != nil {
		return fmt.Errorf("ensure aiperf: prepare EICE key: %w", err)
	}
	defer cleanup()

	_ = pushSSHPublicKeyFn(ctx, probOpts.Region, probOpts.InstanceID, pubKeyPath)

	checkCmd := "aiperf --version 2>/dev/null && echo ok || pip install --quiet aiperf && echo installed"
	out, err := aiperfSSHExecFn(ctx, probOpts.Region, probOpts.InstanceID, keyPath, checkCmd)
	if err != nil {
		return fmt.Errorf("ensure aiperf: install failed: %w (output: %s)", err, strings.TrimSpace(out))
	}
	return nil
}

// parseAiperfJSON parses the JSON blob emitted by `aiperf --output-format json`.
// The JSON may be preceded by progress / info lines — we scan for the first '{'.
func parseAiperfJSON(raw string) (*AiperfResult, error) {
	start := strings.Index(raw, "{")
	if start < 0 {
		return nil, fmt.Errorf("no JSON object found in aiperf output")
	}
	var result AiperfResult
	if err := json.Unmarshal([]byte(raw[start:]), &result); err != nil {
		return nil, err
	}
	return &result, nil
}
