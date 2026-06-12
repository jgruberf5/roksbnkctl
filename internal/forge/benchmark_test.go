package forge_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

// ---------------------------------------------------------------------------
// MapAiperfResultToPayload — pure mapping, no network
// ---------------------------------------------------------------------------

func sampleAiperfResult() *jumphost.AiperfResult {
	return &jumphost.AiperfResult{
		Model:             "meta-llama/Llama-3.1-8B-Instruct",
		BaseURL:           "http://10.0.10.100",
		Endpoint:          "/v1/chat/completions",
		RunStart:          "2026-06-12T10:00:00Z",
		RunEnd:            "2026-06-12T10:01:30Z",
		DurationSeconds:   90.0,
		DurationMinutes:   1.5,
		TotalRequests:     20,
		Successful:        19,
		Failed:            1,
		SuccessRatePct:    95.0,
		TotalInputTokens:  10240,
		TotalOutputTokens: 2560,
		AvgInputTokens:    512.0,
		AvgOutputTokens:   128.0,
		Latency: jumphost.LatencyStats{
			P50:  0.45,
			P95:  0.98,
			P99:  1.20,
			Mean: 0.50,
			Min:  0.10,
			Max:  1.50,
			TTFT: jumphost.LatencyDistribution{Mean: 0.12, P50: 0.11, P95: 0.20, P99: 0.25},
			ITL:  jumphost.LatencyDistribution{Mean: 0.03, P50: 0.03, P95: 0.05, P99: 0.06},
		},
		Throughput: jumphost.ThroughputStats{
			OverallRPS:   0.22,
			PeakRPS:      0.30,
			TokensPerSec: 28.4,
		},
		Phases: map[string]any{},
	}
}

