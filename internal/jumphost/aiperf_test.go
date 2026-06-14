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
			VIP: "10.0.10.100",
			// SourceIP intentionally set but should NOT produce --interface
			SourceIP: "10.0.11.50",
		},
		Config: jumphost.AiperfConfig{
			Model:      "llama3",
			HostHeader: "awsbnkctl-aiinference.local",
		},
	}

	cmd := jumphost.BuildAiperfCmd(opts)

	mustContain := []string{
		"aiperf profile",
		"-m", "llama3",
		"-u", "http://10.0.10.100",
		"--endpoint-type", "chat",
		"--concurrency", "1",
		"--request-count", "10",
		"--synthetic-input-tokens-mean", "512",
		"--output-tokens-mean", "128",
		"--tokenizer",
		"NousResearch/Meta-Llama-3-8B-Instruct",
		"--header", "Host:awsbnkctl-aiinference.local",
		"--artifact-dir",
		"--ui", "none",
		"cat",
		"profile_export_aiperf.json",
	}
	for _, want := range mustContain {
		if !strings.Contains(cmd, want) {
			t.Errorf("buildAiperfCmd missing %q; cmd=%q", want, cmd)
		}
	}

	// Must NOT contain removed flags.
	mustNotContain := []string{
		"--base-url", "--num-users", "--num-requests", "--input-len", "--output-len",
		"--output-format", "--interface", "--run-label",
	}
	for _, bad := range mustNotContain {
		if strings.Contains(cmd, bad) {
			t.Errorf("buildAiperfCmd must NOT contain %q (removed flag); cmd=%q", bad, cmd)
		}
	}
}

