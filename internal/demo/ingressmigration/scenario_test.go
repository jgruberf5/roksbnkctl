package ingressmigration_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	demingresmig "github.com/JLCode-tech/awsbnkctl/internal/demo/ingressmigration"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
)

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
//	waitDeploymentAvailable(migration-backend) →
//	waitDeploymentAvailable(ingress-nginx-controller) →
//	waitDeploymentAvailable(haproxy-ingress-kubernetes-ingress) →
//	waitCondition(Programmed) →
//	waitHTTPRouteCondition(Accepted) →
//	waitHTTPRouteCondition(ResolvedRefs) →
//	ResyncHTTPRoutes →
//	RunBNKProbe → RunInClusterCurl(nginx) → RunInClusterCurl(haproxy)
//
// Moving ResyncHTTPRoutes before the condition waits regresses the cne-controller
// pool-member stale-bug workaround (project_pool_member_sync_root_cause).
func TestVerifyCallOrder(t *testing.T) {
	var calls []string

	noop := demingresmig.SetForgeScanFn(func(_ context.Context, _ int, _ io.Writer) {})
	defer noop()

	deps := demingresmig.VerifyDeps{
		WaitDeploymentAvailableFn: func(_ context.Context, _ *scenarios.Context, _, name string, _ time.Duration) error {
			calls = append(calls, "waitDeploymentAvailable("+name+")")
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
		RunBNKProbeFn: func(_ context.Context, _ *scenarios.Context, _ string, _ time.Duration) (bool, string) {
			calls = append(calls, "RunBNKProbe")
			return true, "ok"
		},
		RunInClusterCurlFn: func(_ context.Context, _ *scenarios.Context, _, _, host string, _ time.Duration) (bool, string, error) {
			calls = append(calls, "RunInClusterCurl("+host+")")
			return true, "ok", nil
		},
	}

	s := demingresmig.NewScenarioForTest(deps)
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
		Options:      map[string]string{"vip": "10.0.10.113"},
	}

	s.Verify(sctx)

	want := []string{
		"waitDeploymentAvailable(migration-backend)",
		"waitDeploymentAvailable(ingress-nginx-controller)",
		"waitDeploymentAvailable(haproxy-ingress-kubernetes-ingress)",
		"waitCondition(Programmed)",
		"waitHTTPRouteCondition(Accepted)",
		"waitHTTPRouteCondition(ResolvedRefs)",
		"ResyncHTTPRoutes",
		"RunBNKProbe",
		"RunInClusterCurl(web.nginx.migration.local)",
		"RunInClusterCurl(web.haproxy.migration.local)",
	}

	// The F5BnkGateway check uses ctx.Dynamic which is nil — it records as a
	// failed assertion but does not call any of our hooks, so we only check the
	// hook calls.
	if len(calls) != len(want) {
		t.Fatalf("call sequence = %v\nwant            = %v", calls, want)
	}
	for i, got := range calls {
		if got != want[i] {
			t.Errorf("calls[%d] = %q, want %q", i, got, want[i])
		}
	}
}

func TestIngressMigration_Registered(t *testing.T) {
	s := demo.Find("ingress-migration")
	if s == nil {
		t.Fatal("ingress-migration not registered — init() not called?")
	}
	if s.Rating() != scenarios.Green {
		t.Errorf("rating = %q, want green", s.Rating())
	}
	if len(s.Dependencies()) != 0 {
		t.Errorf("dependencies = %v, want empty", s.Dependencies())
	}
}

func TestIngressMigration_Title(t *testing.T) {
	s := demo.Find("ingress-migration")
	if s == nil {
		t.Fatal("scenario not registered")
	}
	if !strings.Contains(strings.ToLower(s.Title()), "ingress") {
		t.Errorf("Title() = %q, want it to mention ingress", s.Title())
	}
}

func TestIngressMigration_ManifestsRendered(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := demo.Find("ingress-migration")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	if len(paths) != 6 {
		t.Errorf("expected 6 manifest paths, got %d: %v", len(paths), paths)
	}
	for _, p := range paths {
		if p == "" {
			t.Error("empty path in manifest list")
		}
	}
}

func TestIngressMigration_TemplateSubstitution(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := demo.Find("ingress-migration")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}

	for _, p := range paths {
		rawContent, readErr := readManifest(p)
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		content := string(rawContent)

		// No template directives should remain after rendering.
		if strings.Contains(content, "{{") || strings.Contains(content, "}}") {
			t.Errorf("manifest %s still contains template directives:\n%s", p, content)
		}

		// Namespace resolves to the default.
		if strings.HasSuffix(p, "01-namespace.yaml") {
			if !strings.Contains(content, "demo-ingress-migration") {
				t.Errorf("01-namespace.yaml: expected namespace demo-ingress-migration, got:\n%s", content)
			}
		}

		// GatewayClassName should be "<cluster-name>-gatewayclass".
		if strings.HasSuffix(p, "05-gateway.yaml") {
			if !strings.Contains(content, "test-cluster-gatewayclass") {
				t.Errorf("05-gateway.yaml: expected test-cluster-gatewayclass, got:\n%s", content)
			}
			// VIP defaults to scnVIP (10.0.10.113).
			if !strings.Contains(content, "10.0.10.113") {
				t.Errorf("05-gateway.yaml: expected default VIP 10.0.10.113, got:\n%s", content)
			}
		}
	}
}

func TestIngressMigration_DefaultVIP(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := demo.Find("ingress-migration")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	for _, p := range paths {
		if strings.HasSuffix(p, "05-gateway.yaml") {
			content, readErr := readManifest(p)
			if readErr != nil {
				t.Fatalf("reading %s: %v", p, readErr)
			}
			if !strings.Contains(string(content), "10.0.10.113") {
				t.Errorf("05-gateway.yaml: default VIP should be 10.0.10.113, got:\n%s", string(content))
			}
		}
	}
}

func TestIngressMigration_OverrideVIP(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.200"},
	}

	s := demo.Find("ingress-migration")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	for _, p := range paths {
		if strings.HasSuffix(p, "05-gateway.yaml") {
			content, readErr := readManifest(p)
			if readErr != nil {
				t.Fatalf("reading %s: %v", p, readErr)
			}
			if !strings.Contains(string(content), "10.0.10.200") {
				t.Errorf("05-gateway.yaml: override VIP 10.0.10.200 not found:\n%s", string(content))
			}
			if strings.Contains(string(content), "10.0.10.113") {
				t.Errorf("05-gateway.yaml: default VIP 10.0.10.113 should NOT appear when overridden:\n%s", string(content))
			}
		}
	}
}

func TestIngressMigration_NamespaceFromOptions(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"namespace": "custom-ns"},
	}

	s := demo.Find("ingress-migration")
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
			content, readErr := readManifest(p)
			if readErr != nil {
				t.Fatalf("reading %s: %v", p, readErr)
			}
			if !strings.Contains(string(content), "custom-ns") {
				t.Errorf("01-namespace.yaml: expected custom-ns, got:\n%s", string(content))
			}
		}
	}
}

func TestIngressMigration_DryRun(t *testing.T) {
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

	s := demo.Find("ingress-migration")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	result := scenarios.Run(sctx, s)
	if result.Status != "dry-run" {
		t.Errorf("status = %q, want dry-run", result.Status)
	}
}