func TestMapAiperfResultToPayload_Labels(t *testing.T) {
	result := sampleAiperfResult()
	opts := forge.BenchmarkPushOptions{
		ResultID: "run-abc",
		RunLabel: "ci-nightly",
		Proxy:    "f5-bnk",
	}

	payload := forge.MapAiperfResultToPayload(result, opts)

	if payload.ResultID != "run-abc" {
		t.Errorf("ResultID = %q, want %q", payload.ResultID, "run-abc")
	}
	if payload.ResultVersion != "1.0" {
		t.Errorf("ResultVersion = %q, want %q", payload.ResultVersion, "1.0")
	}
	wantLabels := map[string]string{
		"proxy":     "f5-bnk",
		"run_label": "ci-nightly",
		"base_url":  "http://10.0.10.100",
		"model":     "meta-llama/Llama-3.1-8B-Instruct",
		"endpoint":  "/v1/chat/completions",
	}
	for k, want := range wantLabels {
		if got := payload.Labels[k]; got != want {
			t.Errorf("Labels[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestMapAiperfResultToPayload_DefaultProxy(t *testing.T) {
	payload := forge.MapAiperfResultToPayload(sampleAiperfResult(), forge.BenchmarkPushOptions{})
	if payload.Labels["proxy"] != "f5-bnk" {
		t.Errorf("default proxy = %q, want %q", payload.Labels["proxy"], "f5-bnk")
	}
}

func TestMapAiperfResultToPayload_MetricFields(t *testing.T) {
	payload := forge.MapAiperfResultToPayload(sampleAiperfResult(), forge.BenchmarkPushOptions{})

	if payload.TotalRequests != 20 {
		t.Errorf("TotalRequests = %d, want 20", payload.TotalRequests)
	}
	if payload.Successful != 19 {
		t.Errorf("Successful = %d, want 19", payload.Successful)
	}
	if payload.SuccessRatePct != 95.0 {
		t.Errorf("SuccessRatePct = %v, want 95.0", payload.SuccessRatePct)
	}
	if payload.TotalInputTokens != 10240 {
		t.Errorf("TotalInputTokens = %d, want 10240", payload.TotalInputTokens)
	}
	if payload.TotalOutputTokens != 2560 {
		t.Errorf("TotalOutputTokens = %d, want 2560", payload.TotalOutputTokens)
	}
	if payload.DurationSeconds != 90.0 {
		t.Errorf("DurationSeconds = %v, want 90.0", payload.DurationSeconds)
	}
}

func TestMapAiperfResultToPayload_LatencyMap(t *testing.T) {
	payload := forge.MapAiperfResultToPayload(sampleAiperfResult(), forge.BenchmarkPushOptions{})

	if p50, ok := payload.Latency["p50"].(float64); !ok || p50 != 0.45 {
		t.Errorf("Latency[p50] = %v, want 0.45", payload.Latency["p50"])
	}
	if p99, ok := payload.Latency["p99"].(float64); !ok || p99 != 1.20 {
		t.Errorf("Latency[p99] = %v, want 1.20", payload.Latency["p99"])
	}
	ttft, ok := payload.Latency["ttft"].(map[string]any)
	if !ok {
		t.Fatal("Latency[ttft] is not a map")
	}
	if mean, ok := ttft["mean"].(float64); !ok || mean != 0.12 {
		t.Errorf("Latency.ttft.mean = %v, want 0.12", ttft["mean"])
	}
}

func TestMapAiperfResultToPayload_ThroughputMap(t *testing.T) {
	payload := forge.MapAiperfResultToPayload(sampleAiperfResult(), forge.BenchmarkPushOptions{})

	if rps, ok := payload.Throughput["overall_rps"].(float64); !ok || rps != 0.22 {
		t.Errorf("Throughput[overall_rps] = %v, want 0.22", payload.Throughput["overall_rps"])
	}
	if tps, ok := payload.Throughput["tokens_per_sec"].(float64); !ok || tps != 28.4 {
		t.Errorf("Throughput[tokens_per_sec] = %v, want 28.4", payload.Throughput["tokens_per_sec"])
	}
}

func TestMapAiperfResultToPayload_AgentFields(t *testing.T) {
	opts := forge.BenchmarkPushOptions{
		AgentName:     "jumphost-sydney",
		AgentHostname: "10.0.11.50",
	}
	payload := forge.MapAiperfResultToPayload(sampleAiperfResult(), opts)

	if payload.AgentName == nil || *payload.AgentName != "jumphost-sydney" {
		t.Errorf("AgentName = %v, want %q", payload.AgentName, "jumphost-sydney")
	}
	if payload.AgentHostname == nil || *payload.AgentHostname != "10.0.11.50" {
		t.Errorf("AgentHostname = %v, want %q", payload.AgentHostname, "10.0.11.50")
	}
}

func TestMapAiperfResultToPayload_NilResultIDGeneratesDefault(t *testing.T) {
	payload := forge.MapAiperfResultToPayload(sampleAiperfResult(), forge.BenchmarkPushOptions{})
	if payload.ResultID == "" {
		t.Error("ResultID must not be empty when opts.ResultID is empty")
	}
	if !strings.HasPrefix(payload.ResultID, "aiperf-") {
		t.Errorf("ResultID = %q, want prefix %q", payload.ResultID, "aiperf-")
	}
}

// ---------------------------------------------------------------------------
// PushBenchmarkResult — mock HTTP transport
// ---------------------------------------------------------------------------

// benchmarkServer is a minimal httptest server for the benchmark push path.
type benchmarkServer struct {
	// capturedPath records the POST path.
	capturedPath string
	// capturedBody records the raw JSON body.
	capturedBody []byte
	// authToken is the bearer token the server issued.
	authToken string
	// responseStatus is the status code for POST /api/benchmarks/results.
	// Defaults to 201 when zero.
	responseStatus int
}

func (s *benchmarkServer) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
		s.authToken = "bench-token-xyz"
		_ = json.NewEncoder(w).Encode(map[string]string{"token": s.authToken})

	case r.Method == http.MethodPost && r.URL.Path == forge.BenchmarkPushEndpoint:
		s.capturedPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		s.capturedBody = body
		status := s.responseStatus
		if status == 0 {
			status = http.StatusCreated
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     42,
			"run_id": 42,
			"proxy":  "f5-bnk",
			"model":  "meta-llama/Llama-3.1-8B-Instruct",
			"status": "completed",
		})

	default:
		http.NotFound(w, r)
	}
}

// TestPushBenchmarkResult_PostsToCorrectPath asserts the POST goes to
// /api/benchmarks/results (not some other path).
func TestPushBenchmarkResult_PostsToCorrectPath(t *testing.T) {
	srv := &benchmarkServer{}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	origDo := *forge.BenchmarkHTTPDoFn
	*forge.BenchmarkHTTPDoFn = ts.Client().Do
	defer func() { *forge.BenchmarkHTTPDoFn = origDo }()

	resp, err := forge.PushBenchmarkResult(context.Background(), sampleAiperfResult(), forge.BenchmarkPushOptions{
		RestURL:  ts.URL,
		ResultID: "run-42",
		RunLabel: "test",
	})
	if err != nil {
		t.Fatalf("PushBenchmarkResult: %v", err)
	}

	if srv.capturedPath != forge.BenchmarkPushEndpoint {
		t.Errorf("POST path = %q, want %q", srv.capturedPath, forge.BenchmarkPushEndpoint)
	}
	if resp.ID != 42 {
		t.Errorf("response ID = %d, want 42", resp.ID)
	}
}

