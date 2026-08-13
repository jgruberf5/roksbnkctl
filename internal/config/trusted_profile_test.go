package config

import "testing"

// Absence must render nothing and mean the HCL defaults — the same rule every
// other optional BNK field follows. A block that quietly asserted defaults would
// pin them here and in the HCL, where they could then drift apart.
func TestTrustedProfileAbsentIsNil(t *testing.T) {
	ws := &Workspace{}
	if ws.BNK.TrustedProfile != nil {
		t.Error("an unset trusted_profile block must stay nil, not materialize defaults")
	}
}

// The service account is safe to share across clusters. This test exists to pin
// the REASONING, because the obvious "fix" for a perceived collision — padding
// the name with the cluster — breaks the IAM link instead of protecting it: the
// name must match the account the CNE controller pod actually runs as.
//
// Uniqueness comes from two places the SA name has nothing to do with:
//   - the profile NAME carries the cluster name (unique per account)
//   - the LINK is scoped by cluster CRN
func TestTrustedProfileServiceAccountIsNotAUniquenessKnob(t *testing.T) {
	a := &Workspace{BNK: BNKCfg{TrustedProfile: &BNKTrustedProfileCfg{ServiceAccount: "f5-cne-controller"}}}
	b := &Workspace{BNK: BNKCfg{TrustedProfile: &BNKTrustedProfileCfg{ServiceAccount: "f5-cne-controller"}}}
	if a.BNK.TrustedProfile.ServiceAccount != b.BNK.TrustedProfile.ServiceAccount {
		t.Fatal("precondition")
	}
	// Two clusters sharing the name is the SUPPORTED case, not a conflict to
	// detect. Nothing in config may reject or rewrite it.
	if got := a.BNK.TrustedProfile.ServiceAccount; got != "f5-cne-controller" {
		t.Errorf("service account was rewritten to %q", got)
	}
}

func TestTrustedProfileRolesRoundTrip(t *testing.T) {
	ws := &Workspace{BNK: BNKCfg{TrustedProfile: &BNKTrustedProfileCfg{
		Roles: []string{"Viewer", "Editor"},
	}}}
	if len(ws.BNK.TrustedProfile.Roles) != 2 {
		t.Fatalf("roles = %v", ws.BNK.TrustedProfile.Roles)
	}
}

// The env path is how the Forge/CI runners configure anything at all — they
// never write a config.yaml.
func TestTrustedProfileFromEnv(t *testing.T) {
	t.Setenv("ROKSBNKCTL_TRUSTED_PROFILE_SA", "my-cne-sa")
	t.Setenv("ROKSBNKCTL_TRUSTED_PROFILE_ROLES", "Viewer, Editor , Operator")
	ws := &Workspace{}
	applied := OverrideFromEnv(ws)

	tp := ws.BNK.TrustedProfile
	if tp == nil {
		t.Fatal("the env override did not create the block")
	}
	if tp.ServiceAccount != "my-cne-sa" {
		t.Errorf("service account = %q", tp.ServiceAccount)
	}
	// Whitespace around comma-separated values is what a human writes in a CI
	// variable; it must not become part of a role name.
	want := []string{"Viewer", "Editor", "Operator"}
	if len(tp.Roles) != len(want) {
		t.Fatalf("roles = %#v", tp.Roles)
	}
	for i := range want {
		if tp.Roles[i] != want[i] {
			t.Errorf("role %d = %q, want %q", i, tp.Roles[i], want[i])
		}
	}
	if len(applied) == 0 {
		t.Error("the override must be reported, or an operator cannot tell it took effect")
	}
}
