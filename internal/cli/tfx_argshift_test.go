package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The exact shape issue #50 hit: terraform interpolated an empty chart version, so
// `--version` swallowed `--registry-login` and `repo.f5.com` fell out as a positional.
// Cobra reported `unknown command "repo.f5.com"` — a message about a host, for a bug
// about a missing version. The guard must name the real fault.
func TestRejectShiftedFlagValues_EmptyInterpolation(t *testing.T) {
	var version, registryLogin, chart string
	cmd := &cobra.Command{Use: "pull-chart", Args: rejectShiftedFlagValues, RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().StringVar(&chart, "chart", "", "")
	cmd.Flags().StringVar(&version, "version", "", "")
	cmd.Flags().StringVar(&registryLogin, "registry-login", "", "")
	cmd.SetArgs([]string{
		"--chart", "oci://repo.f5.com/charts/f5-lifecycle-operator",
		"--version", // ← the empty interpolation
		"--registry-login", "repo.f5.com",
	})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a shifted argument list must be rejected, not silently accepted")
	}
	for _, want := range []string{"--version", "--registry-login", "EMPTY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must mention %q so the real fault is visible; got: %v", want, err)
		}
	}
}

// A legitimate invocation must still pass — the guard keys on a value that is itself
// a flag, not on emptiness, so optional flags stay optional.
func TestRejectShiftedFlagValues_ValidInvocationPasses(t *testing.T) {
	var version, chart string
	cmd := &cobra.Command{Use: "pull-chart", Args: rejectShiftedFlagValues, RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().StringVar(&chart, "chart", "", "")
	cmd.Flags().StringVar(&version, "version", "", "")
	cmd.SetArgs([]string{"--chart", "oci://repo.f5.com/charts/x", "--version", "1.2.3"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("a well-formed invocation must pass: %v", err)
	}
}