// TestPushBenchmarkResult_PayloadShape verifies the JSON body sent to forge
// contains the required top-level fields and that labels are correct.
func TestPushBenchmarkResult_PayloadShape(t *testing.T) {
	srv := &benchmarkServer{}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	origDo := *forge.BenchmarkHTTPDoFn
	*forge.BenchmarkHTTPDoFn = ts.Client().Do
	defer func() { *forge.BenchmarkHTTPDoFn = origDo }()

	_, err := forge.PushBenchmarkResult(context.Background(), sampleAiperfResult(), forge.BenchmarkPushOptions{
		RestURL:  ts.URL,
		ResultID: "run-payload-test",
		RunLabel: "payload-check",
		Proxy:    "f5-bnk",
	})
	if err != nil {
		t.Fatalf("PushBenchmarkResult: %v", err)
	}

	// Decode the captured body.
	var body map[string]any
	if err := json.Unmarshal(srv.capturedBody, &body); err != nil {
		t.Fatalf("decode captured body: %v", err)
	}

	// Verify required top-level fields are present.
	required := []string{
		"result_id", "result_version", "labels", "tags",
		"run_start", "run_end", "duration_seconds", "duration_minutes",
		"config", "total_requests", "successful", "failed",
		"success_rate_pct", "total_input_tokens", "total_output_tokens",
		"avg_input_tokens", "avg_output_tokens", "latency", "throughput", "phases",
	}
	for _, field := range required {
		if _, ok := body[field]; !ok {
			t.Errorf("payload missing required field %q", field)
		}
	}

	// Verify labels sub-object.
	labels, ok := body["labels"].(map[string]any)
	if !ok {
		t.Fatal("labels is not a map")
	}
	wantLabels := map[string]string{
		"proxy":     "f5-bnk",
		"run_label": "payload-check",
		"base_url":  "http://10.0.10.100",
		"model":     "meta-llama/Llama-3.1-8B-Instruct",
		"endpoint":  "/v1/chat/completions",
	}
	for k, want := range wantLabels {
		if got, _ := labels[k].(string); got != want {
			t.Errorf("labels[%q] = %q, want %q", k, got, want)
		}
	}

	// Verify result_id and result_version.
	if id, _ := body["result_id"].(string); id != "run-payload-test" {
		t.Errorf("result_id = %q, want %q", id, "run-payload-test")
	}
	if ver, _ := body["result_version"].(string); ver != "1.0" {
		t.Errorf("result_version = %q, want %q", ver, "1.0")
	}
}

// TestPushBenchmarkResult_BearerTokenSent confirms the Authorization header
// is set with the token obtained from /api/auth/login.
func TestPushBenchmarkResult_BearerTokenSent(t *testing.T) {
	var capturedAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "secret-token"})
		case forge.BenchmarkPushEndpoint:
			capturedAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "run_id": 1, "proxy": "f5-bnk", "model": "m", "status": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	origDo := *forge.BenchmarkHTTPDoFn
	*forge.BenchmarkHTTPDoFn = ts.Client().Do
	defer func() { *forge.BenchmarkHTTPDoFn = origDo }()

	_, err := forge.PushBenchmarkResult(context.Background(), sampleAiperfResult(), forge.BenchmarkPushOptions{
		RestURL: ts.URL,
	})
	if err != nil {
		t.Fatalf("PushBenchmarkResult: %v", err)
	}
	if capturedAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", capturedAuth, "Bearer secret-token")
	}
}

// TestPushBenchmarkResult_NilResult returns an error without a network call.
func TestPushBenchmarkResult_NilResult(t *testing.T) {
	_, err := forge.PushBenchmarkResult(context.Background(), nil, forge.BenchmarkPushOptions{
		RestURL: "http://localhost:8000",
	})
	if err == nil {
		t.Error("expected error for nil result, got nil")
	}
}

// TestPushBenchmarkResult_NoRestURL returns an error without a network call.
func TestPushBenchmarkResult_NoRestURL(t *testing.T) {
	_, err := forge.PushBenchmarkResult(context.Background(), sampleAiperfResult(), forge.BenchmarkPushOptions{})
	if err == nil {
		t.Error("expected error for empty RestURL, got nil")
	}
}
