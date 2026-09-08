package cli

import (
	"strings"
	"testing"
)

// agy is Google's coding-agent CLI. The existing TestAgentRecipesAllRender checks
// every recipe cd's into the workspace; these check the things specific to this
// one, because a recipe is copy-pasted by a human and a wrong flag fails in their
// terminal, not in CI.

func TestAgyRecipeIsRegistered(t *testing.T) {
	var listed bool
	for _, n := range agentRecipeNames() {
		if n == "agy" {
			listed = true
		}
	}
	if !listed {
		t.Error("agy missing from agentRecipeNames — `roksbnkctl agent` would not list it")
	}
	if _, ok := agentRecipes["agy"]; !ok {
		t.Fatal("agy missing from agentRecipes — `roksbnkctl agent agy` would not resolve")
	}
	// The command's own Use string is what `--help` shows; a recipe that works but
	// is undiscoverable is only half-added.
	if !strings.Contains(agentCmd.Use, "agy") {
		t.Errorf("agentCmd.Use = %q, does not mention agy", agentCmd.Use)
	}
}

// The recipe must use flags agy actually has. Verified against agy 1.1.27:
// -i is "Run an initial prompt interactively and continue the session", which is
// what loads the persona WITHOUT depending on agy reading AGENTS.md by itself.
// Nothing in the shipped binary references AGENTS.md, so assuming auto-load — as
// the pi and opencode recipes legitimately do for their tools — would have
// produced a recipe that silently starts with no persona at all.
func TestAgyRecipeLoadsThePersonaExplicitly(t *testing.T) {
	out := agentRecipes["agy"]("/work/.roksbnkctl/acme", "")

	if !strings.Contains(out, "agy -i ") {
		t.Errorf("agy recipe does not use -i to seed the prompt:\n%s", out)
	}
	for _, want := range []string{"AGENTS.md", "personas/solution-architect.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("agy recipe never mentions %s:\n%s", want, out)
		}
	}
	// Guard against reintroducing the auto-load assumption.
	if strings.Contains(out, "auto-load") && !strings.Contains(out, "without relying") {
		t.Errorf("agy recipe claims auto-load; agy does not advertise AGENTS.md support:\n%s", out)
	}
}

// The prompt spans two lines with a backslash continuation INSIDE double quotes.
// Bash removes backslash-newline there, so it pastes as a single argument — but
// an unbalanced quote would silently split it into two, and the second half would
// be read as a filename. Counting quotes is what catches that.
func TestAgyRecipeQuotingIsBalanced(t *testing.T) {
	out := agentRecipes["agy"]("/work/.roksbnkctl/acme", "")
	if n := strings.Count(out, `"`); n%2 != 0 {
		t.Errorf("agy recipe has %d double quotes — unbalanced, so the prompt would "+
			"split into multiple shell words:\n%s", n, out)
	}
	// Every continuation must be a backslash at end-of-line; a trailing space
	// after it stops being a continuation and breaks the paste.
	for i, line := range strings.Split(out, "\n") {
		if strings.HasSuffix(line, "\\ ") || strings.HasSuffix(line, "\\\t") {
			t.Errorf("line %d ends with a backslash followed by whitespace, which is not a "+
				"line continuation: %q", i+1, line)
		}
	}
}
