package cli

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// env.example is GENERATED, and a generated file checked into the tree is only
// as good as the check that it is current. Without this, adding an override
// leaves the template silently missing it — and the template is what an operator
// pipes into a .env, so a missing variable is a setting they never learn exists.
//
// This is the same bargain as the book's generated chapters: edit the SOURCE
// (the config struct's doc comment, or the override table), then regenerate.
func TestEnvExampleIsCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the generator; part of the full suite")
	}
	out, err := exec.Command("go", "run", "../../tools/refgen/env-example").Output()
	if err != nil {
		t.Fatalf("running the generator: %v", err)
	}
	if string(out) != string(exampleEnvFile) {
		t.Errorf("internal/cli/env.example is stale.\n"+
			"Regenerate with:  go generate ./internal/cli/\n"+
			"(on disk %d bytes, generated %d)\n"+
			"Edit the SOURCE — the config struct's doc comment or the override table — "+
			"not the template.", len(exampleEnvFile), len(out))
	}
}

// The template's whole purpose is to be a complete starting point, so every
// override the tool actually reads must appear in it. A variable that exists but
// is undocumented here is unreachable to anyone working from the template.
func TestEnvExampleNamesEveryOverride(t *testing.T) {
	text := string(exampleEnvFile)
	var missing []string
	for _, name := range config.SupportedOverrideNames() {
		if !strings.Contains(text, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("env.example omits %d override(s), so they are invisible to anyone "+
			"starting from it:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}
