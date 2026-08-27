package orchestration

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
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
	deleted, remaining, _ := drainBNKCustomResources(
		context.Background(), dc, gvrs, "f5-bnk", 10*time.Second, time.Millisecond, time.Second, &buf, nil)

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
	_, remaining, _ := drainBNKCustomResources(
		context.Background(), dc, gvrs, "f5-bnk", 30*time.Millisecond, time.Millisecond, time.Second, &buf, nil)

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
	_, remaining, _ := drainBNKCustomResources(
		context.Background(), dc, []schema.GroupVersionResource{gvrIPAM},
		"f5-bnk", 5*time.Second, time.Millisecond, time.Second, &buf, nil)

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
	deleted, remaining, _ := drainBNKCustomResources(
		context.Background(), dc, []schema.GroupVersionResource{gvrCNEInstance, gvrIPAM},
		"f5-bnk", time.Second, time.Millisecond, time.Second, &buf, nil)
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
		[]schema.GroupVersionResource{gvrCNEInstance}, "f5-bnk", time.Second, time.Millisecond, time.Second, &buf, nil)

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
		_, remaining, _ := drainBNKCustomResources(ctx, dc,
			[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk", time.Hour, time.Second, time.Second, nil, nil)
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

// THE WEBHOOK SWEEP MUST COME FIRST (#235).
//
// #217 pinned the opposite order here, and the test made the defect durable.
// Its reasoning was that "the CR deletes are validated by f5validate-f5-bnk, so
// removing it first means the API server calls a webhook whose service is gone".
// That is backwards: removing the ValidatingWebhookConfiguration means the API
// server has NOTHING to call.
//
// The webhook is served BY the install being torn down -- f5-validation-svc
// selects app=f5-cne-controller -- with failurePolicy: Fail. Leaving it in place
// while the controller goes away makes the API server REFUSE every delete:
//
//	failed calling webhook "f5validate.f5net.com": no endpoints available
//	for service "f5-validation-svc"
//
// which timed the drain out at 4m per namespace, left the finalizers in place,
// and produced the exact namespace stall #217 existed to prevent.
//
// PAIRWISE, for the reason the original was: RunTrialDown calls both sweeps
// twice, so comparing the first of each only ever inspects the containerised
// branch and a swap in the local path survives.
func TestTheWebhookSweepRunsBeforeTheCRDrain(t *testing.T) {
	calls := callsInFunc(t, "lifecycle.go", "RunTrialDown")
	webhooks := indicesOf(calls, "startTeardownWebhookSweep")
	drains := indicesOf(calls, "sweepBNKCustomResources")

	if len(webhooks) == 0 || len(drains) == 0 {
		t.Fatalf("expected both sweeps in RunTrialDown; webhooks=%v drains=%v", webhooks, drains)
	}
	if len(webhooks) != len(drains) {
		t.Fatalf("the sweeps are not paired: %d webhook sweep(s) at %v, %d drain(s) at %v.\n"+
			"Every destroy path needs both, so an unpaired call means one path is missing one.",
			len(webhooks), webhooks, len(drains), drains)
	}
	for i := range webhooks {
		if webhooks[i] > drains[i] {
			t.Errorf("on path %d the CR drain (call %d) runs before the webhook sweep (call %d).\n"+
				"f5validate-f5-bnk is served by the CNE controller with failurePolicy: Fail, so "+
				"leaving it in place makes the API server refuse every delete once that controller "+
				"is gone — the drain then times out and the namespace stalls (#235).",
				i+1, drains[i], webhooks[i])
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
		[]schema.GroupVersionResource{gvrIPAM}, []schema.GroupVersionResource{gvrIPAM}, "f5-bnk", nil)
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
		[]schema.GroupVersionResource{gvrIPAM}, []schema.GroupVersionResource{gvrIPAM}, "f5-bnk", nil)
	if present != 0 || unknown != 0 {
		t.Errorf("present=%d unknown=%d; a deleted CRD has nothing left to wait for", present, unknown)
	}

	// Through the live path rather than a helper: awaitGone was retired when the
	// drain started re-issuing deletes (#241), and a test that keeps a retired
	// helper alive is testing code no teardown runs.
	start := time.Now()
	deleted, remaining, _ := drainPhase(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, []schema.GroupVersionResource{gvrIPAM}, "f5-bnk",
		time.Now().Add(2*time.Second), 50*time.Millisecond, time.Second, nil, nil)
	if deleted != 0 || remaining != 0 {
		t.Errorf("deleted=%d remaining=%d for an absent CRD", deleted, remaining)
	}
	if time.Since(start) > time.Second {
		t.Errorf("waited %s for a CRD that no longer exists", time.Since(start))
	}
}

// #235 review. The ordering fix removes the CAUSE of the 8-minute stall; this
// removes the ability to burn the budget at all.
//
// the webhook sweep is best-effort by design — it returns quietly on an
// unreachable cluster, credentials that no longer resolve, or a webhook it does
// not match. Any of those and the drain meets a live failurePolicy: Fail webhook
// whose backend is already gone, and the API server refuses EVERY delete.
// Polling the full budget then waits on a condition nothing will change.
func TestADrainWhereEveryDeleteIsRefusedDoesNotWait(t *testing.T) {
	dc := newFake(
		cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"),
		cr(gvrIPAMRange, "IPAMRange", "f5-bnk", "range-1"),
	)
	// What a failurePolicy: Fail webhook with no endpoints actually returns.
	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf(`Internal error occurred: failed calling webhook ` +
			`"f5validate.f5net.com": no endpoints available for service "f5-validation-svc"`)
	})

	var buf bytes.Buffer
	start := time.Now()
	deleted, remaining, _ := drainBNKCustomResources(
		context.Background(), dc, []schema.GroupVersionResource{gvrIPAM, gvrIPAMRange},
		"f5-bnk", 30*time.Second, 50*time.Millisecond, 200*time.Millisecond, &buf, nil)
	elapsed := time.Since(start)

	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 — every delete was refused", deleted)
	}
	if remaining != 2 {
		t.Errorf("remaining = %d, want 2 — both objects are still there", remaining)
	}
	// The point of the change: it must not spend the budget discovering this.
	if elapsed > 5*time.Second {
		t.Errorf("waited %s against a webhook refusing every delete.\n"+
			"Zero accepted with objects present cannot resolve by waiting — that is the "+
			"4m-per-namespace stall in #235, reachable whenever the webhook sweep does not "+
			"do its job.", elapsed)
	}
	if !strings.Contains(buf.String(), "every delete was refused") {
		t.Errorf("the operator is not told why it stopped early:\n%s", buf.String())
	}
}

