package cli

import (
	"os"
	"strings"
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
// This alone is NOT sufficient, which is why the build path is also fixed
// rather than unique. m.Run() does not return when a test panics, when -timeout
// fires, or on Ctrl-C, so nothing below runs in any of those cases. The fixed
// path bounds them; this bounds the clean case to nothing at all.
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

// The build path must stay FIXED. A unique directory plus this cleanup looks
// equivalent and is not: m.Run() never returns on a panic, a -timeout kill, or
// Ctrl-C, so a unique path resumes growing without limit in exactly the
// situations a developer hits most. Measured on the unique-path version — clean
// pass 0, t.Fatal 0, but panic 112MB, timeout 112MB, SIGINT 199MB, and three
// panicking runs 336MB across three directories.
//
// With a fixed path `go build -o` overwrites, so three panicking runs leave one
// directory and 112MB, however each one died.
func TestTheBuildPathIsFixedSoAbnormalExitsCannotAccumulate(t *testing.T) {
	src, err := os.ReadFile("argv_strictness_test.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	if strings.Contains(body, `os.MkdirTemp("", "roksbnkctl-argv-build`) {
		t.Error("the test binary is built into a UNIQUE tempdir again.\n" +
			"TestMain cannot clean that up after a panic, a -timeout kill, or Ctrl-C — " +
			"m.Run() never returns — so the directories accumulate exactly as they did " +
			"before #157. Use a fixed path under os.TempDir(); `go build -o` overwrites, " +
			"which bounds the footprint at one binary however the run dies.")
	}
	if !strings.Contains(body, `filepath.Join(os.TempDir(), "roksbnkctl-argv-build")`) {
		t.Error("could not find the fixed build path; if it moved, update this guard — " +
			"a check that cannot see the thing it guards is worse than none")
	}
}