func TestBuildAiperfCmd_StreamingFlag(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:     "llama3",
			Streaming: true,
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "--streaming") {
		t.Errorf("buildAiperfCmd missing --streaming when Streaming=true; cmd=%q", cmd)
	}
	// Must NOT use the old --stream flag
	if strings.Contains(cmd, " --stream ") || strings.Contains(cmd, " --stream\n") {
		t.Errorf("buildAiperfCmd must use --streaming not --stream; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_NoStreamByDefault(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if strings.Contains(cmd, "--streaming") {
		t.Errorf("buildAiperfCmd must NOT contain --streaming when Streaming=false; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_CustomConcurrencyAndRequests(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:       "llama3",
			Concurrency: 4,
			NumRequests: 100,
			ISL:         256,
			OSL:         64,
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	for _, want := range []string{
		"--concurrency 4",
		"--request-count 100",
		"--synthetic-input-tokens-mean 256",
		"--output-tokens-mean 64",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("buildAiperfCmd missing %q; cmd=%q", want, cmd)
		}
	}
}

func TestBuildAiperfCmd_NoInterfaceEvenWithSourceIP(t *testing.T) {
	// SourceIP is kept on ProbeOptions but must NOT produce --interface
	// (the VIP is on-link on the jumphost external-ENI subnet).
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100", SourceIP: "10.0.11.50"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if strings.Contains(cmd, "--interface") {
		t.Errorf("buildAiperfCmd must NOT include --interface (removed); cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_HostHeaderWhenSet(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:      "llama3",
			HostHeader: "myroute.local",
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "--header") {
		t.Errorf("buildAiperfCmd missing --header when HostHeader set; cmd=%q", cmd)
	}
	if !strings.Contains(cmd, "Host:myroute.local") {
		t.Errorf("buildAiperfCmd missing Host:myroute.local; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_NoHostHeaderWhenEmpty(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if strings.Contains(cmd, "--header") {
		t.Errorf("buildAiperfCmd must NOT include --header when HostHeader empty; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_CustomTokenizer(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:     "llama3",
			Tokenizer: "meta-llama/Llama-3-8B",
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "meta-llama/Llama-3-8B") {
		t.Errorf("buildAiperfCmd must use custom tokenizer; cmd=%q", cmd)
	}
}

// ---------------------------------------------------------------------------
// parseAiperfJSON / RunAiperf with stubbed SSH exec
// ---------------------------------------------------------------------------

// minimalAiperfJSON is a real aiperf 0.10.0 profile_export_aiperf.json fixture.
// Pre-pended noise tests that parseAiperfJSON scans for '{'.
const minimalAiperfJSON = `some info line
another info line
{
  "schema_version": "1.3",
  "aiperf_version": "0.10.0",
  "benchmark_id": "f43cfc1c6cce",
  "request_throughput": {"unit": "requests/sec", "avg": 0.8233},
  "request_latency": {
    "unit": "ms",
    "avg": 2413.5,
    "p50": 2431.75,
    "p90": 2438.5,
    "p99": 2439.45,
    "min": 2358.58,
    "max": 2439.55,
    "std": 30.01,
    "count": 10,
    "sum": 24135.8
  },
  "request_count": {"unit": "requests", "avg": 10.0},
  "time_to_first_token": {
    "unit": "ms",
    "avg": 195.84,
    "p50": 175.05,
    "p90": 251.45,
    "p99": 253.45,
    "min": 126.76,
    "max": 253.67,
    "std": 46.21,
    "count": 10,
    "sum": 1958.4
  },
  "inter_token_latency": {
    "unit": "ms",
    "avg": 35.2,
    "p50": 34.73,
    "p90": 35.95,
    "p99": 35.96,
    "min": 34.66,
    "max": 35.96,
    "std": 0.60,
    "count": 10,
    "sum": 352.0
  },
  "output_token_throughput": {"unit": "tokens/sec", "avg": 52.69},
  "output_sequence_length": {"unit": "tokens", "avg": 64.0, "p50": 64.0, "p90": 64.0, "p99": 64.0, "min": 64.0, "max": 64.0, "std": 0.0, "count": 10, "sum": 640.0},
  "input_sequence_length":  {"unit": "tokens", "avg": 256.0, "p50": 256.0, "p90": 256.0, "p99": 256.0, "min": 256.0, "max": 256.0, "std": 0.0, "count": 10, "sum": 2560.0},
  "total_output_tokens":    {"unit": "tokens", "avg": 640.0},
  "benchmark_duration":     {"unit": "sec", "avg": 12.14},
  "start_time": "2026-06-13T05:11:41.465270",
  "end_time":   "2026-06-13T05:11:53.616416",
  "was_cancelled": false,
  "error_summary": []
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
			Model:       "llama3",
			Concurrency: 2,
			NumRequests: 20,
		},
	}

	result, err := jumphost.RunAiperf(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}

	// Assert the command was passed to the SSH exec seam.
	if !strings.Contains(capturedCmd, "aiperf profile") {
		t.Errorf("SSH exec was not called with 'aiperf profile'; got %q", capturedCmd)
	}
	if !strings.Contains(capturedCmd, "--concurrency 2") {
		t.Errorf("command missing --concurrency 2; got %q", capturedCmd)
	}
	if !strings.Contains(capturedCmd, "--endpoint-type") {
		t.Errorf("command missing --endpoint-type; got %q", capturedCmd)
	}

	// Assert the result was parsed.
	if result == nil {
		t.Fatal("RunAiperf returned nil result")
	}
	if result.TotalRequests != 10 {
		t.Errorf("TotalRequests = %d, want 10", result.TotalRequests)
	}
	if result.RequestLatency.P50 != 2431.75 {
		t.Errorf("RequestLatency.P50 = %v, want 2431.75", result.RequestLatency.P50)
	}
	if result.TTFT.P50 != 175.05 {
		t.Errorf("TTFT.P50 = %v, want 175.05", result.TTFT.P50)
	}
	if result.OutputTokenThroughput != 52.69 {
		t.Errorf("OutputTokenThroughput = %v, want 52.69", result.OutputTokenThroughput)
	}
}

// TestParseAiperfJSON_Fixture tests the pure JSON-parsing function against the
// golden fixture above, asserting the mapping of all forwarded fields.
func TestParseAiperfJSON_Fixture(t *testing.T) {
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
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	})
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"Model", result.Model, "llama3"},
		{"BaseURL", result.BaseURL, "http://10.0.10.100"},
		{"AiperfVersion", result.AiperfVersion, "0.10.0"},
		{"SchemaVersion", result.SchemaVersion, "1.3"},
		{"BenchmarkID", result.BenchmarkID, "f43cfc1c6cce"},
		{"TotalRequests", result.TotalRequests, 10},
		{"Successful", result.Successful, 10},
		{"Failed", result.Failed, 0},
		{"DurationSeconds", result.DurationSeconds, 12.14},
		{"RequestLatency.P50", result.RequestLatency.P50, 2431.75},
		{"RequestLatency.P99", result.RequestLatency.P99, 2439.45},
		{"TTFT.Avg", result.TTFT.Avg, 195.84},
		{"TTFT.P50", result.TTFT.P50, 175.05},
		{"ITL.Avg", result.ITL.Avg, 35.2},
		{"RequestThroughput", result.RequestThroughput, 0.8233},
		{"OutputTokenThroughput", result.OutputTokenThroughput, 52.69},
		{"AvgInputTokens", result.AvgInputTokens, 256.0},
		{"AvgOutputTokens", result.AvgOutputTokens, 64.0},
		{"TotalOutputTokens", result.TotalOutputTokens, 640.0},
		{"WasCancelled", result.WasCancelled, false},
		{"StartTime", result.StartTime, "2026-06-13T05:11:41.465270"},
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
	// Return minimal JSON without identity fields.
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, _ string) (string, error) {
		return `{
			"request_count": {"unit":"requests","avg":5.0},
			"benchmark_duration": {"unit":"sec","avg":1.0},
			"request_throughput": {"unit":"requests/sec","avg":5.0},
			"request_latency": {"unit":"ms","avg":200.0,"p50":200.0,"p90":200.0,"p99":200.0,"min":100.0,"max":300.0},
			"time_to_first_token": {"unit":"ms","avg":50.0,"p50":50.0,"p90":50.0,"p99":50.0,"min":30.0,"max":70.0},
			"inter_token_latency": {"unit":"ms","avg":10.0,"p50":10.0,"p90":10.0,"p99":10.0,"min":5.0,"max":15.0},
			"output_token_throughput": {"unit":"tokens/sec","avg":50.0},
			"output_sequence_length": {"unit":"tokens","avg":64.0},
			"input_sequence_length": {"unit":"tokens","avg":256.0},
			"total_output_tokens": {"unit":"tokens","avg":320.0},
			"error_summary": [],
			"was_cancelled": false,
			"start_time": "2026-06-13T05:00:00",
			"end_time": "2026-06-13T05:00:01"
		}`, nil
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

// TestEnsureAiperf_UsesPython311 verifies that the install command uses
// python3.11 (NOT python3 -m pip) and installs via dnf + python3.11-pip +
// ensurepip fallback + python3.11 -m pip.
// AL2023 ships Python 3.9 which can't run aiperf; python3.11 is available via dnf.
// AL2023's python3.11 package does NOT bundle pip — python3.11-pip must be
// installed explicitly (with ensurepip as a belt-and-suspenders fallback).
func TestEnsureAiperf_UsesPython311(t *testing.T) {
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
		return "ok:aiperf 0.10.0", nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	err := jumphost.EnsureAiperf(context.Background(), jumphost.ProbeOptions{
		Region:     "ap-southeast-2",
		InstanceID: "i-0abc123",
	})
	if err != nil {
		t.Fatalf("EnsureAiperf: unexpected error: %v", err)
	}

	// Must reference python3.11 in the install path.
	if !strings.Contains(capturedCmd, "python3.11") {
		t.Errorf("EnsureAiperf command must reference python3.11; got: %q", capturedCmd)
	}
	// Must reference python3.11 -m pip.
	if !strings.Contains(capturedCmd, "python3.11 -m pip install") {
		t.Errorf("EnsureAiperf command must contain %q; got: %q", "python3.11 -m pip install", capturedCmd)
	}
	// Must use dnf to install python3.11 AND python3.11-pip (AL2023 omits pip from base package).
	if !strings.Contains(capturedCmd, "dnf install") {
		t.Errorf("EnsureAiperf command must use dnf install for python3.11; got: %q", capturedCmd)
	}
	if !strings.Contains(capturedCmd, "python3.11-pip") {
		t.Errorf("EnsureAiperf command must install python3.11-pip (AL2023 python3.11 omits pip); got: %q", capturedCmd)
	}
	// Must include ensurepip fallback for belt-and-suspenders pip availability.
	if !strings.Contains(capturedCmd, "ensurepip") {
		t.Errorf("EnsureAiperf command must include ensurepip fallback; got: %q", capturedCmd)
	}
	// Must check for placeholder 0.1.0.
	if !strings.Contains(capturedCmd, "0\\.1\\.0") && !strings.Contains(capturedCmd, "0.1.0") {
		t.Errorf("EnsureAiperf must guard against 0.1.0 placeholder; got: %q", capturedCmd)
	}
}

// ---------------------------------------------------------------------------
// AiperfResult.RawJSON — verify RunAiperf populates the raw JSON field
// ---------------------------------------------------------------------------

// TestRunAiperf_RawJSONPopulated verifies that RunAiperf sets RawJSON on the
// returned result to the verbatim JSON content (trimmed to start at '{').
func TestRunAiperf_RawJSONPopulated(t *testing.T) {
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
		// Prepend stale lines that precede the JSON — RunAiperf must trim them.
		return "noise line\nanother noise\n" + minimalAiperfJSON[strings.Index(minimalAiperfJSON, "{"):], nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	result, err := jumphost.RunAiperf(context.Background(), jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{Region: "r", InstanceID: "i", VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	})
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}
	if result.RawJSON == "" {
		t.Fatal("RawJSON must not be empty after RunAiperf")
	}
	if !strings.HasPrefix(result.RawJSON, "{") {
		t.Errorf("RawJSON must start at '{', got prefix %q", result.RawJSON[:min(30, len(result.RawJSON))])
	}
	// Spot-check a known key from the fixture.
	if !strings.Contains(result.RawJSON, `"schema_version"`) {
		t.Error("RawJSON missing 'schema_version' key from fixture")
	}
	if !strings.Contains(result.RawJSON, `"benchmark_id"`) {
		t.Error("RawJSON missing 'benchmark_id' key from fixture")
	}
}

// TestRunAiperf_RawJSONWithNoNoise verifies RawJSON when stdout starts
// directly with '{' (no leading noise lines).
func TestRunAiperf_RawJSONWithNoNoise(t *testing.T) {
	origPrepare := *jumphost.PrepareEICEKeyFn
	*jumphost.PrepareEICEKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/k", "/k.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareEICEKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	// Pure JSON — no noise prefix.
	pureJSON := minimalAiperfJSON[strings.Index(minimalAiperfJSON, "{"):]

	origExec := *jumphost.AiperfSSHExecFn
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, _ string) (string, error) {
		return pureJSON, nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	result, err := jumphost.RunAiperf(context.Background(), jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{Region: "r", InstanceID: "i", VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	})
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}
	if result.RawJSON != pureJSON {
		t.Errorf("RawJSON mismatch: got len=%d, want len=%d", len(result.RawJSON), len(pureJSON))
	}
}
