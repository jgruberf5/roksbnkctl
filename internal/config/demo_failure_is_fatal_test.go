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
// This drives each demo with a roksbnkctl stub that FAILS the world-changing
// subcommands, and requires a non-zero exit and no completion banner. It is the
// inverse of TestDemosReachTheirLastPhaseWithNoForgeConfigured: that one proves
// the demo gets to the end when it should, this one proves it does not when it
// shouldn't.
func TestDemosFailWhenAPhaseFails(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("no bash: %v", err)
	}
	if testing.Short() {
		t.Skip("drives the demos; runs in the full suite")
	}
	root := repoRootForDemoTest(t)

	demos := []string{
		"scripts/demos/cluster-lifecycle-cli-demo/cluster-lifecycle-cli-demo.sh",
		"scripts/demos/cluster-lifecycle-ci-demo/cluster-lifecycle-ci-demo.sh",
	}

	for _, rel := range demos {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			stub := failingToolchain(t)
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

			if runErr == nil {
				t.Errorf("the demo exited 0 even though every world-changing command failed.\n"+
					"A demo that always reports success cannot be used to verify a release.\n"+
					"--- output tail ---\n%s", tail(string(out), 25))
			}
			if strings.Contains(string(out), "DEMO COMPLETE") || strings.Contains(string(out), "PIPELINE COMPLETE") {
				t.Errorf("the demo printed its completion banner after a failed phase.\n--- output tail ---\n%s",
					tail(string(out), 25))
			}
		})
	}
}

// failingToolchain is stubToolchain's evil twin: the same fakes, except
// roksbnkctl exits non-zero for the subcommands that change the world, and
// zero for the informational ones. That split matters — failing everything
// would also fail `version`, and the demo would die in preflight for a reason
// unrelated to what this test is about.
func failingToolchain(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	rbk := `#!/bin/sh
for a in "$@"; do
  case "$a" in
    up|down) echo "stub: simulated failure of '$*'" >&2; exit 1 ;;
  esac
done
case "$1" in
  version) echo "roksbnkctl v99.0.0 (commit stub, built now)" ;;
esac
exit 0
`
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("roksbnkctl", rbk)
	for _, n := range []string{"terraform", "helm", "jq", "kubectl"} {
		write(n, "#!/bin/sh\nexit 0\n")
	}
	// docker runs the CI demo's steps; make it delegate to the same stub so the
	// containerised commands fail the same way the host ones do.
	write("docker", `#!/bin/sh
for a in "$@"; do
  case "$a" in
    up|down) echo "stub: simulated failure of '$*'" >&2; exit 1 ;;
  esac
done
exit 0
`)
	return dir
}
