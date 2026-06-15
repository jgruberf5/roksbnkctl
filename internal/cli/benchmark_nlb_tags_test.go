package cli

// benchmark_nlb_tags_test.go — unit tests for Slice-4 NLB opt-in tag logic.
//
// Tests:
//   - hasNonBNKProxy: pure helper covering empty, f5-bnk-only, and mixed cases.
//   - nlbOptInTags: conditional tag map returned only for non-BNK shootouts.
//   - registerBenchmarkTargetFn injection seam: Tags captured in target options
//     for the four cases from the acceptance criteria:
//       (a) no --proxies             → nil Tags
//       (b) --proxies f5-bnk         → nil Tags
//       (c) --proxies envoy,f5-bnk   → all three tags with defaults
//       (d) custom --upstream-service / --upstream-port reflected

import (
	"context"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/forge"
)

// ---------------------------------------------------------------------------
// hasNonBNKProxy — pure helper
// ---------------------------------------------------------------------------

func TestHasNonBNKProxy(t *testing.T) {
	cases := []struct {
		csv  string
		want bool
	}{
		{"", false},
		{"f5-bnk", false},
		{"f5-bnk,f5-bnk", false},
		{" f5-bnk , f5-bnk ", false},
		{"envoy", true},
		{"envoy,f5-bnk", true},
		{"f5-bnk,envoy", true},
		{"haproxy,nginx", true},
		{"nodeport", true},
	}
	for _, tc := range cases {
		got := hasNonBNKProxy(tc.csv)
		if got != tc.want {
			t.Errorf("hasNonBNKProxy(%q) = %v, want %v", tc.csv, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// nlbOptInTags — conditional tag map
// ---------------------------------------------------------------------------

func TestNlbOptInTags_NoProxies_NilTags(t *testing.T) {
	got := nlbOptInTags("", "vllm", "80")
	if got != nil {
		t.Errorf("nlbOptInTags(\"\", ...) = %v, want nil", got)
	}
}

func TestNlbOptInTags_BNKOnly_NilTags(t *testing.T) {
	got := nlbOptInTags("f5-bnk", "vllm", "80")
	if got != nil {
		t.Errorf("nlbOptInTags(\"f5-bnk\", ...) = %v, want nil", got)
	}
}

func TestNlbOptInTags_NonBNK_AllThreeTags(t *testing.T) {
	got := nlbOptInTags("envoy,f5-bnk", "vllm", "80")
	if got == nil {
		t.Fatal("nlbOptInTags(\"envoy,f5-bnk\", ...) = nil, want map with three keys")
	}
	if got["proxy_expose"] != "internal-nlb" {
		t.Errorf("proxy_expose = %q, want \"internal-nlb\"", got["proxy_expose"])
	}
	if got["upstream_service"] != "vllm" {
		t.Errorf("upstream_service = %q, want \"vllm\"", got["upstream_service"])
	}
	if got["upstream_port"] != "80" {
		t.Errorf("upstream_port = %q, want \"80\"", got["upstream_port"])
	}
}

func TestNlbOptInTags_CustomUpstream_Reflected(t *testing.T) {
	got := nlbOptInTags("envoy", "inference-svc", "8080")
	if got == nil {
		t.Fatal("nlbOptInTags(\"envoy\", ...) = nil, want map")
	}
	if got["upstream_service"] != "inference-svc" {
		t.Errorf("upstream_service = %q, want \"inference-svc\"", got["upstream_service"])
	}
	if got["upstream_port"] != "8080" {
		t.Errorf("upstream_port = %q, want \"8080\"", got["upstream_port"])
	}
}

// ---------------------------------------------------------------------------
// registerBenchmarkTargetFn seam: Tags captured in BenchmarkTargetOptions
//
// These table-driven tests exercise the four acceptance-criteria cases by
// calling resolveForgeGraph with the injection seam stubbed so no network
// call is made. They assert the Tags field of the captured options.
// ---------------------------------------------------------------------------

func TestResolveForgeGraph_NLBTags(t *testing.T) {
	cases := []struct {
		name            string
		proxiesCSV      string
		upstreamService string
		upstreamPort    string
		wantTags        map[string]string // nil means expect nil (not empty map)
	}{
		{
			name:            "(a) no --proxies → nil Tags",
			proxiesCSV:      "",
			upstreamService: "vllm",
			upstreamPort:    "80",
			wantTags:        nil,
		},
		{
			name:            "(b) --proxies f5-bnk → nil Tags",
			proxiesCSV:      "f5-bnk",
			upstreamService: "vllm",
			upstreamPort:    "80",
			wantTags:        nil,
		},
		{
			name:            "(c) --proxies envoy,f5-bnk → all three tags with defaults",
			proxiesCSV:      "envoy,f5-bnk",
			upstreamService: "vllm",
			upstreamPort:    "80",
			wantTags: map[string]string{
				"proxy_expose":     "internal-nlb",
				"upstream_service": "vllm",
				"upstream_port":    "80",
			},
		},
		{
			name:            "(d) custom upstream-service/port reflected",
			proxiesCSV:      "envoy",
			upstreamService: "inference-svc",
			upstreamPort:    "8080",
			wantTags: map[string]string{
				"proxy_expose":     "internal-nlb",
				"upstream_service": "inference-svc",
				"upstream_port":    "8080",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Save/restore all globals and seams touched by resolveForgeGraph.
			origProxies := flagBenchProxies
			origUpstreamService := flagBenchUpstreamService
			origUpstreamPort := flagBenchUpstreamPort
			origForgeURL := flagBenchForgeURL
			origWorkspace := flagBenchWorkspace
			origVIP := flagBenchVIP
			origModel := flagBenchModel
			origInstanceID := flagBenchInstanceID
			origRegAgent := registerBenchmarkAgentFn
			origRegTarget := registerBenchmarkTargetFn
			defer func() {
				flagBenchProxies = origProxies
				flagBenchUpstreamService = origUpstreamService
				flagBenchUpstreamPort = origUpstreamPort
				flagBenchForgeURL = origForgeURL
				flagBenchWorkspace = origWorkspace
				flagBenchVIP = origVIP
				flagBenchModel = origModel
				flagBenchInstanceID = origInstanceID
				registerBenchmarkAgentFn = origRegAgent
				registerBenchmarkTargetFn = origRegTarget
			}()

			// Configure globals.
			flagBenchProxies = tc.proxiesCSV
			flagBenchUpstreamService = tc.upstreamService
			flagBenchUpstreamPort = tc.upstreamPort
			flagBenchForgeURL = "http://localhost:9999"
			flagBenchWorkspace = "test-ws"
			flagBenchVIP = "10.0.10.100"
			flagBenchModel = "test-model"
			flagBenchInstanceID = "i-test"

			// Stub agent registration (required so resolveForgeGraph doesn't error).
			registerBenchmarkAgentFn = func(_ context.Context, _ forge.BenchmarkAgentOptions) (forge.BenchmarkAgentResponse, error) {
				return forge.BenchmarkAgentResponse{ID: 1, Name: "agent"}, nil
			}

			// Capture the target opts; skip the rest (cluster_id=0 → ErrTargetNoClusterID).
			var capturedOpts forge.BenchmarkTargetOptions
			registerBenchmarkTargetFn = func(_ context.Context, opts forge.BenchmarkTargetOptions) (forge.BenchmarkTargetResponse, error) {
				capturedOpts = opts
				// Return ErrTargetNoClusterID to exit early (cluster_id will be 0
				// since there is no real forge_link.json for "test-ws").
				return forge.BenchmarkTargetResponse{}, forge.ErrTargetNoClusterID
			}

			_ = resolveForgeGraph(context.Background(), forge.RestCreds{}, "agent-name")

			// Assert Tags.
			if tc.wantTags == nil {
				if capturedOpts.Tags != nil {
					t.Errorf("Tags = %v, want nil", capturedOpts.Tags)
				}
				return
			}
			if capturedOpts.Tags == nil {
				t.Fatalf("Tags = nil, want %v", tc.wantTags)
			}
			for k, want := range tc.wantTags {
				got := capturedOpts.Tags[k]
				if got != want {
					t.Errorf("Tags[%q] = %q, want %q", k, got, want)
				}
			}
			if len(capturedOpts.Tags) != len(tc.wantTags) {
				t.Errorf("len(Tags) = %d, want %d; got %v", len(capturedOpts.Tags), len(tc.wantTags), capturedOpts.Tags)
			}
		})
	}
}