// The fail-fast must not fire when deletes ARE being accepted but finalizers are
// still holding the objects — that is normal progress and must still be waited
// for, which is the whole reason the drain exists.
func TestASlowFinalizerIsStillWaitedFor(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))
	var mu sync.Mutex
	lists := 0
	dc.PrependReactor("delete", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil // accepted, but the object lingers
	})
	dc.PrependReactor("list", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		lists++
		gone := lists > 3
		mu.Unlock()
		l := &unstructured.UnstructuredList{}
		l.SetGroupVersionKind(gvrIPAM.GroupVersion().WithKind("IPAMList"))
		if !gone {
			l.Items = []unstructured.Unstructured{*cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1")}
		}
		return true, l, nil
	})

	var buf bytes.Buffer
	_, remaining, _ := drainBNKCustomResources(
		context.Background(), dc, []schema.GroupVersionResource{gvrIPAM},
		"f5-bnk", 5*time.Second, time.Millisecond, time.Second, &buf, nil)
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0 — the delete was accepted and the object did drain", remaining)
	}
	if strings.Contains(buf.String(), "every delete was refused") {
		t.Error("the fail-fast fired on a delete that WAS accepted — that is ordinary " +
			"finalizer progress and must still be waited for")
	}
}

// THE #241 CASE. The drain used to issue each delete exactly once and then poll
// only for the objects to disappear, so a delete refused on that single pass was
// never attempted again — the drain sat for its whole budget waiting on objects
// it had asked to remove once, at the one moment the API server was refusing.
//
// That moment is not hypothetical: FLO re-creates the validating webhook about
// ten seconds into the drain and its endpoint is not ready for another
// twenty-four, so refusals last roughly half a minute and then stop.
//
// The property is that the drain SURVIVES a refusal that clears, which only
// re-issuing the deletes can satisfy.
func TestARefusalThatClearsIsRetriedRatherThanWaitedOut(t *testing.T) {
	dc := newFake(
		cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"),
		cr(gvrIPAMRange, "IPAMRange", "f5-bnk", "range-1"),
	)

	var mu sync.Mutex
	refusing := true
	// Refuse everything for the first few passes, exactly as a re-created
	// failurePolicy: Fail webhook with no ready endpoint does, then stop.
	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		if refusing {
			return true, nil, fmt.Errorf(`Internal error occurred: failed calling webhook ` +
				`"f5validate.f5net.com": no endpoints available for service "f5-validation-svc"`)
		}
		return false, nil, nil // fall through to the tracker: the delete lands
	})

	go func() {
		time.Sleep(150 * time.Millisecond)
		mu.Lock()
		refusing = false
		mu.Unlock()
	}()

	var buf bytes.Buffer
	deleted, remaining, _ := drainBNKCustomResources(
		context.Background(), dc, []schema.GroupVersionResource{gvrIPAM, gvrIPAMRange},
		"f5-bnk", 10*time.Second, 20*time.Millisecond, 5*time.Second, &buf, nil)

	if remaining != 0 {
		t.Errorf("remaining = %d, want 0.\n"+
			"The refusal cleared after 150ms and the objects were deletable, but the drain "+
			"never asked again — that is #241: each delete is issued once and the wait only "+
			"polls for objects to disappear.\noutput:\n%s", remaining, buf.String())
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 — both objects should have been removed once the refusal lifted", deleted)
	}
}

