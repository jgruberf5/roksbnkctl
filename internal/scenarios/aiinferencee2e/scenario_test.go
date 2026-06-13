package aiinferencee2e_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/aiinferencee2e"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios/aiinferencee2e"
)

func readFileHelper(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 — test helper, path from WriteManifest
}

func makeCluster() *intent.Cluster {
	return &intent.Cluster{
		Metadata: intent.Metadata{
			Name:   "test-cluster",
			Region: "ap-southeast-2",
		},
		Network: intent.Network{
			VPCCidr: "10.0.0.0/16",
			AZs:     []string{"ap-southeast-2a"},
			Subnets: intent.Subnets{
				Public:  []intent.SubnetSpec{{CIDR: "10.0.1.0/24", AZ: "ap-southeast-2a"}},
				Private: []intent.SubnetSpec{{CIDR: "10.0.2.0/24", AZ: "ap-southeast-2a"}},
			},
			DataPath: &intent.DataPathSpec{
				External: intent.SubnetSpec{CIDR: "10.0.10.0/24", AZ: "ap-southeast-2a"},
				Internal: intent.SubnetSpec{CIDR: "10.0.20.0/24", AZ: "ap-southeast-2a"},
			},
		},
		Pattern: "host-device",
	}
}

func TestRegistered(t *testing.T) {
	s := scenarios.Find("ai-inference-e2e")
	if s == nil {
		t.Fatal("ai-inference-e2e not registered — init() not called?")
	}
	if s.Rating() != scenarios.Green {
		t.Errorf("rating = %q, want green", s.Rating())
	}
	if len(s.Dependencies()) != 0 {
		t.Errorf("dependencies = %v, want empty", s.Dependencies())
	}
}

func TestManifestsRendered(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := scenarios.Find("ai-inference-e2e")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	if len(paths) != 5 {
		t.Errorf("expected 5 manifest paths, got %d: %v", len(paths), paths)
	}

	for _, p := range paths {
		if p == "" {
			t.Error("empty path in manifest list")
			continue
		}
		rawContent, readErr := readFileHelper(p)
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		content := string(rawContent)
		// No leftover template directives.
		if strings.Contains(content, "{{") || strings.Contains(content, "}}") {
			t.Errorf("manifest %s still contains template directives:\n%s", p, content)
		}
		// vLLM manifest must request the GPU resource and carry the nodeSelector.
		if strings.HasSuffix(p, "03-vllm.yaml") {
			if !strings.Contains(content, "nvidia.com/gpu") {
				t.Errorf("03-vllm.yaml missing nvidia.com/gpu resource request:\n%s", content)
			}
			if !strings.Contains(content, "awsbnkctl.io/gpu") {
				t.Errorf("03-vllm.yaml missing awsbnkctl.io/gpu nodeSelector:\n%s", content)
			}
			if !strings.Contains(content, "meta-llama/Meta-Llama-3-8B-Instruct") {
				t.Errorf("03-vllm.yaml missing Llama-3-8B-Instruct model arg:\n%s", content)
			}
			if !strings.Contains(content, "NoSchedule") {
				t.Errorf("03-vllm.yaml missing nvidia.com/gpu NoSchedule toleration:\n%s", content)
			}
		}
		// HTTPRoute must attach to the scenario gateway and target vllm backend.
		if strings.HasSuffix(p, "05-httproute.yaml") {
			if !strings.Contains(content, "scn-aiinference-gateway") {
				t.Errorf("05-httproute.yaml missing parentRef scn-aiinference-gateway:\n%s", content)
			}
			if !strings.Contains(content, "name: vllm") {
				t.Errorf("05-httproute.yaml missing backendRef name: vllm:\n%s", content)
			}
		}
	}
}

