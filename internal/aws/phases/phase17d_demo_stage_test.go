package phases

import (
	"context"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// TestPhase17dDemoStage_NoopWhenDemoDisabled confirms the self-gate: when
// DemoEnabled() is false, the phase returns nil without reading state or making
// any remote calls (which would panic on nil clients or fail on missing state keys).
func TestPhase17dDemoStage_NoopWhenDemoDisabled(t *testing.T) {
	cl := jumphostCluster() // jumphost enabled, but demo NOT enabled
	dir := t.TempDir()
	st, _ := state.Load(dir)
	// Clients is nil — any real call would panic; confirms no calls are made.
	err := Phase17dDemoStage(context.Background(), cl, st, nil, false)
	if err != nil {
		t.Errorf("expected nil (no-op), got: %v", err)
	}
}

// TestPhase17dDemoStage_DryRunNoStateMutation confirms dryRun short-circuits:
// no state keys are written and no remote calls are made (clients is nil).
func TestPhase17dDemoStage_DryRunNoStateMutation(t *testing.T) {
	cl := jumphostCluster()
	cl.EnableDemo()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	// Do NOT pre-populate state keys — dry-run must short-circuit before reading them.
	err := Phase17dDemoStage(context.Background(), cl, st, nil, true /*dryRun*/)
	if err != nil {
		t.Fatalf("dry-run returned error: %v", err)
	}
	if st.Get("DEMO_CLIENT_STAGED_AT") != "" {
		t.Error("dry-run must not write DEMO_CLIENT_STAGED_AT to state")
	}
}

// TestPhase17dDemoStage_MissingInstanceIDErrors confirms a clear error is returned
// when JUMPHOST_INSTANCE_ID is absent from state.
func TestPhase17dDemoStage_MissingInstanceIDErrors(t *testing.T) {
	cl := jumphostCluster()
	cl.EnableDemo()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	// Do NOT set JUMPHOST_INSTANCE_ID.
	st.Set("JUMPHOST_BNK_EXT_ENI_IP", "10.0.10.123")

	err := Phase17dDemoStage(context.Background(), cl, st, nil, false)
	if err == nil {
		t.Fatal("expected error for missing JUMPHOST_INSTANCE_ID, got nil")
	}
}

// TestPhase17dDemoStage_MissingSourceIPErrors confirms a clear error is returned
// when JUMPHOST_BNK_EXT_ENI_IP is absent.
func TestPhase17dDemoStage_MissingSourceIPErrors(t *testing.T) {
	cl := jumphostCluster()
	cl.EnableDemo()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("JUMPHOST_INSTANCE_ID", "i-test")
	// Do NOT set JUMPHOST_BNK_EXT_ENI_IP.

	err := Phase17dDemoStage(context.Background(), cl, st, nil, false)
	if err == nil {
		t.Fatal("expected error for missing JUMPHOST_BNK_EXT_ENI_IP, got nil")
	}
}
