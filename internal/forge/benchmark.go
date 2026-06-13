package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

// BenchmarkPushEndpoint is the forge REST path for result ingestion.
// Matches POST /api/benchmarks/results in the Python schema.
const BenchmarkPushEndpoint = "/api/benchmarks/results"

// BenchmarkResultPayload is the Go representation of forge's
// BenchmarkResultPush schema. Field names match the Python pydantic model
// exactly so json.Marshal produces the shape forge expects.
//
// Reference: bnk-forge-v2/backend/schemas/benchmarks.py BenchmarkResultPush
type BenchmarkResultPayload struct {
	// Identity
	ResultID      string `json:"result_id"`
	ResultVersion string `json:"result_version"`

	// Labels — forge extracts proxy, run_label, base_url, model, endpoint
	Labels map[string]string `json:"labels"`
	Tags   map[string]string `json:"tags"`

	// Timing
	RunStart        string  `json:"run_start"`
	RunEnd          string  `json:"run_end"`
	DurationSeconds float64 `json:"duration_seconds"`
	DurationMinutes float64 `json:"duration_minutes"`

	// Config used — forwarded verbatim
	Config map[string]any `json:"config"`

	// Proxy detection (optional)
	ProxyDetection map[string]any `json:"proxy_detection,omitempty"`

	// Summary
	TotalRequests     int     `json:"total_requests"`
	Successful        int     `json:"successful"`
	Failed            int     `json:"failed"`
	SuccessRatePct    float64 `json:"success_rate_pct"`
	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
	AvgInputTokens    float64 `json:"avg_input_tokens"`
	AvgOutputTokens   float64 `json:"avg_output_tokens"`

	// Aggregates
	Latency    map[string]any `json:"latency"`
	Throughput map[string]any `json:"throughput"`

	// Per-phase breakdown
	Phases map[string]any `json:"phases"`

	// Raw timeline (optional)
	Timeline []any `json:"timeline,omitempty"`

	// Agent metadata
	AgentName     *string `json:"agent_name,omitempty"`
	AgentHostname *string `json:"agent_hostname,omitempty"`
}

// BenchmarkPushResponse is the shape forge returns on success.
// Matches BenchmarkResultPushResponse in the Python schema.
type BenchmarkPushResponse struct {
	ID     int    `json:"id"`
	RunID  int    `json:"run_id"`
	Proxy  string `json:"proxy"`
	Model  string `json:"model"`
	Status string `json:"status"`
}

// BenchmarkPushOptions carries all caller-supplied metadata for a push.
type BenchmarkPushOptions struct {
	// RestURL is the forge REST base URL (e.g. "http://localhost:8000").
	RestURL string
	// Creds are the forge REST login credentials.
	Creds RestCreds
	// ResultID is the unique run identifier.
	ResultID string
	// RunLabel is a human label (e.g. "ci-run-1").
	RunLabel string
	// Proxy is the proxy label (e.g. "f5-bnk"). Default: "f5-bnk".
	Proxy string
	// AgentName optionally identifies the machine that ran aiperf.
	AgentName string
	// AgentHostname is the jumphost's DNS name or IP.
	AgentHostname string
	// AiperfConfig carries the benchmark config used (forwarded verbatim to forge).
	AiperfConfig map[string]any
}

// benchmarkHTTPDoFn is the injectable HTTP transport seam used by
// PushBenchmarkResult. Default: http.DefaultClient.Do.
// Tests replace it via the BenchmarkHTTPDoFn export in benchmark_export_test.go.
var benchmarkHTTPDoFn func(*http.Request) (*http.Response, error) = http.DefaultClient.Do

