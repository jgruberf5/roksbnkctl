package diameter_test

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
	demdiameter "github.com/JLCode-tech/awsbnkctl/internal/demo/diameter"
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
//	waitL4RouteCondition(Accepted) → RunDiameterProbe
//
// Moving RunDiameterProbe before the L4Route Accepted wait MUST fail this test.
func TestVerifyCallOrder(t *testing.T) {
	var calls []string

	deps := demdiameter.VerifyDeps{
		WaitDeploymentAvailableFn: func(_ context.Context, _ *scenarios.Context, _, _ string, _ time.Duration) error {
			calls = append(calls, "waitDeploymentAvailable")
			return nil
		},
		WaitConditionFn: func(_ context.Context, _ *scenarios.Context, _ schema.GroupVersionResource, _, _, condType string, _ time.Duration) error {
			calls = append(calls, "waitCondition("+condType+")")
			return nil
		},
		WaitL4RouteConditionFn: func(_ context.Context, _ *scenarios.Context, _, _, condType string, _ time.Duration) error {
			calls = append(calls, "waitL4RouteCondition("+condType+")")
			return nil
		},
		RunDiameterProbeFn: func(_ context.Context, _ *scenarios.Context, _ string) (bool, string) {
			calls = append(calls, "RunDiameterProbe")
			return true, "recorded"
		},
	}

	s := demdiameter.NewScenarioForTest(deps)
	cl := makeCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("JUMPHOST_INSTANCE_ID", "i-test")

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		State:        st,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.110"},
	}

	s.Verify(sctx)

	want := []string{
		"waitDeploymentAvailable",
		"waitCondition(Programmed)",
		"waitL4RouteCondition(Accepted)",
		"RunDiameterProbe",
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

func TestDiameter_Registered(t *testing.T) {
	s := demo.Find("diameter")
	if s == nil {
		t.Fatal("diameter not registered — init() not called?")
	}
	if s.Rating() != scenarios.Green {
		t.Errorf("rating = %q, want green", s.Rating())
	}
	if len(s.Dependencies()) != 0 {
		t.Errorf("dependencies = %v, want empty", s.Dependencies())
	}
}

func TestDiameter_Title(t *testing.T) {
	s := demo.Find("diameter")
	if s == nil {
		t.Fatal("scenario not registered")
	}
	if !strings.Contains(s.Title(), "Diameter") {
		t.Errorf("Title() = %q, want it to mention Diameter", s.Title())
	}
}

func TestDiameter_ManifestsRendered(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := demo.Find("diameter")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	if len(paths) != 5 {
		t.Errorf("got %d manifest paths, want 5", len(paths))
	}

	// Verify no template placeholders remain and no hard-coded cluster-specific values.
	for _, p := range paths {
		content, readErr := readFileHelper(p)
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		c := string(content)
		if strings.Contains(c, "{{") || strings.Contains(c, "}}") {
			t.Errorf("%s: unrendered template placeholder found", p)
		}
		if strings.Contains(c, "syd-tracer-gatewayclass") {
			t.Errorf("%s: still contains hard-coded gatewayclass name", p)
		}
	}
}

// TestDiameter_DefaultVIP verifies that when no Options["vip"] is set the demo
// uses scnVIP (10.0.10.110) — its dedicated VIP.
func TestDiameter_DefaultVIP(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := demo.Find("diameter")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}

	for _, p := range paths {
		if strings.HasSuffix(p, "06-gateway.yaml") {
			content, readErr := readFileHelper(p)
			if readErr != nil {
				t.Fatalf("reading %s: %v", p, readErr)
			}
			if !strings.Contains(string(content), "10.0.10.110") {
				t.Errorf("06-gateway.yaml: default VIP should be 10.0.10.110, got:\n%s", string(content))
			}
		}
	}
}

// TestDiameter_OverrideVIP verifies that Options["vip"] overrides the default scnVIP.
func TestDiameter_OverrideVIP(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.200"},
	}

	s := demo.Find("diameter")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}

	for _, p := range paths {
		if strings.HasSuffix(p, "06-gateway.yaml") {
			content, readErr := readFileHelper(p)
			if readErr != nil {
				t.Fatalf("reading %s: %v", p, readErr)
			}
			if !strings.Contains(string(content), "10.0.10.200") {
				t.Errorf("06-gateway.yaml: override VIP 10.0.10.200 not found:\n%s", string(content))
			}
			if strings.Contains(string(content), "10.0.10.110") {
				t.Errorf("06-gateway.yaml: default VIP 10.0.10.110 should NOT appear when overridden:\n%s", string(content))
			}
		}
	}
}

// TestDiameter_NamespaceOverride verifies that Options["namespace"] applies to
// all 5 rendered manifests.
func TestDiameter_NamespaceOverride(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()
	const customNS = "my-diameter-test"

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"namespace": customNS},
	}

	s := demo.Find("diameter")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	if len(paths) != 5 {
		t.Errorf("got %d manifest paths, want 5", len(paths))
	}

	for _, p := range paths {
		content, readErr := readFileHelper(p)
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		c := string(content)
		if !strings.Contains(c, customNS) {
			t.Errorf("%s: custom namespace %q not found", p, customNS)
		}
	}
}

func TestDiameter_DryRun(t *testing.T) {
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

	s := demo.Find("diameter")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	result := scenarios.Run(sctx, s)
	if result.Status != "dry-run" {
		t.Errorf("status = %q, want dry-run", result.Status)
	}
}
