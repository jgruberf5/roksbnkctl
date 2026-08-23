package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A demo that runs roksbnkctl inside a container must FORWARD the
// ROKSBNKCTL_* overrides into it, or the pipeline cannot be configured from CI
// at all — which is the one situation a container runner exists to serve.
//
// The cluster-lifecycle CI demo forwarded exactly four variables:
//
//	-e IBMCLOUD_API_KEY -e BNK_FORGE_URL -e BNK_FORGE_USER -e BNK_FORGE_PASSWORD
//
// Every ROKSBNKCTL_* override was silently dropped. Nothing failed loudly: the
// workspace simply fell back to its defaults, tried to read FAR credentials from
// a COS bucket it had no access to, and died with "AccessDenied" — a message
// with no visible connection to the setting that had gone missing.
//
// The .env.example parity guard did not cover this. It checks that overrides are
// LISTED for the blueprint demo; it says nothing about whether a container demo
// passes them through.
func TestContainerDemosForwardOverrides(t *testing.T) {
	root := repoRootForDemoTest(t)

	scripts, err := filepath.Glob(filepath.Join(root, "scripts/demos/*/*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no demo scripts found: %v", err)
	}

	dockerRun := regexp.MustCompile(`docker run --rm`)
	checked := 0

	for _, f := range scripts {
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			continue
		}
		body := string(b)
		if !dockerRun.MatchString(body) {
			continue
		}
		rel, _ := filepath.Rel(root, f)
		checked++

		// Two legitimate mechanisms: forward the environment by name, or write
		// the overrides into an --env-file. Either reaches the container; neither
		// present means nothing does.
		forwards := strings.Contains(body, "ROKSBNKCTL_*")
		envFile := strings.Contains(body, "--env-file") && strings.Contains(body, "ROKSBNKCTL_")

		if !forwards && !envFile {
			t.Errorf("%s runs roksbnkctl in a container but forwards no ROKSBNKCTL_* override.\n"+
				"Nothing fails loudly when one is dropped — the workspace falls back to a default and "+
				"the eventual error names something else entirely. Forward them by NAME (so values stay "+
				"out of argv) or write them to an --env-file.", rel)
		}
	}

	// Forwarding must not be blanket. ROKSBNKCTL_HOME names a directory on the
	// HOST; pushing it into the container makes roksbnkctl try to write there and
	// fail with "mkdir /mnt/d: permission denied", because the container sets its
	// own ROKSBNKCTL_HOME to the mounted /work volume. A first version of the
	// forwarding did exactly that and broke the pipeline at phase 2.
	for _, f := range scripts {
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			continue
		}
		body := string(b)
		// Only the scripts that actually BUILD a -e forward list from the
		// environment. A script that merely mentions ROKSBNKCTL_* in prose, or
		// sets specific ones by hand, is not doing the thing this checks.
		if !dockerRun.MatchString(body) || !strings.Contains(body, `RUN_ENV+=(-e`) {
			continue
		}
		rel, _ := filepath.Rel(root, f)
		if !strings.Contains(body, "ROKSBNKCTL_HOME)") {
			t.Errorf("%s forwards ROKSBNKCTL_* into a container without excluding ROKSBNKCTL_HOME.\n"+
				"That variable names a HOST directory; the container sets its own to the mounted "+
				"volume, and overriding it makes roksbnkctl write to a path it cannot see.", rel)
		}
	}

	if checked == 0 {
		t.Fatal("found no container-running demos; the glob or the docker-run match is wrong")
	}
	t.Logf("checked %d container-running demo(s)", checked)
}
