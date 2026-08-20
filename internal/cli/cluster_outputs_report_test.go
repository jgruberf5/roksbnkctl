package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #119. `cluster up` writes cluster-outputs.json on both of its exit paths and
// used to handle the failure differently on each: the changed path warned and
// named the recovery command, the no-change path fifteen lines away discarded
// the error with `_ =`.
//
// The silent one is the likelier to be hit. Re-running `cluster up` against an
// already-converged cluster is routine, and it is exactly the run where nothing
// else on screen would hint that the record had not been refreshed.
//
// This asserts the WIRING at the source, because the two paths differ only in a
// branch of one long function — the shape where a fix applied to one and not
// the other is invisible in review, which is how they diverged in the first
// place.
func TestBothClusterUpPathsReportAFailedOutputsWrite(t *testing.T) {
	src, err := os.ReadFile("cluster_phase.go")
	if err != nil {
		t.Fatalf("reading cluster_phase.go: %v", err)
	}
	body := string(src)

	fn := funcBody(t, body, "runClusterUp")

	// Neither path may discard the error.
	if discarded := regexp.MustCompile(`_\s*=\s*persistClusterOutputs\(`).FindString(fn); discarded != "" {
		t.Errorf("runClusterUp discards a cluster-outputs.json write failure (%q). "+
			"Without cluster_id recorded, the admission-policy sweep falls back to resolving "+
			"the cluster by NAME — the documented cause of a sweep landing zero deletes.", discarded)
	}

	// Both calls go through the reporting helper, so they cannot drift again.
	direct := regexp.MustCompile(`[^y]persistClusterOutputs\(ctx`).FindAllString(fn, -1)
	if len(direct) > 0 {
		t.Errorf("runClusterUp calls persistClusterOutputs directly (%v); "+
			"use tryPersistClusterOutputs so both exit paths report identically", direct)
	}
	if n := strings.Count(fn, "tryPersistClusterOutputs("); n != 2 {
		t.Errorf("expected both cluster up exit paths to record outputs via "+
			"tryPersistClusterOutputs, found %d call(s)", n)
	}
}

// The report has to be actionable: a warning that does not say how to recover
// leaves the operator with a broken record and no next step.
func TestTheOutputsWriteWarningNamesTheRecoveryCommand(t *testing.T) {
	src, err := os.ReadFile("cluster_phase.go")
	if err != nil {
		t.Fatal(err)
	}
	fn := funcBody(t, string(src), "tryPersistClusterOutputs")

	for _, want := range []string{"warning:", "cluster-outputs.json", "cluster register"} {
		if !strings.Contains(fn, want) {
			t.Errorf("the failure report should mention %q:\n%s", want, fn)
		}
	}
	// It must not fail the command — the cluster is up either way.
	if strings.Contains(fn, "return err") {
		t.Error("tryPersistClusterOutputs must not propagate the error: terraform succeeded, " +
			"and failing the command would misreport a successful cluster build as a failure")
	}
}

// funcBody returns the body of `func <name>`, delimited by the closing brace in
// column 0.
func funcBody(t *testing.T, src, name string) string {
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

// The three comments citing prompts/ pointed at a directory removed from the
// repo at v1.12.0 (#120). One of them was the package comment on the binary's
// entrypoint — the first thing a reader opens, opening with a dead end.
//
// Scans every package, including tests: the sweep that filed #120 looked only
// at non-test Go and missed a fourth citation in internal/cred/resolver_test.go,
// which this found on its first run.
func TestNoCommentCitesTheDeletedPromptsDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("walks the repo; runs in the full suite")
	}
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "prompts")); err == nil {
		t.Skip("prompts/ exists again; the citations are no longer dead")
	}
	// Split so this file does not match its own detection string.
	needle := "prompts/" + "sprint"

	var offenders []string
	// Scoped to the Go source trees rather than the repo: walking everything
	// drags in .git and the built book, which costs ~50s for nothing.
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			rel := strings.TrimPrefix(filepath.ToSlash(path), "../../")
			if rel == "internal/cli/cluster_outputs_report_test.go" {
				return nil
			}
			for _, line := range strings.Split(string(body), "\n") {
				if strings.Contains(line, needle) {
					offenders = append(offenders, rel+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("comment(s) cite prompts/, which was removed from the repo at v1.12.0:\n  %s\n"+
			"A reader of the current release cannot follow it. State the rationale inline instead.",
			strings.Join(offenders, "\n  "))
	}
}
