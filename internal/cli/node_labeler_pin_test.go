package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

// The BOM must name the image the install actually pulls. Those live in two
// places -- the shipped terraform's variable defaults and this package's
// default* constants -- and until #270 the only thing keeping them equal was a
// comment saying they mirrored each other.
//
// Drift here is quiet and expensive: the mirror ends up holding one image while
// the install pulls another, so an air-gapped cluster fails at pod-pull time
// with an ImagePullBackOff for a registry it cannot reach, long after `registry
// verify` said the mirror was complete.

func terraformDefault(t *testing.T, variable string) string {
	t.Helper()
	p := filepath.Join(repoRootForDocTest(t), "terraform", "modules", "flo", "modules", "flo", "variables.tf")
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading %s: %v", p, err)
	}
	// The default line inside `variable "<name>" { ... }`. Anchored to the block
	// so another variable's default cannot be picked up by accident.
	re := regexp.MustCompile(`(?s)variable\s+"` + regexp.QuoteMeta(variable) + `"\s*\{.*?default\s*=\s*"([^"]*)"`)
	m := re.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no default found for terraform variable %q in %s", variable, p)
	}
	return string(m[1])
}

func TestNodeLabelerDefaultsMatchTheShippedTerraform(t *testing.T) {
	if got, want := defaultNodeLabelerTag, terraformDefault(t, "node_labeler_image_tag"); got != want {
		t.Errorf("defaultNodeLabelerTag = %q, terraform node_labeler_image_tag = %q — "+
			"the BOM would mirror a different image than the install pulls", got, want)
	}

	// The BOM's host and name have to agree with the terraform too, or the
	// mirrored path and the pull path diverge. Deps() is the only place that
	// builds the artifact, so ask it rather than re-reading the constants.
	var found bool
	for _, a := range bnkbom.Deps("v1.17.3", defaultNodeLabelerTag) {
		if a.Origin != bnkbom.OriginNodeLabeler {
			continue
		}
		found = true
		if got, want := a.SourceHost, terraformDefault(t, "node_labeler_image_host"); got != want {
			t.Errorf("BOM node-labeler SourceHost = %q, terraform node_labeler_image_host = %q", got, want)
		}
		if got, want := a.Name, terraformDefault(t, "node_labeler_image_name"); got != want {
			t.Errorf("BOM node-labeler Name = %q, terraform node_labeler_image_name = %q", got, want)
		}
	}
	if !found {
		t.Fatal("Deps() produced no node-labeler artifact")
	}
}

// TestNodeLabelerTagIsPinned is the guard for the defect itself. Equality with
// the terraform is not enough -- both sides could agree on "latest", which is
// exactly the state #270 describes.
func TestNodeLabelerTagIsPinned(t *testing.T) {
	for _, tc := range []struct{ what, tag string }{
		{"defaultNodeLabelerTag", defaultNodeLabelerTag},
		{"terraform node_labeler_image_tag", terraformDefault(t, "node_labeler_image_tag")},
	} {
		if tc.tag == "latest" || tc.tag == "" {
			t.Errorf("%s = %q — a floating tag makes `registry verify` report a good "+
				"mirror as digest-mismatched as soon as upstream re-pushes (#270)", tc.what, tc.tag)
		}
	}
}
