package demo

import (
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"

	// Side-effect imports: register the real Green scenarios so Catalogue()
	// returns a non-trivial set. These mirror the imports in internal/cli/scenarios.go.
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/aiinferencee2e"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/aisemanticcache"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/aitokencounting"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/egresssnat"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/externalresourcepool"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/httproutee2e"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/httptrafficsplit"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/multivip"
	_ "github.com/JLCode-tech/awsbnkctl/internal/scenarios/proxyprotocoll4"
)

// setupDemoRegistryForCatalogue sets a known demo registry for catalogue tests.
// Called from tests that need a stable demo set. Restores via defer.
func setupDemoRegistryForCatalogue(t *testing.T) func() {
	t.Helper()
	saved := registry
	registry = nil
	Register(&fakeScenario{name: "http2", title: "HTTP/2 Demo", rating: scenarios.Green})
	Register(&fakeScenario{name: "diameter", title: "Diameter Demo", rating: scenarios.Green})
	return func() { registry = saved }
}

// TestCatalogue_DemosFirst asserts that every demo registry entry appears
// before any scenario entry in the catalogue.
func TestCatalogue_DemosFirst(t *testing.T) {
	restore := setupDemoRegistryForCatalogue(t)
	defer restore()

	cat := Catalogue()
	demoNames := map[string]bool{"http2": true, "diameter": true}

	sawScenario := false
	for _, s := range cat {
		inDemo := demoNames[s.Name()]
		if sawScenario && inDemo {
			t.Errorf("demo entry %q appears after a scenario entry — demos must come first", s.Name())
		}
		if !inDemo {
			sawScenario = true
		}
	}
}

// TestCatalogue_OnlyGreenScenarios asserts that every scenario-side entry has
// Rating == Green (Amber/Red are excluded).
func TestCatalogue_OnlyGreenScenarios(t *testing.T) {
	restore := setupDemoRegistryForCatalogue(t)
	defer restore()

	demoNames := map[string]bool{"http2": true, "diameter": true}
	cat := Catalogue()
	for _, s := range cat {
		if demoNames[s.Name()] {
			continue // demo entries — not subject to Green-only filter
		}
		if s.Rating() != scenarios.Green {
			t.Errorf("scenario %q has rating %q in catalogue, want green", s.Name(), s.Rating())
		}
	}
}

// TestCatalogue_DemosBefore_Scenarios verifies catalogue structure:
// demo section length == demo.All() length, then scenario section is all Green.
func TestCatalogue_DemosBefore_Scenarios(t *testing.T) {
	restore := setupDemoRegistryForCatalogue(t)
	defer restore()

	demoCount := len(All())
	cat := Catalogue()

	if len(cat) < demoCount {
		t.Fatalf("catalogue shorter than demo registry: %d < %d", len(cat), demoCount)
	}

	// First demoCount entries must be the demo entries in order.
	demos := All()
	for i, s := range cat[:demoCount] {
		if s.Name() != demos[i].Name() {
			t.Errorf("cat[%d].Name() = %q, want %q (demo order)", i, s.Name(), demos[i].Name())
		}
	}

	// Remaining entries must all be Green.
	for _, s := range cat[demoCount:] {
		if s.Rating() != scenarios.Green {
			t.Errorf("post-demo entry %q has rating %q, want green", s.Name(), s.Rating())
		}
	}
}

// TestFindInCatalogue_DemoFirst asserts that FindInCatalogue("http2") returns the
// demo registry entry, not a scenario (demo lookup wins).
func TestFindInCatalogue_DemoFirst(t *testing.T) {
	restore := setupDemoRegistryForCatalogue(t)
	defer restore()

	s := FindInCatalogue("http2")
	if s == nil {
		t.Fatal("FindInCatalogue(\"http2\") = nil, want non-nil")
	}
	if s.Name() != "http2" {
		t.Errorf("Name() = %q, want \"http2\"", s.Name())
	}
	// Must be from demo registry.
	if !IsDemoEntry(s) {
		t.Error("FindInCatalogue(\"http2\") returned a non-demo entry; demo should win")
	}
}

// TestFindInCatalogue_GreenScenario asserts that a known Green scenario is
// resolved when not in the demo registry.
func TestFindInCatalogue_GreenScenario(t *testing.T) {
	restore := setupDemoRegistryForCatalogue(t)
	defer restore()

	s := FindInCatalogue("http-routing-e2e")
	if s == nil {
		t.Fatal("FindInCatalogue(\"http-routing-e2e\") = nil, want non-nil")
	}
	if s.Name() != "http-routing-e2e" {
		t.Errorf("Name() = %q, want \"http-routing-e2e\"", s.Name())
	}
	if s.Rating() != scenarios.Green {
		t.Errorf("Rating() = %q, want green", s.Rating())
	}
}

// TestFindInCatalogue_RefusesAmber asserts that an Amber scenario is not
// returned by FindInCatalogue (returns nil).
func TestFindInCatalogue_RefusesAmber(t *testing.T) {
	restore := setupDemoRegistryForCatalogue(t)
	defer restore()

	// ai-token-counting is Amber — confirmed by aitokencounting/scenario.go.
	s := FindInCatalogue("ai-token-counting")
	if s != nil {
		t.Errorf("FindInCatalogue(\"ai-token-counting\") = %q, want nil (Amber excluded)", s.Name())
	}
}

// TestFindInCatalogue_NotFound asserts that an unknown name returns nil.
func TestFindInCatalogue_NotFound(t *testing.T) {
	restore := setupDemoRegistryForCatalogue(t)
	defer restore()

	s := FindInCatalogue("this-does-not-exist")
	if s != nil {
		t.Errorf("FindInCatalogue(unknown) = %q, want nil", s.Name())
	}
}

// TestIsDemoEntry distinguishes demo vs scenario entries.
func TestIsDemoEntry(t *testing.T) {
	restore := setupDemoRegistryForCatalogue(t)
	defer restore()

	demoEntry := Find("http2")
	if demoEntry == nil {
		t.Fatal("pre-condition: http2 not in demo registry")
	}
	if !IsDemoEntry(demoEntry) {
		t.Error("IsDemoEntry(http2) = false, want true")
	}

	greenScenario := scenarios.Find("http-routing-e2e")
	if greenScenario == nil {
		t.Fatal("pre-condition: http-routing-e2e not in scenarios registry")
	}
	if IsDemoEntry(greenScenario) {
		t.Error("IsDemoEntry(http-routing-e2e) = true, want false (it's a scenario)")
	}
}
