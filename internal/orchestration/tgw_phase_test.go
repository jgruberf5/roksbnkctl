package orchestration

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/ibm"
)

func TestTGWConnectionForVPC(t *testing.T) {
	const vpc = "r006-a0acc788-3cbb-44ba-95ea-acb68ad5df59"
	crn := "crn:v1:bluemix:public:is:us-south:a/acct::vpc:" + vpc
	conns := []ibm.TGWConnection{
		{ID: "c-other", Name: "other", NetworkType: "vpc", NetworkID: "crn:...:vpc:r006-different", Status: "attached"},
		{ID: "c-classic", Name: "classic", NetworkType: "classic", NetworkID: "", Status: "attached"},
		{ID: "c-mine", Name: "us-south-test-1", NetworkType: "vpc", NetworkID: crn, Status: "attached"},
	}

	got := tgwConnectionForVPC(conns, vpc)
	if got == nil || got.ID != "c-mine" {
		t.Fatalf("expected to match c-mine, got %+v", got)
	}

	// A VPC not attached to this gateway -> nil (apply proceeds).
	if tgwConnectionForVPC(conns, "r006-notpresent") != nil {
		t.Error("an unattached VPC must not match")
	}

	// A failed/deleting connection is not "already connected" -> nil (recreate).
	failing := []ibm.TGWConnection{{ID: "c-f", NetworkType: "vpc", NetworkID: crn, Status: "failed"}}
	if tgwConnectionForVPC(failing, vpc) != nil {
		t.Error("a failed connection must not count as already-attached")
	}

	// pending counts as already-attached (attachment in progress).
	pending := []ibm.TGWConnection{{ID: "c-p", NetworkType: "vpc", NetworkID: crn, Status: "pending"}}
	if got := tgwConnectionForVPC(pending, vpc); got == nil || got.ID != "c-p" {
		t.Errorf("a pending connection should match, got %+v", got)
	}
}