// The retry must not fire deletes at objects the API server has already accepted.
// Re-deleting a finalizing object is at best noise in the audit log and at worst
// a second deletionTimestamp attempt every poll for four minutes.
func TestTheRetryDoesNotReDeleteObjectsAlreadyBeingFinalized(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))

	var mu sync.Mutex
	deletes := 0
	dc.PrependReactor("delete", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		deletes++
		mu.Unlock()
		// Accept, but leave the object in place with a deletionTimestamp — a
		// finalizer holding on, which is the normal case the drain waits for.
		obj := cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1")
		now := metav1.Now()
		obj.SetDeletionTimestamp(&now)
		obj.SetFinalizers([]string{"f5.com/cne"})
		_ = dc.Tracker().Update(gvrIPAM, obj, "f5-bnk")
		return true, nil, nil
	})

	var buf bytes.Buffer
	drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk",
		300*time.Millisecond, 20*time.Millisecond, 5*time.Second, &buf, nil)

	mu.Lock()
	n := deletes
	mu.Unlock()

	if n != 1 {
		t.Errorf("issued %d deletes for one object; want 1.\n"+
			"deleteAllIn must skip objects that already carry a deletionTimestamp, or every "+
			"poll re-deletes everything that is merely waiting on a finalizer.", n)
	}
}

// A refusal that never clears must still not consume the whole budget — #235's
// point, and it survives the retry. What changed is that it now takes evidence
// over time rather than a single pass.
func TestAPermanentRefusalStopsAfterTheGraceNotTheBudget(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))
	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf(`Internal error occurred: failed calling webhook ` +
			`"f5validate.f5net.com": no endpoints available for service "f5-validation-svc"`)
	})

	var buf bytes.Buffer
	start := time.Now()
	_, remaining, _ := drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk",
		30*time.Second, 20*time.Millisecond, 300*time.Millisecond, &buf, nil)
	elapsed := time.Since(start)

	if remaining != 1 {
		t.Errorf("remaining = %d, want 1", remaining)
	}
	if elapsed < 300*time.Millisecond {
		t.Errorf("gave up after %s, before the %s grace elapsed — that is the #235 behaviour "+
			"that made #241 unrecoverable: a refusal is not proven permanent by one pass", elapsed, 300*time.Millisecond)
	}
	if elapsed > 5*time.Second {
		t.Errorf("waited %s of a 30s budget on a refusal that never clears", elapsed)
	}
	if !strings.Contains(buf.String(), "every delete was refused") {
		t.Errorf("the operator is not told why it stopped early:\n%s", buf.String())
	}
}

// A finalizing object is PROGRESS, not a refusal, and the difference has to
// survive the retry loop.
//
// Once the drain re-issues deletes every poll, the second and later passes
// necessarily accept nothing: everything they would delete already carries a
// deletionTimestamp. If "nothing accepted" alone started the give-up clock, every
// ordinary teardown would abandon its own objects one grace period after asking
// for them — turning the fail-fast from a safety net into the bug.
//
// So the give-up condition needs all three: nothing accepted, NOTHING MARKED,
// and refusals. This pins the middle one, which no other test reaches.
func TestAnObjectWaitingOnAFinalizerDoesNotStartTheGiveUpClock(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))

	var mu sync.Mutex
	finalizedAt := time.Now().Add(400 * time.Millisecond)

	dc.PrependReactor("delete", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		obj := cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1")
		now := metav1.Now()
		obj.SetDeletionTimestamp(&now)
		obj.SetFinalizers([]string{"f5.com/cne"})
		mu.Lock()
		defer mu.Unlock()
		_ = dc.Tracker().Update(gvrIPAM, obj, "f5-bnk")
		return true, nil, nil
	})
	// The finalizer eventually lets go, well after the grace would have expired.
	dc.PrependReactor("list", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		l := &unstructured.UnstructuredList{}
		l.SetGroupVersionKind(gvrIPAM.GroupVersion().WithKind("IPAMList"))
		if time.Now().Before(finalizedAt) {
			obj := cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1")
			now := metav1.Now()
			obj.SetDeletionTimestamp(&now)
			obj.SetFinalizers([]string{"f5.com/cne"})
			l.Items = []unstructured.Unstructured{*obj}
		}
		return true, l, nil
	})

	var buf bytes.Buffer
	// Grace is much SHORTER than how long the finalizer takes. Correct code waits
	// anyway, because a marked object is progress; code that keys only on
	// "nothing accepted" gives up at 100ms.
	_, remaining, _ := drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk",
		5*time.Second, 20*time.Millisecond, 100*time.Millisecond, &buf, nil)

	if remaining != 0 {
		t.Errorf("remaining = %d, want 0.\n"+
			"The object was accepted on the first pass and was merely waiting on a finalizer. "+
			"Later passes accept nothing because there is nothing left to accept — that is "+
			"normal, not a refusal, and must not start the give-up clock.\noutput:\n%s",
			remaining, buf.String())
	}
	if strings.Contains(buf.String(), "every delete was refused") {
		t.Errorf("the give-up fired while a finalizer was running:\n%s", buf.String())
	}
}

