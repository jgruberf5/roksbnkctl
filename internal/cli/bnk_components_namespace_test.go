package cli

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The defect: the component table hardcoded "f5-bnk", so `logs` and
// `bnk status` looked in a namespace that a renamed — or COLLAPSED (#66) —
// install does not use, and reported "0 pods" for a healthy deployment.
func TestBNKComponentsFollowTheConfiguredNamespace(t *testing.T) {
	const custom = "bnk-prod"
	got := bnkComponentsIn(custom)

	var checked int
	for _, c := range got {
		if c.Name == "cert-manager" {
			// cert-manager has its own namespace and must NOT be rewritten —
			// it is not a BNK component, it is a dependency.
			if c.Ns != "cert-manager" {
				t.Errorf("cert-manager namespace was rewritten to %q", c.Ns)
			}
			continue
		}
		checked++
		if c.Ns != custom {
			t.Errorf("component %q probes namespace %q, want %q", c.Name, c.Ns, custom)
		}
	}
	if checked == 0 {
		t.Fatal("no BNK-namespaced components in the table — the test proves nothing")
	}
}

// An unresolvable namespace must behave exactly as the hardcoded table did, so
// the fix cannot break a caller that has no workspace (e.g. `logs` run outside
// one).
func TestBNKComponentsFallBackToTheDefaultNamespace(t *testing.T) {
	for _, c := range bnkComponentsIn("") {
		if c.Name == "cert-manager" {
			continue
		}
		if c.Ns != config.DefaultFLONamespace {
			t.Errorf("component %q fell back to %q, want %q", c.Name, c.Ns, config.DefaultFLONamespace)
		}
	}
}

// Resolving must not mutate the package-level table — a second call with a
// different namespace would otherwise see the first call's substitution.
func TestBNKComponentsResolutionDoesNotMutateTheTable(t *testing.T) {
	_ = bnkComponentsIn("first")
	for _, c := range bnkComponentsIn("second") {
		if c.Name == "cert-manager" {
			continue
		}
		if c.Ns != "second" {
			t.Fatalf("component %q kept namespace %q from a previous call", c.Name, c.Ns)
		}
	}
	// And the template itself still carries the sentinel.
	for _, c := range bnkComponents {
		if c.Name != "cert-manager" && c.Ns != nsBNK {
			t.Fatalf("the package table was mutated: %q now has namespace %q", c.Name, c.Ns)
		}
	}
}

// The one-namespace case end to end: every BNK component, including the ones
// that live in the utils namespace by default, resolves to the single name.
func TestBNKComponentsInOneNamespaceInstall(t *testing.T) {
	const shared = "f5-bnk"
	for _, c := range bnkComponentsIn(shared) {
		if c.Name == "cert-manager" {
			continue
		}
		if c.Ns != shared {
			t.Errorf("one-namespace install: %q probes %q, want %q", c.Name, c.Ns, shared)
		}
	}
}
