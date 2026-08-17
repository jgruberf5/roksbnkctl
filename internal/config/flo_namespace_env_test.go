package config

import "testing"

// Collapsing both namespaces onto one value is supported — verified end to end
// against BNK 2.3, where FLO tolerates sharedComponentNamespace equalling its
// own namespace (#66). Without these overrides that capability was unreachable
// from any env-driven runner, including every BNK Forge module, because
// `init --non-interactive` builds config.yaml from the environment alone.
func TestFLONamespacesFromEnv(t *testing.T) {
	t.Setenv("ROKSBNKCTL_FLO_NAMESPACE", "f5-bnk")
	t.Setenv("ROKSBNKCTL_FLO_UTILS_NAMESPACE", "f5-bnk")
	ws := &Workspace{}
	applied := OverrideFromEnv(ws)

	if ws.BNK.FLONamespace != "f5-bnk" || ws.BNK.FLOUtilsNamespace != "f5-bnk" {
		t.Fatalf("flo=%q utils=%q, want both f5-bnk", ws.BNK.FLONamespace, ws.BNK.FLOUtilsNamespace)
	}
	var n int
	for _, a := range applied {
		if a == "bnk.flo_namespace (ROKSBNKCTL_FLO_NAMESPACE)" ||
			a == "bnk.flo_utils_namespace (ROKSBNKCTL_FLO_UTILS_NAMESPACE)" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("both must be reported as applied: %v", applied)
	}
}

// Independently settable — collapsing is one use, not the only one.
func TestFLONamespacesIndependent(t *testing.T) {
	t.Setenv("ROKSBNKCTL_FLO_NAMESPACE", "bnk-prod")
	ws := &Workspace{}
	OverrideFromEnv(ws)
	if ws.BNK.FLONamespace != "bnk-prod" {
		t.Errorf("flo_namespace = %q", ws.BNK.FLONamespace)
	}
	if ws.BNK.FLOUtilsNamespace != "" {
		t.Errorf("utils must stay unset so the terraform default applies, got %q", ws.BNK.FLOUtilsNamespace)
	}
}

// Unset renders nothing and leaves the HCL defaults (f5-bnk / f5-utils).
func TestFLONamespacesUnsetChangesNothing(t *testing.T) {
	ws := &Workspace{}
	OverrideFromEnv(ws)
	if ws.BNK.FLONamespace != "" || ws.BNK.FLOUtilsNamespace != "" {
		t.Errorf("unset must stay empty: flo=%q utils=%q", ws.BNK.FLONamespace, ws.BNK.FLOUtilsNamespace)
	}
}
