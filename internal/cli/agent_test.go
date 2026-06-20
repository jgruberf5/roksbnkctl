package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func TestAgentRecipesAllRender(t *testing.T) {
	for _, name := range agentRecipeNames() {
		fn, ok := agentRecipes[name]
		if !ok {
			t.Errorf("agentRecipeNames lists %q but it's not in agentRecipes", name)
			continue
		}
		out := fn("/work/.roksbnkctl/acme", "")
		if !strings.Contains(out, "/work/.roksbnkctl/acme") {
			t.Errorf("%s recipe doesn't cd into the workspace dir:\n%s", name, out)
		}
	}
}

func TestAgentRecipeEndpointWoven(t *testing.T) {
	// claude weaves ANTHROPIC_BASE_URL; aider/openai weave their own flags.
	out := agentRecipes["claude"]("/w", "https://llm.local/v1")
	if !strings.Contains(out, "ANTHROPIC_BASE_URL=https://llm.local/v1") {
		t.Errorf("claude recipe didn't weave the endpoint:\n%s", out)
	}
}

func TestAgentDefault(t *testing.T) {
	if got := agentDefault(nil); got != "claude" {
		t.Errorf("nil workspace default = %q, want claude", got)
	}
	ws := &config.Workspace{Agent: &config.AgentCfg{Default: "aider"}}
	if got := agentDefault(ws); got != "aider" {
		t.Errorf("configured default = %q, want aider", got)
	}
}

func TestCopyEmbeddedFilesScaffoldsAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()

	written, skipped, err := copyEmbeddedFiles(dir)
	if err != nil {
		t.Fatalf("copyEmbeddedFiles: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("first run skipped files: %v", skipped)
	}
	// Spot-check the key artifacts landed.
	for _, rel := range []string{"AGENTS.md", "personas/solution-architect.md", "personas/cloud-operator.md", "decisions.md"} {
		if _, statErr := os.Stat(filepath.Join(dir, rel)); statErr != nil {
			t.Errorf("expected %s to be written: %v", rel, statErr)
		}
	}
	if len(written) < 5 {
		t.Errorf("expected several files written, got %d: %v", len(written), written)
	}

	// Second run: everything already exists → all skipped, nothing written.
	written2, skipped2, err := copyEmbeddedFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(written2) != 0 {
		t.Errorf("re-run wrote files (should be idempotent): %v", written2)
	}
	if len(skipped2) == 0 {
		t.Error("re-run skipped nothing — expected existing files to be left as-is")
	}

	// An operator edit must survive a re-run.
	agents := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("EDITED"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := copyEmbeddedFiles(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(agents)
	if string(b) != "EDITED" {
		t.Error("re-run clobbered an operator-edited AGENTS.md")
	}
}

func TestAgentInitEndToEnd(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	pointWorkspaceFlag(t, "agent-demo")

	if err := runAgentInit(newCmd(), nil); err != nil {
		t.Fatalf("runAgentInit: %v", err)
	}
	dir, err := config.WorkspaceDir("agent-demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"AGENTS.md", "personas/test-engineer.md", "personas/doc-specialist.md", "journal"} {
		if _, statErr := os.Stat(filepath.Join(dir, rel)); statErr != nil {
			t.Errorf("agent init missing %s: %v", rel, statErr)
		}
	}
}
