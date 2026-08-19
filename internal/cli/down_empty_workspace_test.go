package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// An orchestrated teardown runs the phase-down commands UNCONDITIONALLY — the
// demos' `teardown`, `roksbnkctl down`, a BNK Forge reverse-order module chain.
// So "this workspace has nothing left to destroy" has to be success for ALL of
// them or none: one command disagreeing turns a teardown that in fact finished
// into a reported failure, which is what #89 was.
//
// The inconsistency IS the defect, so this asserts they agree rather than
// asserting any single one's behaviour. `bnk down` and `tgw disconnect` already
// returned 0; `cluster down` and `down` returned 1.
func TestPhaseDownsAgreeOnAnEmptyWorkspace(t *testing.T) {
	bin := buildTestBinary(t)
	home := t.TempDir()

	// A workspace with a config and no state at all — the shape left behind once
	// a teardown has genuinely completed.
	ws := filepath.Join(home, ".roksbnkctl", "empty")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "ibmcloud:\n  region: us-south\n  resource_group: default\n" +
		"  api_key_b64: dGVzdA==\nprefix: empty\ncluster:\n  create: false\n  name: none\n"
	if err := os.WriteFile(filepath.Join(ws, "config.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"bnk down", []string{"-w", "empty", "bnk", "down", "--auto"}},
		{"tgw disconnect", []string{"-w", "empty", "tgw", "disconnect", "--auto"}},
		{"cluster down", []string{"-w", "empty", "cluster", "down", "--auto"}},
		{"down", []string{"-w", "empty", "down", "--auto"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = append(os.Environ(), "ROKSBNKCTL_HOME="+filepath.Join(home, ".roksbnkctl"), "HOME="+home)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s must succeed on an empty workspace — an unconditional teardown "+
					"runs every phase, so a non-zero exit here fails a teardown that worked (#89).\n"+
					"exit: %v\noutput:\n%s", tc.name, err, out)
			}
			if !strings.Contains(string(out), "nothing to do") &&
				!strings.Contains(string(out), "Nothing to destroy") {
				t.Errorf("%s should SAY it had nothing to do, not exit silently; got:\n%s", tc.name, out)
			}
		})
	}
}

// buildTestBinary compiles roksbnkctl once for the exit-code assertions above.
// The exit code is the contract under test, so it has to be a real process —
// calling the RunE funcs directly would not exercise it.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "roksbnkctl")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/roksbnkctl")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building test binary: %v\n%s", err, out)
	}
	return bin
}

// The other half of the distinction, and the reason it matters.
//
// DetectPresence reports all-false for an UNINITIALISED workspace exactly as it
// does for an empty one — they are indistinguishable to it, and mean opposite
// things. Making "empty" a success without this guard turned `-w prdo down` (a
// typo for prod) into a silent success that destroyed nothing, which is how
// someone concludes a teardown happened that did not. Caught reviewing the #89
// fix; the same hole already existed in `bnk down` and `tgw disconnect`.
func TestPhaseDownsRejectAnUninitialisedWorkspace(t *testing.T) {
	bin := buildTestBinary(t)
	home := t.TempDir() // no workspace created inside it

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"bnk down", []string{"-w", "no-such-ws", "bnk", "down", "--auto"}},
		{"tgw disconnect", []string{"-w", "no-such-ws", "tgw", "disconnect", "--auto"}},
		{"cluster down", []string{"-w", "no-such-ws", "cluster", "down", "--auto"}},
		{"down", []string{"-w", "no-such-ws", "down", "--auto"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = append(os.Environ(), "ROKSBNKCTL_HOME="+filepath.Join(home, ".roksbnkctl"), "HOME="+home)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("%s must REFUSE a workspace that does not exist — reporting success "+
					"for a mistyped -w is indistinguishable from a teardown that worked.\noutput:\n%s", tc.name, out)
			}
			if !strings.Contains(string(out), "not initialised") {
				t.Errorf("%s should name the cause; got:\n%s", tc.name, out)
			}
		})
	}
}
