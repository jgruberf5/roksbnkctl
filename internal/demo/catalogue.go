package demo

import "github.com/JLCode-tech/awsbnkctl/internal/scenarios"

// Catalogue returns the unified presentation catalogue: demo registry entries
// first (in registration order), then Green scenarios from
// internal/scenarios (in their registration order). Amber/Red scenarios are
// excluded. The two registries remain disjoint; this is a CLI-level union
// only — see registry.go's package doc.
func Catalogue() []scenarios.Scenario {
	result := All()
	for _, s := range scenarios.All() {
		if s.Rating() == scenarios.Green {
			result = append(result, s)
		}
	}
	return result
}

// FindInCatalogue resolves a name across the catalogue. Demo registry is
// consulted first; falls through to scenarios.Find iff Rating==Green.
// Returns nil if not found in either registry (or found in scenarios but
// not Green).
func FindInCatalogue(name string) scenarios.Scenario {
	if s := Find(name); s != nil {
		return s
	}
	s := scenarios.Find(name)
	if s != nil && s.Rating() == scenarios.Green {
		return s
	}
	return nil
}

// IsDemoEntry reports whether s was registered in the demo registry
// (as opposed to the scenarios registry).
func IsDemoEntry(s scenarios.Scenario) bool {
	return Find(s.Name()) != nil
}
