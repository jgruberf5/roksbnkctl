package cli

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Deleting the current workspace moves the pointer to another existing one
// (alphabetically first) rather than refusing or leaving it dangling.
func TestRunWSDelete_AutoSwitchesCurrent(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	for _, n := range []string{"alpha", "beta", "gamma"} {
		if err := config.SaveWorkspace(n, &config.Workspace{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := config.SetCurrent("beta"); err != nil {
		t.Fatal(err)
	}

	flagWSForce = true // skip the confirm prompt + resource guard
	defer func() { flagWSForce = false }()
	if err := runWSDelete(nil, []string{"beta"}); err != nil {
		t.Fatalf("runWSDelete: %v", err)
	}

	g, _ := config.LoadGlobal()
	if g.CurrentWorkspace != "alpha" {
		t.Errorf("after deleting current %q, current = %q, want %q (first remaining)", "beta", g.CurrentWorkspace, "alpha")
	}
	if config.WorkspaceExists("beta") {
		t.Errorf("workspace %q still exists after delete", "beta")
	}
}

// Deleting the last workspace clears the current pointer — no phantom default.
func TestRunWSDelete_LastWorkspaceClearsCurrent(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("only", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("only"); err != nil {
		t.Fatal(err)
	}

	flagWSForce = true
	defer func() { flagWSForce = false }()
	if err := runWSDelete(nil, []string{"only"}); err != nil {
		t.Fatalf("runWSDelete: %v", err)
	}

	g, _ := config.LoadGlobal()
	if g.CurrentWorkspace != "" {
		t.Errorf("after deleting the last workspace, current = %q, want empty", g.CurrentWorkspace)
	}
}