// A kind the API server cannot list is NOT a drained kind. A transient etcd
// error, an RBAC change mid-teardown, an apiserver restart -- any of them makes
// the list fail while the objects are still there, and reporting "drained" then
// tells terraform to go ahead and delete a namespace whose contents are unknown.
//
// countIn has always separated this from a deleted CRD, and
// TestAFailingListIsNotCountedAsDrained pins it -- but only by calling countIn
// directly. Nothing asserted it through the drain, which is the only path a
// teardown actually takes.
func TestAnUnlistableKindIsNotReportedAsDrained(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))
	dc.PrependReactor("list", "ipams", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("etcdserver: request timed out")
	})

	var buf bytes.Buffer
	deleted, remaining, _ := drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk",
		300*time.Millisecond, 20*time.Millisecond, time.Second, &buf, nil)

	if deleted != 0 {
		t.Errorf("deleted = %d; nothing could even be listed", deleted)
	}
	if remaining == 0 {
		t.Error("the drain reported everything drained for a kind it could not list.\n" +
			"A failing list is not an empty list: the objects may well still be there, and " +
			"reporting 0 remaining tells the destroy to proceed against unknown contents.")
	}
}

// The give-up is evaluated before the deadline is, so on the final iteration the
// remaining budget is already negative. Offering to skip "-2s" of budget is a
// number that cannot be true, and a log that prints impossible numbers stops
// being read -- which matters here, because this message is the only thing
// telling an operator why the teardown stopped early.
func TestTheGiveUpMessageNeverReportsANegativeBudget(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))
	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf(`Internal error occurred: failed calling webhook ` +
			`"f5validate.f5net.com": no endpoints available for service "f5-validation-svc"`)
	})

	var buf bytes.Buffer
	// The poll has to be LARGE relative to the budget, which is the real shape:
	// production polls every 3s against a 4m budget, so the loop can overshoot the
	// deadline by up to a full poll. A short poll here would overshoot by
	// milliseconds, which .Round(time.Second) renders as "0s" -- the test would
	// pass with or without the clamp and prove nothing.
	drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk",
		100*time.Millisecond, time.Second, 50*time.Millisecond, &buf, nil)

	if strings.Contains(buf.String(), "the -") {
		t.Errorf("the give-up message reports a negative remaining budget:\n%s", buf.String())
	}
}

// THE LIVE FAILURE, reproduced (#241).
//
// #244 put the webhook sweep in a loop and #245 made the drain retry refused
// deletes. On a real BNK 2.4 teardown both worked exactly as designed and the
// drain STILL timed out with all seven CRs untouched -- deletionTimestamp=NO on
// every one after four minutes.
//
// The measurement that explains it: FLO restores f5validate-<ns> 375-815ms after
// it is removed, while the sweep ticks every 3s. So the webhook is present for
// about 85% of the teardown, 177 successful removals produced ZERO accepted
// deletes, and tuning the interval cannot fix it -- polling faster than a 400ms
// reconcile means hammering the apiserver for the length of a destroy to lose
// slightly less often.
//
// The two schedules are independent, and nothing makes them coincide. So the
// refusal itself has to be the trigger: the error IS proof the webhook is back,
// so remove it and retry that delete immediately.
//
// This models it faithfully: the webhook is ALWAYS back by the time any delete is
// attempted, so a fix that relies on catching a gap cannot pass.
func TestARefusalNeutralisesTheWebhookAndRetriesImmediately(t *testing.T) {
	dc := newFake(
		cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"),
		cr(gvrIPAMRange, "IPAMRange", "f5-bnk", "range-1"),
	)

	var mu sync.Mutex
	webhookPresent := true // FLO has always already put it back
	neutralised := 0

	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		defer mu.Unlock()
		if webhookPresent {
			return true, nil, fmt.Errorf(`Internal error occurred: failed calling webhook ` +
				`"f5validate.f5net.com": failed to call webhook: Post ` +
				`"https://f5-validation-svc.f5-bnk.svc:3340/f5-validator?timeout=10s": ` +
				`no endpoints available for service "f5-validation-svc"`)
		}
		return false, nil, nil // falls through to the tracker: the delete lands
	})

	neutralise := func(context.Context) bool {
		mu.Lock()
		defer mu.Unlock()
		neutralised++
		webhookPresent = false // removed -- and the retry happens before FLO can restore it
		return true
	}

	var buf bytes.Buffer
	deleted, remaining, _ := drainBNKCustomResources(
		context.Background(), dc, []schema.GroupVersionResource{gvrIPAM, gvrIPAMRange},
		"f5-bnk", 10*time.Second, 20*time.Millisecond, 5*time.Second, &buf, neutralise)

	if remaining != 0 {
		t.Errorf("remaining = %d, want 0.\n"+
			"Every delete met a live failurePolicy: Fail webhook, which is the state a real "+
			"teardown is in ~85%% of the time. Waiting for the background sweep to happen to "+
			"remove it at the right moment is the losing strategy this replaces.\noutput:\n%s",
			remaining, buf.String())
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}
	mu.Lock()
	n := neutralised
	mu.Unlock()
	if n == 0 {
		t.Error("the refusal never triggered a webhook removal — the drain waited instead of acting")
	}
}

