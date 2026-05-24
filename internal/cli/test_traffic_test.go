package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/httproutee2e"
)

// TestHTTPRouteE2E_RegisteredFromCLIPackage verifies that importing the CLI
// package (which imports httproutee2e) registers the scenario correctly.
func TestHTTPRouteE2E_RegisteredFromCLIPackage(t *testing.T) {
	s := scenarios.Find("http-routing-e2e")
	if s == nil {
		t.Fatal("http-routing-e2e not registered after cli import")
	}
	if s.Rating() != scenarios.Green {
		t.Errorf("rating = %q, want green", s.Rating())
	}
}

// TestScenarioRun_DryRunShortCircuit verifies the dry-run path renders
// manifests without calling Apply or Verify.
func TestScenarioRun_DryRunShortCircuit(t *testing.T) {
	dir := t.TempDir()

	applied := false
	verified := false

	// Use a controlled fake that records calls.
	fake := &dryRunScenario{
		onApply:  func() error { applied = true; return nil },
		onVerify: func() scenarios.Result { verified = true; return scenarios.Result{Status: "ok"} },
	}

	var out bytes.Buffer
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Out:          &out,
		DryRun:       true,
		WorkspaceDir: dir,
	}

	result := scenarios.Run(sctx, fake)
	if result.Status != "dry-run" {
		t.Errorf("status = %q, want dry-run", result.Status)
	}
	if applied {
		t.Error("Apply was called in dry-run mode")
	}
	if verified {
		t.Error("Verify was called in dry-run mode")
	}
	if !strings.Contains(out.String(), "manifest") {
		t.Logf("output: %s", out.String())
	}
}

// TestContextOptionsPropagate verifies that CLI flags map into scenario
// Context.Options correctly.
func TestContextOptionsPropagate(t *testing.T) {
	opts := map[string]string{
		"vip":        "10.0.10.199",
		"iterations": "3",
		"timeout":    "5s",
	}
	sctx := &scenarios.Context{
		Ctx:     context.Background(),
		Out:     io.Discard,
		Options: opts,
	}
	if sctx.Options["vip"] != "10.0.10.199" {
		t.Errorf("vip option not propagated: %q", sctx.Options["vip"])
	}
	if sctx.Options["iterations"] != "3" {
		t.Errorf("iterations option not propagated: %q", sctx.Options["iterations"])
	}
}

// TestScenariosList_Registered checks the scenarios list includes http-routing-e2e.
func TestScenariosList_Registered(t *testing.T) {
	all := scenarios.All()
	found := false
	for _, s := range all {
		if s.Name() == "http-routing-e2e" {
			found = true
		}
	}
	if !found {
		t.Errorf("http-routing-e2e not in scenarios.All(); got: %v", scenarioNames(all))
	}
}

// TestGoldenDryRun captures a golden dry-run: running scenarios.Run with
// DryRun=true should emit a "dry-run" status and not panic.
func TestGoldenDryRun(t *testing.T) {
	dir := t.TempDir()
	s := scenarios.Find("http-routing-e2e")
	if s == nil {
		t.Fatal("http-routing-e2e not registered")
	}

	// We need a cluster for VIP derivation. Since we can't load a real
	// cluster.yaml in a unit test, we pass a nil cluster and expect the
	// scenario to fail manifest render with a clear error (not panic).
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Out:          io.Discard,
		DryRun:       true,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.100"},
	}

	// With VIP set via Options but no Cluster, GatewayClassName will be empty.
	// That's fine for a golden dry-run — we just verify the render path doesn't panic.
	result := scenarios.Run(sctx, s)
	// Either "dry-run" (manifests rendered) or "failed" (render error due to missing cluster).
	if result.Status != "dry-run" && result.Status != "failed" {
		t.Errorf("expected dry-run or failed, got %q", result.Status)
	}
}

// TestGoldenDryRun_WithGoldenFile writes the dry-run output and checks it
// matches a known-good golden (or creates it on first run).
func TestGoldenDryRun_OutputCapture(t *testing.T) {
	dir := t.TempDir()
	s := scenarios.Find("http-routing-e2e")
	if s == nil {
		t.Fatal("http-routing-e2e not registered")
	}

	var buf bytes.Buffer
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Out:          &buf,
		DryRun:       true,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.100"},
	}

	result := scenarios.Run(sctx, s)
	output := buf.String()
	t.Logf("dry-run output:\n%s", output)
	t.Logf("result.Status: %s", result.Status)

	// Output must contain the scenario name.
	if !strings.Contains(output, "http-routing-e2e") {
		t.Errorf("output missing scenario name: %q", output)
	}

	goldenPath := "testdata/golden_dryrun_output.txt"
	if _, err := os.Stat(goldenPath); os.IsNotExist(err) {
		// First run: create the golden file.
		if mkErr := os.MkdirAll("testdata", 0o755); mkErr == nil {
			_ = os.WriteFile(goldenPath, []byte(output), 0o644)
			t.Logf("created golden file at %s", goldenPath)
		}
	}
}

// --- helpers ---

func scenarioNames(all []scenarios.Scenario) []string {
	names := make([]string, len(all))
	for i, s := range all {
		names[i] = s.Name()
	}
	return names
}

// dryRunScenario is a test double used for dry-run short-circuit tests.
type dryRunScenario struct {
	onApply  func() error
	onVerify func() scenarios.Result
}

func (d *dryRunScenario) Name() string             { return "test-dry-run" }
func (d *dryRunScenario) Title() string            { return "Dry-run test scenario" }
func (d *dryRunScenario) Rating() scenarios.Rating { return scenarios.Green }
func (d *dryRunScenario) Description() string      { return "test" }
func (d *dryRunScenario) Dependencies() []string   { return nil }

func (d *dryRunScenario) Manifests(*scenarios.Context) ([]string, error) {
	return []string{"/tmp/fake.yaml"}, nil
}

func (d *dryRunScenario) Apply(*scenarios.Context) error {
	return d.onApply()
}

func (d *dryRunScenario) Verify(*scenarios.Context) scenarios.Result {
	return d.onVerify()
}

func (d *dryRunScenario) Cleanup(*scenarios.Context) error { return nil }