// MapAiperfResultToPayload converts an AiperfResult + options into the
// BenchmarkResultPayload that forge's POST /api/benchmarks/results expects.
//
// Field mapping (aiperf → forge schema):
//
//	result.Model          → labels["model"]
//	result.BaseURL        → labels["base_url"]
//	result.Endpoint       → labels["endpoint"]
//	opts.Proxy            → labels["proxy"]   (default: "f5-bnk")
//	opts.RunLabel         → labels["run_label"]
//	result.Latency.*      → payload.Latency (as dict)
//	result.Throughput.*   → payload.Throughput (as dict)
//	result.Phases         → payload.Phases
func MapAiperfResultToPayload(result *jumphost.AiperfResult, opts BenchmarkPushOptions) BenchmarkResultPayload {
	proxy := opts.Proxy
	if proxy == "" {
		proxy = "f5-bnk"
	}
	resultID := opts.ResultID
	if resultID == "" {
		resultID = fmt.Sprintf("aiperf-%d", time.Now().UnixNano())
	}

	labels := map[string]string{
		"proxy":     proxy,
		"run_label": opts.RunLabel,
		"base_url":  result.BaseURL,
		"model":     result.Model,
		"endpoint":  result.Endpoint,
	}

	cfg := opts.AiperfConfig
	if cfg == nil {
		cfg = map[string]any{}
	}

	// Map latency struct → dict for the schema's `latency: dict` field.
	latency := map[string]any{
		"p50":  result.Latency.P50,
		"p95":  result.Latency.P95,
		"p99":  result.Latency.P99,
		"mean": result.Latency.Mean,
		"min":  result.Latency.Min,
		"max":  result.Latency.Max,
		"ttft": map[string]any{
			"mean": result.Latency.TTFT.Mean,
			"p50":  result.Latency.TTFT.P50,
			"p95":  result.Latency.TTFT.P95,
			"p99":  result.Latency.TTFT.P99,
		},
		"itl": map[string]any{
			"mean": result.Latency.ITL.Mean,
			"p50":  result.Latency.ITL.P50,
			"p95":  result.Latency.ITL.P95,
			"p99":  result.Latency.ITL.P99,
		},
	}

	throughput := map[string]any{
		"overall_rps":    result.Throughput.OverallRPS,
		"peak_rps":       result.Throughput.PeakRPS,
		"tokens_per_sec": result.Throughput.TokensPerSec,
	}

	phases := result.Phases
	if phases == nil {
		phases = map[string]any{}
	}

	payload := BenchmarkResultPayload{
		ResultID:          resultID,
		ResultVersion:     "1.0",
		Labels:            labels,
		Tags:              map[string]string{},
		RunStart:          result.RunStart,
		RunEnd:            result.RunEnd,
		DurationSeconds:   result.DurationSeconds,
		DurationMinutes:   result.DurationMinutes,
		Config:            cfg,
		TotalRequests:     result.TotalRequests,
		Successful:        result.Successful,
		Failed:            result.Failed,
		SuccessRatePct:    result.SuccessRatePct,
		TotalInputTokens:  result.TotalInputTokens,
		TotalOutputTokens: result.TotalOutputTokens,
		AvgInputTokens:    result.AvgInputTokens,
		AvgOutputTokens:   result.AvgOutputTokens,
		Latency:           latency,
		Throughput:        throughput,
		Phases:            phases,
		Timeline:          result.Timeline,
	}

	if opts.AgentName != "" {
		n := opts.AgentName
		payload.AgentName = &n
	}
	if opts.AgentHostname != "" {
		h := opts.AgentHostname
		payload.AgentHostname = &h
	}

	return payload
}

// PushBenchmarkResult pushes an aiperf result to forge's REST benchmark
// ingestion endpoint (POST /api/benchmarks/results).
//
// It logs in with RestCreds, maps the AiperfResult → BenchmarkResultPayload,
// and POSTs to BenchmarkPushEndpoint. The HTTP transport is injectable via
// benchmarkHTTPDoFn for testing.
func PushBenchmarkResult(ctx context.Context, result *jumphost.AiperfResult, opts BenchmarkPushOptions) (BenchmarkPushResponse, error) {
	if result == nil {
		return BenchmarkPushResponse{}, fmt.Errorf("forge.PushBenchmarkResult: result is nil")
	}
	if opts.RestURL == "" {
		return BenchmarkPushResponse{}, fmt.Errorf("forge.PushBenchmarkResult: RestURL is required")
	}

	base := strings.TrimRight(opts.RestURL, "/")

	// Login to obtain a bearer token using the injectable transport.
	token, err := bmkRestLogin(ctx, base, opts.Creds.restUsername(), opts.Creds.restPassword())
	if err != nil {
		return BenchmarkPushResponse{}, fmt.Errorf("forge benchmark push: login: %w", err)
	}

	payload := MapAiperfResultToPayload(result, opts)

	var resp BenchmarkPushResponse
	if err := bmkRestPost(ctx, base+BenchmarkPushEndpoint, token, payload, &resp); err != nil {
		return BenchmarkPushResponse{}, fmt.Errorf("forge benchmark push: %w", err)
	}
	return resp, nil
}

// bmkRestLogin logs in over REST using the injectable benchmarkHTTPDoFn.
func bmkRestLogin(ctx context.Context, base, username, password string) (string, error) {
	body := map[string]string{"username": username, "password": password}
	var resp struct {
		Token string `json:"token"`
	}
	if err := bmkRestPost(ctx, base+"/api/auth/login", "", body, &resp); err != nil {
		return "", err
	}
	if resp.Token == "" {
		return "", fmt.Errorf("forge REST login: empty token in response")
	}
	return resp.Token, nil
}

// bmkRestPost POSTs JSON body to url with an optional bearer token and
// decodes the response into out. Uses benchmarkHTTPDoFn so tests can inject
// a mock transport without touching http.DefaultClient.
func bmkRestPost(ctx context.Context, url, token string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := benchmarkHTTPDoFn(req)
	if err != nil {
		return fmt.Errorf("http POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return &restHTTPErr{StatusCode: resp.StatusCode, URL: url, Body: truncateREST(string(respBytes), 400)}
	}
	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response from %s: %w", url, err)
		}
	}
	return nil
}