// The neutraliser must fire only on a webhook refusal. An RBAC denial or an etcd
// timeout is not something removing a webhook fixes, and deleting cluster-scoped
// admission config in response to an unrelated error is a blast radius nobody
// asked for.
func TestAnUnrelatedErrorDoesNotTriggerAWebhookRemoval(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))
	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf(`ipams.fic.f5.com "ipam-1" is forbidden: ` +
			`User "system:serviceaccount:default:tf" cannot delete resource "ipams"`)
	})

	fired := 0
	neutralise := func(context.Context) bool { fired++; return true }

	var buf bytes.Buffer
	drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk",
		300*time.Millisecond, 20*time.Millisecond, 100*time.Millisecond, &buf, neutralise)

	if fired != 0 {
		t.Errorf("an RBAC denial triggered %d webhook removal(s); want 0 — removing admission "+
			"configuration cannot fix a permissions error, and doing it anyway is unasked-for "+
			"blast radius on a live cluster", fired)
	}
}

func TestWebhookRefusalRecognisesTheRealMessageAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"the real one, verbatim from a live teardown",
			fmt.Errorf(`Internal error occurred: failed calling webhook "f5validate.f5net.com": ` +
				`failed to call webhook: Post "https://f5-validation-svc.f5-bnk.svc:3340/f5-validator?timeout=10s": ` +
				`no endpoints available for service "f5-validation-svc"`), true},
		{"the #208 shape, service not found",
			fmt.Errorf(`Internal error occurred: failed calling webhook "f5validate.f5net.com": ` +
				`service "f5-validation-svc" not found`), true},
		{"RBAC", fmt.Errorf(`ipams.fic.f5.com "x" is forbidden: User "y" cannot delete`), false},
		{"etcd", fmt.Errorf("etcdserver: request timed out"), false},
		{"a service with no endpoints, but no webhook involved",
			fmt.Errorf(`no endpoints available for service "something-else"`), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := webhookRefusal(tc.err); got != tc.want {
				t.Errorf("webhookRefusal = %v, want %v", got, tc.want)
			}
		})
	}
}

// The retry must not be gated on the neutraliser having DELETED something.
//
// "The webhook is already gone" is the BEST case for retrying, not a reason to
// skip it -- and it happens routinely, because the background sweep is removing
// the same webhook on its own 3s tick. If the sweep removes it a few
// milliseconds before a delete that was already in flight, the delete comes back
// refused, the neutraliser finds nothing left to delete, and gating on that skips
// the retry at the exact moment it would have succeeded.
//
// Worse, the refusal then counts toward the give-up: accepted==0, marked==0,
// refused>0 starts the clock on a cluster where the webhook is absent and every
// delete would now be accepted.
//
// Asserted against deleteAllIn rather than the whole drain, because ONE CALL IS
// EXACTLY ONE PASS. Driving this through drainBNKCustomResources was the first
// attempt and it proved nothing: an ordinary later pass drained the object and
// the test passed either way. Shrinking the budget to force a single pass did not
// work either -- the fake client is fast enough that a 1ms deadline had not
// expired by the end of the first pass.
func TestTheRetryIsNotGatedOnTheNeutraliserFindingSomethingToDelete(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "ipam-1"))

	attempts := 0
	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		attempts++
		if attempts == 1 {
			return true, nil, fmt.Errorf(`Internal error occurred: failed calling webhook ` +
				`"f5validate.f5net.com": no endpoints available for service "f5-validation-svc"`)
		}
		return false, nil, nil // the webhook is gone; the retry lands
	})

	// The background sweep already removed it, so there is nothing left for the
	// neutraliser to delete -- which is what DeleteOrphanedAdmissionWebhooks
	// reports as zero removed.
	called := 0
	neutralise := func(context.Context) bool { called++; return false }

	gvrs := []schema.GroupVersionResource{gvrIPAM}
	p := deleteAllIn(context.Background(), dc, gvrs, gvrs, "f5-bnk", nil, map[string]bool{}, map[string]bool{}, neutralise)

	if called != 1 {
		t.Fatalf("neutralise called %d time(s); want 1 — the refusal should have triggered it", called)
	}
	if attempts != 2 {
		t.Errorf("the delete was attempted %d time(s); want 2.\n"+
			"A webhook refusal must be retried after attempting neutralisation, whether or not "+
			"the neutraliser had anything left to remove. The webhook already being gone is the "+
			"best case for retrying, not a reason not to.", attempts)
	}
	if p.accepted != 1 || p.refused != 0 {
		t.Errorf("accepted=%d refused=%d, want 1/0 — the retry succeeded, so the pass made progress "+
			"and must not count toward the give-up clock", p.accepted, p.refused)
	}
}

