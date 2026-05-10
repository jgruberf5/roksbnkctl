package remote_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

// withTempHome points ROKSBNKCTL_HOME at a fresh tempdir and seeds a
// minimally-valid workspace there. Returns the workspace name.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, dir)
	const name = "ws1"
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-east", ResourceGroup: "default"},
		Cluster:  config.ClusterCfg{Name: "c1"},
		TFSource: config.TFSourceCfg{Type: "embedded"},
	}
	if err := config.SaveWorkspace(name, ws); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	// Sanity: file exists
	cfgPath := filepath.Join(dir, name, "config.yaml")
	if _, err := config.LoadWorkspace(name); err != nil {
		t.Fatalf("verify seed (%s): %v", cfgPath, err)
	}
	return name
}

func TestSetTarget_AddListShowRemove(t *testing.T) {
	ws := withTempHome(t)

	if err := remote.SetTarget(ws, "j1", config.TargetCfg{
		Host: "1.2.3.4", User: "ubuntu", KeyPath: "~/.ssh/id_ed25519",
	}); err != nil {
		t.Fatalf("SetTarget: %v", err)
	}
	if err := remote.SetTarget(ws, "j2", config.TargetCfg{
		Host: "5.6.7.8", User: "root", KeySource: "agent", Port: 2222,
	}); err != nil {
		t.Fatalf("SetTarget j2: %v", err)
	}

	list, err := remote.ListTargets(ws)
	if err != nil {
		t.Fatalf("ListTargets: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 targets, got %d", len(list))
	}
	// Sorted alphabetically.
	if list[0].Name != "j1" || list[1].Name != "j2" {
		t.Errorf("unexpected order: %s, %s", list[0].Name, list[1].Name)
	}
	// Defaulting: port=0 round-trips as 22 in the runtime form.
	if list[0].Port != 22 {
		t.Errorf("j1 default port: want 22, got %d", list[0].Port)
	}
	if list[1].Port != 2222 {
		t.Errorf("j2 explicit port: want 2222, got %d", list[1].Port)
	}

	got, err := remote.LoadTarget(ws, "j1")
	if err != nil {
		t.Fatalf("LoadTarget: %v", err)
	}
	if got.Host != "1.2.3.4" || got.User != "ubuntu" || got.KeyPath != "~/.ssh/id_ed25519" {
		t.Errorf("LoadTarget j1: %+v", got)
	}

	if err := remote.RemoveTarget(ws, "j1"); err != nil {
		t.Fatalf("RemoveTarget: %v", err)
	}
	if _, err := remote.LoadTarget(ws, "j1"); !errors.Is(err, remote.ErrTargetNotFound) {
		t.Errorf("after Remove, want ErrTargetNotFound, got %v", err)
	}
}

func TestLoadTarget_NotFound(t *testing.T) {
	ws := withTempHome(t)
	_, err := remote.LoadTarget(ws, "nope")
	if !errors.Is(err, remote.ErrTargetNotFound) {
		t.Errorf("want ErrTargetNotFound, got %v", err)
	}
}

func TestSetTarget_Validation(t *testing.T) {
	ws := withTempHome(t)
	cases := []struct {
		name string
		cfg  config.TargetCfg
	}{
		{"no host", config.TargetCfg{User: "u"}},
		{"no user", config.TargetCfg{Host: "h"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := remote.SetTarget(ws, "x", c.cfg); err == nil {
				t.Errorf("want error for %s", c.name)
			}
		})
	}
}

func TestRemoveTarget_Idempotent(t *testing.T) {
	ws := withTempHome(t)
	// Removing a missing target is a no-op (no error).
	if err := remote.RemoveTarget(ws, "phantom"); err != nil {
		t.Errorf("removing missing target: %v", err)
	}
}

func TestKeySourceDescription(t *testing.T) {
	tcs := []struct {
		t    *remote.Target
		want string
	}{
		{&remote.Target{KeyPath: "/k"}, "file:/k"},
		{&remote.Target{KeySource: "agent"}, "agent"},
		{&remote.Target{KeySource: "tf-output:k"}, "tf-output:k"},
		{&remote.Target{}, "(unset)"},
	}
	for _, c := range tcs {
		if got := c.t.KeySourceDescription(); got != c.want {
			t.Errorf("KeySourceDescription(%+v) = %q, want %q", c.t, got, c.want)
		}
	}
}
