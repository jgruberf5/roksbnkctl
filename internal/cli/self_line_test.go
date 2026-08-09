package cli

import "testing"

// The release line is what keeps a BNK 2.3 operator off the 2.4 line, because
// the version number deliberately cannot: roksbnkctl's version is not tied to
// the BNK version, so both lines share one rising sequence and v1.43.0 vs
// v1.44.0 says nothing about which product line a build belongs to.
func TestReleaseLineMarkerParsing(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"trunk", "## roksbnkctl v1.44.0\n\n<!-- roksbnkctl-release-line: main -->\n**Release line:** `main`\n", "main"},
		{"release branch", "<!-- roksbnkctl-release-line: bnk-2-3 -->", "bnk-2-3"},
		{"tolerates whitespace", "<!--   roksbnkctl-release-line:   bnk-2-4   -->", "bnk-2-4"},
		// Every release cut before the split carries no marker. That must read as
		// "unknown", never as a line name, or those releases become ineligible.
		{"pre-split release", "## roksbnkctl v1.42.0\n\nSingle-binary CLI…", ""},
		{"empty body", "", ""},
	}
	for _, c := range cases {
		if got := (ghRelease{Body: c.body}).Line(); got != c.want {
			t.Errorf("%s: Line() = %q, want %q", c.name, got, c.want)
		}
	}
}

// onSameLine decides whether a release is a safe AUTOMATIC target. Both
// "unknown" cases must stay permissive: an unstamped binary is every build that
// predates this mechanism, and refusing to update those would be a regression
// far worse than the hazard being fixed.
func TestOnSameLine(t *testing.T) {
	orig := Line
	t.Cleanup(func() { Line = orig })

	marked := func(l string) ghRelease {
		return ghRelease{Body: "<!-- roksbnkctl-release-line: " + l + " -->"}
	}

	cases := []struct {
		name     string
		building string
		rel      ghRelease
		want     bool
	}{
		{"same line", "bnk-2-3", marked("bnk-2-3"), true},
		{"crossing lines", "bnk-2-3", marked("main"), false},
		{"crossing the other way", "main", marked("bnk-2-3"), false},
		// Permissive on either unknown.
		{"unstamped binary, marked release", "", marked("main"), true},
		{"stamped binary, pre-split release", "bnk-2-3", ghRelease{Body: "no marker"}, true},
		{"both unknown", "", ghRelease{}, true},
	}
	for _, c := range cases {
		Line = c.building
		if got := c.rel.onSameLine(); got != c.want {
			t.Errorf("%s: onSameLine() = %v, want %v", c.name, got, c.want)
		}
	}
}

// A marker must not be matched loosely — a release body that merely mentions
// the phrase must not be read as a stamp.
func TestReleaseLineMarker_RequiresTheComment(t *testing.T) {
	for _, body := range []string{
		"roksbnkctl-release-line: main",
		"Release line: main",
		"<!-- some-other-marker: main -->",
	} {
		if got := (ghRelease{Body: body}).Line(); got != "" {
			t.Errorf("body %q must not parse as a line stamp, got %q", body, got)
		}
	}
}
