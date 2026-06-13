package jumphost_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

// ---------------------------------------------------------------------------
// buildAiperfCmd — command construction
// ---------------------------------------------------------------------------

func TestBuildAiperfCmd_DefaultsAndRequiredFlags(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{
			VIP:      "10.0.10.100",
			SourceIP: "10.0.11.50",
		},
		Config: jumphost.AiperfConfig{
			Model: "meta-llama/Llama-3.1-8B-Instruct",
		},
		RunLabel: "ci-run-1",
	}

	cmd := jumphost.BuildAiperfCmd(opts)

	mustContain := []string{
		"aiperf",
		"--base-url", "http://10.0.10.100",
		"--model",
		"meta-llama/Llama-3.1-8B-Instruct",
		"--endpoint", "/v1/chat/completions",
		"--num-users", "1",
		"--num-requests", "10",
		"--input-len", "512",
		"--output-len", "128",
		"--output-format", "json",
		"--interface", "10.0.11.50",
		"--run-label", "ci-run-1",
	}
	for _, want := range mustContain {
		if !strings.Contains(cmd, want) {
			t.Errorf("buildAiperfCmd missing %q; cmd=%q", want, cmd)
		}
	}
}

func TestBuildAiperfCmd_StreamingFlag(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:     "llama",
			Streaming: true,
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "--stream") {
		t.Errorf("buildAiperfCmd missing --stream when Streaming=true; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_NoStreamByDefault(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama"},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if strings.Contains(cmd, "--stream") {
		t.Errorf("buildAiperfCmd must NOT contain --stream when Streaming=false; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_CustomConcurrencyAndRequests(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:       "llama",
			Concurrency: 4,
			NumRequests: 100,
			ISL:         256,
			OSL:         64,
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	for _, want := range []string{"--num-users 4", "--num-requests 100", "--input-len 256", "--output-len 64"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("buildAiperfCmd missing %q; cmd=%q", want, cmd)
		}
	}
}

func TestBuildAiperfCmd_NoInterfaceWhenSourceIPEmpty(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100", SourceIP: ""},
		Config:       jumphost.AiperfConfig{Model: "llama"},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if strings.Contains(cmd, "--interface") {
		t.Errorf("buildAiperfCmd must NOT include --interface when SourceIP is empty; cmd=%q", cmd)
	}
}

// ---------------------------------------------------------------------------
// parseAiperfJSON / RunAiperf with stubbed SSH exec
// ---------------------------------------------------------------------------

// minimalAiperfJSON is a representative aiperf JSON fixture. It mirrors the
// fields forge's BenchmarkResultPush schema consumes. Pre-pended noise tests
// that parseAiperfJSON scans for '{'.
const minimalAiperfJSON = `some info line
another info line
{
  "model": "meta-llama/Llama-3.1-8B-Instruct",
  "base_url": "http://10.0.10.100",
  "endpoint": "/v1/chat/completions",
  "run_start": "2026-06-12T10:00:00Z",
  "run_end": "2026-06-12T10:01:30Z",
  "duration_seconds": 90.0,
  "duration_minutes": 1.5,
  "total_requests": 20,
  "successful": 19,
  "failed": 1,
  "success_rate_pct": 95.0,
  "total_input_tokens": 10240,
  "total_output_tokens": 2560,
  "avg_input_tokens": 512.0,
  "avg_output_tokens": 128.0,
  "latency": {
    "p50": 0.45,
    "p95": 0.98,
    "p99": 1.20,
    "mean": 0.50,
    "min": 0.10,
    "max": 1.50,
    "ttft": {"mean": 0.12, "p50": 0.11, "p95": 0.20, "p99": 0.25},
    "itl":  {"mean": 0.03, "p50": 0.03, "p95": 0.05, "p99": 0.06}
  },
  "throughput": {
    "overall_rps": 0.22,
    "peak_rps": 0.30,
    "tokens_per_sec": 28.4
  },
  "phases": {}
}`

// TestRunAiperf_SSHExecStubbed verifies that RunAiperf:
//  1. calls the SSH exec seam with the constructed aiperf command
//  2. parses the JSON result back into an AiperfResult
func TestRunAiperf_SSHExecStubbed(t *testing.T) {
	// Capture what command was passed to the SSH exec seam.
	var capturedCmd string

	// Stub: replace prepareEICEKey, pushSSHPublicKey, and aiperfSSHExec seams.
	origPrepare := *jumphost.PrepareEICEKeyFn
	*jumphost.PrepareEICEKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/fake/key", "/fake/key.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareEICEKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	origExec := *jumphost.AiperfSSHExecFn
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, cmd string) (string, error) {
		capturedCmd = cmd
		return minimalAiperfJSON, nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{
			Region:     "ap-southeast-2",
			InstanceID: "i-0abc123",
			VIP:        "10.0.10.100",
			SourceIP:   "10.0.11.50",
		},
		Config: jumphost.AiperfConfig{
			Model:       "meta-llama/Llama-3.1-8B-Instruct",
			Concurrency: 2,
			NumRequests: 20,
		},
		RunLabel: "ci",
	}

	result, err := jumphost.RunAiperf(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}

	// Assert the command was passed to the SSH exec seam.
	if !strings.Contains(capturedCmd, "aiperf") {
		t.Errorf("SSH exec was not called with an aiperf command; got %q", capturedCmd)
	}
	if !strings.Contains(capturedCmd, "--num-users 2") {
		t.Errorf("command missing --num-users 2; got %q", capturedCmd)
	}

	// Assert the result was parsed.
	if result == nil {
		t.Fatal("RunAiperf returned nil result")
	}
	if result.TotalRequests != 20 {
		t.Errorf("TotalRequests = %d, want 20", result.TotalRequests)
	}
	if result.Successful != 19 {
		t.Errorf("Successful = %d, want 19", result.Successful)
	}
	if result.Latency.P50 != 0.45 {
		t.Errorf("Latency.P50 = %v, want 0.45", result.Latency.P50)
	}
	if result.Throughput.TokensPerSec != 28.4 {
		t.Errorf("Throughput.TokensPerSec = %v, want 28.4", result.Throughput.TokensPerSec)
	}
}

