package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repository's working agreements live in AGENTS.md, because that is the
// filename the widest set of coding agents read. CLAUDE.md beside it is a
// one-line `@AGENTS.md` include -- the same convention `roksbnkctl agent init`
// scaffolds into a workspace (internal/embedded/files/).
//
// The failure this guards is not a crash. It is someone answering "the rules are
// in CLAUDE.md" by pasting rules INTO CLAUDE.md, at which point there are two
// copies of the working agreements and they start disagreeing -- silently, and
// worst in exactly the situation the agreements exist for: two agent sessions in
// the same checkout, each reading a different file.

func TestClaudeMDIsOnlyAnIncludeOfAgentsMD(t *testing.T) {
	root := repoRootForDocTest(t)

	body, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	got := strings.TrimSpace(string(body))
	if got != "@AGENTS.md" {
		t.Errorf("CLAUDE.md is %q, want exactly \"@AGENTS.md\".\n"+
			"The working agreements belong in AGENTS.md; CLAUDE.md is the include that\n"+
			"points Claude Code at them. Content here means two copies that can disagree.", got)
	}
}

func TestAgentsMDCarriesTheWorkingAgreements(t *testing.T) {
	root := repoRootForDocTest(t)

	body, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("reading AGENTS.md: %v", err)
	}
	s := string(body)

	// Section headings, not prose: these are the agreements other sessions are
	// told to follow, and an empty or stub AGENTS.md with a valid include in
	// CLAUDE.md would otherwise pass the test above while the rules were gone.
	for _, want := range []string{
		"# Working agreements for this repository",
		"## Every issue gets labelled when it is opened",
		"## Every PR gets a complete review, posted as a comment on the PR",
		"## Tests must exercise behaviour",
		"## Never `git add -A`",
		"## Stacked PRs",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("AGENTS.md no longer contains %q", want)
		}
	}
}

// The controls that ENFORCE the agreements cite them by filename. A rename that
// left those pointing at the old name would send the next reader to a file that
// is now a one-line include -- the "documentation describing behaviour no code
// implements" class from #279, applied to the rules themselves.
func TestTheEnforcementScriptsCiteAgentsMD(t *testing.T) {
	root := repoRootForDocTest(t)

	for _, rel := range []string{
		filepath.Join(".githooks", "pre-push"),
		filepath.Join("scripts", "pr-review-audit.sh"),
		filepath.Join("scripts", "branch-hygiene.sh"),
	} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		s := string(body)
		if strings.Contains(s, "CLAUDE.md") {
			t.Errorf("%s still cites CLAUDE.md; the agreements moved to AGENTS.md", rel)
		}
		if !strings.Contains(s, "AGENTS.md") {
			t.Errorf("%s cites neither file — it enforces a rule it does not name", rel)
		}
	}
}
