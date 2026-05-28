// Package demo provides the registry, narration module, and lifecycle helpers
// for the `demo {list,run,clean}` command group.
//
// Demo use-cases (C1/C2/D slices) implement [scenarios.Scenario] and
// self-register into this package's registry via init(). This registry is
// distinct from [scenarios.registry]: `demo list` shows a curated demo
// catalogue; `scenarios list` shows the validation suite. The two must never
// share entries so that deleting the demo feature deletes this package
// wholesale without surgical un-tagging in scenarios.
package demo

import "github.com/JLCode-tech/awsbnkctl/internal/scenarios"

// registry holds demo use-cases. Initialised to nil; Register appends.
var registry []scenarios.Scenario

// Register adds s to the demo use-case catalogue.
// Panics on duplicate Name() — that is a build-time programmer error,
// not something an operator can trigger.
func Register(s scenarios.Scenario) {
	for _, e := range registry {
		if e.Name() == s.Name() {
			panic("demo: duplicate registration: " + s.Name())
		}
	}
	registry = append(registry, s)
}

// All returns a copy of the registered demo use-cases in registration order.
func All() []scenarios.Scenario {
	return append([]scenarios.Scenario(nil), registry...)
}

// Find returns the demo use-case with the given name, or nil.
func Find(name string) scenarios.Scenario {
	for _, s := range registry {
		if s.Name() == name {
			return s
		}
	}
	return nil
}
