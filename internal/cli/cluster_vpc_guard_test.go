package cli

import (
	"sort"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/ibm"
)

func sn(id, name, vpc string) ibm.Subnet {
	var s ibm.Subnet
	s.ID = id
	s.Name = name
	s.VPC.ID = vpc
	return s
}

func TestForeignSubnetsInVPC(t *testing.T) {
	subnets := []ibm.Subnet{
		// ours by name prefix, in our VPC
		sn("0717-a", "us-south-test-1-cluster-subnet-zone1", "vpc-1"),
		sn("0727-b", "us-south-test-1-cluster-subnet-zone2", "vpc-1"),
		// an adopter's subnets in our VPC → foreign
		sn("0737-c", "us-south-test-2-cluster-subnet-zone1", "vpc-1"),
		sn("0747-d", "us-south-test-2-cluster-subnet-zone2", "vpc-1"),
		// ours by recorded id even though the name wouldn't match
		sn("0757-e", "weird-name", "vpc-1"),
		// a lookalike prefix that must NOT count as ours (test-1 vs test-10)
		sn("0767-f", "us-south-test-10-cluster-subnet-zone1", "vpc-1"),
		// a subnet in a DIFFERENT vpc → ignored entirely
		sn("0777-g", "someone-else-subnet", "vpc-2"),
	}
	ownIDs := []string{"0757-e"}
	got := foreignSubnetsInVPC(subnets, "vpc-1", "us-south-test-1-cluster", ownIDs)
	sort.Strings(got)
	want := []string{
		"us-south-test-10-cluster-subnet-zone1",
		"us-south-test-2-cluster-subnet-zone1",
		"us-south-test-2-cluster-subnet-zone2",
	}
	if len(got) != len(want) {
		t.Fatalf("foreign = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("foreign = %v, want %v", got, want)
		}
	}
}

func TestForeignSubnetsInVPC_NoneWhenSolo(t *testing.T) {
	subnets := []ibm.Subnet{
		sn("a", "solo-cluster-subnet-zone1", "vpc-1"),
		sn("b", "solo-cluster-subnet-zone2", "vpc-1"),
		sn("c", "solo-cluster-subnet-zone3", "vpc-1"),
	}
	if got := foreignSubnetsInVPC(subnets, "vpc-1", "solo-cluster", nil); len(got) != 0 {
		t.Fatalf("a solo cluster's own subnets must not be flagged foreign, got %v", got)
	}
}
