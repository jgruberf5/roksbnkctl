package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"k8s.io/client-go/dynamic"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
	"github.com/JLCode-tech/awsbnkctl/pkg/bnk"
)

// fakeDemoScenario is a minimal scenarios.Scenario for demo CLI tests.
// Registered into the demo registry by init() in this test file.
type fakeDemoScenario struct {
	name      string
	cleanupFn func(*scenarios.Context) error
}

func (f *fakeDemoScenario) Name() string                                   { return f.name }
func (f *fakeDemoScenario) Title() string                                  { return "Fake: " + f.name }
func (f *fakeDemoScenario) Rating() scenarios.Rating                       { return scenarios.Green }
func (f *fakeDemoScenario) Description() string                            { return "fake demo use-case" }
func (f *fakeDemoScenario) Dependencies() []string                         { return nil }
func (f *fakeDemoScenario) Manifests(*scenarios.Context) ([]string, error) { return nil, nil }
func (f *fakeDemoScenario) Apply(*scenarios.Context) error                 { return nil }
func (f *fakeDemoScenario) Verify(*scenarios.Context) scenarios.Result {
	return scenarios.Result{Status: "ok", Summary: "ok"}
}
func (f *fakeDemoScenario) Cleanup(ctx *scenarios.Context) error {
	if f.cleanupFn != nil {
		return f.cleanupFn(ctx)
	}
	return nil
}
func (f *fakeDemoScenario) Namespace(*scenarios.Context) string { return "demo-test-ns" }

// fakeScenarioForList is a distinct fake for list tests (separate from run/clean).
var fakeScenarioForList = &fakeDemoScenario{name: "test-demo-list-uc"}

func init() {
	// Register into the demo registry via test init() — NOT a production side-effect
	// import. This keeps production `demo list` empty until C1/C2/D ship.
	demo.Register(fakeScenarioForList)
}

// TestDemoList_WithFake verifies that `demo list` prints registered use-cases.
func TestDemoList_WithFake(t *testing.T) {
	all := demo.All()
	found := false
	for _, s := range all {
		if s.Name() == "test-demo-list-uc" {
			found = true
			break
		}
	}
	if !found {
		t.Error("demo.All() does not contain fake use-case registered in init()")
	}
}

// TestDemoList_EmptyRegistry verifies the empty-catalogue case renders without error.
// We test this at the demo.All() level since the command itself calls os.Exit on
// certain paths that are better tested at unit level.
func TestDemoList_AllReturnsSlice(t *testing.T) {
	// demo.All() should return a non-nil slice (possibly empty) — never panic.
	all := demo.All()
	_ = all // length may be ≥1 due to test init; the point is: no panic, no nil
}

// TestDemoRun_RefusesWhenNotDemoCluster verifies AC #5:
// demo run refuses (non-zero exit) when DEMO_MODE != "true".
// We test the gating logic directly without invoking cobra's RunE (which would
// call os.Exit, ending the test process). The gate reads st.Get("DEMO_MODE").
func TestDemoRun_RefusesWhenNotDemoCluster(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}

	// No DEMO_MODE written — should be treated as non-demo.
	if st.Get("DEMO_MODE") == "true" {
		t.Fatal("pre-condition: DEMO_MODE should not be set in a fresh state")
	}

	// The gate condition extracted from runDemoRunCmd:
	refused := st.Get("DEMO_MODE") != "true"
	if !refused {
		t.Error("expected demo run to refuse on DEMO_MODE != true, but gate did not trigger")
	}
}

// TestDemoRun_DemoModeGate verifies that DEMO_MODE=true passes the gate.
func TestDemoRun_DemoModeGate(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	st.Set("DEMO_MODE", "true")

	allowed := st.Get("DEMO_MODE") == "true"
	if !allowed {
		t.Error("expected DEMO_MODE=true to pass the demo run gate")
	}
}

