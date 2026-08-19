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
