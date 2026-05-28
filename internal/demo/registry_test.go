package demo

import (
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
)

// fakeScenario is a minimal test double satisfying [scenarios.Scenario].
type fakeScenario struct {
	name   string
	title  string
	rating scenarios.Rating
}

func (f *fakeScenario) Name() string                                   { return f.name }
func (f *fakeScenario) Title() string                                  { return f.title }
func (f *fakeScenario) Rating() scenarios.Rating                       { return f.rating }
func (f *fakeScenario) Description() string                            { return "fake use-case: " + f.name }
func (f *fakeScenario) Dependencies() []string                         { return nil }
func (f *fakeScenario) Manifests(*scenarios.Context) ([]string, error) { return nil, nil }
func (f *fakeScenario) Apply(*scenarios.Context) error                 { return nil }
func (f *fakeScenario) Verify(*scenarios.Context) scenarios.Result {
	return scenarios.Result{Status: "ok", Summary: "fake verify ok"}
}
func (f *fakeScenario) Cleanup(*scenarios.Context) error    { return nil }
func (f *fakeScenario) Namespace(*scenarios.Context) string { return "fake-ns" }

// resetRegistry wipes the package-level registry for test isolation.
// Called at the start of each test that mutates the registry.
func resetRegistry() { registry = nil }

func TestRegistry_AllEmpty(t *testing.T) {
	resetRegistry()
	if got := All(); len(got) != 0 {
		t.Errorf("All() on empty registry = %d entries, want 0", len(got))
	}
}

func TestRegistry_RegisterAndAll(t *testing.T) {
	resetRegistry()
	s1 := &fakeScenario{name: "uc-a", title: "Use-Case A", rating: scenarios.Green}
	s2 := &fakeScenario{name: "uc-b", title: "Use-Case B", rating: scenarios.Amber}
	Register(s1)
	Register(s2)

	all := All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
	if all[0].Name() != "uc-a" {
		t.Errorf("all[0].Name() = %q, want \"uc-a\"", all[0].Name())
	}
	if all[1].Name() != "uc-b" {
		t.Errorf("all[1].Name() = %q, want \"uc-b\"", all[1].Name())
	}
}

func TestRegistry_Find(t *testing.T) {
	resetRegistry()
	s := &fakeScenario{name: "uc-find", title: "Find Me", rating: scenarios.Green}
	Register(s)

	got := Find("uc-find")
	if got == nil {
		t.Fatal("Find(\"uc-find\") = nil, want non-nil")
	}
	if got.Name() != "uc-find" {
		t.Errorf("Find: Name() = %q, want \"uc-find\"", got.Name())
	}

	if Find("nonexistent") != nil {
		t.Error("Find(\"nonexistent\") != nil, want nil")
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	resetRegistry()
	Register(&fakeScenario{name: "dup"})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate Name(), did not panic")
		}
	}()
	Register(&fakeScenario{name: "dup"})
}

func TestRegistry_AllReturnsCopy(t *testing.T) {
	resetRegistry()
	Register(&fakeScenario{name: "copy-test"})
	a := All()
	b := All()
	// Appending to one copy must not affect the other or the internal registry.
	a = append(a, &fakeScenario{name: "injected"})
	if len(a) != 2 {
		t.Errorf("append to a did not grow the local copy: len = %d", len(a))
	}
	if len(b) != 1 {
		t.Errorf("All() returned shared slice: b len = %d after mutating a", len(b))
	}
	if len(registry) != 1 {
		t.Errorf("All() mutated the internal registry: len = %d", len(registry))
	}
}
