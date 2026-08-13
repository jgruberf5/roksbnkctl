package config

import "testing"

// #51 — the connection half of GSLB. Absent means unchanged behaviour: the
// datacenter name alone, as before.
func TestGTMAbsentIsNil(t *testing.T) {
	if (&Workspace{}).BNK.GTM != nil {
		t.Error("an unset gtm block must stay nil")
	}
}

func TestGTMFromEnv(t *testing.T) {
	t.Setenv("ROKSBNKCTL_GTM_URL", "https://gtm.example.com")
	t.Setenv("ROKSBNKCTL_GTM_USERNAME", "admin")
	t.Setenv("ROKSBNKCTL_GTM_PASSWORD", "s3cr3t")
	ws := &Workspace{}
	OverrideFromEnv(ws)

	g := ws.BNK.GTM
	if g == nil {
		t.Fatal("gtm block not created")
	}
	if g.URL != "https://gtm.example.com" || g.Username != "admin" {
		t.Errorf("url=%q user=%q", g.URL, g.Username)
	}
	// The password arrives RAW and is stored base64 — same as the CIS BIG-IP
	// credential and the IBM API key. Never the plaintext on disk.
	if g.PasswordB64 == "s3cr3t" {
		t.Error("password stored in plaintext")
	}
	if g.PasswordB64 != "czNjcjN0" {
		t.Errorf("password_b64 = %q, want base64 of the raw value", g.PasswordB64)
	}
}

// #67's remaining half: external and internal can now differ. nil means "use the
// shared value", so an unset override changes nothing.
func TestPerVLANPrefixLenAbsentIsNil(t *testing.T) {
	ws := &Workspace{BNK: BNKCfg{Network: &BNKNetworkCfg{}}}
	if ws.BNK.Network.VLANPrefixLenExternal != nil || ws.BNK.Network.VLANPrefixLenInternal != nil {
		t.Error("unset per-VLAN overrides must stay nil so the shared value applies")
	}
}

func TestPerVLANPrefixLenFromEnv(t *testing.T) {
	t.Setenv("ROKSBNKCTL_VLAN_PREFIXLEN", "24")
	t.Setenv("ROKSBNKCTL_VLAN_PREFIXLEN_EXTERNAL", "23")
	t.Setenv("ROKSBNKCTL_VLAN_PREFIXLEN_INTERNAL", "26")
	ws := &Workspace{}
	OverrideFromEnv(ws)

	n := ws.BNK.Network
	if n == nil || n.VLANPrefixLen == nil || *n.VLANPrefixLen != 24 {
		t.Fatalf("shared = %v", n)
	}
	// The whole point: a /23 external and a /26 internal, which one scalar could
	// not express.
	if n.VLANPrefixLenExternal == nil || *n.VLANPrefixLenExternal != 23 {
		t.Errorf("external = %v, want 23", n.VLANPrefixLenExternal)
	}
	if n.VLANPrefixLenInternal == nil || *n.VLANPrefixLenInternal != 26 {
		t.Errorf("internal = %v, want 26", n.VLANPrefixLenInternal)
	}
}

// Setting only one leaves the other inheriting the shared value.
func TestPerVLANPrefixLenPartial(t *testing.T) {
	t.Setenv("ROKSBNKCTL_VLAN_PREFIXLEN_EXTERNAL", "23")
	ws := &Workspace{}
	OverrideFromEnv(ws)
	if ws.BNK.Network.VLANPrefixLenExternal == nil {
		t.Fatal("external not set")
	}
	if ws.BNK.Network.VLANPrefixLenInternal != nil {
		t.Error("internal must stay nil so it inherits the shared value")
	}
}

func TestPerVLANPrefixLenRejectsNonsense(t *testing.T) {
	for _, bad := range []string{"0", "33", "-1", "abc"} {
		t.Setenv("ROKSBNKCTL_VLAN_PREFIXLEN_EXTERNAL", bad)
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.Network != nil && ws.BNK.Network.VLANPrefixLenExternal != nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
