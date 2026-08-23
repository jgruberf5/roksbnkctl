package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A teardown that fails must not report success.
//
// The lifecycle demos ran their teardown with `run` and then `exit 0`
// unconditionally, so a destroy that failed still returned 0. Driving repeated
// runs, that is worse than a plain failure: the next run starts against
// half-deleted infrastructure and dies on
//
//	Provided Name (bnk24d-cluster-vpc) is not unique
//
// which names the collision and not the cause. Observed exactly that — a destroy
// failed with "context deadline exceeded", teardown reported 0, and the next run
// failed six minutes later looking like a completely different problem.
//
// This is the same defect as phases printing ✓ over a failure, in the one place
// that change did not reach.
func TestDemoTeardownsPropagateFailure(t *testing.T) {
	root := repoRootForDemoTest(t)

	scripts, err := filepath.Glob(filepath.Join(root, "scripts/demos/*/*.sh"))
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no demo scripts: %v", err)
	}

	// `teardown; exit 0` discards the status. `exit $?` keeps it.
	discards := regexp.MustCompile(`teardown;\s*exit\s+0`)
	checked := 0

	for _, f := range scripts {
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			continue
		}
		body := string(b)
		if !strings.Contains(body, "teardown(){") {
			continue
		}
		rel, _ := filepath.Rel(root, f)
		checked++

		if discards.MatchString(body) {
			t.Errorf("%s runs teardown then `exit 0`, discarding its status.\n"+
				"A caller driving repeated runs cannot then tell a clean teardown from a wedged one, "+
				"and the next run fails on something that looks unrelated. Use `exit $?`.", rel)
		}
	}
	if checked == 0 {
		t.Fatal("found no demo teardown functions; the glob or the match is wrong")
	}
	t.Logf("checked %d demo teardown(s)", checked)
}
