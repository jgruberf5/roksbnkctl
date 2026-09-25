package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
)

// #302. These assert on what `cleanup` actually does with the workspace, not on
// the predicate in internal/ibm — a workspace whose adopt keys never reach
// SweepScope would leave that predicate correct and the command still
// dangerous.

func adoptWorkspace() *config.Workspace {
	ws := &config.Workspace{Prefix: "sm-cli"}
	ws.Cluster = config.ClusterCfg{Create: false, Name: "sm-cli"}
	ws.Resources = &config.ResourcesCfg{
		TransitGateway:    config.ResourceToggle{Create: false, Existing: "shared-tgw"},
		RegistryCOS:       config.ResourceToggle{Create: false, Existing: "sm-cli-registry-cos"},
		ClientVPC:         config.ResourceToggle{Create: false, Existing: "sm-cli-client-vpc"},
		ClusterVPC:        config.ResourceToggle{Create: false, Existing: "r014-abcdef"},
		TestingSSHKeyName: "sm-cli-key",
	}
	return ws
}

// Every adopt path the workspace exposes must reach the sweep. #302 traced only
// the COS path and said so; the others share the prefix predicate.
func TestAdoptedRefsCoverEveryAdoptPath(t *testing.T) {
	refs := adoptedRefs(adoptWorkspace())

	want := map[string]string{
		"sm-cli":              "cluster",
		"shared-tgw":          "transit_gateway",
		"sm-cli-registry-cos": "cos_instance",
		"sm-cli-client-vpc":   "vpc",
		"r014-abcdef":         "vpc",
		"sm-cli-key":          "ssh_key",
	}
	got := map[string]string{}
	for _, r := range refs {
		got[r.Value] = r.Kind
		if r.Source == "" {
			t.Errorf("ref %q has no Source; the operator would see a resource spared with no reason given", r.Value)
		}
	}
	for v, kind := range want {
		if got[v] != kind {
			t.Errorf("adopt value %q → kind %q, want %q (path not wired into the sweep)", v, got[v], kind)
		}
	}
}

// create: true must NOT protect. Otherwise cleanup stops cleaning up after
// itself, which is the failure this fix could easily introduce.
func TestCreatedResourcesAreNotProtected(t *testing.T) {
	ws := &config.Workspace{Prefix: "sm-cli"}
	ws.Cluster = config.ClusterCfg{Create: true, Name: "sm-cli"}
	ws.Resources = &config.ResourcesCfg{
		TransitGateway: config.ResourceToggle{Create: true, Existing: "leftover-value"},
		RegistryCOS:    config.ResourceToggle{Create: true, Existing: "sm-cli-registry-cos"},
		ClientVPC:      config.ResourceToggle{Create: true, Existing: "x"},
		ClusterVPC:     config.ResourceToggle{Create: true, Existing: "y"},
	}
	if refs := adoptedRefs(ws); len(refs) != 0 {
		t.Errorf("create: true produced %d protection ref(s): %+v — a stale `existing` value must not spare a resource the tool owns", len(refs), refs)
	}
}

// A workspace with no resources block must not panic and must protect nothing
// beyond an adopted cluster.
func TestAdoptedRefsWithoutResourcesBlock(t *testing.T) {
	ws := &config.Workspace{Prefix: "sm-cli"}
	ws.Cluster = config.ClusterCfg{Create: true}
	if refs := adoptedRefs(ws); len(refs) != 0 {
		t.Errorf("want no refs, got %+v", refs)
	}
}

// The delete loop iterates the DELETABLE slice, so --auto cannot reach a
// protected resource: it is not in the collection the loop walks.
func TestPartitionKeepsProtectedOutOfTheDeleteSet(t *testing.T) {
	orphans := []ibm.OrphanResource{
		{Kind: "cos_instance", Name: "sm-cli-registry-cos", Protected: true, ProtectedBy: "resources.registry_cos.existing"},
		{Kind: "vpc", Name: "sm-cli-vpc"},
		{Kind: "cluster", Name: "sm-cli", Protected: true, ProtectedBy: "cluster.name (create: false)"},
	}
	deletable, protected := partitionProtected(orphans)

	if len(deletable) != 1 || deletable[0].Name != "sm-cli-vpc" {
		t.Fatalf("deletable = %+v, want only the unprotected VPC", deletable)
	}
	if len(protected) != 2 {
		t.Fatalf("protected = %+v, want both adopted resources reported", protected)
	}
	for _, o := range deletable {
		if o.Protected {
			t.Fatal("a protected resource reached the delete set")
		}
	}
}

