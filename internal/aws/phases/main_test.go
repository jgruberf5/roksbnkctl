package phases

import (
	"context"
	"os"
	"testing"
)

// TestMain is the test binary entry-point for the phases package.
//
// It replaces smIAMPropagationWaitFn with a no-op before running any tests so
// that unit tests that exercise PhaseSageMakerUp do not incur the 60-second
// production IAM-propagation sleep. Tests that want to assert whether the wait
// fires (or is skipped) inject their own sentinel function and restore it via
// t.Cleanup — see TestPhaseSageMakerUp_FreshRole_WaitsForPropagation and
// TestPhaseSageMakerUp_ExistingRole_SkipsWait.
func TestMain(m *testing.M) {
	// Replace the production wait with a no-op for the entire test binary.
	smIAMPropagationWaitFn = func(_ context.Context) error { return nil }
	os.Exit(m.Run())
}
