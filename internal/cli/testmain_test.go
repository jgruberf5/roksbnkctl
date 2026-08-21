package cli

import (
	"os"
	"testing"
)

// TestMain removes the tempdir that holds the compiled roksbnkctl binary the
// subprocess tests run against (#157).
//
// The binary is shared across sub-tests via a sync.Once, so it cannot use
// t.TempDir() — the first test to finish would delete it out from under the
// rest. Process scope is its real lifetime, and that is what TestMain gives.
//
// The previous code simply never removed it, on the stated grounds that
// os.TempDir is "cleaned up by the OS". That is not true on a tmpfs that clears
// only at reboot: each run left 112MB behind, and 94 runs filled a 16GB /tmp.
// The failure then surfaced as a linker error in an unrelated package, which is
// a very long way from the cause.
//
// CI never saw it — a fresh runner throws the filesystem away — so this only
// ever bit someone running the suite repeatedly on one machine.
func TestMain(m *testing.M) {
	code := m.Run()
	if binBuildDir != "" {
		_ = os.RemoveAll(binBuildDir)
	}
	os.Exit(code)
}