// THE REMAINING HALF OF #241, found by a live teardown after #244/#245/#261.
//
// Four of BNK 2.4's seven namespaced CRs are owned by the CNEInstance -- verified
// on a real 2.4.0-EA install:
//
//	CNEController  ownedBy CNEInstance/f5-bnk-f5-cne-controller
//	F5Tmm          ownedBy CNEInstance/f5-bnk-f5-cne-controller
//	Afm            ownedBy CNEInstance/f5-bnk-f5-cne-controller
//	Downloader     ownedBy CNEInstance/f5-bnk-f5-cne-controller
//
// The drain deleted them anyway, while the CNEInstance that declares them still
// existed. That fights the operator: the CNEInstance IS the statement that those
// objects should exist, so the controller reconciles them back or their uninstall
// finalizers wait for a teardown nobody has asked for. They never finalized inside
// the 4m budget, and the repair path cleared their finalizers by hand on every
// single teardown.
//
// None of the earlier fixes could have helped, and the reason is worth stating:
// the webhook only guards k8s.f5net.com, while all seven of these are k8s.f5.com.
// The webhook never touched them.
func TestOwnedChildrenAreLeftToTheirOwner(t *testing.T) {
	child := cr(gvrIPAM, "IPAM", "f5-bnk", "owned-child")
	child.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "k8s.f5.com/v1",
		Kind:       "CNEInstance",
		Name:       "f5-bnk-f5-cne-controller",
	}})
	dc := newFake(child, cr(gvrCNEInstance, "CNEInstance", "f5-bnk", "f5-bnk-f5-cne-controller"))

	var mu sync.Mutex
	var deletedNames []string
	dc.PrependReactor("delete", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		deletedNames = append(deletedNames, a.(k8stesting.DeleteAction).GetName())
		mu.Unlock()
		return false, nil, nil
	})

	var buf bytes.Buffer
	drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM, gvrCNEInstance}, "f5-bnk",
		300*time.Millisecond, 20*time.Millisecond, time.Second, &buf, nil)

	mu.Lock()
	got := append([]string(nil), deletedNames...)
	mu.Unlock()

	for _, n := range got {
		if n == "owned-child" {
			t.Errorf("the drain deleted owned-child, which is owned by a CNEInstance this same "+
				"drain removes.\nDeleting a child while its owner still exists fights the "+
				"controller: the owner is the declaration that the child should exist, so it is "+
				"reconciled back or its finalizer waits forever. Deletions attempted: %v", got)
		}
	}
}

// An object owned by something the drain is NOT deleting must still be deleted --
// otherwise the skip becomes a way to leave objects behind forever.
func TestAnObjectOwnedBySomethingElseIsStillDeleted(t *testing.T) {
	orphanish := cr(gvrIPAM, "IPAM", "f5-bnk", "owned-elsewhere")
	orphanish.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "something-we-do-not-touch",
	}})
	dc := newFake(orphanish)

	var mu sync.Mutex
	deletes := 0
	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		mu.Lock()
		deletes++
		mu.Unlock()
		return false, nil, nil
	})

	var buf bytes.Buffer
	drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk",
		300*time.Millisecond, 20*time.Millisecond, time.Second, &buf, nil)

	mu.Lock()
	n := deletes
	mu.Unlock()
	if n == 0 {
		t.Error("an object owned by a Deployment the drain never touches was skipped. " +
			"The skip must only apply when the OWNER is also being deleted, or objects are " +
			"left behind on the strength of an ownerReference nobody is acting on.")
	}
}

