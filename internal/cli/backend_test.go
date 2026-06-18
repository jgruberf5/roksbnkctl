package cli

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// validBackendSpec accepts the four spec forms and rejects everything else —
// the syntactic guard `backend set` uses (it doesn't require the backend to be
// registered in this build, since k8s/ssh resolve lazily at run time).
func TestValidBackendSpec(t *testing.T) {
	good := []string{"local", "docker", "k8s", "ssh:bastion", "ssh:jumphost-eu-de-1"}
	for _, s := range good {
		if err := validBackendSpec(s); err != nil {
			t.Errorf("validBackendSpec(%q) = %v; want nil", s, err)
		}
	}
	bad := []string{"", "weird", "ssh", "local:x", "docker:y", "k8s:z"}
	for _, s := range bad {
		if err := validBackendSpec(s); err == nil {
			t.Errorf("validBackendSpec(%q) = nil; want an error", s)
		}
	}
}

// `backend set` / `unset` write (and clear) the exec: override so the operator
// never hand-edits config.yaml. Verify the round-trip + the validation guards.
func TestRunBackendSetUnset(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("be", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("be"); err != nil {
		t.Fatal(err)
	}
	prev := flagWorkspace
	flagWorkspace = ""
	t.Cleanup(func() { flagWorkspace = prev })

	// unknown tool / bad spec → error, nothing written.
	if err := runBackendSet(nil, []string{"bogus", "docker"}); err == nil {
		t.Error("unknown tool must error")
	}
	if err := runBackendSet(nil, []string{"ibmcloud", "weird"}); err == nil {
		t.Error("bad backend spec must error")
	}

	if err := runBackendSet(nil, []string{"ibmcloud", "ssh:bastion"}); err != nil {
		t.Fatalf("backend set: %v", err)
	}
	ws, err := config.LoadWorkspace("be")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Exec["ibmcloud"].Backend != "ssh:bastion" {
		t.Fatalf("exec[ibmcloud] = %+v; want ssh:bastion", ws.Exec["ibmcloud"])
	}

	// unset removes the override; with the map now empty it drops to nil so the
	// emptied exec: block stays out of the file.
	if err := runBackendUnset(nil, []string{"ibmcloud"}); err != nil {
		t.Fatalf("backend unset: %v", err)
	}
	ws, err = config.LoadWorkspace("be")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ws.Exec["ibmcloud"]; ok {
		t.Errorf("override not removed: %+v", ws.Exec)
	}
	if ws.Exec != nil {
		t.Errorf("emptied exec map should be nil, got %+v", ws.Exec)
	}
}
