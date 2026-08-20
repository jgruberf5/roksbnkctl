package cli

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The CHANGELOG is the release's user-facing record, and two ways of corrupting
// it both happened while preparing v1.50.0 — neither visible to any other test:
//
//   - #117's entry landed under **v1.43.0**, a release from three weeks
//     earlier, because the edit matched the first "### Changed" in the file and
//     Unreleased had no Changed section yet. A change shipped in one release,
//     documented under another.
//   - Four entries belonging under Fixed were stranded under Documentation:
//     each PR inserted at "### Fixed", and a later PR added a new section
//     heading above them.
//
// Both are the same shape — an append that lands by pattern rather than by
// position — and both are invisible until someone reads the rendered file.
func TestChangelogUnreleasedCoversEveryIssueClosedSinceTheLastTag(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to git; runs in the full suite")
	}
	body, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("reading CHANGELOG.md: %v", err)
	}
	unreleased := unreleasedSection(t, string(body))
	if strings.TrimSpace(unreleased) == "" {
		t.Skip("no Unreleased section — nothing pending")
	}

	// Issues closed by the commits since the last tag, from their "(#NN)"
	// merge-commit suffixes.
	out, err := exec.Command("git", "-C", "../..", "log", "--format=%s", lastTag(t)+"..HEAD").Output()
	if err != nil {
		t.Skipf("git log unavailable: %v", err)
	}
	// A squash merge reads "subject (#issue) (#pr)"; the issue is the first.
	re := regexp.MustCompile(`\(#(\d+)\)`)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		m := re.FindAllStringSubmatch(line, -1)
		if len(m) < 2 {
			continue // no issue reference, e.g. a chore commit
		}
		seen[m[0][1]] = true
	}
	for issue := range seen {
		if !strings.Contains(unreleased, "#"+issue) {
			t.Errorf("issue #%s was closed since %s but is not mentioned in the Unreleased "+
				"section — the release would ship a fix nobody can find in the changelog",
				issue, lastTag(t))
		}
	}
}

// Every entry must sit under a section, and only under the ones this project
// uses. A stray heading is how four Fixed entries ended up under Documentation.
func TestChangelogUnreleasedSectionsAreKnown(t *testing.T) {
	body, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	unreleased := unreleasedSection(t, string(body))
	known := map[string]bool{"Added": true, "Changed": true, "Fixed": true, "Removed": true, "Security": true, "Documentation": true}

	var current string
	for _, line := range strings.Split(unreleased, "\n") {
		if strings.HasPrefix(line, "### ") {
			current = strings.TrimPrefix(line, "### ")
			if !known[current] {
				t.Errorf("unknown Unreleased section %q — Keep a Changelog uses "+
					"Added/Changed/Fixed/Removed/Security, plus Documentation here", current)
			}
			continue
		}
		if strings.HasPrefix(line, "- ") && current == "" {
			t.Errorf("entry outside any section: %.70s", line)
		}
	}
}

// No entry may appear under a RELEASED heading after that release was tagged.
// #117's entry landed under v1.43.0 and would have shipped that way.
func TestChangelogReleasedSectionsAreNotEditedAfterTagging(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to git; runs in the full suite")
	}
	tag := lastTag(t)
	// Compare the file ON DISK against the tagged version, not `git diff
	// tag..HEAD` — that only sees committed state, so it cannot catch the
	// defect while it is still in the working tree, which is exactly when a
	// release is being prepared.
	out, err := exec.Command("git", "-C", "../..", "show", tag+":CHANGELOG.md").Output()
	if err != nil {
		t.Skipf("git show unavailable: %v", err)
	}
	current, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}

	// Every released section must be byte-identical to what was tagged. An
	// entry appended to one is a change documented under a release it did not
	// ship in — #117's landed under v1.43.0, three weeks early.
	for name, tagged := range releasedSections(string(out)) {
		now, ok := releasedSections(string(current))[name]
		if !ok {
			t.Errorf("released section %q disappeared from the changelog", name)
			continue
		}
		if now != tagged {
			t.Errorf("released section %q changed since %s.\nA change cannot ship in a "+
				"release that is already cut — put the entry under ## Unreleased.\n"+
				"--- was ---\n%s\n--- now ---\n%s",
				name, tag, truncate(tagged), truncate(now))
		}
	}
}

// releasedSections maps "## vX.Y.Z — date" to its body, excluding Unreleased.
func releasedSections(body string) map[string]string {
	out := map[string]string{}
	parts := strings.Split(body, "\n## ")
	for _, p := range parts[1:] {
		head := p
		if i := strings.Index(p, "\n"); i >= 0 {
			head = p[:i]
		}
		if strings.HasPrefix(head, "Unreleased") {
			continue
		}
		out[head] = p
	}
	return out
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

func unreleasedSection(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, "## Unreleased")
	if start < 0 {
		return ""
	}
	rest := body[start+len("## Unreleased"):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end]
	}
	return rest
}

func lastTag(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "-C", "../..", "describe", "--tags", "--abbrev=0").Output()
	if err != nil {
		t.Skipf("no tags: %v", err)
	}
	return strings.TrimSpace(string(out))
}