// An owned child must not count as PRESENT, or skipping it is worse than not
// skipping it.
//
// present drives "has this phase drained yet". If an owned child keeps the leaf
// phase non-empty, the wait always times out, the ROOT phase never runs, the
// CNEInstance is never deleted -- and so the children are never garbage-collected
// either. The first version of the skip did exactly that: it stopped deleting the
// children and then blocked forever waiting for them to disappear.
func TestAnOwnedChildDoesNotKeepThePhaseFromDraining(t *testing.T) {
	child := cr(gvrIPAM, "IPAM", "f5-bnk", "owned-child")
	child.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "k8s.f5.com/v1", Kind: "CNEInstance", Name: "root",
	}})
	dc := newFake(child)

	gvrs := []schema.GroupVersionResource{gvrIPAM, gvrCNEInstance}
	p := deleteAllIn(context.Background(), dc, []schema.GroupVersionResource{gvrIPAM},
		gvrs, "f5-bnk", nil, map[string]bool{}, map[string]bool{}, nil)

	if p.owned != 1 {
		t.Errorf("owned = %d, want 1", p.owned)
	}
	if p.present != 0 {
		t.Errorf("present = %d, want 0.\n"+
			"An owned child is not this drain's to drain — it goes when its owner goes. "+
			"Counting it keeps the phase permanently non-empty, so the wait times out and the "+
			"root phase never deletes the owner that would have collected it.", p.present)
	}
}

// A CR the product's own webhook forbids deleting must not block the phase either.
//
//	admission webhook "f5validate.f5net.com" denied the request:
//	Default TCP parameters CR cannot be deleted!
//
// f5-big-tcp-settings/sys-default-tcp answers this every time. Retrying cannot
// change it, and removing the webhook to force it through would be deleting an
// object BNK states must not be deleted. It goes when the namespace goes.
func TestADeliberateWebhookDenialDoesNotBlockTheDrain(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "sys-default-tcp"))
	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf(`admission webhook "f5validate.f5net.com" denied the request: ` +
			`Default TCP parameters CR cannot be deleted!`)
	})

	var buf bytes.Buffer
	gvrs := []schema.GroupVersionResource{gvrIPAM}
	p := deleteAllIn(context.Background(), dc, gvrs, gvrs, "f5-bnk", &buf, map[string]bool{}, map[string]bool{}, nil)

	if p.denied != 1 {
		t.Errorf("denied = %d, want 1", p.denied)
	}
	if p.present != 0 {
		t.Errorf("present = %d, want 0 — a CR the product forbids deleting cannot be drained by "+
			"anyone, so counting it keeps the phase from ever completing", p.present)
	}
	if p.refused != 0 {
		t.Errorf("refused = %d, want 0 — a deliberate denial is not a refusal to retry past, and "+
			"must not start the give-up clock", p.refused)
	}
	if !strings.Contains(buf.String(), "cannot be deleted") {
		t.Errorf("the operator is not told why it was left behind:\n%s", buf.String())
	}
}

func TestWebhookDenialIsNotConfusedWithAnUncallableWebhook(t *testing.T) {
	denial := fmt.Errorf(`admission webhook "f5validate.f5net.com" denied the request: Default TCP parameters CR cannot be deleted!`)
	uncallable := fmt.Errorf(`Internal error occurred: failed calling webhook "f5validate.f5net.com": no endpoints available for service "f5-validation-svc"`)

	if !webhookDenial(denial) || webhookDenial(uncallable) {
		t.Errorf("webhookDenial: denial=%v uncallable=%v; want true/false",
			webhookDenial(denial), webhookDenial(uncallable))
	}
	// The two need OPPOSITE handling, so neither predicate may claim both.
	if webhookRefusal(denial) || !webhookRefusal(uncallable) {
		t.Errorf("webhookRefusal: denial=%v uncallable=%v; want false/true",
			webhookRefusal(denial), webhookRefusal(uncallable))
	}
}

// The remaining count must apply the SAME exclusions the delete pass does.
//
// deleteAllIn stopped counting owned children and product-forbidden CRs as
// present, but countIn -- which produces the number the operator is shown -- still
// counted everything. So a teardown that had correctly left four children to their
// owner reported "7 BNK custom resource(s) did not finalize", and a number that
// overstates the problem is how a healthy teardown gets investigated as a broken
// one.
func TestTheRemainingCountExcludesWhatTheDrainDeliberatelyLeft(t *testing.T) {
	child := cr(gvrIPAM, "IPAM", "f5-bnk", "owned-child")
	child.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "k8s.f5.com/v1", Kind: "CNEInstance", Name: "root",
	}})
	dc := newFake(
		child,
		cr(gvrIPAM, "IPAM", "f5-bnk", "sys-default-tcp"),
		cr(gvrIPAM, "IPAM", "f5-bnk", "genuinely-stuck"),
	)

	all := []schema.GroupVersionResource{gvrIPAM, gvrCNEInstance}
	phase := []schema.GroupVersionResource{gvrIPAM}
	denied := map[string]bool{"ipams/sys-default-tcp": true}

	present, unknown := countIn(context.Background(), dc, phase, all, "f5-bnk", denied)

	if unknown != 0 {
		t.Errorf("unknown = %d, want 0", unknown)
	}
	if present != 1 {
		t.Errorf("present = %d, want 1 — only genuinely-stuck actually failed to drain.\n"+
			"owned-child goes with its owner and sys-default-tcp goes with the namespace; "+
			"neither is something that failed to finalize.", present)
	}
}

