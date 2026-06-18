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

// `state s3` / `state local` write the state: block so the operator never
// hand-edits config.yaml. Verify the round-trip + the required-flag guard.
func TestRunStateS3AndLocal(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("st", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("st"); err != nil {
		t.Fatal(err)
	}
	prev := flagWorkspace
	flagWorkspace = ""
	t.Cleanup(func() {
		flagWorkspace = prev
		flagStateEndpoint, flagStateBucket, flagStateRegion = "", "", ""
		flagStateKeyPrefix, flagStateAccessSrc, flagStateSecretSrc = "", "", ""
	})

	// missing required flags → error, nothing written.
	flagStateEndpoint, flagStateBucket, flagStateRegion = "", "", ""
	if err := runStateS3(nil, nil); err == nil {
		t.Fatal("state s3 with no flags must error")
	}

	flagStateEndpoint = "https://s3.us-south.cloud-object-storage.appdomain.cloud"
	flagStateBucket = "acme-tfstate"
	flagStateRegion = "us-south"
	flagStateKeyPrefix = "roksbnkctl"
	if err := runStateS3(nil, nil); err != nil {
		t.Fatalf("state s3: %v", err)
	}
	ws, err := config.LoadWorkspace("st")
	if err != nil {
		t.Fatal(err)
	}
	if ws.State.Backend != "s3" || ws.State.S3 == nil {
		t.Fatalf("after state s3, State = %+v; want backend s3 + s3 block", ws.State)
	}
	if ws.State.S3.Bucket != "acme-tfstate" || ws.State.S3.Region != "us-south" || ws.State.S3.KeyPrefix != "roksbnkctl" {
		t.Errorf("s3 block not persisted: %+v", *ws.State.S3)
	}

	// flip back to local; the s3 block is kept (inert) for an easy switch back.
	if err := runStateLocal(nil, nil); err != nil {
		t.Fatalf("state local: %v", err)
	}
	ws, err = config.LoadWorkspace("st")
	if err != nil {
		t.Fatal(err)
	}
	if ws.State.Backend != "local" {
		t.Errorf("after state local, backend = %q; want local", ws.State.Backend)
	}
	if ws.State.S3 == nil {
		t.Error("state local shouldn't drop the s3 block")
	}
}
