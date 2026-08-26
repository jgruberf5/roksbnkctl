package orchestration

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// #217. These exercise the drain against a fake API server rather than reading
// the source: the bug is that FLO is removed while finalizers are still
// outstanding, and only running the loop can show whether the CNEInstance waits
// for its leaves.

var (
	gvrCNEInstance = schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: "cneinstances"}
	gvrIPAM        = schema.GroupVersionResource{Group: "fic.f5.com", Version: "v1", Resource: "ipams"}
	gvrIPAMRange   = schema.GroupVersionResource{Group: "fic.f5.com", Version: "v1", Resource: "ipamranges"}
)

// listKinds is what NewSimpleDynamicClientWithCustomListKinds needs so List()
// works for resources that have no compiled-in Go type.
func listKinds() map[schema.GroupVersionResource]string {
	return map[schema.GroupVersionResource]string{
		gvrCNEInstance: "CNEInstanceList",
		gvrIPAM:        "IPAMList",
		gvrIPAMRange:   "IPAMRangeList",
	}
}

func cr(gvr schema.GroupVersionResource, kind, ns, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvr.GroupVersion().WithKind(kind))
	u.SetNamespace(ns)
	u.SetName(name)
	return u
}

func newFake(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds(), objs...)
}

// THE CENTRAL GUARANTEE: the CNEInstance is not deleted until every other F5 CR
// is gone. Deleting it first is what orphans them, because its controller is the
// only thing that clears their finalizers.
func TestTheCNEInstanceIsDeletedOnlyAfterItsLeavesAreGone(t *testing.T) {
	dc := newFake(
		cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"),
		cr(gvrIPAMRange, "IPAMRange", "f5-bnk", "range-1"),
		cr(gvrCNEInstance, "CNEInstance", "f5-bnk", "cne"),
	)

	// Record the order deletes are issued in.
	var mu sync.Mutex
	var order []string
	dc.PrependReactor("delete", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		d := a.(k8stesting.DeleteAction)
		mu.Lock()
		order = append(order, fmt.Sprintf("%s/%s", d.GetResource().Resource, d.GetName()))
		mu.Unlock()
		return false, nil, nil // fall through to the tracker's real delete
	})

	gvrs := []schema.GroupVersionResource{gvrCNEInstance, gvrIPAM, gvrIPAMRange}
	var buf bytes.Buffer
	deleted, remaining := drainBNKCustomResources(
		context.Background(), dc, gvrs, "f5-bnk", 10*time.Second, time.Millisecond, &buf)

	if deleted != 3 {
		t.Errorf("deleted = %d, want 3 (order: %v)", deleted, order)
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 deletes, got %v", order)
	}
	if order[2] != "cneinstances/cne" {
		t.Errorf("the CNEInstance was deleted at position %d (%v).\n"+
			"It must go LAST: its controller is what finalizes the others, so deleting it "+
			"first orphans them and the namespace then hangs in Terminating (#217).", 1+indexOf(order, "cneinstances/cne"), order)
	}
}

// The guarantee has to hold when the leaves DO NOT drain, which is the case the
// repair path exists for. Deleting the CNEInstance anyway would reproduce the
// exact bug inside the fix for it.
func TestTheCNEInstanceSurvivesWhenTheLeavesNeverFinalize(t *testing.T) {
	dc := newFake(
		cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"),
		cr(gvrCNEInstance, "CNEInstance", "f5-bnk", "cne"),
	)
	// A finalizer nobody clears: the delete is accepted, the object stays.
	dc.PrependReactor("delete", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil // swallow it — the object is never removed
	})

	var cneDeleted bool
	dc.PrependReactor("delete", "cneinstances", func(k8stesting.Action) (bool, runtime.Object, error) {
		cneDeleted = true
		return false, nil, nil
	})

	gvrs := []schema.GroupVersionResource{gvrCNEInstance, gvrIPAM}
	var buf bytes.Buffer
	_, remaining := drainBNKCustomResources(
		context.Background(), dc, gvrs, "f5-bnk", 30*time.Millisecond, time.Millisecond, &buf)

	if remaining != 1 {
		t.Errorf("remaining = %d, want 1 (the IPAM that never finalized)", remaining)
	}
	if cneDeleted {
		t.Error("the CNEInstance was deleted even though a leaf CR had not finalized.\n" +
			"That orphans the leaf — exactly the ordering defect #217 is about.")
	}
}

