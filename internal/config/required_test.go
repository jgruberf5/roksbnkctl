package config

import (
	"os"
	"regexp"
	"testing"
)

// #229 review. The cheatsheet's "Req" column derived requiredness from
// `omitempty`, which is a MARSHALLING directive and says nothing about whether a
// value must be supplied. It marked 25 fields required when four are, and missed
// `prefix` — whose absence is the most common `init` failure — because `prefix`
// happens to carry omitempty.
//
// A cheatsheet is trusted at a glance, and that is the glance an operator takes
// first, so the wrong answer there is worse than no column. These pin the one
// list both readers share.

func TestEveryRequiredFieldIsActuallyChecked(t *testing.T) {
	// An empty workspace is missing all of them, by definition.
	got := MissingRequiredFields(&Workspace{})
	if len(got) != len(RequiredConfigFields) {
		t.Fatalf("MissingRequiredFields on an empty workspace = %v, want all %d of %v",
			got, len(RequiredConfigFields), RequiredConfigFields)
	}
	// And a field listed but unchecked panics rather than being silently
	// advertised as required and never enforced.
	for _, f := range RequiredConfigFields {
		found := false
		for _, g := range got {
			if g == f {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is in RequiredConfigFields but MissingRequiredFields did not report it", f)
		}
	}
}

func TestASuppliedFieldIsNotReportedMissing(t *testing.T) {
	ws := &Workspace{Prefix: "p"}
	ws.IBMCloud.Region = "us-east"
	ws.IBMCloud.ResourceGroup = "default"
	ws.TFSource.Type = "embedded"
	if got := MissingRequiredFields(ws); len(got) != 0 {
		t.Errorf("a complete workspace reported missing: %v", got)
	}
	// Whitespace is not a value: " " passes a != "" check and fails at apply.
	ws.Prefix = "   "
	if got := MissingRequiredFields(ws); len(got) != 1 || got[0] != "prefix" {
		t.Errorf("a whitespace-only prefix must be missing, got %v", got)
	}
}

// The cheatsheet marks exactly these fields. If the list grows and the page is
// regenerated, the two stay in step; if someone adds a field to one place only,
// this fails.
func TestTheCheatsheetMarksExactlyTheRequiredFields(t *testing.T) {
	b, err := os.ReadFile("../../scripts/demos/config-cheatsheet.html")
	if err != nil {
		t.Skipf("cheatsheet not generated: %v", err)
	}
	re := regexp.MustCompile(`<tr data-req="true"[^>]*><td class="path"><code>([^<]+)</code>`)
	marked := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		marked[m[1]] = true
	}
	want := map[string]bool{}
	for _, f := range RequiredConfigFields {
		want[f] = true
	}
	for f := range want {
		if !marked[f] {
			t.Errorf("%q is required but the cheatsheet does not mark it — an operator "+
				"reading the page would omit it and `init` would refuse", f)
		}
	}
	for f := range marked {
		if !want[f] {
			t.Errorf("the cheatsheet marks %q required and `init` does not require it — "+
				"the page sends people to fill in fields that have working defaults", f)
		}
	}
	if len(marked) == 0 {
		t.Error("no fields marked required at all; the extraction regex has drifted, so " +
			"this comparison would pass vacuously")
	}
}
