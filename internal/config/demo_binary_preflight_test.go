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
// The runtime warning is in scripts/demos/lib/demo-format.sh (preflight_binary),
// because the host binary is not a repo artifact and no test can see it. What a
// test CAN pin is that every demo which runs the binary actually calls the
// preflight — the wiring, which is one line per script and exactly the kind of
// thing that gets dropped when a new demo is copied from an old one.
//
// Sibling guards: TestDemoRunnerTagMatchesTheCurrentRelease (the Argo runner
// image) and TestDemoScriptVersionStampsMatchTheCurrentRelease (header stamps).
// Both check tracked defaults; neither could see the host binary, which is the
// gap this closes.
func TestEveryDemoThatRunsTheBinaryCallsThePreflight(t *testing.T) {
	root := repoRootForDemoTest(t)
	demos := filepath.Join(root, "scripts", "demos")

	// A script "runs the binary" if it resolves one from ROKSBNKCTL_BIN.
	resolves := regexp.MustCompile(`(?m)^(?:ROKSBNKCTL_BIN|RBK)="\$\{ROKSBNKCTL_BIN:-`)

	var missing []string
	var checked int
	err := filepath.Walk(demos, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".sh") {
			return err
		}
		// The library defines the helper; it does not call it.
		if strings.Contains(filepath.ToSlash(path), "/demos/lib/") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if !resolves.Match(body) {
			return nil
		}
		checked++
		if !strings.Contains(string(body), "preflight_binary") {
			rel, _ := filepath.Rel(root, path)
			missing = append(missing, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", demos, err)
	}

	if checked == 0 {
		t.Fatal("found no demo script resolving ROKSBNKCTL_BIN — this test can no longer " +
			"detect the drift, which is worse than the drift")
	}
	if len(missing) > 0 {
		t.Errorf("demo script(s) run roksbnkctl without calling preflight_binary:\n  %s\n\n"+
			"Without it the demo runs whatever version happens to be on PATH and says nothing, "+
			"so a validation pass can report green for a release it never exercised. Add\n"+
			"  preflight_binary \"$ROKSBNKCTL_BIN\"\n"+
			"to the script's preflight.", strings.Join(missing, "\n  "))
	}
}

// The helper has to keep doing the two things that make it worth having: print
// the resolved binary so the mismatch is on screen (and in the recording), and
// WARN rather than refuse — running a demo against a locally built binary is a
// normal thing to do while developing.
func TestThePreflightWarnsAndDoesNotRefuseOnAVersionMismatch(t *testing.T) {
	root := repoRootForDemoTest(t)
	body, err := os.ReadFile(filepath.Join(root, "scripts", "demos", "lib", "demo-format.sh"))
	if err != nil {
		t.Fatal(err)
	}
	fn := shellFuncBody(t, string(body), "preflight_binary")

	for _, want := range []string{"command -v", "version", "CHANGELOG.md"} {
		if !strings.Contains(fn, want) {
			t.Errorf("preflight_binary should reference %q:\n%s", want, fn)
		}
	}
	// The resolved path must be printed, not just compared — that is what puts
	// the mismatch in the recording.
	if !strings.Contains(fn, "$resolved") {
		t.Error("preflight_binary must print the resolved binary path; the runner-image " +
			"preflight is the precedent, and it is why the Argo half never had this problem")
	}
	// A mismatch must not be fatal. Sliced from the CONDITIONAL, not from the
	// message: slicing at "VERSION MISMATCH" starts after the verb, so a `die`
	// on that very line falls outside the window and the check passes against
	// the thing it is meant to catch. (Found by mutating the script to die and
	// watching this test stay green.)
	cond := strings.Index(fn, `!= "$latest"`)
	if cond < 0 {
		t.Fatal("could not find the version comparison; this test can no longer see the branch it checks")
	}
	if strings.Contains(fn[cond:], "die ") {
		t.Error("a version mismatch must warn, not die: demoing a locally built binary is " +
			"legitimate, and a hard refusal would block it")
	}
	// The CHANGELOG extraction must match the Go guards', or the shell check and
	// the test can disagree about what "current" means.
	if !strings.Contains(fn, `'^## v[0-9]+\.[0-9]+\.[0-9]+'`) {
		t.Error("preflight_binary should extract the release the same way " +
			"TestDemoRunnerTagMatchesTheCurrentRelease does")
	}
}

// shellFuncBody returns the body of a `name(){ ... }` shell function, from its
// opening line to the closing brace at column 0.
func shellFuncBody(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, name+"(){")
	if start < 0 {
		t.Fatalf("shell function %s(){ not found", name)
	}
	rest := src[start:]
	if end := strings.Index(rest, "\n}"); end >= 0 {
		return rest[:end]
	}
	return rest
}
