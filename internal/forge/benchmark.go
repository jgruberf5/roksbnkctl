package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// BenchmarkRawAiperfEndpoint is the forge REST path for rich raw aiperf ingest.
// Body = verbatim profile_export_aiperf.json bytes (bare JSON).
// Query params: proxy, model, url, agent_name, run_label,
// target_id, config_id, proxy_deployment_id, dataset_name.
// Forge performs the canonical transform; no Go-side field mapping needed.
const BenchmarkRawAiperfEndpoint = "/api/benchmarks/results/aiperf"

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
	Latency       map[string]any `json:"latency"`
	Throughput    map[string]any `json:"throughput"`
	AiperfMetrics map[string]any `json:"aiperf_metrics,omitempty"`

	// Per-phase breakdown
	Phases map[string]any `json:"phases"`

	// Raw timeline (optional)
	Timeline []any `json:"timeline,omitempty"`

	// Agent metadata
	AgentName     *string `json:"agent_name,omitempty"`
	AgentHostname *string `json:"agent_hostname,omitempty"`

	// Linkage — optional forge foreign keys (omitted when zero/nil).
	TargetID          *int `json:"target_id,omitempty"`
	ConfigID          *int `json:"config_id,omitempty"`
	ProxyDeploymentID *int `json:"proxy_deployment_id,omitempty"`
}

