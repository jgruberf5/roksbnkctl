package test

import (
	"os"
	"strings"
	"testing"
)

// The shipped example grid must always parse, validate, and expand — it's
// the doc users copy, so a schema change that breaks it should break CI.
func TestShippedExampleMatrix(t *testing.T) {
	raw, err := os.ReadFile("testdata/matrix.example.yaml")
	if err != nil {
		t.Fatalf("reading example: %v", err)
	}
	spec, err := ParseMatrix(raw)
	if err != nil {
		t.Fatalf("example does not parse/validate: %v", err)
	}
	cells, err := spec.Expand("")
	if err != nil {
		t.Fatalf("example does not expand: %v", err)
	}
	if len(cells) != 10 {
		t.Fatalf("example expanded %d cells, want 10", len(cells))
	}

	// Every cell must produce a runnable argv.
	var iperf, l7 int
	for _, c := range cells {
		if _, err := c.Argv(); err != nil {
			t.Errorf("cell %q argv: %v", c.Name, err)
		}
		switch c.Family {
		case FamilyIperf3:
			iperf++
		case FamilyL7:
			l7++
		}
	}
	if iperf != 4 || l7 != 6 {
		t.Errorf("family split = %d iperf3 / %d l7, want 4 / 6", iperf, l7)
	}

	// The route fixtures must render from the example's gateway identity.
	if got := strings.Count(RenderFixturesOrEmpty(spec), "kind:"); got == 0 {
		t.Error("example fixtures rendered nothing")
	}
}

// RenderFixturesOrEmpty is a test helper mirroring the CLI's render path.
func RenderFixturesOrEmpty(spec *MatrixSpec) string {
	out, err := RenderFixtures(FixturePlan{
		Gateway:      spec.Gateway,
		HTTPBackend:  spec.Fixtures.HTTPBackend,
		Routes:       spec.Fixtures.Routes,
		GatewayName:  spec.Gateway.Name,
		HTTPSection:  spec.Gateway.HTTPSection,
		HTTPSSection: spec.Gateway.HTTPSSection,
		TCPSection:   spec.Gateway.TCPSection,
	})
	if err != nil {
		return ""
	}
	return out
}
