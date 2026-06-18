package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Shared test helpers for the cli package (formerly in init_var_file_test.go,
// removed with the `init --var-file` flag).

// runRootCmd drives the public cobra surface — the same code path a real
// `roksbnkctl ...` invocation runs — capturing stdout/stderr and returning the
// Execute error so negative-path assertions can grep error text.
func runRootCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	rootCmd.SetArgs(args)
	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// resetInitFlags zeroes the package-global flag vars init reads, both before
// AND after the test, so a stray value from a previous sub-test (or a prior
// package test) cannot bleed into the run.
func resetInitFlags(t *testing.T) {
	t.Helper()
	prevWS, prevTF, prevUpg, prevVF :=
		flagWorkspace, flagTFSource, flagUpgradeTF, flagVarFiles
	flagWorkspace, flagTFSource, flagUpgradeTF, flagVarFiles = "", "", false, nil
	t.Cleanup(func() {
		flagWorkspace, flagTFSource, flagUpgradeTF, flagVarFiles =
			prevWS, prevTF, prevUpg, prevVF
	})
}

// stageHermeticHome points ROKSBNKCTL_HOME at a fresh tempdir so the test never
// touches the operator's real workspace tree.
func stageHermeticHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, home)
	return home
}

// skipIfNoLiveIBMCreds skips a positive-path test that needs live IBM Cloud
// credentials (init runs ibm.Verify() before persisting), unless an API key is
// present in the environment.
func skipIfNoLiveIBMCreds(t *testing.T) {
	t.Helper()
	for _, v := range []string{"IBMCLOUD_API_KEY", "IC_API_KEY"} {
		if os.Getenv(v) != "" {
			return
		}
	}
	t.Skip("skipped: init runs ibm.Verify() before persisting; no IBMCLOUD_API_KEY in env")
}

// writeTFVars drops content at <dir>/<name> and returns the absolute path — used
// by the phase-level `--var-file` tests (cluster/bnk/gateway up/down).
func writeTFVars(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("seeding tfvars fixture %s: %v", p, err)
	}
	return p
}
