package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #143. ROKSBNKCTL_BIN defaults to whatever `roksbnkctl` is on PATH. During the
// v1.50.0 validation that was v1.43.0 — eighteen releases old — and the CLI half
// of a demo ran against it for two full passes before anyone checked.
//
// That is worse than an ordinary stale pin, because the demo LOOKS right: the
// banner says the current version, the Argo runner image is correct, and the CLI
// steps quietly exercise an old build. A validation pass can report green for a
// release that was never run.
//
// The runtime warning lives in scripts/demos/lib/preflight-binary.sh, because
// the host binary is not a repo artifact and no test can see it. What a test CAN
// pin is that every demo running the binary actually CALLS the preflight and can
// REACH it.
//
// Both halves are load-bearing, and the first version of this guard had neither.
// It matched the string "preflight_binary" anywhere in the file, so it stayed
// green against disconnected-cluster-cli-demo.sh — which called the function
// without sourcing anything that defines it, making the call a silent
// "command not found" in the one demo that ships the local binary to a VSI.
// A guard that cannot fail on the defect named in its own error message is
// worse than no guard: it converts an open question into a false answer.

// resolvesTheBinary matches any assignment deriving a binary from
// ROKSBNKCTL_BIN — any variable name, either quote style, with or without
// `export`, at any indentation. The first version anchored on
// ^(ROKSBNKCTL_BIN|RBK)=" and so missed `export ROKSBNKCTL_BIN=…` and any third
// variable name.
var resolvesTheBinary = regexp.MustCompile(`(?m)^\s*(?:export\s+)?\w+=["']\$\{ROKSBNKCTL_BIN:-`)

// invokesTheBinary matches the variable expanded in command position — the
// binary actually being RUN, with an argument after it.
//
// Both are required. record.sh resolves ROKSBNKCTL_BIN and exports it for the
// demo it launches, but never runs the binary itself; the preflight belongs in
// the script that runs it, which already has one. Requiring only the assignment
// would flag the wrapper and push a redundant check into it.
var invokesTheBinary = regexp.MustCompile(`(?m)["']\$\{?(?:ROKSBNKCTL_BIN|RBK)\}?["']\s+\S`)

// callsPreflight matches an actual call — a line whose first token is the
// function name followed by an argument. A mention in a comment does not match.
var callsPreflight = regexp.MustCompile(`(?m)^\s*preflight_binary\s+"?\$`)

// canReachPreflight matches sourcing something that defines it, or defining it
// inline. Calling without either is the "command not found" case.
var canReachPreflight = regexp.MustCompile(`(?m)^\s*(?:source|\.)\s+.*(?:preflight-binary|demo-format)\.sh|^\s*preflight_binary\(\)`)

func TestEveryDemoThatRunsTheBinaryCallsAndCanReachThePreflight(t *testing.T) {
	root := repoRootForDemoTest(t)
	demos := filepath.Join(root, "scripts", "demos")

	var noCall, noSource []string
	var checked int
	err := filepath.Walk(demos, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".sh") {
			return err
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		// The libraries define the helper; they do not run demos.
		if strings.Contains(filepath.ToSlash(path), "/demos/lib/") {
			return nil
		}
		if !resolvesTheBinary.Match(body) || !invokesTheBinary.Match(body) {
			return nil
		}
		checked++
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if !callsPreflight.Match(body) {
			noCall = append(noCall, rel)
			return nil
		}
		if !canReachPreflight.Match(body) {
			noSource = append(noSource, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", demos, err)
	}

	// A floor, so a rename that stops the walk matching anything fails loudly
	// rather than passing with nothing checked.
	if checked < 5 {
		t.Errorf("only %d demo script(s) matched as running the binary; expected at least 5. "+
			"If the resolution idiom changed, update resolvesTheBinary/invokesTheBinary — a guard that matches "+
			"nothing passes silently.", checked)
	}
	if len(noCall) > 0 {
		t.Errorf("demo script(s) run roksbnkctl without calling preflight_binary:\n  %s\n\n"+
			"Without it the demo runs whatever version is on PATH and says nothing, so a "+
			"validation pass can report green for a release it never exercised.",
			strings.Join(noCall, "\n  "))
	}
	if len(noSource) > 0 {
		t.Errorf("demo script(s) CALL preflight_binary but never source a file defining it:\n  %s\n\n"+
			"The call is a silent `command not found` — these scripts run without `set -e`, so "+
			"the demo continues with no version line at all. Add:\n"+
			"  source \"$(cd -P \"$(dirname \"$(readlink -f \"${BASH_SOURCE[0]}\")\")/../lib\" && pwd)/preflight-binary.sh\"",
			strings.Join(noSource, "\n  "))
	}
}

// The helper must keep doing the two things that make it worth having: print the
// resolved binary, so the mismatch is on screen and therefore in the recording,
// and WARN rather than refuse, because demoing a locally built binary is normal.
func TestThePreflightPrintsTheBinaryAndWarnsRatherThanRefusing(t *testing.T) {
	root := repoRootForDemoTest(t)
	body, err := os.ReadFile(filepath.Join(root, "scripts", "demos", "lib", "preflight-binary.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)

	if !strings.Contains(src, "$resolved") {
		t.Error("preflight_binary must print the resolved binary path; the runner-image " +
			"preflight is the precedent, and it is why the Argo half never had this problem")
	}
	// A mismatch must not be fatal. Sliced from the CONDITIONAL, not from the
	// message: slicing at "VERSION MISMATCH" starts after the verb, so a `die`
	// on that very line falls outside the window and the check passes against
	// the thing it is meant to catch. (Found by mutating the script to die and
	// watching this test stay green.)
	cond := strings.Index(src, `!= "$latest"`)
	if cond < 0 {
		t.Fatal("could not find the version comparison; this test can no longer see the branch it checks")
	}
	if strings.Contains(src[cond:], "die ") {
		t.Error("a version mismatch must warn, not die: demoing a locally built binary is " +
			"legitimate, and a hard refusal would block it")
	}

	// The version must be taken VERBATIM, not reduced to a vX.Y.Z substring.
	// `make build` stamps `git describe --dirty`, so a local build reports
	// v1.50.0-3-gdeadbee-dirty; grepping out just the semver part reported that
	// as "v1.50.0" and warned about nothing — an uncommitted build rendering on
	// camera as the release, which is the failure this exists to prevent.
	if regexp.MustCompile(`installed=.*grep -oE 'v\[0-9\]`).MatchString(src) {
		t.Error("the installed version is being reduced to a semver substring; a `make build` " +
			"binary would then report as the release. Take the version field verbatim.")
	}

	// Resolution must be physical. Logical resolution walks back through a
	// symlinked lib/ into the wrong tree, the CHANGELOG is not found, and the
	// check vanishes while the ✓ line still prints.
	if !strings.Contains(src, "cd -P") {
		t.Error("repo-root resolution must use `cd -P`; logical resolution through a symlinked " +
			"lib/ silently skips the version check while still printing the tick")
	}
	// And a missing CHANGELOG must SAY so rather than returning quietly.
	if !strings.Contains(src, "cannot confirm") {
		t.Error("when the CHANGELOG cannot be read the helper must say the check did not run; " +
			"returning quietly is the same silent no-op this feature exists to remove")
	}
}
