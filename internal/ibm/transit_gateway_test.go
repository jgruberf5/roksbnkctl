package ibm

import "testing"

func TestMatchTransitGateway(t *testing.T) {
	gws := []TransitGateway{
		{ID: "r018-aaaa-1111", Name: "shared-tgw"},
		{ID: "r018-bbbb-2222", Name: "other-tgw"},
	}

	t.Run("by name", func(t *testing.T) {
		g, err := matchTransitGateway(gws, "shared-tgw")
		if err != nil || g.ID != "r018-aaaa-1111" {
			t.Fatalf("by name: got %+v, err %v", g, err)
		}
	})
	t.Run("by id", func(t *testing.T) {
		// The whole reason the module lists all gateways instead of the name-only
		// singular data source: an operator may pass the id.
		g, err := matchTransitGateway(gws, "r018-bbbb-2222")
		if err != nil || g.Name != "other-tgw" {
			t.Fatalf("by id: got %+v, err %v", g, err)
		}
	})
	t.Run("no match errors", func(t *testing.T) {
		if _, err := matchTransitGateway(gws, "nope"); err == nil {
			t.Error("expected an error for an unknown gateway")
		}
	})
	t.Run("ambiguous name errors", func(t *testing.T) {
		dup := []TransitGateway{{ID: "a", Name: "dup"}, {ID: "b", Name: "dup"}}
		if _, err := matchTransitGateway(dup, "dup"); err == nil {
			t.Error("two gateways named 'dup' must be an error, not an arbitrary pick")
		}
	})
}

func TestConnectionStateFor(t *testing.T) {
	const vpcCRN = "crn:v1:bluemix:public:is:eu-gb:a/acct::vpc:r018-vpc-1"
	conns := []TGWConnection{
		{Name: "other", NetworkType: "vpc", NetworkID: "crn:...:vpc:r018-vpc-9", Status: "attached"},
		{Name: "ours", NetworkType: "vpc", NetworkID: vpcCRN, Status: "pending"},
	}

	if got := connectionStateFor(conns, vpcCRN); got != "pending" {
		t.Errorf("state for our VPC = %q, want pending", got)
	}
	// CRNs are compared case-insensitively — IBM has been inconsistent on region
	// casing in CRNs, and a case mismatch must not read as "detached".
	if got := connectionStateFor(conns, "CRN:V1:BLUEMIX:PUBLIC:IS:EU-GB:A/ACCT::VPC:R018-VPC-1"); got != "pending" {
		t.Errorf("case-insensitive match failed: got %q", got)
	}
	// A VPC with no connection on this gateway is detached, distinct from an error.
	if got := connectionStateFor(conns, "crn:...:vpc:not-attached"); got != "" {
		t.Errorf("unattached VPC = %q, want \"\" (detached)", got)
	}
}
