package config

import "testing"

// Without this the setting was unreachable from the environment entirely: the
// per-zone overrides carry six fields and no mask, so every env-driven
// deployment — CI, and every BNK Forge blueprint — was pinned to the terraform
// default of 24 regardless of the CIDRs it supplied.
func TestVLANPrefixLenFromEnv(t *testing.T) {
	t.Setenv("ROKSBNKCTL_VLAN_PREFIXLEN", "23")
	ws := &Workspace{}
	applied := OverrideFromEnv(ws)

	if ws.BNK.Network == nil || ws.BNK.Network.VLANPrefixLen == nil {
		t.Fatal("the override did not set the prefix length")
	}
	if got := *ws.BNK.Network.VLANPrefixLen; got != 23 {
		t.Errorf("prefix length = %d, want 23", got)
	}
	var found bool
	for _, a := range applied {
		if a == "bnk.network.vlan_prefixlen (ROKSBNKCTL_VLAN_PREFIXLEN)" {
			found = true
		}
	}
	if !found {
		t.Errorf("not reported as applied: %v", applied)
	}
}

// It must be settable WITHOUT respecifying the zones — it is network-wide, not
// per-zone, and requiring six env vars per zone to change one mask would be its
// own trap.
func TestVLANPrefixLenIndependentOfZones(t *testing.T) {
	t.Setenv("ROKSBNKCTL_VLAN_PREFIXLEN", "23")
	ws := &Workspace{}
	OverrideFromEnv(ws)
	if ws.BNK.Network.VLANPrefixLen == nil {
		t.Fatal("prefix length requires zones to be set — it must not")
	}
	if len(ws.BNK.Network.Zones) != 0 {
		t.Error("setting the mask must not invent zones")
	}
}

// Deliberately NOT derived from, or validated against, the zone CIDRs. A mask
// that disagrees with its subnet is usually a mistake, but it is also a tool:
// making TMM treat a smaller or larger block as directly connected, with static
// routes steering the remainder, is how a specific traffic pattern is forced.
// This test exists so nobody "fixes" the disagreement by rejecting it.
func TestVLANPrefixLenMayDisagreeWithZoneCIDRs(t *testing.T) {
	t.Setenv("ROKSBNKCTL_VLAN_PREFIXLEN", "25")
	t.Setenv("ROKSBNKCTL_ZONE1_EXT_VLAN_CIDR", "10.155.15.0/23")
	t.Setenv("ROKSBNKCTL_ZONE1_INT_VLAN_CIDR", "10.254.99.0/23")
	t.Setenv("ROKSBNKCTL_ZONE1_INT_SNAT_CIDR", "10.10.11.0/23")
	t.Setenv("ROKSBNKCTL_ZONE1_INT_VIP_CIDR", "10.135.15.0/23")
	t.Setenv("ROKSBNKCTL_ZONE1_EXTERNAL_SELFIP", "10.155.15.10")
	t.Setenv("ROKSBNKCTL_ZONE1_INTERNAL_SELFIP", "10.254.99.10")

	ws := &Workspace{}
	OverrideFromEnv(ws)

	if ws.BNK.Network.VLANPrefixLen == nil || *ws.BNK.Network.VLANPrefixLen != 25 {
		t.Fatalf("a /25 mask against /23 subnets must be accepted verbatim, got %v",
			ws.BNK.Network.VLANPrefixLen)
	}
	if len(ws.BNK.Network.Zones) != 1 {
		t.Errorf("zones = %d, want 1", len(ws.BNK.Network.Zones))
	}
}

// A zone override replaces .Zones only; a mask already in config.yaml survives.
func TestZoneOverrideDoesNotClobberPrefixLen(t *testing.T) {
	for _, kv := range [][2]string{
		{"ROKSBNKCTL_ZONE1_EXT_VLAN_CIDR", "10.155.15.0/23"},
		{"ROKSBNKCTL_ZONE1_INT_VLAN_CIDR", "10.254.99.0/23"},
		{"ROKSBNKCTL_ZONE1_INT_SNAT_CIDR", "10.10.11.0/23"},
		{"ROKSBNKCTL_ZONE1_INT_VIP_CIDR", "10.135.15.0/23"},
		{"ROKSBNKCTL_ZONE1_EXTERNAL_SELFIP", "10.155.15.10"},
		{"ROKSBNKCTL_ZONE1_INTERNAL_SELFIP", "10.254.99.10"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	pre := 23
	ws := &Workspace{BNK: BNKCfg{Network: &BNKNetworkCfg{VLANPrefixLen: &pre}}}
	OverrideFromEnv(ws)

	if ws.BNK.Network.VLANPrefixLen == nil || *ws.BNK.Network.VLANPrefixLen != 23 {
		t.Errorf("config.yaml's prefix length was clobbered by the zone override: %v",
			ws.BNK.Network.VLANPrefixLen)
	}
}

// A malformed or out-of-range value cannot be honoured and must not abort a
// whole deployment; the terraform default stands and nothing is reported.
func TestVLANPrefixLenRejectsNonsense(t *testing.T) {
	for _, bad := range []string{"0", "33", "-1", "twenty-three", "23.5", " "} {
		t.Setenv("ROKSBNKCTL_VLAN_PREFIXLEN", bad)
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.Network != nil && ws.BNK.Network.VLANPrefixLen != nil {
			t.Errorf("%q was accepted as a prefix length (= %d)", bad, *ws.BNK.Network.VLANPrefixLen)
		}
	}
}