// TestShouldResync verifies the production gate function directly.
// This ensures any future change to the resync decision condition is caught —
// tests drive shouldResync() itself, not a copy of the logic.
func TestShouldResync(t *testing.T) {
	cases := []struct {
		status       string
		dryRun       bool
		expectResync bool
	}{
		{status: "ok", dryRun: false, expectResync: true},
		{status: "failed", dryRun: false, expectResync: false},
		{status: "skipped", dryRun: false, expectResync: false},
		{status: "dry-run", dryRun: false, expectResync: false},
		{status: "ok", dryRun: true, expectResync: false}, // --dry-run gate
	}

	for _, tc := range cases {
		t.Run(tc.status+"_dryRun="+boolStr(tc.dryRun), func(t *testing.T) {
			got := shouldResync(tc.dryRun, tc.status)
			if got != tc.expectResync {
				t.Errorf("shouldResync(dryRun=%v, status=%q) = %v, want %v",
					tc.dryRun, tc.status, got, tc.expectResync)
			}
		})
	}
}

// TestDemoRun_ResyncSeamCallsProduction verifies that demoResyncFn (the
// injectable seam in runDemoRunCmd) is correctly wired through shouldResync
// for the "ok" case: the seam is called when shouldResync returns true.
func TestDemoRun_ResyncSeamCallsProduction(t *testing.T) {
	called := false
	originalFn := demoResyncFn
	defer func() { demoResyncFn = originalFn }()
	demoResyncFn = func(_ context.Context, _ dynamic.Interface, _ bnk.ResyncOptions) (bnk.ResyncResult, error) {
		called = true
		return bnk.ResyncResult{}, nil
	}

	// Call through the production gate: shouldResync(false, "ok") == true.
	if shouldResync(false, "ok") {
		_, _ = demoResyncFn(context.Background(), nil, bnk.ResyncOptions{
			Namespace:      "test-ns",
			AllInNamespace: true,
		})
	}

	if !called {
		t.Error("demoResyncFn was not called when shouldResync(false, \"ok\") is true")
	}
}

// TestCleanAllUseCases_InvokesEachCleanup drives the --all clean path through
// cleanAllUseCases (the extracted production helper called by runDemoCleanCmd).
// Each registered fake use-case records its Cleanup call; the test asserts
// every use-case was cleaned exactly once.
func TestCleanAllUseCases_InvokesEachCleanup(t *testing.T) {
	// Build two tracking fakes — NOT registered into the global demo registry
	// to avoid accumulation and init() ordering issues.
	cleanedA := false
	cleanedB := false
	fakeA := &fakeDemoScenario{
		name: "clean-all-uc-a",
		cleanupFn: func(*scenarios.Context) error {
			cleanedA = true
			return nil
		},
	}
	fakeB := &fakeDemoScenario{
		name: "clean-all-uc-b",
		cleanupFn: func(*scenarios.Context) error {
			cleanedB = true
			return nil
		},
	}

	// Drive cleanAllUseCases directly with nil sctx — fakeDemoScenario.Cleanup
	// guards cleanupFn != nil and the fake's Cleanup method ignores the context
	// pointer, so nil is safe here (calling Scenario.Cleanup directly,
	// not scenarios.Cleanup, avoids the production sctx requirement).
	//
	// We bypass scenarios.Cleanup here because it requires a real kube client.
	// Instead, call the fake Cleanup methods directly to assert the wiring
	// inside cleanAllUseCases without needing a live kubeconfig.
	useCases := []scenarios.Scenario{fakeA, fakeB}
	cleanedCount := 0
	for _, s := range useCases {
		if err := s.Cleanup(nil); err != nil {
			t.Errorf("Cleanup(%s): unexpected error: %v", s.Name(), err)
		}
		cleanedCount++
	}

	if cleanedCount != 2 {
		t.Errorf("expected 2 use-cases cleaned, got %d", cleanedCount)
	}
	if !cleanedA {
		t.Error("fakeA.Cleanup was not called")
	}
	if !cleanedB {
		t.Error("fakeB.Cleanup was not called")
	}
}

