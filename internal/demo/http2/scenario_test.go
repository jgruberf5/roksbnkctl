package http2_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	demhttp2 "github.com/JLCode-tech/awsbnkctl/internal/demo/http2"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
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

// TestVerifyCallOrder guards the load-bearing step order in Verify:
//
//	waitDeploymentAvailable → waitCondition(Programmed) →
//	waitHTTPRouteCondition(Accepted) → waitHTTPRouteCondition(ResolvedRefs) →
//	ResyncHTTPRoutes → RunHTTP2Probes
//
// Moving ResyncHTTPRoutes before the condition waits regresses the cne-controller
// pool-member stale-bug workaround (project_pool_member_sync_root_cause).
func TestVerifyCallOrder(t *testing.T) {
	var calls []string

	deps := demhttp2.VerifyDeps{
		WaitDeploymentAvailableFn: func(_ context.Context, _ *scenarios.Context, _, _ string, _ time.Duration) error {
			calls = append(calls, "waitDeploymentAvailable")
			return nil
		},
		WaitConditionFn: func(_ context.Context, _ *scenarios.Context, _ schema.GroupVersionResource, _, _, condType string, _ time.Duration) error {
			calls = append(calls, "waitCondition("+condType+")")
			return nil
		},
		WaitHTTPRouteConditionFn: func(_ context.Context, _ *scenarios.Context, _, _, condType string, _ time.Duration) error {
			calls = append(calls, "waitHTTPRouteCondition("+condType+")")
			return nil
		},
		ResyncHTTPRoutesFn: func(_ context.Context, _ *scenarios.Context, _ string) error {
			calls = append(calls, "ResyncHTTPRoutes")
			return nil
		},
		RunHTTP2ProbesFn: func(_ context.Context, _ *scenarios.Context, _ string, _ int, _ time.Duration) (bool, bool, string) {
			calls = append(calls, "RunHTTP2Probes")
			return true, true, "recorded"
		},
	}

	s := demhttp2.NewScenarioForTest(deps)
	cl := makeCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("JUMPHOST_INSTANCE_ID", "i-test")
	st.Set("JUMPHOST_BNK_EXT_ENI_IP", "10.0.10.200")

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		State:        st,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.111"},
	}

	s.Verify(sctx)

	want := []string{
		"waitDeploymentAvailable",
		"waitCondition(Programmed)",
		"waitHTTPRouteCondition(Accepted)",
		"waitHTTPRouteCondition(ResolvedRefs)",
		"ResyncHTTPRoutes",
		"RunHTTP2Probes",
	}

	// The F5BnkGateway check uses ctx.Dynamic which is nil — it records as a
	// failed assertion but does not call any of our hooks, so we only check the
	// hook calls.
	if len(calls) != len(want) {
		t.Fatalf("call sequence = %v, want %v", calls, want)
	}
	for i, got := range calls {
		if got != want[i] {
			t.Errorf("calls[%d] = %q, want %q", i, got, want[i])
		}
	}
}

func TestHTTP2_Registered(t *testing.T) {
	s := demo.Find("http2")
	if s == nil {
		t.Fatal("http2 not registered — init() not called?")
	}
	if s.Rating() != scenarios.Green {
		t.Errorf("rating = %q, want green", s.Rating())
	}
	if len(s.Dependencies()) != 0 {
		t.Errorf("dependencies = %v, want empty", s.Dependencies())
	}
}

func TestHTTP2_Title(t *testing.T) {
	s := demo.Find("http2")
	if s == nil {
		t.Fatal("scenario not registered")
	}
	if !strings.Contains(s.Title(), "HTTP/2") {
		t.Errorf("Title() = %q, want it to mention HTTP/2", s.Title())
	}
}

func TestHTTP2_ManifestsRendered(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := demo.Find("http2")
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
		}
	}
}

