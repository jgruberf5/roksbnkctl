package cli

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// resolvedWorkspaceName must turn the raw -w flag into the RESOLVED workspace
// name the orchestration uses as its identifier (cluster-outputs.json lookup,
// applied-tfvars paths). The bug it guards: a no-`-w` `up` passed the empty raw
// flag straight through, so the cluster-outputs.json reuse-handoff missed, the
// create_roks_cluster=false override was skipped, and the BNK/Testing legs tried
// to re-create the cluster VPC/transit-gateway ("Provided Name … is not unique").
func TestResolvedWorkspaceName(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("ws-a", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveWorkspace("ws-b", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("ws-a"); err != nil {
		t.Fatal(err)
	}
	old := flagWorkspace
	defer func() { flagWorkspace = old }()

	// No -w → resolves to the current-workspace pointer (the broken case).
	flagWorkspace = ""
	if got := resolvedWorkspaceName(); got != "ws-a" {
		t.Errorf("no -w: resolvedWorkspaceName() = %q, want %q (current)", got, "ws-a")
	}
	// -w overrides the current pointer for this invocation.
	flagWorkspace = "ws-b"
	if got := resolvedWorkspaceName(); got != "ws-b" {
		t.Errorf("-w ws-b: resolvedWorkspaceName() = %q, want %q", got, "ws-b")
	}
}
