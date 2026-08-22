package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A demo that reports success no matter what happened cannot verify anything,
// which is the one job it has when it runs unattended.
//
// The lifecycle demos are `set -uo pipefail` with no `-e`, `run` returned the
// command's status faithfully, and no call site ever looked at it. Observed on
// BNK 2.4: the swap phase's `bnk up` failed on EVERY object with "namespace
// f5-bnk is being terminated", and the next line printed was
//
//	✓ BNK removed and reinstalled — no re-provisioning, the cluster never moved
//
// then DEMO COMPLETE, then exit 0. Five such runs would have been recorded as
// five clean passes.
//
// Each world-changing step is failed ON ITS OWN. A stub that failed every
// `up`/`down` only proved the FIRST `must` worked: an adversarial review
// reverted every later `must` back to `run` and the test stayed green, leaving
// `bnk up`, `testing up`, `bnk down` and the swap-phase `bnk up` unguarded —
// the last being the exact failure this change exists for.
func TestDemosFailWhenAPhaseFails(t *testing.T) {
	if testing.Short() {
		t.Skip("drives the demos; runs in the full suite")
	}
	root := repoRootForDemoTest(t)

	demos := []string{
		"scripts/demos/cluster-lifecycle-cli-demo/cluster-lifecycle-cli-demo.sh",
		"scripts/demos/cluster-lifecycle-ci-demo/cluster-lifecycle-ci-demo.sh",
	}
	steps := []string{"cluster up", "bnk up", "testing up", "bnk down", "test"}

	for _, rel := range demos {
		for _, step := range steps {
			name := filepath.Base(rel) + "/" + strings.ReplaceAll(step, " ", "-")
			t.Run(name, func(t *testing.T) {
				runDemoExpectingFailure(t, root, rel, step)
			})
		}
	}
}

// runDemoExpectingFailure drives one demo with a stub that fails exactly one
// subcommand and succeeds at everything else, so the demo walks forward to that
// step and dies there rather than at the first thing that could fail.
func runDemoExpectingFailure(t *testing.T, root, rel, failStep string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}
	stub := failingToolchain(t, failStep)
	script := filepath.Join(root, rel)
	work := t.TempDir()

	cmd := exec.Command(bash, script)
	cmd.Dir = filepath.Dir(script)
	cmd.Env = append(os.Environ(),
		"PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"),
		"IBMCLOUD_API_KEY=stub-key-for-the-dry-run-000",
		"DRY_RUN=0", "AUTO_ADVANCE=1",
		"PHASE_DELAY=0", "CMD_RENDER_HOLD=0", "CMD_POST_HOLD=0",
		"OUT_SETTLE_HOLD=0", "OUT_POST_HOLD=0", "PHASE_BANNER_HOLD=0",
		"FORGE_URL=", "FORGE_USER=", "FORGE_PASS=",
		"BNK_FORGE_URL=", "BNK_FORGE_USER=", "BNK_FORGE_PASSWORD=",
		"ROKSBNKCTL_BIN=roksbnkctl",
		"CI_WORK="+work, "CI_WORKSPACE=stubws",
		"ROKSBNKCTL_HOME="+filepath.Join(work, "home"),
	)
	out, runErr := cmd.CombinedOutput()

	// The step must actually have been REACHED, or this case proves nothing: a
	// demo that died earlier would also exit non-zero, for the wrong reason.
	if !strings.Contains(string(out), "stub: simulated failure") {
		t.Fatalf("the stub never failed anything, so %q was never reached and this case "+
			"checked nothing.\n--- output tail ---\n%s", failStep, tail(string(out), 25))
	}
	if runErr == nil {
		t.Errorf("%q failed and the demo still exited 0.\n"+
			"A demo that reports success over a failed phase cannot verify a release.\n"+
			"--- output tail ---\n%s", failStep, tail(string(out), 25))
	}
	if strings.Contains(string(out), "DEMO COMPLETE") || strings.Contains(string(out), "PIPELINE COMPLETE") {
		t.Errorf("the demo printed its completion banner after %q failed.\n--- output tail ---\n%s",
			failStep, tail(string(out), 25))
	}
}

// failingToolchain is stubToolchain's evil twin: the same fakes, except
// roksbnkctl (and docker, which the CI demo runs it through) exits non-zero for
// exactly one subcommand.
//
// Matching is on the joined argv, so "bnk up" and "cluster up" are
// distinguishable — a bare "up" would fail both and collapse five cases into
// one. `version` must keep succeeding or the demo dies in preflight for a
// reason unrelated to what is being tested.
func failingToolchain(t *testing.T, failStep string) string {
	t.Helper()
	dir := t.TempDir()

	fail := `#!/bin/sh
case " $* " in
  *" ` + failStep + ` "*) echo "stub: simulated failure of '$*'" >&2; exit 1 ;;
esac
case "$1" in
  version) echo "roksbnkctl v99.0.0 (commit stub, built now)" ;;
esac
exit 0
`
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("roksbnkctl", fail)
	write("docker", fail)
	for _, n := range []string{"terraform", "helm", "jq", "kubectl"} {
		write(n, "#!/bin/sh\nexit 0\n")
	}
	return dir
}
