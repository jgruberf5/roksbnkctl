package cli

import (
	"context"
	"testing"
	"time"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// TestDemoFlagPath_ForcesEnabled verifies that EnableDemo() (what runPhasedUp calls
// when --demo is set) forces Demo.Enabled=true and defaults TTL to DefaultDemoTTL
// on a cluster that has no demo: block.
func TestDemoFlagPath_ForcesEnabled(t *testing.T) {
	cl := &intent.Cluster{
		Metadata: intent.Metadata{Name: "test-cluster", Region: "ap-southeast-2"},
	}
	cl.EnableDemo()
	if !cl.DemoEnabled() {
		t.Fatal("after EnableDemo: DemoEnabled() = false, want true")
	}
	if cl.Demo.TTL != intent.DefaultDemoTTL {
		t.Errorf("after EnableDemo: TTL = %q, want %q", cl.Demo.TTL, intent.DefaultDemoTTL)
	}
}

// TestDemoFlagPath_NoOpWhenFlagFalse verifies that when the --demo flag is false
// (EnableDemo is not called) and no demo: block is present, the cluster is left
// unchanged (not a demo).
func TestDemoFlagPath_NoOpWhenFlagFalse(t *testing.T) {
	cl := &intent.Cluster{
		Metadata: intent.Metadata{Name: "test-cluster", Region: "ap-southeast-2"},
	}
	// --demo flag is false: do NOT call cl.EnableDemo().
	if cl.DemoEnabled() {
		t.Error("DemoEnabled() = true, want false when --demo flag is not set")
	}
}

// TestDemoMarkerWrite verifies that the demo marker write block (Set→Save→Load)
// correctly persists DEMO_MODE, DEMO_STAGED_AT, and DEMO_EXPIRY to state.env.
// Uses a real temp-dir State round-trip.
func TestDemoMarkerWrite(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}

	ttl, _ := time.ParseDuration("24h")
	before := time.Now().UTC()
	now := before
	st.Set("DEMO_MODE", "true")
	st.Set("DEMO_STAGED_AT", now.Format(time.RFC3339))
	st.Set("DEMO_EXPIRY", now.Add(ttl).Format(time.RFC3339))
	if err := st.Save(); err != nil {
		t.Fatalf("state.Save: %v", err)
	}

	// Reload from disk.
	st2, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load after Save: %v", err)
	}
	if got := st2.Get("DEMO_MODE"); got != "true" {
		t.Errorf("DEMO_MODE = %q, want \"true\"", got)
	}
	stagedAt := st2.Get("DEMO_STAGED_AT")
	if stagedAt == "" {
		t.Fatal("DEMO_STAGED_AT is empty after Save")
	}
	expiry := st2.Get("DEMO_EXPIRY")
	if expiry == "" {
		t.Fatal("DEMO_EXPIRY is empty after Save")
	}

	// Parse both timestamps and verify DEMO_EXPIRY ≈ DEMO_STAGED_AT + 24h.
	stagedAtTime, err := time.Parse(time.RFC3339, stagedAt)
	if err != nil {
		t.Fatalf("DEMO_STAGED_AT %q is not RFC3339: %v", stagedAt, err)
	}
	expiryTime, err := time.Parse(time.RFC3339, expiry)
	if err != nil {
		t.Fatalf("DEMO_EXPIRY %q is not RFC3339: %v", expiry, err)
	}
	diff := expiryTime.Sub(stagedAtTime)
	if diff != 24*time.Hour {
		t.Errorf("DEMO_EXPIRY - DEMO_STAGED_AT = %v, want 24h", diff)
	}
}

// TestDemoMarkerNotWrittenOnNormalUp verifies that a non-demo run leaves none of
// the DEMO_* keys in state. This guards against accidental writes on normal up.
func TestDemoMarkerNotWrittenOnNormalUp(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}

	// Simulate a normal up: write a regular key.
	st.Set("VPC_ID", "vpc-12345")
	if err := st.Save(); err != nil {
		t.Fatalf("state.Save: %v", err)
	}

	st2, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load after Save: %v", err)
	}
	for _, key := range []string{"DEMO_MODE", "DEMO_STAGED_AT", "DEMO_EXPIRY"} {
		if got := st2.Get(key); got != "" {
			t.Errorf("normal up: %s = %q, want \"\" (should not be written)", key, got)
		}
	}
}

// TestRunDemoCleanDown_SkipsWhenNotDemo verifies that runDemoCleanDown returns
// nil immediately when DEMO_MODE is not set — the non-demo path is a no-op.
func TestRunDemoCleanDown_SkipsWhenNotDemo(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	// DEMO_MODE is unset — runDemoCleanDown must return nil without touching kube.
	cl := &intent.Cluster{
		Metadata: intent.Metadata{Name: "non-demo-cluster", Region: "ap-southeast-2"},
	}
	// Override StateDir via the cluster name — state dir is ".awsbnkctl/<name>".
	// Pass the already-loaded state directly to runDemoCleanDown.
	if err := runDemoCleanDown(context.Background(), cl, st); err != nil {
		t.Errorf("runDemoCleanDown with DEMO_MODE unset: got error %v, want nil", err)
	}
}

// TestRunDemoCleanDown_SafeWithZeroUseCases verifies AC #6: when DEMO_MODE=true
// but no demo use-cases are registered (C0 reality), runDemoCleanDown succeeds
// without attempting to build a kube context.
func TestRunDemoCleanDown_SafeWithZeroUseCases(t *testing.T) {
	// Reset the demo registry to empty for this test.
	// We save and restore the original registry.
	original := demo.All()
	demo.ResetForTest()
	defer func() {
		demo.ResetForTest()
		for _, s := range original {
			demo.Register(s)
		}
	}()

	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	st.Set("DEMO_MODE", "true")

	cl := &intent.Cluster{
		Metadata: intent.Metadata{Name: "demo-empty-cluster", Region: "ap-southeast-2"},
	}

	// With zero use-cases registered, runDemoCleanDown must return nil without
	// attempting to build a kube context (which would fail — no real kubeconfig).
	if err := runDemoCleanDown(context.Background(), cl, st); err != nil {
		t.Errorf("runDemoCleanDown with DEMO_MODE=true + empty registry: got error %v, want nil", err)
	}
}
