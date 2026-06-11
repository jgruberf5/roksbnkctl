package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// state migrate refuses unless the workspace declares the s3 backend — the
// guard that keeps it from doing anything to a local-state workspace.
func TestRunStateMigrate_RequiresS3Backend(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "state-mig-test"
	if err := config.SaveWorkspace(ws, &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
		// no state: block → backend defaults to local
	}); err != nil {
		t.Fatal(err)
	}

	prev := flagWorkspace
	flagWorkspace = ws
	t.Cleanup(func() { flagWorkspace = prev })

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())

	err := runStateMigrate(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "state.backend") {
		t.Fatalf("want an error about state.backend not being s3, got %v", err)
	}
}
