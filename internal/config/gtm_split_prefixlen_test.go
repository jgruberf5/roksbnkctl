package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

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

// L4: the struct-literal tests above pin nothing about the YAML contract. A typo
// in a yaml tag would let every one of them pass while the field silently never
// loads from config.yaml — which is the only way most users set it.
func TestNewFieldsRoundTripThroughYAML(t *testing.T) {
	const src = `
bnk:
  trusted_profile:
    service_account: my-sa
    roles: [Viewer, Operator]
  gtm:
    url: https://gtm.example.com
    username: admin
    password_b64: cGFzcw==
  network:
    vlan_prefixlen: 24
    vlan_prefixlen_external: 23
    vlan_prefixlen_internal: 26
cluster:
  network_mode: single-nic
  existing_subnet_ids: [0717-a, 0717-b]
`
	var ws Workspace
	if err := yaml.Unmarshal([]byte(src), &ws); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ws.BNK.TrustedProfile == nil || ws.BNK.TrustedProfile.ServiceAccount != "my-sa" {
		t.Errorf("trusted_profile.service_account did not load: %+v", ws.BNK.TrustedProfile)
	}
	if ws.BNK.TrustedProfile == nil || len(ws.BNK.TrustedProfile.Roles) != 2 {
		t.Errorf("trusted_profile.roles did not load")
	}
	if ws.BNK.GTM == nil || ws.BNK.GTM.URL == "" || ws.BNK.GTM.Username == "" || ws.BNK.GTM.PasswordB64 == "" {
		t.Errorf("gtm.* did not load: %+v", ws.BNK.GTM)
	}
	n := ws.BNK.Network
	if n == nil || n.VLANPrefixLenExternal == nil || *n.VLANPrefixLenExternal != 23 {
		t.Errorf("vlan_prefixlen_external did not load: %+v", n)
	}
	if n == nil || n.VLANPrefixLenInternal == nil || *n.VLANPrefixLenInternal != 26 {
		t.Errorf("vlan_prefixlen_internal did not load")
	}
	if ws.Cluster.NetworkMode != "single-nic" {
		t.Errorf("cluster.network_mode did not load")
	}
	if len(ws.Cluster.ExistingSubnetIDs) != 2 {
		t.Errorf("cluster.existing_subnet_ids did not load")
	}
}