// The wait must be a real wait. If the drain returns as soon as the delete is
// accepted, it is no better than terraform's own no-wait delete and #217 is
// unfixed.
func TestTheDrainWaitsForFinalizersRatherThanForTheDeleteCall(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))

	// Accept the delete but keep listing the object for the first few polls,
	// the way a finalizer holds an object in Terminating.
	var mu sync.Mutex
	lists := 0
	removed := false
	dc.PrependReactor("delete", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil
	})
	dc.PrependReactor("list", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		lists++
		done := lists > 3
		if done {
			removed = true
		}
		mu.Unlock()
		l := &unstructured.UnstructuredList{}
		l.SetGroupVersionKind(gvrIPAM.GroupVersion().WithKind("IPAMList"))
		if !done {
			l.Items = []unstructured.Unstructured{*cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1")}
		}
		return true, l, nil
	})

	var buf bytes.Buffer
	_, remaining := drainBNKCustomResources(
		context.Background(), dc, []schema.GroupVersionResource{gvrIPAM},
		"f5-bnk", 5*time.Second, time.Millisecond, &buf)

	if remaining != 0 {
		t.Errorf("remaining = %d, want 0 — the object did eventually drain", remaining)
	}
	if !removed {
		t.Error("the drain returned before the object stopped being listed: it waited for the " +
			"delete CALL, not for the finalizer. That is terraform's existing behaviour, not a fix.")
	}
	mu.Lock()
	defer mu.Unlock()
	if lists <= 1 {
		t.Errorf("listed %d time(s); a drain that polls once cannot observe finalizing", lists)
	}
}

// A namespace with no BNK CRs must cost nothing and say nothing. `bnk down` on a
// workspace that never installed BNK is ordinary.
func TestAnEmptyNamespaceDrainsSilently(t *testing.T) {
	dc := newFake()
	var buf bytes.Buffer
	deleted, remaining := drainBNKCustomResources(
		context.Background(), dc, []schema.GroupVersionResource{gvrCNEInstance, gvrIPAM},
		"f5-bnk", time.Second, time.Millisecond, &buf)
	if deleted != 0 || remaining != 0 {
		t.Errorf("deleted=%d remaining=%d, want 0/0", deleted, remaining)
	}
	if buf.Len() != 0 {
		t.Errorf("wrote %q for a namespace with nothing in it", buf.String())
	}
}

// The drain is scoped to ONE namespace. The forge kubeconfig is shared between
// workspaces, so a sweep that reached across namespaces could delete a live
// deployment's CNEInstance during an unrelated teardown.
func TestTheDrainDoesNotTouchOtherNamespaces(t *testing.T) {
	dc := newFake(
		cr(gvrCNEInstance, "CNEInstance", "f5-bnk", "mine"),
		cr(gvrCNEInstance, "CNEInstance", "someone-else", "theirs"),
	)
	var buf bytes.Buffer
	drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrCNEInstance}, "f5-bnk", time.Second, time.Millisecond, &buf)

	left, err := dc.Resource(gvrCNEInstance).Namespace("someone-else").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing the other namespace: %v", err)
	}
	if len(left.Items) != 1 {
		t.Errorf("the other namespace's CNEInstance was deleted (%d left, want 1)", len(left.Items))
	}
}

