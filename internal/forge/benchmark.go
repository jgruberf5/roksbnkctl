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
// Field mapping (aiperf 0.10.0 → forge schema):
//
//	result.Model                  → labels["model"]
//	result.BaseURL                → labels["base_url"]
//	result.Endpoint               → labels["endpoint"]
//	opts.Proxy                    → labels["proxy"]   (default: "f5-bnk")
//	opts.RunLabel                 → labels["run_label"]
//	result.RequestLatency.*       → payload.Latency["request_latency"]
//	result.TTFT.*                 → payload.Latency["ttft"]
//	result.ITL.*                  → payload.Latency["itl"]
//	result.RequestThroughput      → payload.Throughput["overall_rps"]
//	result.OutputTokenThroughput  → payload.Throughput["tokens_per_sec"]
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

	// Map aiperf 0.10.0 DistributionStats → dict for the schema's latency field.
	latency := map[string]any{
		"p50": result.RequestLatency.P50,
		"p90": result.RequestLatency.P90,
		"p99": result.RequestLatency.P99,
		"avg": result.RequestLatency.Avg,
		"min": result.RequestLatency.Min,
		"max": result.RequestLatency.Max,
		"ttft": map[string]any{
			"avg": result.TTFT.Avg,
			"p50": result.TTFT.P50,
			"p90": result.TTFT.P90,
			"p99": result.TTFT.P99,
		},
		"itl": map[string]any{
			"avg": result.ITL.Avg,
			"p50": result.ITL.P50,
			"p90": result.ITL.P90,
			"p99": result.ITL.P99,
		},
	}

	throughput := map[string]any{
		"overall_rps":    result.RequestThroughput,
		"tokens_per_sec": result.OutputTokenThroughput,
	}

	// Compute success_rate_pct from counts.
	successRatePct := 0.0
	if result.TotalRequests > 0 {
		successRatePct = float64(result.Successful) / float64(result.TotalRequests) * 100.0
	}

	// total_input_tokens / total_output_tokens as integers for schema compat.
	totalInputTokens := int(result.AvgInputTokens * float64(result.TotalRequests))
	totalOutputTokens := int(result.TotalOutputTokens)

	payload := BenchmarkResultPayload{
		ResultID:          resultID,
		ResultVersion:     "1.0",
		Labels:            labels,
		Tags:              map[string]string{},
		RunStart:          result.StartTime,
		RunEnd:            result.EndTime,
		DurationSeconds:   result.DurationSeconds,
		DurationMinutes:   result.DurationMinutes,
		Config:            cfg,
		TotalRequests:     result.TotalRequests,
		Successful:        result.Successful,
		Failed:            result.Failed,
		SuccessRatePct:    successRatePct,
		TotalInputTokens:  totalInputTokens,
		TotalOutputTokens: totalOutputTokens,
		AvgInputTokens:    result.AvgInputTokens,
		AvgOutputTokens:   result.AvgOutputTokens,
		Latency:           latency,
		Throughput:        throughput,
		Phases:            map[string]any{},
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

// BenchmarkConfigEndpoint is the forge REST path for saved RunConfig presets.
const BenchmarkConfigEndpoint = "/api/benchmarks/configs"

// BenchmarkConfigOptions carries the data for registering a preset with forge.
type BenchmarkConfigOptions struct {
	// RestURL is the forge REST base URL.
	RestURL string
	// Creds are the forge REST login credentials.
	Creds RestCreds
	// Name is the preset name as stored in forge (e.g. "awsbnkctl-latency").
	Name string
	// Description is a short human-readable label.
	Description string
	// ConfigJSON is the RunConfig payload forwarded verbatim to forge.
	ConfigJSON map[string]any
}

// BenchmarkConfigResponse is the subset of forge's BenchmarkConfigResponse
// fields that callers need.
type BenchmarkConfigResponse struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Tool string `json:"tool"`
}

// RegisterBenchmarkConfig POSTs a RunConfig preset to forge's
// /api/benchmarks/configs endpoint.
//
// Best-effort: callers should log on error rather than aborting the run.
// Uses the same benchmarkHTTPDoFn transport seam as PushBenchmarkResult.
func RegisterBenchmarkConfig(ctx context.Context, opts BenchmarkConfigOptions) (BenchmarkConfigResponse, error) {
	if opts.RestURL == "" {
		return BenchmarkConfigResponse{}, fmt.Errorf("forge.RegisterBenchmarkConfig: RestURL is required")
	}
	if opts.Name == "" {
		return BenchmarkConfigResponse{}, fmt.Errorf("forge.RegisterBenchmarkConfig: Name is required")
	}

	base := strings.TrimRight(opts.RestURL, "/")

	token, err := bmkRestLogin(ctx, base, opts.Creds.restUsername(), opts.Creds.restPassword())
	if err != nil {
		return BenchmarkConfigResponse{}, fmt.Errorf("forge benchmark config: login: %w", err)
	}

	body := map[string]any{
		"name":        opts.Name,
		"tool":        "aiperf",
		"config_json": opts.ConfigJSON,
	}
	if opts.Description != "" {
		body["description"] = opts.Description
	}

	var resp BenchmarkConfigResponse
	if err := bmkRestPost(ctx, base+BenchmarkConfigEndpoint, token, body, &resp); err != nil {
		return BenchmarkConfigResponse{}, fmt.Errorf("forge benchmark config: %w", err)
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
	req, err := newBmkRequest(ctx, http.MethodPost, url, token, body)
	if err != nil {
		return err
	}
	return doBmkRequest(req, url, out)
}

// bmkRestGet GETs url with an optional bearer token and decodes the JSON
// response into out. Uses the same benchmarkHTTPDoFn seam as bmkRestPost.
func bmkRestGet(ctx context.Context, url, token string, out any) error {
	req, err := newBmkRequest(ctx, http.MethodGet, url, token, nil)
	if err != nil {
		return err
	}
	return doBmkRequest(req, url, out)
}

// bmkRestPut PUTs JSON body to url with an optional bearer token and decodes
// the response into out. Uses the same benchmarkHTTPDoFn seam.
func bmkRestPut(ctx context.Context, url, token string, body, out any) error {
	req, err := newBmkRequest(ctx, http.MethodPut, url, token, body)
	if err != nil {
		return err
	}
	return doBmkRequest(req, url, out)
}

// newBmkRequest builds an *http.Request with JSON body (when body != nil) and
// Authorization header. Shared by bmkRestPost / bmkRestGet / bmkRestPut.
func newBmkRequest(ctx context.Context, method, url, token string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// doBmkRequest executes req via benchmarkHTTPDoFn, checks for HTTP errors, and
// decodes the JSON response into out (when out != nil and body is non-empty).
func doBmkRequest(req *http.Request, url string, out any) error {
	resp, err := benchmarkHTTPDoFn(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", req.Method, url, err)
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
