package ibm

import (
	"strings"
	"testing"
)

// IntendedPrefixes must reproduce terraform's cidrsubnet(cidr, 2, i) exactly, or
// the guard would refuse (or allow) the wrong thing.
func TestIntendedPrefixes(t *testing.T) {
	cases := []struct {
		cidr string
		want []string
	}{
		// The default: byte-identical to what IBM's "auto" assigns today, which is
		// why opting in on a new cluster changes no addresses.
		{"10.241.0.0/16", []string{"10.241.0.0/18", "10.241.64.0/18", "10.241.128.0/18"}},
		{"10.242.0.0/16", []string{"10.242.0.0/18", "10.242.64.0/18", "10.242.128.0/18"}},
		{"172.16.0.0/17", []string{"172.16.0.0/19", "172.16.32.0/19", "172.16.64.0/19"}},
		{"10.250.0.0/18", []string{"10.250.0.0/20", "10.250.16.0/20", "10.250.32.0/20"}},
	}
	for _, tc := range cases {
		got, err := IntendedPrefixes(tc.cidr)
		if err != nil {
			t.Fatalf("IntendedPrefixes(%q): %v", tc.cidr, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("IntendedPrefixes(%q) = %v, want %v", tc.cidr, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("IntendedPrefixes(%q)[%d] = %q, want %q", tc.cidr, i, got[i], tc.want[i])
			}
		}
	}
}

// Empty means IBM auto-assignment — and the guard must still know what that takes,
// because the whole point is to refuse BEFORE the VPC exists.
func TestIntendedPrefixes_EmptyIsAutoDefault(t *testing.T) {
	got, err := IntendedPrefixes("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "10.241.0.0/18" {
		t.Fatalf("empty cidr should yield the auto defaults, got %v", got)
	}
	// Must be a copy: a caller mutating the result must not corrupt the package var.
	got[0] = "0.0.0.0/0"
	again, _ := IntendedPrefixes("")
	if again[0] != "10.241.0.0/18" {
		t.Error("IntendedPrefixes returned an aliased slice — a caller corrupted DefaultAutoPrefixes")
	}
}

func TestIntendedPrefixes_Rejects(t *testing.T) {
	for _, bad := range []string{"not-a-cidr", "10.241.0.0", "10.241.0.0/20", "10.241.0.0/24"} {
		if _, err := IntendedPrefixes(bad); err == nil {
			t.Errorf("IntendedPrefixes(%q) should have errored", bad)
		}
	}
}

// The case that actually bites: two clusters with no vpc_cidr set both take the
// auto defaults, so every zone prefix collides.
func TestFindPrefixConflicts_IdenticalAutoDefaults(t *testing.T) {
	intended, _ := IntendedPrefixes("")
	attached := map[string][]string{"fdisco-cluster-vpc": DefaultAutoPrefixes}

	conflicts, err := FindPrefixConflicts(intended, attached)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 3 {
		t.Fatalf("want 3 zone conflicts, got %d: %v", len(conflicts), conflicts)
	}
	if !strings.Contains(conflicts[0].String(), "fdisco-cluster-vpc") {
		t.Errorf("the conflict must name the VPC already holding the prefix, got %q", conflicts[0])
	}
}

// A distinct block is the fix — it must come back clean.
func TestFindPrefixConflicts_DistinctBlocksAreClean(t *testing.T) {
	intended, _ := IntendedPrefixes("10.242.0.0/16")
	attached := map[string][]string{"other-cluster-vpc": DefaultAutoPrefixes}

	conflicts, err := FindPrefixConflicts(intended, attached)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("10.242/16 must not conflict with 10.241/18s, got %v", conflicts)
	}
}

// Containment counts, in both directions — an equality check would miss it, and a
// /18 sitting inside someone's /16 is exactly the shape that gets missed by eye.
func TestFindPrefixConflicts_Containment(t *testing.T) {
	conflicts, err := FindPrefixConflicts([]string{"10.241.64.0/18"},
		map[string][]string{"services-vpc": {"10.241.0.0/16"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("a /18 inside a /16 must conflict, got %v", conflicts)
	}
	// …and the other way round.
	conflicts, err = FindPrefixConflicts([]string{"10.241.0.0/16"},
		map[string][]string{"services-vpc": {"10.241.64.0/18"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("a /16 containing a /18 must conflict, got %v", conflicts)
	}
}

// The non-cluster VPCs on the gateway (Harbor's services VPC, the FLP) are
// legitimately attached and must not trip the guard when they do not overlap.
func TestFindPrefixConflicts_UnrelatedAttachmentsIgnored(t *testing.T) {
	intended, _ := IntendedPrefixes("10.241.0.0/16")
	attached := map[string][]string{
		"bnk-svc-vpc": {"10.243.0.0/24"}, // Harbor + FLP live here
		"client-vpc":  {"10.244.0.0/18"},
	}
	conflicts, err := FindPrefixConflicts(intended, attached)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unrelated VPCs must not conflict, got %v", conflicts)
	}
}

func TestFindPrefixConflicts_BadCIDRSurfaces(t *testing.T) {
	if _, err := FindPrefixConflicts([]string{"nonsense"}, map[string][]string{"v": {"10.0.0.0/8"}}); err == nil {
		t.Error("a malformed intended CIDR should error, not silently pass")
	}
	if _, err := FindPrefixConflicts([]string{"10.0.0.0/8"}, map[string][]string{"v": {"nonsense"}}); err == nil {
		t.Error("a malformed attached CIDR should error, not silently pass")
	}
}

// A workspace re-running `cluster up` sees its OWN VPC attached to the gateway,
// carrying exactly the prefixes it intends to use. The caller must exclude it before
// calling FindPrefixConflicts — this pins the arithmetic that makes that necessary,
// so the exclusion is never mistaken for over-caution and removed.
//
// It is not hypothetical: the first cut of the cluster-up guard compared against
// every attached VPC, and the second run of a disconnected workflow refused itself
// with "10.243.0.0/18 overlaps 10.243.0.0/18 on VPC bnk-dc-cluster-vpc". A guard
// that blocks an idempotent re-run is worse than no guard, because the retry after a
// partial failure is exactly when you need it to get out of the way.
func TestFindPrefixConflicts_AVPCOverlapsItself(t *testing.T) {
	intended, _ := IntendedPrefixes("10.243.0.0/16")
	own := map[string][]string{"bnk-dc-cluster-vpc": intended}

	conflicts, err := FindPrefixConflicts(intended, own)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 3 {
		t.Fatalf("a VPC trivially overlaps itself on all three zones, got %d: %v", len(conflicts), conflicts)
	}
	// …so with the owner excluded, as the guard must, it is clean.
	conflicts, err = FindPrefixConflicts(intended, map[string][]string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("excluding our own VPC must leave nothing to conflict with, got %v", conflicts)
	}
}