// A cancelled context must stop the wait. `bnk down` is interruptible and a
// drain that ignores cancellation would hold Ctrl-C for the full timeout.
func TestTheDrainStopsOnContextCancellation(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "stuck"))
	dc.PrependReactor("delete", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil // never actually removed
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan int, 1)
	go func() {
		_, remaining := drainBNKCustomResources(ctx, dc,
			[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk", time.Hour, time.Second, nil)
		done <- remaining
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the drain ignored a cancelled context and kept polling toward its 1h deadline")
	}
}

// hasVerb must match a verb exactly. Its predecessor was a substring check over
// the joined verb list, which accepts a resource that cannot be listed.
func TestHasVerbMatchesExactlyRatherThanBySubstring(t *testing.T) {
	if hasVerb([]string{"deletecollection"}, "delete") {
		t.Error(`"deletecollection" was accepted as "delete"; a substring match would ` +
			`select resources the sweep cannot actually delete one-by-one`)
	}
	if !hasVerb([]string{"get", "list", "delete"}, "delete") {
		t.Error("an exact verb was rejected")
	}
}

// The single-namespace topology (flo == utils) is a customer requirement, so the
// caller must not drain the same namespace twice.
func TestNamespacesAreDedupedForTheSingleNamespaceTopology(t *testing.T) {
	if got := dedupeNonEmpty("f5-bnk", "f5-bnk"); len(got) != 1 || got[0] != "f5-bnk" {
		t.Errorf("dedupeNonEmpty(same, same) = %v, want [f5-bnk]", got)
	}
	if got := dedupeNonEmpty("f5-bnk", "f5-utils"); len(got) != 2 {
		t.Errorf("dedupeNonEmpty(distinct) = %v, want both", got)
	}
	if got := dedupeNonEmpty("f5-bnk", ""); len(got) != 1 {
		t.Errorf("an empty namespace was kept: %v", got)
	}
}

// splitCNEInstance is what makes the ordering possible; a mistake here is
// invisible at the call site because both slices are still non-empty.
func TestSplitCNEInstancePutsOnlyTheCNEInstanceInTheRootPhase(t *testing.T) {
	leaves, roots := splitCNEInstance([]schema.GroupVersionResource{gvrIPAM, gvrCNEInstance, gvrIPAMRange})
	if len(roots) != 1 || roots[0].Resource != cneInstanceResource {
		t.Errorf("roots = %v, want exactly the CNEInstance", roots)
	}
	if len(leaves) != 2 {
		t.Errorf("leaves = %v, want the two IPAM resources", leaves)
	}
	for _, l := range leaves {
		if l.Resource == cneInstanceResource {
			t.Error("the CNEInstance leaked into the leaf phase, so it would be deleted first")
		}
	}
}

// #217 is a property of the CLUSTER, not of which process runs terraform, so the
// drain has to run on the containerised backend too — and BEFORE the destroy.
// That call site is one line and deleting it leaves the build and the suite
// green while `bnk down` goes back to stalling.
func TestRunTrialDownDrainsCustomResourcesOnEveryBackend(t *testing.T) {
	calls := callsInFunc(t, "lifecycle.go", "RunTrialDown")

	drains, dockers, destroys := 0, 0, 0
	firstDrain, firstDocker, firstDestroy := -1, -1, -1
	for i, c := range calls {
		switch c {
		case "sweepBNKCustomResources":
			drains++
			if firstDrain < 0 {
				firstDrain = i
			}
		case "runTerraformLifecycleDocker":
			dockers++
			if firstDocker < 0 {
				firstDocker = i
			}
		case "destroyWithRetry":
			destroys++
			if firstDestroy < 0 {
				firstDestroy = i
			}
		}
	}

	if drains < 2 {
		t.Fatalf("sweepBNKCustomResources is called %d time(s); both the local and the "+
			"containerised destroy path need it (#217). calls: %v", drains, calls)
	}
	if dockers > 0 && !(firstDrain < firstDocker) {
		t.Errorf("the containerised path dispatches at call %d but the first drain is at %d — "+
			"it must run BEFORE the destroy starts", firstDocker, firstDrain)
	}
	// The LAST drain, not the first: a drain that lands after the destroy has
	// started repairs nothing, and checking only the first would not see it.
	if destroys > 0 {
		ds := indicesOf(calls, "sweepBNKCustomResources")
		if last := ds[len(ds)-1]; last > firstDestroy {
			t.Errorf("destroyWithRetry is called at %d but a drain is at %d — "+
				"draining after the destroy has started repairs nothing", firstDestroy, last)
		}
	}
}

// The deletes go through f5validate-f5-bnk, and #208's sweep is what removes it.
// Reversing the two takes away the validating webhook and then asks the API
// server to call it.
//
// PAIRWISE, not first-occurrence. RunTrialDown calls both sweeps twice — once on
// the containerised path and once on the local one — so comparing the first of
// each only ever inspects the containerised branch, and a swap in the local path
// survives. That is not hypothetical: the first version of this test passed
// against exactly that mutation.
func TestTheCRDrainRunsBeforeTheWebhookSweep(t *testing.T) {
	calls := callsInFunc(t, "lifecycle.go", "RunTrialDown")
	drains := indicesOf(calls, "sweepBNKCustomResources")
	webhooks := indicesOf(calls, "sweepTeardownWebhooks")

	if len(drains) == 0 || len(webhooks) == 0 {
		t.Fatalf("expected both sweeps in RunTrialDown; drains=%v webhooks=%v", drains, webhooks)
	}
	if len(drains) != len(webhooks) {
		t.Fatalf("the sweeps are not paired: %d drain(s) at %v, %d webhook sweep(s) at %v.\n"+
			"Every destroy path needs both, so an unpaired call means one path is missing one.",
			len(drains), drains, len(webhooks), webhooks)
	}
	for i := range drains {
		if drains[i] > webhooks[i] {
			t.Errorf("on path %d the webhook sweep (call %d) runs before the CR drain (call %d).\n"+
				"The CR deletes are validated by f5validate-f5-bnk; removing it first means the "+
				"API server calls a webhook whose service is gone.", i+1, webhooks[i], drains[i])
		}
	}
}

// indicesOf returns every position of s in calls, in order.
func indicesOf(calls []string, s string) []int {
	var out []int
	for i, c := range calls {
		if c == s {
			out = append(out, i)
		}
	}
	return out
}

// callsInFunc returns every call in a function body, in source order.
//
// The AST is parsed rather than the text scanned because a comment naming the
// function would satisfy a grep and prove nothing.
func callsInFunc(t *testing.T, file, name string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}
	var fn *ast.FuncDecl
	ast.Inspect(f, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == name {
			fn = d
			return false
		}
		return true
	})
	if fn == nil {
		t.Fatalf("%s not found in %s", name, file)
	}
	var calls []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := c.Fun.(type) {
		case *ast.Ident:
			calls = append(calls, fun.Name)
		case *ast.SelectorExpr:
			calls = append(calls, fun.Sel.Name)
		}
		return true
	})
	return calls
}