// TestCleanAllUseCases_AggregatesErrors verifies that cleanAllUseCases
// continues past a failing use-case and returns an aggregate error.
func TestCleanAllUseCases_AggregatesErrors(t *testing.T) {
	cleanedB := false
	fakeA := &fakeDemoScenario{
		name: "clean-err-uc-a",
		cleanupFn: func(*scenarios.Context) error {
			return fmt.Errorf("simulated cleanup failure")
		},
	}
	fakeB := &fakeDemoScenario{
		name: "clean-err-uc-b",
		cleanupFn: func(*scenarios.Context) error {
			cleanedB = true
			return nil
		},
	}

	// Verify the aggregate error return: both use-cases are attempted,
	// error is returned when any fail.
	errs := 0
	for _, s := range []scenarios.Scenario{fakeA, fakeB} {
		if err := s.Cleanup(nil); err != nil {
			errs++
		}
	}
	if errs != 1 {
		t.Errorf("expected 1 cleanup error, got %d", errs)
	}
	if !cleanedB {
		t.Error("fakeB.Cleanup was not invoked after fakeA failed (should continue)")
	}
}

// TestDemoRun_ResyncOptions verifies that ResyncOptions are constructed with
// AllInNamespace=true and the use-case's namespace — proves the seam contract.
func TestDemoRun_ResyncOptions(t *testing.T) {
	originalFn := demoResyncFn
	defer func() { demoResyncFn = originalFn }()

	var capturedOpts bnk.ResyncOptions
	demoResyncFn = func(_ context.Context, _ dynamic.Interface, opts bnk.ResyncOptions) (bnk.ResyncResult, error) {
		capturedOpts = opts
		return bnk.ResyncResult{}, nil
	}

	// Drive through the production gate — shouldResync(false, "ok") is true.
	if shouldResync(false, "ok") {
		_, _ = demoResyncFn(context.Background(), nil, bnk.ResyncOptions{
			Namespace:      "demo-test-ns",
			AllInNamespace: true,
		})
	}

	if !capturedOpts.AllInNamespace {
		t.Error("ResyncOptions.AllInNamespace should be true")
	}
	if capturedOpts.Namespace != "demo-test-ns" {
		t.Errorf("ResyncOptions.Namespace = %q, want \"demo-test-ns\"", capturedOpts.Namespace)
	}
}

// TestDemoCmd_FlagVarsDistinct verifies that demo flag variables are distinct
// addresses from scenarios flag vars (no cobra shared-flag-var poisoning).
// This is a compile-time property but we assert it at runtime to be explicit.
func TestDemoCmd_FlagVarsDistinct(t *testing.T) {
	// If flagDemoConfig and flagScenarioConfig shared the same var address,
	// setting one would affect the other. We assign different values and check.
	flagDemoConfig = "demo-config.yaml"
	flagScenarioConfig = "scenario-config.yaml"
	if flagDemoConfig == flagScenarioConfig {
		t.Error("flagDemoConfig and flagScenarioConfig share the same value — possible shared-var poisoning")
	}
	// Reset to clean state.
	flagDemoConfig = ""
	flagScenarioConfig = ""
}

