package orchestration

import (
	"os"
	"path/filepath"
	"testing"
)

// The guard's whole safety property is that it fires ONLY on an empty state. A
// workspace that owns an install must converge exactly as before — a false positive
// here would block legitimate re-runs, which is worse than the bug being fixed.
func TestBNKStateHasResources(t *testing.T) {
	cases := []struct {
		name  string
		state string
		want  bool
	}{
		{"no state file at all", "", false},
		{"empty resources, spaced", `{"version":4,"resources": []}`, false},
		{"empty resources, compact", `{"version":4,"resources":[]}`, false},
		{"has resources", `{"version":4,"resources":[{"mode":"managed","type":"helm_release"}]}`, true},
		// Not a state we recognise: treated as "no resources", which only leads to
		// checking the cluster — never to a spurious refusal on its own.
		{"unparseable", `not json at all`, false},
		{"no resources key", `{"version":4,"outputs":{}}`, false},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if c.state != "" {
			if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(c.state), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := bnkStateHasResources(dir); got != c.want {
			t.Errorf("%s: bnkStateHasResources = %v, want %v", c.name, got, c.want)
		}
	}
}

// A nil workspace or context must be a silent no-op, not a panic: the guard runs
// early on paths where either can legitimately be absent.
func TestGuardUnownedBNKInstall_NilsAreNoOp(t *testing.T) {
	if err := guardUnownedBNKInstall(t.Context(), nil, nil, os.Stderr); err != nil {
		t.Errorf("nil inputs must be a no-op, got %v", err)
	}
}
