package cli

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func zones(cidrs ...[2]string) []config.BNKZoneCfg {
	z := make([]config.BNKZoneCfg, 0, len(cidrs))
	for _, c := range cidrs {
		z = append(z, config.BNKZoneCfg{ExtVLANCIDR: c[0], IntVLANCIDR: c[1]})
	}
	return z
}

// The suggestion exists so the prompt stops offering a stale 24 against subnets
// the operator just typed — the ordering bug that produced /24 self-IPs on /23
// VLANs (#67).
func TestCommonVLANPrefixLenAgrees(t *testing.T) {
	got, ok := commonVLANPrefixLen(zones(
		[2]string{"10.155.15.0/23", "10.254.99.0/23"},
		[2]string{"10.156.16.0/23", "10.254.100.0/23"},
	))
	if !ok || got != 23 {
		t.Errorf("got (%d, %v), want (23, true)", got, ok)
	}
}

// No single right answer ⇒ suggest nothing, rather than a mask correct for only
// some zones. A confidently wrong default is worse than a stale one, because it
// looks like the tool worked it out.
func TestCommonVLANPrefixLenDisagreementSuggestsNothing(t *testing.T) {
	for _, c := range [][]config.BNKZoneCfg{
		zones([2]string{"10.155.15.0/23", "10.254.99.0/24"}),                                  // ext vs int
		zones([2]string{"10.155.15.0/23", "10.254.99.0/23"}, [2]string{"10.1.0.0/24", "10.2.0.0/24"}), // zone vs zone
	} {
		if got, ok := commonVLANPrefixLen(c); ok {
			t.Errorf("disagreeing CIDRs suggested %d; want no suggestion", got)
		}
	}
}

// Garbage in, no suggestion — never a partially-derived guess.
func TestCommonVLANPrefixLenRejectsUnparseable(t *testing.T) {
	for _, c := range []([]config.BNKZoneCfg){
		zones([2]string{"", "10.254.99.0/23"}),
		zones([2]string{"not-a-cidr", "10.254.99.0/23"}),
		zones([2]string{"10.155.15.0", "10.254.99.0/23"}), // no mask
		{},
	} {
		if got, ok := commonVLANPrefixLen(c); ok {
			t.Errorf("input %v suggested %d; want no suggestion", c, got)
		}
	}
}

// Only the VLAN CIDRs carry the self-IPs. SNAT and VIP ranges are routed and
// legitimately sized differently, so they must not drag the suggestion around.
func TestCommonVLANPrefixLenIgnoresSNATAndVIP(t *testing.T) {
	z := []config.BNKZoneCfg{{
		ExtVLANCIDR: "10.155.15.0/23",
		IntVLANCIDR: "10.254.99.0/23",
		IntSNATCIDR: "10.10.11.0/28", // deliberately different
		IntVIPCIDR:  "10.135.15.0/25",
	}}
	got, ok := commonVLANPrefixLen(z)
	if !ok || got != 23 {
		t.Errorf("got (%d, %v), want (23, true) — SNAT/VIP must not affect it", got, ok)
	}
}