// TestParseAiperfJSON_Fixture tests the pure JSON-parsing function against the
// golden fixture above, asserting the mapping of all forwarded fields.
func TestParseAiperfJSON_Fixture(t *testing.T) {
	// Call RunAiperf with full stub to exercise path through parseAiperfJSON.
	// We reuse the RunAiperf integration test pattern but focus on mapping.
	origPrepare := *jumphost.PrepareEICEKeyFn
	*jumphost.PrepareEICEKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/k", "/k.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareEICEKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	origExec := *jumphost.AiperfSSHExecFn
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, _ string) (string, error) {
		return minimalAiperfJSON, nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	result, err := jumphost.RunAiperf(context.Background(), jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{Region: "r", InstanceID: "i", VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "meta-llama/Llama-3.1-8B-Instruct"},
	})
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"Model", result.Model, "meta-llama/Llama-3.1-8B-Instruct"},
		{"BaseURL", result.BaseURL, "http://10.0.10.100"},
		{"Endpoint", result.Endpoint, "/v1/chat/completions"},
		{"DurationSeconds", result.DurationSeconds, 90.0},
		{"TotalRequests", result.TotalRequests, 20},
		{"Successful", result.Successful, 19},
		{"Failed", result.Failed, 1},
		{"SuccessRatePct", result.SuccessRatePct, 95.0},
		{"TotalInputTokens", result.TotalInputTokens, 10240},
		{"TotalOutputTokens", result.TotalOutputTokens, 2560},
		{"Latency.P50", result.Latency.P50, 0.45},
		{"Latency.P99", result.Latency.P99, 1.20},
		{"Latency.TTFT.Mean", result.Latency.TTFT.Mean, 0.12},
		{"Latency.ITL.Mean", result.Latency.ITL.Mean, 0.03},
		{"Throughput.OverallRPS", result.Throughput.OverallRPS, 0.22},
		{"Throughput.TokensPerSec", result.Throughput.TokensPerSec, 28.4},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
}

// TestRunAiperf_BackfillsIdentityFromConfig verifies that when aiperf omits
// model/base_url/endpoint fields, RunAiperf backfills them from the config.
func TestRunAiperf_BackfillsIdentityFromConfig(t *testing.T) {
	origPrepare := *jumphost.PrepareEICEKeyFn
	*jumphost.PrepareEICEKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/k", "/k.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareEICEKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	origExec := *jumphost.AiperfSSHExecFn
	// Return JSON without identity fields.
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, _ string) (string, error) {
		return `{"total_requests":5,"successful":5,"failed":0,"success_rate_pct":100,"latency":{},"throughput":{},"phases":{}}`, nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	result, err := jumphost.RunAiperf(context.Background(), jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{Region: "r", InstanceID: "i", VIP: "10.0.11.50"},
		Config: jumphost.AiperfConfig{
			Model:        "my-model",
			EndpointPath: "/v1/chat/completions",
		},
	})
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}
	if result.Model != "my-model" {
		t.Errorf("Model = %q, want %q (backfill)", result.Model, "my-model")
	}
	if result.BaseURL != "http://10.0.11.50" {
		t.Errorf("BaseURL = %q, want %q (backfill)", result.BaseURL, "http://10.0.11.50")
	}
	if result.Endpoint != "/v1/chat/completions" {
		t.Errorf("Endpoint = %q, want %q (backfill)", result.Endpoint, "/v1/chat/completions")
	}
}

// TestEnsureAiperf_UsesPython3MPip verifies that the install command sent to
// the jumphost uses "python3 -m pip install" and NOT a bare "pip install".
// AL2023 has no standalone pip/pip3 binary on PATH — only python3 -m pip.
func TestEnsureAiperf_UsesPython3MPip(t *testing.T) {
	var capturedCmd string

	origPrepare := *jumphost.PrepareEICEKeyFn
	*jumphost.PrepareEICEKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/fake/key", "/fake/key.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareEICEKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	origExec := *jumphost.AiperfSSHExecFn
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, cmd string) (string, error) {
		capturedCmd = cmd
		return "ok", nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	err := jumphost.EnsureAiperf(context.Background(), jumphost.ProbeOptions{
		Region:     "ap-southeast-2",
		InstanceID: "i-0abc123",
	})
	if err != nil {
		t.Fatalf("EnsureAiperf: unexpected error: %v", err)
	}

	// Must use python3 -m pip, not a bare pip.
	if !strings.Contains(capturedCmd, "python3 -m pip install") {
		t.Errorf("EnsureAiperf command must contain %q; got: %q", "python3 -m pip install", capturedCmd)
	}
	// Must NOT contain a bare "|| pip install" (the old AL2023-broken form).
	if strings.Contains(capturedCmd, "|| pip install") {
		t.Errorf("EnsureAiperf command must NOT contain bare %q; got: %q", "|| pip install", capturedCmd)
	}
	// Must bootstrap pip via ensurepip.
	if !strings.Contains(capturedCmd, "python3 -m ensurepip") {
		t.Errorf("EnsureAiperf command must contain %q; got: %q", "python3 -m ensurepip", capturedCmd)
	}
}
