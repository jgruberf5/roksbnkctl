package tf

import (
	"path/filepath"
	"testing"
)

// phaseLabel decides which per-phase state dir terraform.applied.tfvars is
// written to (config.appliedTFVarsPath). A missing case silently misroutes one
// phase's snapshot into another phase's dir — which is exactly how `gateway up`
// (no "state-gateway" case) clobbered the BNK phase's snapshot and broke
// `bnk down`. Pin every dedicated phase dir → its label so that can't recur.
func TestPhaseLabel_RoutesByStateDir(t *testing.T) {
	cases := []struct {
		dir, want string
	}{
		{"state-cluster", "cluster"},
		{"state-testing", "testing"},
		{"state-gateway", "gateway"},
	}
	for _, c := range cases {
		w := &Workspace{name: "phaselabel-ws", stateDir: filepath.Join("/nonexistent/ws", c.dir)}
		if got := w.phaseLabel(nil); got != c.want {
			t.Errorf("phaseLabel(stateDir base %q) = %q, want %q", c.dir, got, c.want)
		}
	}
}
