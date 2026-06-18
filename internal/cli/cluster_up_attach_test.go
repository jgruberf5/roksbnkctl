package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// TestClusterUp_AttachGuard pins the create-or-attach branch: with
// cluster.create=false, `cluster up` attaches to the existing cluster named in
// config — and fails fast (never runs terraform) when no name is set.
func TestClusterUp_AttachGuard(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws", &config.Workspace{
		Cluster: config.ClusterCfg{Create: false, Name: ""},
	}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("ws"); err != nil {
		t.Fatal(err)
	}
	old := flagWorkspace
	flagWorkspace = ""
	defer func() { flagWorkspace = old }()

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	err := runClusterUp(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "cluster.name is empty") {
		t.Fatalf("want a cluster.name-empty error, got %v", err)
	}
}
