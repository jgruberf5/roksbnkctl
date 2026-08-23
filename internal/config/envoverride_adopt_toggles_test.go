package config

import "testing"

// The adopt-what-exists toggles (#186). A container-driven install takes its
// whole configuration from -e variables, so without these an operator cannot
// install onto a cluster that already runs cert-manager — the failure they get
// is `namespaces "cert-manager" already exists`, which names the collision but
// not the setting that resolves it.
//
// These assert the VALUE LANDS ON THE WORKSPACE, not merely that the name is
// listed/documented/allowlisted. Gutting a setter to `func(b bool) { _ = b }`
// leaves every surface guard green, so only this shape catches it.
func TestOverrideAdoptTogglesFromEnv(t *testing.T) {
	cases := []struct {
		env string
		get func(*Workspace) bool
	}{
		{"ROKSBNKCTL_CERT_MANAGER_CREATE", func(w *Workspace) bool { return w.Resources.CertManager.Create }},
		{"ROKSBNKCTL_REGISTRY_COS_CREATE", func(w *Workspace) bool { return w.Resources.RegistryCOS.Create }},
		{"ROKSBNKCTL_CLUSTER_JUMPHOSTS_CREATE", func(w *Workspace) bool { return w.Resources.ClusterJumphosts.Create }},
	}
	for _, c := range cases {
		// both directions: false must land, and true must land, so a setter
		// hard-wired to either constant fails.
		for _, want := range []bool{false, true} {
			t.Run(c.env, func(t *testing.T) {
				for _, e := range SupportedOverrideNames() {
					t.Setenv(e, "")
				}
				if want {
					t.Setenv(c.env, "true")
				} else {
					t.Setenv(c.env, "false")
				}
				ws := &Workspace{Resources: &ResourcesCfg{
					CertManager:      ResourceToggle{Create: !want},
					RegistryCOS:      ResourceToggle{Create: !want},
					ClusterJumphosts: ResourceToggle{Create: !want},
				}}
				OverrideFromEnv(ws)
				if got := c.get(ws); got != want {
					t.Errorf("%s=%v: got %v, want %v", c.env, want, got, want)
				}
			})
		}
	}
}

// A Workspace with a nil Resources is the NORMAL shape on the env-only init
// path — there is no file to have populated it. Dereferencing it without a nil
// guard is a SIGSEGV, not a config error.
func TestOverrideAdoptTogglesTolerateNilResources(t *testing.T) {
	for _, e := range SupportedOverrideNames() {
		t.Setenv(e, "")
	}
	t.Setenv("ROKSBNKCTL_CERT_MANAGER_CREATE", "false")

	ws := &Workspace{} // Resources is nil
	OverrideFromEnv(ws)

	if ws.Resources == nil {
		t.Fatal("Resources still nil; the override did not take effect")
	}
	if ws.Resources.CertManager.Create {
		t.Error("cert_manager.create = true; want false from the environment")
	}
}