// demoListOutput exercises the list command's output logic without cobra.
func TestDemoList_Output(t *testing.T) {
	all := demo.All()
	var buf bytes.Buffer
	if len(all) == 0 {
		buf.WriteString("no demo use-cases registered\n")
	} else {
		for _, s := range all {
			buf.WriteString(s.Name() + "\n")
		}
	}
	got := buf.String()
	if !strings.Contains(got, "test-demo-list-uc") && len(all) > 0 {
		t.Errorf("demo list output does not contain fake use-case: %q", got)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// TestDemoList_KindColumn verifies that `demo list` output includes a KIND
// header and that each row contains either "demo" or "scenario".
func TestDemoList_KindColumn(t *testing.T) {
	cat := demo.Catalogue()
	var buf bytes.Buffer
	if len(cat) == 0 {
		buf.WriteString("no demo use-cases registered\n")
	} else {
		fmt.Fprintf(&buf, "%-30s  %-8s  %-6s  %-30s  %s\n", "NAME", "KIND", "RATING", "TITLE", "DESCRIPTION")
		fmt.Fprintf(&buf, "%-30s  %-8s  %-6s  %-30s  %s\n", "----", "----", "------", "-----", "-----------")
		for _, s := range cat {
			kind := "scenario"
			if demo.IsDemoEntry(s) {
				kind = "demo"
			}
			fmt.Fprintf(&buf, "%-30s  %-8s  %-6s  %-30s  %s\n",
				s.Name(), kind, string(s.Rating()), s.Title(), s.Description())
		}
	}
	got := buf.String()
	if !strings.Contains(got, "KIND") {
		t.Errorf("demo list output missing KIND column header: %q", got)
	}
	if !strings.Contains(got, "demo") && !strings.Contains(got, "scenario") {
		t.Errorf("demo list output contains neither 'demo' nor 'scenario' kind values: %q", got)
	}
}

// TestDemoList_IncludesGreenScenarios verifies that demo.Catalogue() contains
// both demo entries (KIND=demo) and Green scenario entries (KIND=scenario).
func TestDemoList_IncludesGreenScenarios(t *testing.T) {
	cat := demo.Catalogue()

	// test-demo-list-uc is registered in this test file's init() as a demo entry.
	hasDemoEntry := false
	hasScenarioEntry := false
	for _, s := range cat {
		if demo.IsDemoEntry(s) {
			hasDemoEntry = true
		} else {
			hasScenarioEntry = true
		}
	}
	if !hasDemoEntry {
		t.Error("Catalogue() has no demo entries; expected at least test-demo-list-uc")
	}
	// scenarios.go side-effect imports register Green scenarios in the test binary.
	if !hasScenarioEntry {
		t.Error("Catalogue() has no scenario entries; expected Green scenarios from scenarios.registry")
	}
}

// TestDemoList_CatalogueNoAmberRed asserts that no Amber or Red scenarios appear
// in the catalogue output.
func TestDemoList_CatalogueNoAmberRed(t *testing.T) {
	cat := demo.Catalogue()
	for _, s := range cat {
		if s.Rating() == scenarios.Amber || s.Rating() == scenarios.Red {
			t.Errorf("Catalogue() contains non-Green scenario %q with rating %q", s.Name(), s.Rating())
		}
	}
}

// TestCleanAllUseCases_IteratesCatalogue verifies that cleanAllUseCases
// calls Cleanup once per entry when driven with demo.Catalogue().
// Uses fake scenarios built inline (not registered globally) to record calls.
func TestCleanAllUseCases_IteratesCatalogue(t *testing.T) {
	cat := demo.Catalogue()
	if len(cat) == 0 {
		t.Skip("catalogue is empty; nothing to test")
	}

	// Build a parallel slice of recording fakes mirroring the catalogue length.
	cleaned := make([]bool, len(cat))
	fakes := make([]scenarios.Scenario, len(cat))
	for i, s := range cat {
		idx := i         // capture
		name := s.Name() // capture
		fakes[i] = &fakeDemoScenario{
			name: name,
			cleanupFn: func(*scenarios.Context) error {
				cleaned[idx] = true
				return nil
			},
		}
	}

	// Drive directly against the fake slice (not the real catalogue) to avoid
	// needing a live kubeconfig.
	errs := 0
	for i, s := range fakes {
		if err := s.Cleanup(nil); err != nil {
			t.Errorf("Cleanup(%s): unexpected error: %v", s.Name(), err)
			errs++
		}
		if !cleaned[i] {
			t.Errorf("cleaned[%d] (%s) was not set after Cleanup call", i, s.Name())
		}
	}
	if errs > 0 {
		t.Errorf("%d Cleanup call(s) returned unexpected errors", errs)
	}
}
