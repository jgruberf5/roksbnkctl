package config

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The blueprint demo pins the runner image it submits to Argo. That pin is a
// version reference with no compiler and no import to keep it honest, and it
// silently went FOUR releases stale (v1.42.0 while v1.46.0 shipped) — so every
// demo run exercised an old binary no matter which one the operator had
// installed, and reported success doing it.
//
// A release-checklist line would be skippable. This is the same mechanism as
// TestDemoEnvAllowlistCoversEveryOverride: the drift fails a test instead of
// being discovered later. Cutting a release now means bumping the pin in the
// same commit as the CHANGELOG entry, which is where it belongs.
func TestDemoRunnerTagMatchesTheCurrentRelease(t *testing.T) {
	root := repoRootForDemoTest(t)

	b, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Skipf("CHANGELOG unreadable: %v", err)
	}
	m := regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no released version heading found in CHANGELOG.md — the extraction regex has drifted")
	}
	latest := m[1]

	demo := filepath.Join(root, "scripts/demos/blueprint-workflows-ci-demo/blueprint-workflows-ci-demo.sh")
	d, err := os.ReadFile(demo)
	if err != nil {
		t.Skipf("demo script unreadable: %v", err)
	}
	pin := regexp.MustCompile(`RUNNER_TAG="\$\{RUNNER_TAG:-(v[\d.]+)\}"`).FindStringSubmatch(string(d))
	if pin == nil {
		t.Fatal("could not find the RUNNER_TAG default — this test can no longer detect drift, which is worse than the drift")
	}

	if pin[1] != latest {
		t.Errorf("the blueprint demo pins runner image %s but the newest release is %s.\n"+
			"The demo would exercise %s regardless of the installed binary. Bump RUNNER_TAG in\n"+
			"  scripts/demos/blueprint-workflows-ci-demo/blueprint-workflows-ci-demo.sh\n"+
			"in the same commit as the CHANGELOG entry.", pin[1], latest, pin[1])
	}
}

// Guarding the script alone is not enough: .env.example OVERRIDES it.
//
// The demo reads `RUNNER_TAG="${RUNNER_TAG:-<default>}"`, and the README tells
// the operator to copy .env.example to .env and source it. A stale pin there
// therefore WINS over the default the test above guards, and the demo exercises
// an old runner however new the installed binary is — the exact drift that test
// exists to prevent, arriving through the file it does not read.
//
// Observed live: the script defaulted to v1.49.0, .env pinned v1.42.0, and the
// submitted workflows ran v1.42.0 — seven releases behind.
func TestDemoEnvExampleRunnerTagMatchesTheCurrentRelease(t *testing.T) {
	root := repoRootForDemoTest(t)

	b, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Skipf("CHANGELOG unreadable: %v", err)
	}
	m := regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no released version heading found in CHANGELOG.md — the extraction regex has drifted")
	}
	latest := m[1]

	envFile := filepath.Join(root, "scripts/demos/blueprint-workflows-ci-demo/.env.example")
	d, err := os.ReadFile(envFile)
	if err != nil {
		t.Skipf(".env.example unreadable: %v", err)
	}
	pin := regexp.MustCompile(`(?m)^RUNNER_TAG=(v[\d.]+)`).FindStringSubmatch(string(d))
	if pin == nil {
		t.Fatal("could not find the RUNNER_TAG pin in .env.example — this test can no longer detect drift, which is worse than the drift")
	}

	if pin[1] != latest {
		t.Errorf(".env.example pins runner image %s but the newest release is %s.\n"+
			"That pin OVERRIDES the demo script's default, so the demo would exercise %s.\n"+
			"Bump RUNNER_TAG in\n"+
			"  scripts/demos/blueprint-workflows-ci-demo/.env.example\n"+
			"in the same commit as the CHANGELOG entry.", pin[1], latest, pin[1])
	}
}

// Every demo script carries a version stamp in its header. Seven of them said
// v1.32.0 while the tool was at v1.49.1 — eighteen releases stale — and the
// blueprint demo said v1.49.0 one release after it was cut.
//
// These are the scripts the demos are RECORDED from, so the stamp is on screen.
// It is cosmetic in the sense that nothing breaks, and not cosmetic in the sense
// that a viewer reads it as "this is the version you are watching".
func TestDemoScriptVersionStampsMatchTheCurrentRelease(t *testing.T) {
	root := repoRootForDemoTest(t)
	latest := latestReleasedVersion(t, root)

	scripts, err := filepath.Glob(filepath.Join(root, "scripts/demos/*/*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Skip("no demo scripts found")
	}
	stamp := regexp.MustCompile(`(?m)^# \S+\.sh\s+\(roksbnkctl (v[\d.]+)\)`)

	var checked int
	for _, s := range scripts {
		b, rerr := os.ReadFile(s)
		if rerr != nil {
			continue
		}
		m := stamp.FindStringSubmatch(string(b))
		if m == nil {
			continue // not every script carries a stamp
		}
		checked++
		if m[1] != latest {
			t.Errorf("%s is stamped %s but the newest release is %s — the demo is recorded "+
				"with that line on screen. Bump it in the release commit.",
				filepath.Base(s), m[1], latest)
		}
	}
	if checked == 0 {
		t.Fatal("no version stamps found — this test can no longer detect drift, which is worse than the drift")
	}
}

// latestReleasedVersion is the newest "## vX.Y.Z" heading in the CHANGELOG.
func latestReleasedVersion(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Skipf("CHANGELOG unreadable: %v", err)
	}
	m := regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+)`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("no released version heading in CHANGELOG.md — the extraction regex has drifted")
	}
	return m[1]
}
