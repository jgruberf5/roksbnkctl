// Sprint 18 staff Issue 1 — `cos bucket get --help` smoke test.
//
// Acceptance criterion 9 calls for "a `cos bucket get --help` smoke
// line wired into whatever help-snapshot test the package has." The
// internal/cli package doesn't carry a cobra help-snapshot suite at
// time-of-writing, so staff adds a one-purpose additive test here: it
// exercises `--help` flag parsing for the new subcommand (cobra's
// built-in help generator) and asserts the resulting output names the
// two positional args plus the --no-clobber flag.
//
// Additive only — no edits to existing _test.go files. Lives in
// internal/cli (not internal/cos) because it tests the cobra wiring
// staff adds in cos.go, not the library function.

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestCOSBucketGet_HelpSmoke exercises `roksbnkctl cos bucket get --help`
// at the cobra layer. SetArgs feeds an exact arg vector; SetOut /
// SetErr capture both streams so the test sees whatever cobra prints
// (cobra writes help to OutOrStderr by default).
func TestCOSBucketGet_HelpSmoke(t *testing.T) {
	var out, errBuf bytes.Buffer
	// Save + restore the root command's IO so this test doesn't pollute
	// other tests' captures. rootCmd is package-level; cobra mutates the
	// embedded writers via SetOut / SetErr.
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})

	rootCmd.SetArgs([]string{"cos", "bucket", "get", "--help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("cos bucket get --help returned err: %v", err)
	}

	// Cobra writes help text to the root command's OutOrStderr (which
	// SetOut redirects). Concatenate both buffers so the test isn't
	// brittle to which exact stream cobra picks for help.
	got := out.String() + errBuf.String()
	for _, want := range []string{
		"cos bucket get",
		"<bucket>",
		"<local-dir>",
		"--no-clobber",
		"--instance",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help output missing %q.\nfull output:\n%s", want, got)
		}
	}
}
