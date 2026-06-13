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
		Model:         "meta-llama/Llama-3.1-8B-Instruct",
		BaseURL:       "http://10.0.10.100",
		Endpoint:      "/v1/chat/completions",
		AiperfVersion: "0.10.0",
		SchemaVersion: "1.3",
		BenchmarkID:   "f43cfc1c6cce",
		StartTime:     "2026-06-12T10:00:00Z",
		EndTime:       "2026-06-12T10:01:30Z",
		WasCancelled:  false,
		ErrorSummary:  []any{},

		TotalRequests:   20,
		Successful:      19,
		Failed:          1,
		DurationSeconds: 90.0,
		DurationMinutes: 1.5,

		RequestThroughput:     0.22,
		OutputTokenThroughput: 28.4,

		RequestLatency: jumphost.DistributionStats{
			Unit: "ms",
			Avg:  500.0,
			P50:  450.0,
			P90:  980.0,
			P99:  1200.0,
			Min:  100.0,
			Max:  1500.0,
		},
		TTFT: jumphost.DistributionStats{
			Unit: "ms",
			Avg:  120.0,
			P50:  110.0,
			P90:  200.0,
			P99:  250.0,
		},
		ITL: jumphost.DistributionStats{
			Unit: "ms",
			Avg:  30.0,
			P50:  30.0,
			P90:  50.0,
			P99:  60.0,
		},

		AvgInputTokens:    512.0,
		AvgOutputTokens:   128.0,
		TotalOutputTokens: 2560.0,
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
	if payload.DurationSeconds != 90.0 {
		t.Errorf("DurationSeconds = %v, want 90.0", payload.DurationSeconds)
	}
	// total_input_tokens derived from avg * count
	if payload.TotalInputTokens != 10240 {
		t.Errorf("TotalInputTokens = %d, want 10240 (512*20)", payload.TotalInputTokens)
	}
	// total_output_tokens from TotalOutputTokens field
	if payload.TotalOutputTokens != 2560 {
		t.Errorf("TotalOutputTokens = %d, want 2560", payload.TotalOutputTokens)
	}
}

func TestMapAiperfResultToPayload_LatencyMap(t *testing.T) {
	payload := forge.MapAiperfResultToPayload(sampleAiperfResult(), forge.BenchmarkPushOptions{})

	if p50, ok := payload.Latency["p50"].(float64); !ok || p50 != 450.0 {
		t.Errorf("Latency[p50] = %v, want 450.0", payload.Latency["p50"])
	}
	if p99, ok := payload.Latency["p99"].(float64); !ok || p99 != 1200.0 {
		t.Errorf("Latency[p99] = %v, want 1200.0", payload.Latency["p99"])
	}
	ttft, ok := payload.Latency["ttft"].(map[string]any)
	if !ok {
		t.Fatal("Latency[ttft] is not a map")
	}
	if avg, ok := ttft["avg"].(float64); !ok || avg != 120.0 {
		t.Errorf("Latency.ttft.avg = %v, want 120.0", ttft["avg"])
	}
	if p50, ok := ttft["p50"].(float64); !ok || p50 != 110.0 {
		t.Errorf("Latency.ttft.p50 = %v, want 110.0", ttft["p50"])
	}
	itl, ok := payload.Latency["itl"].(map[string]any)
	if !ok {
		t.Fatal("Latency[itl] is not a map")
	}
	if avg, ok := itl["avg"].(float64); !ok || avg != 30.0 {
		t.Errorf("Latency.itl.avg = %v, want 30.0", itl["avg"])
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