// TestVerifyOrder_AllGreen asserts that when all fn-pointer stubs succeed
// the result is ok with 5 passing assertions (Available + Programmed +
// Accepted + HTTP200 + SSE).
func TestVerifyOrder_AllGreen(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()
	callOrder := make([]string, 0, 5)

	deps := aiinferencee2e.VerifyDeps{
		WaitDeploymentAvailableFn: func(_ context.Context, _ *scenarios.Context, ns, name string, _ time.Duration) error {
			callOrder = append(callOrder, "WaitDeploymentAvailable:"+ns+"/"+name)
			return nil
		},
		WaitConditionFn: func(_ context.Context, _ *scenarios.Context, _ schema.GroupVersionResource, ns, name, condType string, _ time.Duration) error {
			callOrder = append(callOrder, "WaitCondition:"+name+"/"+condType)
			return nil
		},
		WaitHTTPRouteConditionFn: func(_ context.Context, _ *scenarios.Context, ns, name, condType string, _ time.Duration) error {
			callOrder = append(callOrder, "WaitHTTPRouteCondition:"+name+"/"+condType)
			return nil
		},
		RunVLLMSSEProbeFn: func(_ context.Context, _ *scenarios.Context, vip string) (bool, bool, string) {
			callOrder = append(callOrder, "RunVLLMSSEProbe:"+vip)
			return true, true, "HTTP 200 — SSE data: found=true, [DONE] found=true"
		},
	}

	s := aiinferencee2e.NewScenarioForTest(deps)

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	result := s.Verify(sctx)

	if result.Status != "ok" {
		t.Errorf("status = %q, want ok; summary: %s", result.Status, result.Summary)
	}
	if len(result.Assertions) != 5 {
		t.Errorf("assertions count = %d, want 5", len(result.Assertions))
	}
	for _, a := range result.Assertions {
		if !a.OK {
			t.Errorf("assertion %q failed: %s", a.Description, a.Got)
		}
	}

	// Verify call order: WaitDeploymentAvailable must come before WaitCondition
	// (Gateway), which must come before WaitHTTPRouteCondition, which must come
	// before RunVLLMSSEProbe.
	if len(callOrder) != 4 {
		t.Fatalf("expected 4 calls, got %d: %v", len(callOrder), callOrder)
	}
	if !strings.HasPrefix(callOrder[0], "WaitDeploymentAvailable") {
		t.Errorf("call[0] = %q, want WaitDeploymentAvailable", callOrder[0])
	}
	if !strings.HasPrefix(callOrder[1], "WaitCondition") {
		t.Errorf("call[1] = %q, want WaitCondition (Gateway Programmed)", callOrder[1])
	}
	if !strings.HasPrefix(callOrder[2], "WaitHTTPRouteCondition") {
		t.Errorf("call[2] = %q, want WaitHTTPRouteCondition", callOrder[2])
	}
	if !strings.HasPrefix(callOrder[3], "RunVLLMSSEProbe") {
		t.Errorf("call[3] = %q, want RunVLLMSSEProbe", callOrder[3])
	}
}

// TestVerifyOrder_DeploymentFails_ShortCircuits asserts that when the vLLM
// Deployment fails to become Available, Verify short-circuits before the
// HTTP probe so the result has only 1 assertion (and is failed).
func TestVerifyOrder_DeploymentFails_ShortCircuits(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()
	probeInvoked := false

	deps := aiinferencee2e.VerifyDeps{
		WaitDeploymentAvailableFn: func(_ context.Context, _ *scenarios.Context, _, _ string, _ time.Duration) error {
			return context.DeadlineExceeded
		},
		WaitConditionFn: func(_ context.Context, _ *scenarios.Context, _ schema.GroupVersionResource, _, _, _ string, _ time.Duration) error {
			return nil
		},
		WaitHTTPRouteConditionFn: func(_ context.Context, _ *scenarios.Context, _, _, _ string, _ time.Duration) error {
			return nil
		},
		RunVLLMSSEProbeFn: func(_ context.Context, _ *scenarios.Context, _ string) (bool, bool, string) {
			probeInvoked = true
			return true, true, "should not be called"
		},
	}

	s := aiinferencee2e.NewScenarioForTest(deps)

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	result := s.Verify(sctx)

	if result.Status != "failed" {
		t.Errorf("status = %q, want failed", result.Status)
	}
	if len(result.Assertions) != 1 {
		t.Errorf("assertions count = %d, want 1 (short-circuit after Deployment failure)", len(result.Assertions))
	}
	if probeInvoked {
		t.Error("RunVLLMSSEProbeFn was invoked despite short-circuit — must not be called when Deployment is down")
	}
}

// TestVerifyOrder_SSEFails_HTTP200OK asserts the individual SSE and HTTP200
// assertions are distinct: HTTP200 can pass while SSE framing fails.
func TestVerifyOrder_SSEFails_HTTP200OK(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	deps := aiinferencee2e.VerifyDeps{
		WaitDeploymentAvailableFn: func(_ context.Context, _ *scenarios.Context, _, _ string, _ time.Duration) error {
			return nil
		},
		WaitConditionFn: func(_ context.Context, _ *scenarios.Context, _ schema.GroupVersionResource, _, _, _ string, _ time.Duration) error {
			return nil
		},
		WaitHTTPRouteConditionFn: func(_ context.Context, _ *scenarios.Context, _, _, _ string, _ time.Duration) error {
			return nil
		},
		RunVLLMSSEProbeFn: func(_ context.Context, _ *scenarios.Context, _ string) (bool, bool, string) {
			// HTTP 200 but no SSE framing (e.g. non-streaming JSON response).
			return true, false, "HTTP 200 — SSE data: found=false, [DONE] found=false"
		},
	}

	s := aiinferencee2e.NewScenarioForTest(deps)

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	result := s.Verify(sctx)

	if result.Status != "failed" {
		t.Errorf("status = %q, want failed (SSE framing absent)", result.Status)
	}

	// Find the HTTP200 and SSE assertions.
	var http200Assert, sseAssert *scenarios.Assertion
	for i := range result.Assertions {
		a := &result.Assertions[i]
		if strings.Contains(a.Description, "HTTP 200") {
			http200Assert = a
		}
		if strings.Contains(a.Description, "SSE") {
			sseAssert = a
		}
	}
	if http200Assert == nil {
		t.Fatal("HTTP 200 assertion not found")
	}
	if !http200Assert.OK {
		t.Errorf("HTTP 200 assertion = failed, want ok (HTTP 200 was returned)")
	}
	if sseAssert == nil {
		t.Fatal("SSE assertion not found")
	}
	if sseAssert.OK {
		t.Errorf("SSE assertion = ok, want failed (no SSE framing)")
	}
}
