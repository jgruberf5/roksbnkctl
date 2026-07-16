package tf

import (
	"path/filepath"
	"testing"
)

// Every phase's state dir must map to its OWN key in both phase-mapping tables.
// These tables are easy to forget when a phase is added — the FLP phase shipped
// with state-flp/ missing from both, so `phaseFromStateDir` returned "bnk" (on an
// s3/COS backend the FLP phase then shared the BNK phase's remote state object)
// and `phaseLabel` returned "trial" (the FLP apply's tfvars snapshot overwrote the
// BNK phase's). Both are silent, and both can destroy the BNK deployment.
//
// A collision here means one phase can clobber another's state. Add the new phase
// to this table AND to both functions.
func TestPhaseMappingsAreDistinctPerStateDir(t *testing.T) {
	// state dir → the key each table must produce.
	want := map[string]struct{ backendKey, appliedLabel string }{
		"state-cluster": {"cluster", "cluster"},
		"state-testing": {"testing", "testing"},
		"state-gateway": {"gateway", "gateway"},
		"state-flp":     {"flp", "flp"},
		"state-tgw":     {"tgw", "tgw"},
		// The BNK phase's dir is plain "state"; its historic labels differ between
		// the two tables ("bnk" for the backend key, "trial" for the tfvars snapshot).
		"state": {"bnk", "trial"},
	}

	seenBackend := map[string]string{}
	seenLabel := map[string]string{}

	for dir, exp := range want {
		if got := phaseFromStateDir(filepath.Join("/ws", dir)); got != exp.backendKey {
			t.Errorf("phaseFromStateDir(%s) = %q, want %q — the phase is missing from backend.go, so it shares another phase's remote state key", dir, got, exp.backendKey)
		}
		w := &Workspace{stateDir: filepath.Join("/ws", dir)}
		if got := w.phaseLabel(nil); got != exp.appliedLabel {
			t.Errorf("phaseLabel(%s) = %q, want %q — the phase is missing from terraform.go, so its applied-tfvars snapshot clobbers another phase's", dir, got, exp.appliedLabel)
		}

		if prev, dup := seenBackend[exp.backendKey]; dup {
			t.Errorf("backend key %q is produced by BOTH %s and %s — those phases would share one remote state object", exp.backendKey, prev, dir)
		}
		seenBackend[exp.backendKey] = dir

		if prev, dup := seenLabel[exp.appliedLabel]; dup {
			t.Errorf("applied label %q is produced by BOTH %s and %s — one phase's tfvars snapshot would clobber the other's", exp.appliedLabel, prev, dir)
		}
		seenLabel[exp.appliedLabel] = dir
	}
}
