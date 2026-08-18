package ibm

import (
	"errors"
	"strings"
	"testing"
)

const (
	sweptVPCCRN   = "crn:v1:bluemix:public:is:us-east:a/acct::vpc:r014-swept"
	foreignVPCCRN = "crn:v1:bluemix:public:is:us-east:a/acct::vpc:r014-foreign"
)

func sweepWithVPC(crn string) []OrphanResource {
	return []OrphanResource{
		{Kind: "transit_gateway", ID: "gw-1", Name: "f5orph-tgw"},
		{Kind: "vpc", ID: "r014-swept", Name: "f5orph-vpc", Region: "us-east", CRN: crn},
		// A non-VPC resource must never widen what may be detached.
		{Kind: "subnet", ID: "sn-1", Name: "f5orph-subnet", Region: "us-east", CRN: "crn:v1:bluemix:public:is:us-east:a/acct::subnet:sn-1"},
	}
}

func TestSweptVPCCRNsCoversOnlyVPCs(t *testing.T) {
	got := sweptVPCCRNs(sweepWithVPC(sweptVPCCRN))
	if len(got) != 1 || !got[strings.ToLower(sweptVPCCRN)] {
		t.Fatalf("only the VPC CRN may authorise a detach, got %v", got)
	}
}

// A gateway attached solely to a VPC this run is deleting is entirely ours.
func TestPartitionConnections_OwnVPCIsDetachable(t *testing.T) {
	conns := []TGWConnection{
		{ID: "c1", Name: "conn-swept", NetworkType: "vpc", NetworkID: sweptVPCCRN, Status: "attached"},
	}
	ours, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(ours) != 1 || len(foreign) != 0 {
		t.Fatalf("ours=%d foreign=%d — a VPC in the sweep is ours to detach", len(ours), len(foreign))
	}
}

// The case the "re-run cleanup" advice could never fix: a shared gateway whose
// other tenant is outside the sweep. Detaching it is not cleanup's call.
func TestPartitionConnections_ForeignVPCIsRefused(t *testing.T) {
	conns := []TGWConnection{
		{ID: "c1", Name: "conn-swept", NetworkType: "vpc", NetworkID: sweptVPCCRN, Status: "attached"},
		{ID: "c2", Name: "conn-shared", NetworkType: "vpc", NetworkID: foreignVPCCRN, Status: "attached"},
	}
	ours, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(ours) != 1 || len(foreign) != 1 {
		t.Fatalf("ours=%d foreign=%d — a VPC outside the sweep must not be detachable", len(ours), len(foreign))
	}
	if foreign[0].ID != "c2" {
		t.Errorf("wrong connection held back: %s", foreign[0].ID)
	}
}

// Direct Link, GRE tunnels, classic: not VPCs, so nothing in the sweep can ever
// authorise removing them, whatever their network id happens to say.
func TestPartitionConnections_NonVPCIsAlwaysForeign(t *testing.T) {
	conns := []TGWConnection{
		{ID: "c1", Name: "dl", NetworkType: "directlink", NetworkID: sweptVPCCRN, Status: "attached"},
		{ID: "c2", Name: "gre", NetworkType: "gre_tunnel", Status: "attached"},
		{ID: "c3", Name: "classic", NetworkType: "classic", Status: "attached"},
	}
	ours, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(ours) != 0 || len(foreign) != 3 {
		t.Fatalf("ours=%d foreign=%d — no non-VPC attachment is ever in scope", len(ours), len(foreign))
	}
}

// A connection already on its way out is ours: the wait covers it, and calling
// it foreign would refuse a gateway that is seconds from being deletable —
// re-introducing #85 for the exact case a re-run DOES fix.
func TestPartitionConnections_DeletingCountsAsOurs(t *testing.T) {
	conns := []TGWConnection{
		{ID: "c1", Name: "going", NetworkType: "vpc", NetworkID: foreignVPCCRN, Status: "deleting"},
	}
	ours, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(ours) != 1 || len(foreign) != 0 {
		t.Fatalf("ours=%d foreign=%d — a connection already deleting must not refuse the gateway", len(ours), len(foreign))
	}
}

// CRNs are case-insensitive identifiers; a case difference must not silently
// reclassify our own VPC as someone else's and refuse a gateway we own.
func TestPartitionConnections_CRNMatchIsCaseInsensitive(t *testing.T) {
	conns := []TGWConnection{
		{ID: "c1", NetworkType: "VPC", NetworkID: strings.ToUpper(sweptVPCCRN), Status: "attached"},
	}
	ours, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(ours) != 1 || len(foreign) != 0 {
		t.Fatalf("ours=%d foreign=%d — CRN comparison must be case-insensitive", len(ours), len(foreign))
	}
}

// A VPC discovered without a CRN cannot authorise anything. Failing CLOSED here
// costs a refusal; failing open would detach a network on no evidence.
func TestPartitionConnections_VPCWithoutCRNAuthorisesNothing(t *testing.T) {
	sweep := []OrphanResource{{Kind: "vpc", ID: "r014-swept", Name: "f5orph-vpc"}}
	conns := []TGWConnection{{ID: "c1", NetworkType: "vpc", NetworkID: sweptVPCCRN, Status: "attached"}}
	ours, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweep))
	if len(ours) != 0 || len(foreign) != 1 {
		t.Fatalf("ours=%d foreign=%d — a CRN-less VPC must not authorise a detach", len(ours), len(foreign))
	}
}

// The refusal has to be distinguishable from an ordinary delete failure: the
// CLI uses it to suppress "re-run cleanup" advice that cannot work.
func TestForeignConnectionErrorIsIdentifiable(t *testing.T) {
	wrapped := errors.Join(ErrForeignTGWConnection, errors.New("context"))
	if !errors.Is(wrapped, ErrForeignTGWConnection) {
		t.Error("callers must be able to tell a refusal from a transient failure")
	}
}

// The message has to say what is attached, or the operator cannot act on it.
func TestDescribeConnectionsNamesTheAttachment(t *testing.T) {
	got := describeTGWConnections([]TGWConnection{
		{ID: "c2", Name: "conn-shared", NetworkType: "vpc", NetworkID: foreignVPCCRN, Status: "attached"},
	})
	for _, want := range []string{"conn-shared", "vpc", foreignVPCCRN, "attached"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestDisplayTGWFallsBackToID(t *testing.T) {
	if got := displayTGW("", "gw-1"); got != "gw-1" {
		t.Errorf("an unnamed connection must still be identifiable, got %q", got)
	}
	if got := displayTGW("named", "gw-1"); got != "named" {
		t.Errorf("got %q", got)
	}
}