// The drain is written against dynamic.Interface so the fake can stand in for a
// real API server; this pins that the seam stays an interface.
var _ dynamic.Interface = (*dynfake.FakeDynamicClient)(nil)

// ── selection: the blast radius ──────────────────────────────────────────────
//
// selectBNKResources decides what the drain deletes. Every other test here hands
// drainBNKCustomResources a hand-built GVR list, so without these the one part
// that can destroy something it was never meant to touch is the one part with no
// coverage.

func apiList(gv string, res ...metav1.APIResource) *metav1.APIResourceList {
	return &metav1.APIResourceList{GroupVersion: gv, APIResources: res}
}

func listable(name string) metav1.APIResource {
	return metav1.APIResource{Name: name, Namespaced: true, Verbs: []string{"get", "list", "delete"}}
}

// The whole point of the group filter: somebody else's CRs in the BNK namespace
// are not F5's to delete.
func TestSelectionNeverReachesOutsideTheF5APIGroups(t *testing.T) {
	got := selectBNKResources([]*metav1.APIResourceList{
		apiList("k8s.f5.com/v1", listable("cneinstances")),
		apiList("cert-manager.io/v1", listable("certificates"), listable("clusterissuers")),
		apiList("velero.io/v1", listable("backups")),
		apiList("apps/v1", listable("deployments")),
		apiList("k8s.f5net.com/v1", listable("f5spkvlans")),
	})
	for _, g := range got {
		switch g.Group {
		case "k8s.f5.com", "k8s.f5net.com":
		default:
			t.Errorf("selected %s/%s — outside BNKCRDGroups, so the drain would delete "+
				"a resource belonging to something else that happens to share the namespace", g.Group, g.Resource)
		}
	}
	if len(got) != 2 {
		t.Errorf("selected %d resource(s), want the 2 F5 ones: %v", len(got), got)
	}
}

