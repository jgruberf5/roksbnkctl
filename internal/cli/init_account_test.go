package cli

import (
	"context"
	"testing"
)

// A non-TTY interview that defaults workers to 0 must clamp to the 3-AZ floor
// (one worker per zone = 3 total). ic is nil-safe in the non-TTY create path.
func TestRunAccountInterview_WorkerFloor(t *testing.T) {
	cctx := newInitContext(t, "floor-ws", "")
	choices, err := runAccountInterview(context.Background(), nil, cctx, "us-south", "4.18", 0, true)
	if err != nil {
		t.Fatalf("runAccountInterview: %v", err)
	}
	if choices.Cluster.WorkersPerZone != 1 {
		t.Errorf("WorkersPerZone = %d, want clamped to 1 (3-AZ floor)", choices.Cluster.WorkersPerZone)
	}
	if !choices.Cluster.Create {
		t.Errorf("Cluster.Create = false, want true (create default)")
	}
}