// THE ACTUAL CAUSE of the 4m drain timeout on every teardown (#241).
//
// Measured on a live 2.4.0-EA install: delete f5-dssm by hand while the
// CNEInstance is present and FLO has it back in under five seconds. So a drain
// that deletes FLO's components cannot ever finish -- it deletes, FLO restores,
// it counts them present again, and the phase times out having achieved nothing.
//
// OWNERSHIP DOES NOT DETECT THIS. dssms and cnemanifests carry NO ownerReference,
// while cnecontrollers/f5tmms/afms/downloaders do -- and FLO recreates all six
// identically. A skip keyed on ownerReferences alone leaves those two fighting the
// operator forever, which is what the first version of this did.
//
// The rule is the group: k8s.f5.com holds the CNEInstance and the components it
// declares. Deleting the CNEInstance IS the uninstall; the rest go with it.
func TestFLOManagedComponentsAreNeverDeletedDirectly(t *testing.T) {
	g := func(res string) schema.GroupVersionResource {
		return schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: res}
	}
	all := []schema.GroupVersionResource{
		g("cneinstances"), g("cnecontrollers"), g("f5tmms"), g("dssms"),
		g("afms"), g("downloaders"), g("cnemanifests"),
		gvrIPAM, // fic.f5.com — a real leaf the guide DOES name
	}

	leaves, roots := splitCNEInstance(all)

	if len(roots) != 1 || roots[0].Resource != "cneinstances" {
		t.Fatalf("roots = %v, want exactly cneinstances", roots)
	}
	for _, l := range leaves {
		if l.Group == "k8s.f5.com" {
			t.Errorf("%s/%s is in the leaf phase.\n"+
				"Every k8s.f5.com resource except the CNEInstance is a component FLO recreates "+
				"for as long as the CNEInstance exists, so deleting it can never make progress.",
				l.Group, l.Resource)
		}
	}
	// The genuine leaf must survive: the skip must not swallow the objects the
	// guide explicitly requires removing before the CNEInstance.
	found := false
	for _, l := range leaves {
		if l.Resource == "ipams" {
			found = true
		}
	}
	if !found {
		t.Error("ipams was dropped from the leaf phase — IPAM/IPAMRange are exactly what the 2.4 " +
			"guide requires be confirmed gone BEFORE the CNEInstance, so they must still be drained")
	}
}

// dssms and cnemanifests are the two that ownership cannot catch. Pinned by name,
// because they are the reason the group rule exists rather than an owner check.
func TestTheUnownedFLOComponentsAreStillSkipped(t *testing.T) {
	for _, res := range []string{"dssms", "cnemanifests"} {
		gvr := schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: res}
		if !floManagedComponent(gvr) {
			t.Errorf("%s is not treated as FLO-managed. It carries no ownerReference, so the "+
				"ownership skip misses it, and FLO recreates it within seconds of deletion.", res)
		}
	}
	// And the CNEInstance itself must NOT be skipped — deleting it is the uninstall.
	root := schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: "cneinstances"}
	if floManagedComponent(root) {
		t.Error("the CNEInstance was treated as a managed component; deleting it IS the uninstall")
	}
}

// A count with no names is not actionable.
//
// "1 BNK custom resource(s) did not finalize" does not say whether the survivor is
// the CNEInstance -- whose finalizer legitimately tears down the whole product and
// may simply need longer than the budget -- or a leaf that is genuinely stuck.
// Those need opposite responses, and the nameless message cost a diagnostic cycle:
// I was reduced to inferring which object it was from the shape of the teardown.
func TestTheDrainNamesWhatDidNotFinalize(t *testing.T) {
	dc := newFake(cr(gvrIPAM, "IPAM", "f5-bnk", "stuck-one"))
	dc.PrependReactor("delete", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil // accepted, but the object lingers
	})

	var buf bytes.Buffer
	_, remaining, stuck := drainBNKCustomResources(context.Background(), dc,
		[]schema.GroupVersionResource{gvrIPAM}, "f5-bnk",
		200*time.Millisecond, 20*time.Millisecond, time.Second, &buf, nil)

	if remaining != 1 {
		t.Fatalf("remaining = %d, want 1", remaining)
	}
	if len(stuck) != 1 || stuck[0] != "ipams/stuck-one" {
		t.Errorf("stuck = %v, want [ipams/stuck-one] — the operator needs to know WHICH object, "+
			"because a lingering CNEInstance and a lingering leaf mean different things", stuck)
	}
}
