package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #116. `internal/cli/root.go` builds the process context with
// signal.NotifyContext, so every command is handed something that cancels on
// Ctrl-C. Work started on a fresh context.Background() cannot hear it: the
// signal handler accepts the interrupt and the operation runs on regardless,
// which reads to a user as a CLI that will not quit.
//
// Five request-path sites did exactly that — credential resolution (which can
// block on the OS keychain) and a `terraform output` shell-out. This walks the
// tree and fails on any new one, because the defect is invisible in review: a
// context.Background() looks identical whether the caller had a context to
// thread or not.
//
// The allowlist is the point. Each entry is a place where detaching is
// CORRECT, and each has to earn its line here rather than blend in.
var allowedBackgroundContexts = map[string]string{
	// The root of the process context tree — there is nothing to derive from.
	"internal/cli/root.go": "signal.NotifyContext establishes the cancellable root",

	// Cleanup that runs precisely BECAUSE the parent context is already dead.
	// Inheriting it would mean the cleanup could never run.
	"internal/cli/test.go":                 "teardownIperf3Best: cleanup after a cancelled run",
	"internal/cli/test_matrix_fixtures.go": "teardownMatrixFixtures: cleanup after a cancelled run",
	"internal/exec/k8s.go":                 "secret + job cleanup after a cancelled run",
	"internal/exec/ssh.go":                 "remote tempdir cleanup after a cancelled run",
	"internal/exec/docker.go":              "container kill after a cancelled run",
}

var backgroundCtxRE = regexp.MustCompile(`context\.Background\(\)`)

func TestNoNewDetachedContextsOnRequestPaths(t *testing.T) {
	root := filepath.Join("..", "..", "internal")

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if !backgroundCtxRE.Match(body) {
			return nil
		}
		// Normalise to a repo-relative, slash-separated key.
		rel := filepath.ToSlash(path)
		rel = strings.TrimPrefix(rel, "../../")
		if _, ok := allowedBackgroundContexts[rel]; !ok {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	if len(offenders) > 0 {
		t.Errorf("context.Background() in files not on the allowlist: %v\n"+
			"A command's ctx cancels on Ctrl-C; a fresh Background() does not, so work started "+
			"on one keeps running after the user interrupts.\n"+
			"Thread the caller's ctx, or — if this is cleanup that must survive cancellation — "+
			"add the file to allowedBackgroundContexts in %s with the reason.",
			offenders, "internal/cli/context_cancellation_test.go")
	}
}

// The allowlist only earns its keep if every entry is still real. A file that
// stops using context.Background() should lose its exemption, so the next one
// added there has to justify itself instead of inheriting a stale line.
func TestBackgroundContextAllowlistHasNoStaleEntries(t *testing.T) {
	for rel, why := range allowedBackgroundContexts {
		body, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Errorf("allowlisted file %s (%s) does not exist", rel, why)
			continue
		}
		if !backgroundCtxRE.Match(body) {
			t.Errorf("%s no longer uses context.Background() — drop its allowlist entry (%q)", rel, why)
		}
	}
}
