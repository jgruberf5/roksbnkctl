package ibm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// #302: the sweep matches resources by `<prefix>-*` name, which cannot tell a
// resource roksbnkctl CREATED from one the workspace ADOPTED. The motivating
// case is exact: a workspace with prefix `sm-cli` adopting the COS instance
// `sm-cli-registry-cos`.
//
// That matters more than it looks. `registry_cos.create: false` exists for the
// RC-InstanceCountExceeded case — the account is at its COS cap — so deleting
// the adopted instance is not "re-run and it comes back", and the instance
// backs a live cluster's internal registry.

func TestAdoptedCOSIsFoundButProtected(t *testing.T) {
	found := []OrphanResource{
		{Kind: "cos_instance", Name: "sm-cli-registry-cos", ID: "crn:v1:bluemix:public:cloud-object-storage:global:a/acct:inst::"},
		{Kind: "cos_instance", Name: "sm-cli-scratch-cos", ID: "crn:other"},
	}
	adopted := []AdoptedRef{{Kind: "cos_instance", Value: "sm-cli-registry-cos", Source: "resources.registry_cos.existing"}}

	got := markProtected(found, adopted)

	if !got[0].Protected {
		t.Error("the adopted COS was not protected — cleanup would offer to delete an instance roksbnkctl never created")
	}
	if got[0].ProtectedBy != "resources.registry_cos.existing" {
		t.Errorf("ProtectedBy = %q, want the config key that spared it", got[0].ProtectedBy)
	}
	if got[1].Protected {
		t.Error("a COS that is NOT adopted was protected; the sweep would stop cleaning up after itself")
	}
}

// The prefix predicate is what makes this necessary, so pin it: the adopted
// name really does match, and this is not a hypothetical.
func TestTheAdoptedNameGenuinelyMatchesThePrefix(t *testing.T) {
	if !matchesPrefix("sm-cli-registry-cos", "sm-cli") {
		t.Fatal("matchesPrefix no longer matches the adopted name, so this guard is testing nothing; " +
			"either the predicate changed or the premise of #302 is gone")
	}
}

// Kind scoping: a name adopted as one kind must not spare a same-named resource
// of another kind.
func TestProtectionIsScopedByKind(t *testing.T) {
	found := []OrphanResource{{Kind: "vpc", Name: "shared-net"}}
	got := markProtected(found, []AdoptedRef{{Kind: "transit_gateway", Value: "shared-net", Source: "resources.transit_gateway.existing"}})
	if got[0].Protected {
		t.Error("a transit-gateway adoption protected a VPC of the same name")
	}
}

// Adoption by ID and by CRN, not only by name: cluster_vpc.existing is a VPC
// ID, and the COS sweep carries its CRN in ID.
func TestProtectionMatchesIDAndCRN(t *testing.T) {
	found := []OrphanResource{
		{Kind: "vpc", Name: "someones-vpc", ID: "r014-abc-123"},
		{Kind: "cos_instance", Name: "other", CRN: "crn:v1:bluemix:public:cloud-object-storage:global:a/acct:inst::"},
	}
	got := markProtected(found, []AdoptedRef{
		{Kind: "vpc", Value: "r014-abc-123", Source: "resources.cluster_vpc.existing"},
		{Kind: "cos_instance", Value: "crn:v1:bluemix:public:cloud-object-storage:global:a/acct:inst::", Source: "x"},
	})
	if !got[0].Protected {
		t.Error("adoption by VPC ID did not protect; resources.cluster_vpc.existing is an ID, not a name")
	}
	if !got[1].Protected {
		t.Error("adoption by CRN did not protect")
	}
}

// Case: an operator may write either case in config, and IBM names are not
// case-sensitive in practice.
func TestProtectionIsCaseInsensitive(t *testing.T) {
	found := []OrphanResource{{Kind: "cos_instance", Name: "SM-CLI-Registry-COS"}}
	got := markProtected(found, []AdoptedRef{{Kind: "cos_instance", Value: "sm-cli-registry-cos", Source: "x"}})
	if !got[0].Protected {
		t.Error("case difference defeated protection")
	}
}

// An empty Value must never protect anything — an absent config key would
// otherwise spare every resource with an empty name field.
func TestEmptyAdoptedValueProtectsNothing(t *testing.T) {
	found := []OrphanResource{{Kind: "cos_instance", Name: "sm-cli-cos"}, {Kind: "vpc", Name: ""}}
	got := markProtected(found, []AdoptedRef{{Kind: "", Value: "", Source: "unset"}})
	for i, o := range got {
		if o.Protected {
			t.Errorf("entry %d protected by an EMPTY adopted value; an unset config key would disable the sweep", i)
		}
	}
}

// An unscoped ref (Kind "") protects across surfaces deliberately; pin it so
// the "" case is not read as "match nothing".
func TestUnscopedRefProtectsAnyKind(t *testing.T) {
	found := []OrphanResource{{Kind: "ssh_key", Name: "shared-key"}}
	got := markProtected(found, []AdoptedRef{{Kind: "", Value: "shared-key", Source: "x"}})
	if !got[0].Protected {
		t.Error("an unscoped adopted ref did not protect")
	}
}

// The second layer. Filtering protected resources out of cleanup's delete loop
// is a property of the CALLER, and mutation K6 — pointing the loop at the
// unfiltered slice — compiled and passed every test that existed at the time.
// DeleteOrphan refuses them itself, so deleting an adopted resource takes two
// independent mistakes.
func TestDeleteOrphanRefusesAProtectedResource(t *testing.T) {
	// No client fields are touched before the refusal, which is the point: the
	// check happens before any kind-specific work, so a nil client is enough to
	// prove nothing reached the API.
	var c Client
	o := OrphanResource{
		Kind:        "cos_instance",
		Name:        "sm-cli-registry-cos",
		ID:          "crn:v1:bluemix:public:cloud-object-storage:global:a/acct:inst::",
		Protected:   true,
		ProtectedBy: "resources.registry_cos.existing",
	}

	err := c.DeleteOrphan(context.Background(), o, nil)
	if err == nil {
		t.Fatal("DeleteOrphan accepted a protected resource; a caller passing the unfiltered set would delete an adopted COS")
	}
	if !errors.Is(err, ErrProtectedResource) {
		t.Errorf("error %v does not wrap ErrProtectedResource, so callers cannot distinguish it from a transient failure", err)
	}
	for _, want := range []string{"sm-cli-registry-cos", "resources.registry_cos.existing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal message is missing %q: %v", want, err)
		}
	}
}

// An UNprotected resource must still reach the kind switch — otherwise the
// guard above would have disabled cleanup entirely and the test would not say so.
func TestDeleteOrphanStillActsOnUnprotectedResources(t *testing.T) {
	// "instance" funnels into authedDELETE, which fails on the IAM token with an
	// empty api key rather than dereferencing an SDK client. Reaching THAT error
	// is the proof that the refusal did not swallow it.
	c := Client{region: "us-east"}
	o := OrphanResource{Kind: "instance", Name: "sm-cli-jumphost", ID: "0717-abc", Region: "us-east"}

	err := c.DeleteOrphan(context.Background(), o, nil)
	if errors.Is(err, ErrProtectedResource) {
		t.Fatal("an unprotected resource was refused as protected — the guard disabled cleanup entirely")
	}
	if err == nil {
		t.Fatal("expected the unauthenticated delete to fail; a nil error here means it somehow reached the API")
	}
}
