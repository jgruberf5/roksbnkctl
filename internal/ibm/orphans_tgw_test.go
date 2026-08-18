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
	detach, settling, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(detach) != 1 || len(settling) != 0 || len(foreign) != 0 {
		t.Fatalf("detach=%d settling=%d foreign=%d — a VPC in the sweep is ours to detach", len(detach), len(settling), len(foreign))
	}
}

// The case the "re-run cleanup" advice could never fix: a shared gateway whose
// other tenant is outside the sweep. Detaching it is not cleanup's call.
func TestPartitionConnections_ForeignVPCIsRefused(t *testing.T) {
	conns := []TGWConnection{
		{ID: "c1", Name: "conn-swept", NetworkType: "vpc", NetworkID: sweptVPCCRN, Status: "attached"},
		{ID: "c2", Name: "conn-shared", NetworkType: "vpc", NetworkID: foreignVPCCRN, Status: "attached"},
	}
	detach, _, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(detach) != 1 || len(foreign) != 1 {
		t.Fatalf("detach=%d foreign=%d — a VPC outside the sweep must not be detachable", len(detach), len(foreign))
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
	detach, settling, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(detach) != 0 || len(settling) != 0 || len(foreign) != 3 {
		t.Fatalf("detach=%d settling=%d foreign=%d — no non-VPC attachment is ever in scope", len(detach), len(settling), len(foreign))
	}
}

// A connection already on its way out must neither refuse the gateway nor be
// deleted a second time. It gets its own bucket: waited for, never touched.
//
// Refusing would re-introduce #85 for the one case a re-run genuinely fixes.
// Re-deleting is the subtler hazard — authedDELETE forgives only 404, so a
// non-404 rejection of a redundant DELETE would abort the whole gateway delete
// for a connection that was about to disappear on its own.
func TestPartitionConnections_DeletingIsWaitedForNotRedeleted(t *testing.T) {
	conns := []TGWConnection{
		{ID: "c1", Name: "going", NetworkType: "vpc", NetworkID: foreignVPCCRN, Status: "deleting"},
	}
	detach, settling, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(detach) != 0 {
		t.Errorf("detach=%d — a connection already deleting must not be deleted again", len(detach))
	}
	if len(settling) != 1 {
		t.Errorf("settling=%d — it still has to be waited for", len(settling))
	}
	if len(foreign) != 0 {
		t.Errorf("foreign=%d — a connection already deleting must not refuse the gateway", len(foreign))
	}
}

// CRNs are case-insensitive identifiers; a case difference must not silently
// reclassify our own VPC as someone else's and refuse a gateway we own.
func TestPartitionConnections_CRNMatchIsCaseInsensitive(t *testing.T) {
	conns := []TGWConnection{
		{ID: "c1", NetworkType: "VPC", NetworkID: strings.ToUpper(sweptVPCCRN), Status: "attached"},
	}
	detach, _, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if len(detach) != 1 || len(foreign) != 0 {
		t.Fatalf("detach=%d foreign=%d — CRN comparison must be case-insensitive", len(detach), len(foreign))
	}
}

// A VPC discovered without a CRN cannot authorise anything. Failing CLOSED here
// costs a refusal; failing open would detach a network on no evidence.
func TestPartitionConnections_VPCWithoutCRNAuthorisesNothing(t *testing.T) {
	sweep := []OrphanResource{{Kind: "vpc", ID: "r014-swept", Name: "f5orph-vpc"}}
	conns := []TGWConnection{{ID: "c1", NetworkType: "vpc", NetworkID: sweptVPCCRN, Status: "attached"}}
	detach, _, foreign := partitionTGWConnections(conns, sweptVPCCRNs(sweep))
	if len(detach) != 0 || len(foreign) != 1 {
		t.Fatalf("detach=%d foreign=%d — a CRN-less VPC must not authorise a detach", len(detach), len(foreign))
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
