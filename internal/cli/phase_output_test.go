package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// TestRunPhaseOutput exercises the four `<phase> output` commands' shared body:
// a named output emits the raw value; the full set redacts sensitive unless
// --show-sensitive; an unknown name errors.
func TestRunPhaseOutput(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("ws"); err != nil {
		t.Fatal(err)
	}
	dir, err := config.WorkspaceClusterStateDir("ws")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, _ := json.Marshal(map[string]any{"outputs": map[string]map[string]any{
		"cluster_id": {"value": "abc123", "sensitive": false},
		"secret":     {"value": "shh", "sensitive": true},
	}})
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), st, 0o644); err != nil {
		t.Fatal(err)
	}

	oldWS := flagWorkspace
	flagWorkspace = ""
	prevJSON, prevShow := flagOutputJSON, flagOutputShowSensitive
	defer func() {
		flagWorkspace = oldWS
		flagOutputJSON, flagOutputShowSensitive = prevJSON, prevShow
	}()

	run := func(args []string) string {
		cmd := &cobra.Command{}
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		if err := runPhaseOutput(cmd, "cluster", config.WorkspaceClusterStateDir, args); err != nil {
			t.Fatalf("runPhaseOutput(%v): %v", args, err)
		}
		return buf.String()
	}

	flagOutputJSON = false
	if got := strings.TrimSpace(run([]string{"cluster_id"})); got != "abc123" {
		t.Errorf("named output = %q, want abc123", got)
	}

	flagOutputJSON, flagOutputShowSensitive = true, false
	var m map[string]any
	if err := json.Unmarshal([]byte(run(nil)), &m); err != nil {
		t.Fatal(err)
	}
	if m["cluster_id"] != "abc123" || m["secret"] != "<sensitive>" {
		t.Errorf("full set = %+v; want cluster_id:abc123 secret:<sensitive>", m)
	}

	flagOutputShowSensitive = true
	if err := json.Unmarshal([]byte(run(nil)), &m); err != nil {
		t.Fatal(err)
	}
	if m["secret"] != "shh" {
		t.Errorf("--show-sensitive should reveal secret, got %v", m["secret"])
	}

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := runPhaseOutput(cmd, "cluster", config.WorkspaceClusterStateDir, []string{"nope"}); err == nil {
		t.Error("unknown output name must error")
	}
}