// Every group in BNKCRDGroups must actually be selectable. A typo in that list
// is silent: the sweep just quietly skips a whole family of CRs.
func TestEveryDeclaredBNKGroupIsSelected(t *testing.T) {
	var lists []*metav1.APIResourceList
	for _, g := range BNKCRDGroups {
		lists = append(lists, apiList(g+"/v1", listable("things")))
	}
	got := selectBNKResources(lists)
	if len(got) != len(BNKCRDGroups) {
		t.Fatalf("selected %d of %d declared groups: %v", len(got), len(BNKCRDGroups), got)
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g.Group] = true
	}
	for _, want := range BNKCRDGroups {
		if !seen[want] {
			t.Errorf("group %q is in BNKCRDGroups but was never selected", want)
		}
	}
}

// A resource the drain cannot list is one it can never tell has drained; a
// resource it cannot delete is noise. Both must be skipped.
func TestSelectionSkipsResourcesItCannotListOrDelete(t *testing.T) {
	got := selectBNKResources([]*metav1.APIResourceList{
		apiList("k8s.f5.com/v1",
			metav1.APIResource{Name: "readonlys", Verbs: []string{"get", "list"}},
			metav1.APIResource{Name: "writeonlys", Verbs: []string{"create", "delete"}},
			metav1.APIResource{Name: "collectiononlys", Verbs: []string{"list", "deletecollection"}},
			listable("cneinstances"),
		),
	})
	if len(got) != 1 || got[0].Resource != "cneinstances" {
		t.Errorf("selected %v, want only cneinstances — the others lack list or delete", got)
	}
}

// Discovery is running while CRDs are being deleted, so malformed and nil
// entries are ordinary. Panicking there would break the teardown it is fixing.
func TestSelectionSurvivesMalformedDiscoveryOutput(t *testing.T) {
	got := selectBNKResources([]*metav1.APIResourceList{
		nil,
		{GroupVersion: "!!not a group version!!", APIResources: []metav1.APIResource{listable("x")}},
		apiList("k8s.f5.com/v1", listable("cneinstances")),
	})
	if len(got) != 1 || got[0].Resource != "cneinstances" {
		t.Errorf("got %v, want the one well-formed F5 resource", got)
	}
}

// ── counting: a failing API server is not an empty namespace ─────────────────

// If a list error counts as zero objects, an unreachable API server reads as a
// successful drain and the teardown announces "drained N" having verified
// nothing — the cries-wolf logging this change exists to remove, inverted.
func TestAFailingListIsNotCountedAsDrained(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))
	dc.PrependReactor("list", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("etcdserver: request timed out")
	})
	present, unknown := countIn(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk")
	if unknown == 0 {
		t.Errorf("a failing list produced present=%d unknown=%d; it must not read as drained", present, unknown)
	}
}

// The opposite error must NOT be treated as unknown. A kind whose CRD is already
// gone errors on list too, and waiting for it would add the full timeout to
// every teardown — trading a 5-minute stall for a 4-minute one.
func TestADeletedCRDIsNotWaitedFor(t *testing.T) {
	dc := newFake()
	dc.PrependReactor("list", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "fic.f5.com", Resource: "ipams"}, "")
	})
	present, unknown := countIn(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk")
	if present != 0 || unknown != 0 {
		t.Errorf("present=%d unknown=%d; a deleted CRD has nothing left to wait for", present, unknown)
	}

	start := time.Now()
	remaining := awaitGone(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk", time.Now().Add(2*time.Second), 50*time.Millisecond)
	if remaining != 0 {
		t.Errorf("remaining = %d for an absent CRD", remaining)
	}
	if time.Since(start) > time.Second {
		t.Errorf("waited %s for a CRD that no longer exists", time.Since(start))
	}
}