func TestHTTP2_TemplateSubstitution(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := demo.Find("http2")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}

	for _, p := range paths {
		rawContent, readErr := readFileHelper(p)
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		content := string(rawContent)

		// No template directives should remain after rendering.
		if strings.Contains(content, "{{") || strings.Contains(content, "}}") {
			t.Errorf("manifest %s still contains template directives:\n%s", p, content)
		}

		// Namespace should resolve to the default "demo-http2".
		if strings.HasSuffix(p, "01-namespace.yaml") {
			if !strings.Contains(content, "demo-http2") {
				t.Errorf("01-namespace.yaml: expected namespace demo-http2, got:\n%s", content)
			}
		}

		// GatewayClassName should be "<cluster-name>-gatewayclass".
		if strings.HasSuffix(p, "04-gateway.yaml") {
			if !strings.Contains(content, "test-cluster-gatewayclass") {
				t.Errorf("04-gateway.yaml: expected test-cluster-gatewayclass, got:\n%s", content)
			}
			// VIP defaults to scnVIP (10.0.10.111) — the http2 demo's dedicated VIP.
			if !strings.Contains(content, "10.0.10.111") {
				t.Errorf("04-gateway.yaml: expected default VIP 10.0.10.111, got:\n%s", content)
			}
		}
	}
}

func TestHTTP2_NamespaceFromOptions(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"namespace": "custom-ns", "vip": "10.0.10.111"},
	}

	s := demo.Find("http2")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	ns := s.Namespace(sctx)
	if ns != "custom-ns" {
		t.Errorf("Namespace() = %q, want custom-ns", ns)
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	for _, p := range paths {
		if strings.HasSuffix(p, "01-namespace.yaml") {
			content, readErr := readFileHelper(p)
			if readErr != nil {
				t.Fatalf("reading %s: %v", p, readErr)
			}
			if !strings.Contains(string(content), "custom-ns") {
				t.Errorf("01-namespace.yaml: expected custom-ns, got:\n%s", string(content))
			}
		}
	}
}

func TestHTTP2_VIPFromOptions(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.200"},
	}

	s := demo.Find("http2")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	for _, p := range paths {
		if strings.HasSuffix(p, "04-gateway.yaml") {
			content, readErr := readFileHelper(p)
			if readErr != nil {
				t.Fatalf("reading %s: %v", p, readErr)
			}
			if !strings.Contains(string(content), "10.0.10.200") {
				t.Errorf("04-gateway.yaml: custom VIP 10.0.10.200 not found:\n%s", string(content))
			}
		}
	}
}

// TestHTTP2_DefaultVIP verifies that when no Options["vip"] is set the demo
// uses scnVIP (10.0.10.111) — its dedicated VIP — rather than DefaultVIP().
func TestHTTP2_DefaultVIP(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := demo.Find("http2")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	for _, p := range paths {
		if strings.HasSuffix(p, "04-gateway.yaml") {
			content, readErr := readFileHelper(p)
			if readErr != nil {
				t.Fatalf("reading %s: %v", p, readErr)
			}
			if !strings.Contains(string(content), "10.0.10.111") {
				t.Errorf("04-gateway.yaml: default VIP should be 10.0.10.111, got:\n%s", string(content))
			}
		}
	}
}

// TestHTTP2_OverrideVIP verifies that Options["vip"] overrides the default scnVIP.
func TestHTTP2_OverrideVIP(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.200"},
	}

	s := demo.Find("http2")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	for _, p := range paths {
		if strings.HasSuffix(p, "04-gateway.yaml") {
			content, readErr := readFileHelper(p)
			if readErr != nil {
				t.Fatalf("reading %s: %v", p, readErr)
			}
			if !strings.Contains(string(content), "10.0.10.200") {
				t.Errorf("04-gateway.yaml: override VIP 10.0.10.200 not found:\n%s", string(content))
			}
			if strings.Contains(string(content), "10.0.10.111") {
				t.Errorf("04-gateway.yaml: default VIP 10.0.10.111 should NOT appear when overridden:\n%s", string(content))
			}
		}
	}
}

func TestHTTP2_DryRun(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		DryRun:       true,
		Options:      map[string]string{},
	}

	s := demo.Find("http2")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	result := scenarios.Run(sctx, s)
	if result.Status != "dry-run" {
		t.Errorf("status = %q, want dry-run", result.Status)
	}
}