// BenchmarkPushResponse is the shape forge returns on success.
// Matches BenchmarkResultPushResponse in the Python schema.
type BenchmarkPushResponse struct {
	ID       int    `json:"id"`
	RunID    int    `json:"run_id"`
	Proxy    string `json:"proxy"`
	Model    string `json:"model"`
	Status   string `json:"status"`
	TargetID *int   `json:"target_id,omitempty"`
	ConfigID *int   `json:"config_id,omitempty"`
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
	// TargetID links the result to a forge Target record (0 = unset, omitted).
	TargetID int
	// ConfigID links the result to a forge BenchmarkConfig record (0 = unset, omitted).
	ConfigID int
	// ProxyDeploymentID links the result to a forge ProxyDeployment record (0 = unset, omitted).
	ProxyDeploymentID int
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
//	result.RequestLatency.* / 1000 → payload.Latency (SECONDS — forge contract)
//	result.RequestThroughput      → payload.Throughput["overall_rps"] and ["peak_rps"]
//	result.OutputTokenThroughput  → payload.Throughput["gen_tokens_per_sec"]
//	result.TTFT.*                 → payload.AiperfMetrics["ttft"] (raw ms)
//	result.ITL.*                  → payload.AiperfMetrics["itl"]  (raw ms)
//	result.AvgOutputTokens        → payload.AiperfMetrics["osl"]["avg"]
//	result.AvgInputTokens         → payload.AiperfMetrics["isl"]["avg"]
//	opts.ConfigID (non-zero)      → payload.ConfigID pointer
//	opts.TargetID (non-zero)      → payload.TargetID pointer
//	opts.ProxyDeploymentID (n-z)  → payload.ProxyDeploymentID pointer
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

	// Map aiperf 0.10.0 DistributionStats → latency dict (SECONDS).
	// aiperf reports latency in milliseconds; forge's contract divides by 1000.
	latency := map[string]any{
		"min": result.RequestLatency.Min / 1000.0,
		"p50": result.RequestLatency.P50 / 1000.0,
		"p90": result.RequestLatency.P90 / 1000.0,
		"p99": result.RequestLatency.P99 / 1000.0,
		"avg": result.RequestLatency.Avg / 1000.0,
		"max": result.RequestLatency.Max / 1000.0,
	}

	// throughput keys match forge's complete_run_with_aiperf_result contract.
	// gen_tokens_per_sec is the key forge denormalizes to tokens_per_sec column.
	// peak_rps mirrors overall_rps (aiperf has no separate peak).
	throughput := map[string]any{
		"overall_rps":        result.RequestThroughput,
		"peak_rps":           result.RequestThroughput,
		"gen_tokens_per_sec": result.OutputTokenThroughput,
		// backward-compat alias kept so any existing consumers are not broken.
		"tokens_per_sec": result.OutputTokenThroughput,
	}

	// aiperf_metrics carries the raw (ms / scalar) values that forge's compare
	// view reads via result_json["aiperf_metrics"][k]["avg"].
	distMap := func(d jumphost.DistributionStats) map[string]any {
		return map[string]any{
			"avg": d.Avg,
			"p50": d.P50,
			"p90": d.P90,
			"p99": d.P99,
			"min": d.Min,
			"max": d.Max,
		}
	}
	aiperfMetrics := map[string]any{
		"ttft": distMap(result.TTFT),
		"itl":  distMap(result.ITL),
		"osl":  map[string]any{"avg": result.AvgOutputTokens},
		"isl":  map[string]any{"avg": result.AvgInputTokens},
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
		AiperfMetrics:     aiperfMetrics,
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
	if opts.ConfigID != 0 {
		id := opts.ConfigID
		payload.ConfigID = &id
	}
	if opts.TargetID != 0 {
		id := opts.TargetID
		payload.TargetID = &id
	}
	if opts.ProxyDeploymentID != 0 {
		id := opts.ProxyDeploymentID
		payload.ProxyDeploymentID = &id
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

// RawAiperfPushOptions carries all parameters for the raw-JSON aiperf push path.
// All string fields become URL query parameters on the POST; integer IDs are
// omitted from the query string when zero.
type RawAiperfPushOptions struct {
	// RestURL is the forge REST base URL (e.g. "http://localhost:8000").
	RestURL string
	// Creds are the forge REST login credentials.
	Creds RestCreds
	// RawJSON is the verbatim content of profile_export_aiperf.json.
	// Must be a valid JSON object (starts with '{').
	RawJSON []byte
	// Proxy label forwarded as ?proxy=. Default: "f5-bnk".
	Proxy string
	// Model name forwarded as ?model=.
	Model string
	// URL (LLM base URL) forwarded as ?url=.
	URL string
	// AgentName forwarded as ?agent_name=.
	AgentName string
	// RunLabel forwarded as ?run_label=.
	RunLabel string
	// DatasetName forwarded as ?dataset_name=.
	DatasetName string
	// TargetID forwarded as ?target_id= when non-zero.
	TargetID int
	// ConfigID forwarded as ?config_id= when non-zero.
	ConfigID int
	// ProxyDeploymentID forwarded as ?proxy_deployment_id= when non-zero.
	ProxyDeploymentID int
}

// RawAiperfPushResponse is the shape forge returns on success from
// POST /api/benchmarks/results/aiperf.
type RawAiperfPushResponse struct {
	ID       int    `json:"id"`
	RunID    int    `json:"run_id"`
	Proxy    string `json:"proxy"`
	Model    string `json:"model"`
	Status   string `json:"status"`
	TargetID *int   `json:"target_id,omitempty"`
	ConfigID *int   `json:"config_id,omitempty"`
}

// PushRawAiperfResult posts the raw profile_export_aiperf.json bytes to forge's
// rich ingest endpoint (POST /api/benchmarks/results/aiperf).  Forge performs
// the canonical transform — no field mapping happens on the Go side.
//
// Query parameters are derived from opts; zero-valued integer IDs are omitted.
// Uses the same benchmarkHTTPDoFn transport seam as PushBenchmarkResult.
func PushRawAiperfResult(ctx context.Context, opts RawAiperfPushOptions) (RawAiperfPushResponse, error) {
	if opts.RestURL == "" {
		return RawAiperfPushResponse{}, fmt.Errorf("forge.PushRawAiperfResult: RestURL is required")
	}
	if len(opts.RawJSON) == 0 {
		return RawAiperfPushResponse{}, fmt.Errorf("forge.PushRawAiperfResult: RawJSON is required")
	}

	base := strings.TrimRight(opts.RestURL, "/")

	token, err := bmkRestLogin(ctx, base, opts.Creds.restUsername(), opts.Creds.restPassword())
	if err != nil {
		return RawAiperfPushResponse{}, fmt.Errorf("forge raw aiperf push: login: %w", err)
	}

	proxy := opts.Proxy
	if proxy == "" {
		proxy = "f5-bnk"
	}

	// Build the request manually: body is raw JSON bytes (not re-marshalled),
	// and query parameters carry the metadata that forge uses to link the run.
	rawURL := base + BenchmarkRawAiperfEndpoint

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(string(opts.RawJSON)))
	if err != nil {
		return RawAiperfPushResponse{}, fmt.Errorf("forge raw aiperf push: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	q := req.URL.Query()
	q.Set("proxy", proxy)
	if opts.Model != "" {
		q.Set("model", opts.Model)
	}
	if opts.URL != "" {
		q.Set("url", opts.URL)
	}
	if opts.AgentName != "" {
		q.Set("agent_name", opts.AgentName)
	}
	if opts.RunLabel != "" {
		q.Set("run_label", opts.RunLabel)
	}
	if opts.DatasetName != "" {
		q.Set("dataset_name", opts.DatasetName)
	}
	if opts.TargetID != 0 {
		q.Set("target_id", fmt.Sprintf("%d", opts.TargetID))
	}
	if opts.ConfigID != 0 {
		q.Set("config_id", fmt.Sprintf("%d", opts.ConfigID))
	}
	if opts.ProxyDeploymentID != 0 {
		q.Set("proxy_deployment_id", fmt.Sprintf("%d", opts.ProxyDeploymentID))
	}
	req.URL.RawQuery = q.Encode()

	var resp RawAiperfPushResponse
	if err := doBmkRequest(req, rawURL, &resp); err != nil {
		return RawAiperfPushResponse{}, fmt.Errorf("forge raw aiperf push: %w", err)
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
// Idempotent: on name conflict (409 or 400-with-"already exists"), falls back
// to GET /api/benchmarks/configs and returns the existing record by name match.
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
	postErr := bmkRestPost(ctx, base+BenchmarkConfigEndpoint, token, body, &resp)
	if postErr == nil {
		return resp, nil
	}

	// 409 or 400-with-"already exists": name conflict — fall back to list-and-match
	// (mirrors the accessmethod.go and rest.go upsert pattern).
	var herr *restHTTPErr
	if !errors.As(postErr, &herr) {
		return BenchmarkConfigResponse{}, fmt.Errorf("forge benchmark config: %w", postErr)
	}
	isConflict := herr.StatusCode == http.StatusConflict ||
		(herr.StatusCode == http.StatusBadRequest && strings.Contains(herr.Body, "already exists"))
	if !isConflict {
		return BenchmarkConfigResponse{}, fmt.Errorf("forge benchmark config: %w", postErr)
	}

	existing, lookupErr := benchmarkConfigFindByName(ctx, base, token, opts.Name)
	if lookupErr != nil {
		return BenchmarkConfigResponse{}, fmt.Errorf("forge benchmark config: conflict + list failed: %w (original: %v)", lookupErr, postErr)
	}
	return existing, nil
}

// benchmarkConfigFindByName GETs /api/benchmarks/configs and returns the record
// whose name matches exactly.
func benchmarkConfigFindByName(ctx context.Context, base, token, name string) (BenchmarkConfigResponse, error) {
	var list []BenchmarkConfigResponse
	if err := bmkRestGet(ctx, base+BenchmarkConfigEndpoint, token, &list); err != nil {
		return BenchmarkConfigResponse{}, fmt.Errorf("list benchmark configs: %w", err)
	}
	for _, r := range list {
		if r.Name == name {
			return r, nil
		}
	}
	return BenchmarkConfigResponse{}, fmt.Errorf("benchmark config %q not found in forge", name)
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