// The operator has to be able to see WHY something was spared, or they will
// delete it by hand and the guard achieves nothing.
func TestProtectedListingNamesTheConfigKey(t *testing.T) {
	var sb strings.Builder
	printProtected(&sb, []ibm.OrphanResource{
		{Kind: "cos_instance", Name: "sm-cli-registry-cos", ProtectedBy: "resources.registry_cos.existing"},
	})
	out := sb.String()
	for _, want := range []string{"cos_instance", "sm-cli-registry-cos", "resources.registry_cos.existing", "ADOPTED VIA"} {
		if !strings.Contains(out, want) {
			t.Errorf("protected listing is missing %q:\n%s", want, out)
		}
	}
}

// recordingDeleter captures what the delete loop actually asks to delete.
type recordingDeleter struct {
	got   []ibm.OrphanResource
	sweep [][]ibm.OrphanResource
	err   error
}

func (r *recordingDeleter) DeleteOrphan(_ context.Context, o ibm.OrphanResource, sweep []ibm.OrphanResource) error {
	r.got = append(r.got, o)
	r.sweep = append(r.sweep, sweep)
	return r.err
}

// The loop must not ask to delete a protected resource even when handed one.
// Mutation K6 — pointing the loop at the UNFILTERED set — compiled and passed
// every test, so the slice it iterates was load-bearing with nothing pinning
// it. It is not load-bearing any more, and this is what says so.
func TestDeleteOrphansSkipsProtectedEvenWhenHandedThem(t *testing.T) {
	rd := &recordingDeleter{}
	var sb strings.Builder

	list := []ibm.OrphanResource{
		{Kind: "cos_instance", Name: "sm-cli-registry-cos", Protected: true, ProtectedBy: "resources.registry_cos.existing"},
		{Kind: "vpc", Name: "sm-cli-vpc"},
		{Kind: "cluster", Name: "sm-cli", Protected: true, ProtectedBy: "cluster.name (create: false)"},
	}
	failures, refusals := deleteOrphans(context.Background(), rd, list, &sb)

	if len(rd.got) != 1 || rd.got[0].Name != "sm-cli-vpc" {
		t.Fatalf("delete was asked for %+v; only the unprotected VPC may be deleted", rd.got)
	}
	for _, o := range rd.got {
		if o.Protected {
			t.Fatal("the loop asked to delete a protected resource")
		}
	}
	if failures != 0 || refusals != 0 {
		t.Errorf("failures=%d refusals=%d, want 0/0 — skipping is not a failure", failures, refusals)
	}
	if strings.Contains(sb.String(), "sm-cli-registry-cos") {
		t.Errorf("a skipped resource was reported in the delete output; it belongs in the adopted listing only:\n%s", sb.String())
	}
}

// And it must still delete what it should — otherwise the skip above could be
// "delete nothing" and the test would not notice.
func TestDeleteOrphansStillDeletesUnprotected(t *testing.T) {
	rd := &recordingDeleter{}
	var sb strings.Builder
	list := []ibm.OrphanResource{
		{Kind: "vpc", Name: "sm-cli-vpc", Region: "us-east"},
		{Kind: "subnet", Name: "sm-cli-subnet"},
	}
	if failures, _ := deleteOrphans(context.Background(), rd, list, &sb); failures != 0 {
		t.Errorf("failures=%d, want 0", failures)
	}
	if len(rd.got) != 2 {
		t.Fatalf("deleted %d, want both unprotected resources", len(rd.got))
	}
	if !strings.Contains(sb.String(), "sm-cli-vpc (us-east)") {
		t.Errorf("region missing from the progress line:\n%s", sb.String())
	}
}

// The SWEEP argument matters as much as the loop. ibm.sweptVPCCRNs reads it as
// "the VPCs this run is deleting", and the Transit Gateway path detaches
// connections to exactly those. A protected VPC left in the sweep set would
// have its own delete refused while its gateway connection was detached anyway
// — the same harm as #302, arriving through the transit gateway instead.
func TestDeleteOrphansKeepsProtectedOutOfTheSweepSet(t *testing.T) {
	rd := &recordingDeleter{}
	var sb strings.Builder

	list := []ibm.OrphanResource{
		{Kind: "vpc", Name: "adopted-client-vpc", CRN: "crn:vpc:adopted", Protected: true, ProtectedBy: "resources.client_vpc.existing"},
		{Kind: "transit_gateway", Name: "sm-cli-tgw"},
	}
	deleteOrphans(context.Background(), rd, list, &sb)

	if len(rd.sweep) == 0 {
		t.Fatal("no delete was attempted, so the sweep set was never observed")
	}
	for _, sweep := range rd.sweep {
		for _, o := range sweep {
			if o.Protected {
				t.Fatalf("the sweep set handed to DeleteOrphan contains the protected %s %q; "+
					"the transit-gateway path would treat it as a VPC this run is deleting and detach its connection",
					o.Kind, o.Name)
			}
			if o.CRN == "crn:vpc:adopted" {
				t.Fatalf("the adopted VPC's CRN reached the sweep set: %+v", o)
			}
		}
	}
}
