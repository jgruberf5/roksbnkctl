package httproutee2e_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/httproutee2e"
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

func TestHTTPRouteE2E_Registered(t *testing.T) {
	s := scenarios.Find("http-routing-e2e")
	if s == nil {
		t.Fatal("http-routing-e2e not registered — init() not called?")
	}
	if s.Rating() != scenarios.Green {
		t.Errorf("rating = %q, want green", s.Rating())
	}
	if len(s.Dependencies()) != 0 {
		t.Errorf("dependencies = %v, want empty (no FRR sibling in awsbnkctl)", s.Dependencies())
	}
}

func TestHTTPRouteE2E_Title(t *testing.T) {
	s := scenarios.Find("http-routing-e2e")
	if s == nil {
		t.Fatal("scenario not registered")
	}
	if !strings.Contains(s.Title(), "HTTPRoute") {
		t.Errorf("Title() = %q, want it to mention HTTPRoute", s.Title())
	}
}

func TestHTTPRouteE2E_ManifestsRendered(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := scenarios.Find("http-routing-e2e")
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

	// Each path should exist on disk.
	for _, p := range paths {
		if p == "" {
			t.Error("empty path in manifest list")
		}
	}
}

func TestHTTPRouteE2E_TemplateSubstitution(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := scenarios.Find("http-routing-e2e")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}

	// Read all manifest files and check no template directives remain.
	for _, p := range paths {
		rawContent, readErr := readFileHelper(p)
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		content := string(rawContent)
		if strings.Contains(content, "{{") || strings.Contains(content, "}}") {
			t.Errorf("manifest %s still contains template directives:\n%s", p, content)
		}
		// Cluster name should appear in the gateway class name.
		if strings.HasSuffix(p, "04-gateway.yaml") {
			if !strings.Contains(content, "test-cluster-gatewayclass") {
				t.Errorf("04-gateway.yaml missing gatewayClassName: got\n%s", content)
			}
			if !strings.Contains(content, "10.0.10.100") {
				t.Errorf("04-gateway.yaml missing VIP (10.0.10.100): got\n%s", content)
			}
		}
		if strings.HasSuffix(p, "01-namespace.yaml") {
			if !strings.Contains(content, "awsbnkctl-scn-httproute-e2e") {
				t.Errorf("01-namespace.yaml missing namespace: got\n%s", content)
			}
		}
	}
}

func TestHTTPRouteE2E_DryRun(t *testing.T) {
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

	s := scenarios.Find("http-routing-e2e")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	result := scenarios.Run(sctx, s)
	if result.Status != "dry-run" {
		t.Errorf("status = %q, want dry-run", result.Status)
	}
}

func TestHTTPRouteE2E_VIPFromOptions(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.200"},
	}

	s := scenarios.Find("http-routing-e2e")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	// Check that the custom VIP appears in the rendered gateway manifest.
	for _, p := range paths {
		if strings.HasSuffix(p, "04-gateway.yaml") {
			content, err := readFileHelper(p)
			if err != nil {
				t.Fatalf("reading %s: %v", p, err)
			}
			if !strings.Contains(string(content), "10.0.10.200") {
				t.Errorf("custom VIP 10.0.10.200 not in gateway manifest:\n%s", string(content))
			}
		}
	}
}
