package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #118. os.Exit was called from 18 places across five packages, each with its
// own policy: `init` exited 130 on interrupt while every other command turned
// the same Ctrl-C into 1; internal/remote defined a meaningful 126/127 scheme
// that nothing else participated in; and because os.Exit terminates the test
// binary, none of it could be asserted.
//
// Commands now return a coded error and one place maps it to a status. This
// keeps it that way — scattering is how the contract fragmented, and each new
// os.Exit looks locally reasonable, which is exactly why a reviewer would wave
// it through.
var allowedOSExit = map[string]string{
	"internal/cli/root.go":   "the single mapping from a command's error to a process status",
	"cmd/roksbnkctl/main.go": "the argv preflight, which runs before cobra exists to return an error to",
	"internal/cli/init.go": "the interview's SIGINT handler: it fires from a goroutine while a " +
		"terminal read blocks, so there is no error to return from anywhere",
}

func TestOSExitStaysOutOfCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("walks the source trees; runs in the full suite")
	}
	osExit := regexp.MustCompile(`\bos\.Exit\(`)

	var offenders []string
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join("..", "..", dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			rel := strings.TrimPrefix(filepath.ToSlash(path), "../../")
			if _, ok := allowedOSExit[rel]; ok {
				return nil
			}
			if osExit.Match(body) {
				offenders = append(offenders, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	if len(offenders) > 0 {
		t.Errorf("os.Exit outside the allowlist: %v\n"+
			"os.Exit skips deferred cleanup and cannot be tested — the test binary dies with it. "+
			"Return an exitcode.Error instead (exitcode.Silent when the path already printed its "+
			"own report), and root.go maps it. If a call genuinely cannot return, add it to "+
			"allowedOSExit with the reason.", offenders)
	}
}

// An allowlist entry for a file that no longer calls os.Exit is a line the next
// person inherits without noticing.
func TestOSExitAllowlistHasNoStaleEntries(t *testing.T) {
	osExit := regexp.MustCompile(`\bos\.Exit\(`)
	for rel, why := range allowedOSExit {
		body, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Errorf("allowlisted file %s (%s) does not exist", rel, why)
			continue
		}
		if !osExit.Match(body) {
			t.Errorf("%s no longer calls os.Exit — drop its allowlist entry (%q)", rel, why)
		}
	}
}

// The three remaining calls must take their code from the contract, not a
// literal. A literal is how `init`'s 130 and everything else's 1 diverged.
func TestTheRemainingOSExitCallsUseTheContract(t *testing.T) {
	literal := regexp.MustCompile(`os\.Exit\(\s*-?\d`)
	for rel := range allowedOSExit {
		body, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatal(err)
		}
		if m := literal.FindString(string(body)); m != "" {
			t.Errorf("%s calls %s… with a literal; use an internal/exitcode constant so the "+
				"value is stated once", rel, m)
		}
	}
}

// root.go is the single place a command's error becomes a process status, so
// the mapping has to go through the contract rather than a literal — a
// hard-coded os.Exit(1) there would silently flatten every code the commands
// take care to return.
func TestRootMapsErrorsThroughTheContract(t *testing.T) {
	body, err := os.ReadFile("root.go")
	if err != nil {
		t.Fatal(err)
	}
	fn := enclosingFuncBody(t, string(body), "Execute")

	if !strings.Contains(fn, "exitcode.FromError(err)") {
		t.Error("Execute does not resolve the status via exitcode.FromError — every coded error " +
			"a command returns would collapse to one value")
	}
	if !strings.Contains(fn, "exitcode.IsSilent(err)") {
		t.Error("Execute does not check exitcode.IsSilent, so a path that already printed its " +
			"own report gets a bare \"roksbnkctl: \" line on top of it")
	}
}

// Every code a command can return must come from the contract. A literal in a
// RunE is how init's 130 and everything else's 1 diverged in the first place.
func TestCommandsReturnContractCodesNotLiterals(t *testing.T) {
	if testing.Short() {
		t.Skip("walks the source trees; runs in the full suite")
	}
	// exitcode.Silent(<literal>) is legitimate ONLY for a child process's own
	// status, which is a value we received rather than one we chose.
	literal := regexp.MustCompile(`exitcode\.(Wrap|Newf)\(\s*\d`)

	var offenders []string
	// Scoped to the Go source trees: walking the repo root drags in .git and
	// the built book, which takes minutes and finds nothing.
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join("..", "..", dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			if m := literal.FindString(string(body)); m != "" {
				offenders = append(offenders, strings.TrimPrefix(filepath.ToSlash(path), "../../")+": "+m)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("exit code given as a literal:\n  %s\nUse an internal/exitcode constant.",
			strings.Join(offenders, "\n  "))
	}
}

// enclosingFuncBody returns the body of `func <name>`, delimited by the closing
// brace in column 0.
//
// Deliberately named apart from cluster_outputs_report_test.go's funcBody:
// these two guards land on separate branches, and a shared name would make
// whichever merges second fail to build for a reason unrelated to its change.
func enclosingFuncBody(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "func "+name+"(")
	if start < 0 {
		t.Fatalf("%s not found — this test can no longer detect the gap", name)
	}
	rest := src[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("could not delimit %s", name)
	}
	return rest[:end]
}
