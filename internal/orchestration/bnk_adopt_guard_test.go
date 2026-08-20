package orchestration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The guard's whole safety property is that it fires ONLY on an empty state. A
// workspace that owns an install must converge exactly as before — a false positive
// here would block legitimate re-runs, which is worse than the bug being fixed.
func TestStateFileHasResources(t *testing.T) {
	cases := []struct {
		name  string
		state string
		want  bool
	}{
		{"no state file at all", "", false},
		{"empty resources, spaced", `{"version":4,"resources": []}`, false},
		{"empty resources, compact", `{"version":4,"resources":[]}`, false},
		{"has resources", `{"version":4,"resources":[{"mode":"managed","type":"helm_release"}]}`, true},
		// Not a state we recognise: treated as "no resources on disk". What that MEANS
		// is the caller's decision, and depends on the backend.
		{"unparseable", `not json at all`, false},
		{"no resources key", `{"version":4,"outputs":{}}`, false},

		// The shape that broke it (#100). A populated top-level resources array
		// AND a nested, empty `resources` ATTRIBUTE — which is what IBM Cloud
		// resource-group and IAM objects carry, on essentially every deployment
		// this tool makes. A scan for `"resources": []` matches the nested one
		// and reads the whole state as empty, so the guard refuses a workspace
		// that owns its install. Both halves are required: a case with only one
		// of them passes against the broken implementation.
		{
			"populated state whose resources ALSO appear as an empty nested attribute",
			`{"version":4,"resources":[{"mode":"managed","type":"helm_release","instances":[` +
				`{"attributes":{"resource_tags":[],"resources": [],"roles":["Manager"]}}]}]}`,
			true,
		},
		// The same nested attribute must not invent resources either, when the
		// state genuinely holds none.
		{
			"genuinely empty state with the same nested attribute elsewhere",
			`{"version":4,"resources":[],"check_results":[{"attributes":{"resources": []}}]}`,
			false,
		},
	}
	for _, c := range cases {
		dir := t.TempDir()
		if c.state != "" {
			if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(c.state), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := stateFileHasResources(dir); got != c.want {
			t.Errorf("%s: stateFileHasResources = %v, want %v", c.name, got, c.want)
		}
	}
}

// The backend decides what an absent local state file proves.
//
// With backend s3 the state lives in object storage: there is no
// <stateDir>/terraform.tfstate, and .terraform/terraform.tfstate holds only backend
// config with no "resources" key. Reading that as "nothing installed" would make the
// guard refuse every s3 workspace its own `bnk up` — including the one that did the
// install — so the guard must not draw a conclusion from disk alone there.
func TestRemoteStateBackend(t *testing.T) {
	cases := []struct {
		backend string
		want    bool
	}{
		{"", false},
		{"local", false},
		{"  local  ", false},
		{"s3", true},
		{"S3-ish-future-backend", true},
	}
	for _, c := range cases {
		cctx := &config.Context{Workspace: &config.Workspace{}}
		cctx.Workspace.State.Backend = c.backend
		if got := remoteStateBackend(cctx); got != c.want {
			t.Errorf("remoteStateBackend(%q) = %v, want %v", c.backend, got, c.want)
		}
	}
	if remoteStateBackend(nil) {
		t.Error("nil context must not read as a remote backend")
	}
	if remoteStateBackend(&config.Context{}) {
		t.Error("nil workspace must not read as a remote backend")
	}
}

// A nil workspace or context must be a silent no-op, not a panic: the guard runs
// early on paths where either can legitimately be absent.
func TestGuardUnownedBNKInstall_NilsAreNoOp(t *testing.T) {
	if err := guardUnownedBNKInstall(t.Context(), nil, nil, os.Stderr); err != nil {
		t.Errorf("nil inputs must be a no-op, got %v", err)
	}
}
