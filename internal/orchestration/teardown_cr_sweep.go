package orchestration

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// THE OPERATOR MUST OUTLIVE THE OBJECTS IT HAS TO FINALIZE (#217).
//
// terraform destroys the CNEInstance and FLO as two independent graph nodes.
// alekc/kubectl's delete returns as soon as the API server accepts it, so the
// CNEInstance is merely *marked* for deletion; its controller-owned children
// still carry F5 finalizers. Three seconds later helm_release.flo is destroyed,
// and the only thing that could clear those finalizers is gone. The namespace
// delete that follows then blocks until the kubernetes provider's timeout:
//
//	kubectl_manifest.cneinstance[0]: Destruction complete after 0s   <- no wait
//	helm_release.flo[0]:             Destruction complete after 3s   <- finalizer gone
//	kubernetes_namespace_v1.flo[0]:  Still destroying... [4m0s elapsed]
//	Error: context deadline exceeded
//
// Observed on 3 of 3 teardowns — deterministic, not a race.
//
// freeStuckBNKNamespace already repairs this on the failure path, and its own
// comment says what it is: "a REPAIR, not a substitute for the ordering fix that
// would stop the CNEInstance being orphaned". This is that ordering fix. The
// repair stays as the safety net for the cases this cannot reach — a cluster
// whose API server is already unreachable, or finalizers owned by something that
// was broken before the teardown started.
//
// Doing it here rather than as a destroy-time provisioner in terraform is
// deliberate: `when = destroy` provisioners cannot reference variables, run
// outside the retry the CLI already owns, and are invisible to Go tests. The
// webhook sweep for #208 established this shape and this is the same problem
// one layer down.
const (
	// bnkCRDrainTimeout bounds the wait PER NAMESPACE. It is under the
	// kubernetes provider's own 5-minute namespace-delete timeout on purpose:
	// if this cannot drain the namespace, the destroy should still get its
	// full attempt rather than inheriting an already-spent budget.
	bnkCRDrainTimeout = 4 * time.Minute
	bnkCRDrainPoll    = 3 * time.Second

	// bnkCRRefusalGrace is how long every-delete-refused must PERSIST before the
	// drain gives up on it.
	//
	// #235 added the give-up with no grace at all, on the premise that a refusal
	// is permanent: the webhook sweep is best-effort, so if it did not remove the
	// webhook, nothing else will. #241 disproved the premise. FLO re-creates the
	// webhook about ten seconds into the drain and its endpoint is not ready for
	// another twenty-four, so there is a window in which every delete is refused
	// and the condition clears on its own. Giving up at zero seconds inside that
	// window is how the drain returns "nothing drained" while the cluster was
	// seconds away from accepting everything.
	//
	// 45s is comfortably longer than the observed window (~34s) and comfortably
	// shorter than the budget it protects: a genuinely permanent refusal still
	// costs 45s instead of 4m, keeping most of what #235 was worth. With the
	// webhook sweep now looping (#241), refusals clear within about 3s of the
	// webhook reappearing, so in a healthy teardown this grace is never spent.
	bnkCRRefusalGrace = 45 * time.Second
)

// cneInstanceResource is the plural resource name of the CNEInstance CR — the
// root object whose controller finalizes everything else.
const cneInstanceResource = "cneinstances"

// sweepBNKCustomResources deletes BNK's custom resources and waits for them to
// finish finalizing, while FLO is still running to do the finalizing (#217).
//
// Best-effort throughout, for the same reason the webhook sweep is: this exists
// to make a destroy succeed, so refusing to destroy because the preparation
// could not run would invert the point. Every failure path returns quietly and
// leaves terraform to do exactly what it does today.
//
// The kubeconfig comes from clusterKubeconfigBytes, NOT the ambient one. The
// forge kubeconfig is shared between workspaces AND between sessions, so an
// ambient client can point at a different cluster than the one being destroyed
// — and deleting a live cluster's CNEInstance is a far worse bug than the stall
// this fixes.
func sweepBNKCustomResources(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, w io.Writer) {
	if cctx == nil || cctx.Workspace == nil {
		return
	}
	floNS, utilsNS := cctx.Workspace.BNKNamespaces()
	if floNS == "" {
		return
	}
	kubeconfig, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		// A cluster already gone, or credentials that no longer resolve, are
		// both ordinary here — there is nothing left to finalize.
		return
	}
	dc, err := k8s.DynamicFromKubeconfigBytes(kubeconfig)
	if err != nil {
		return
	}
	gvrs := discoverBNKNamespacedGVRs(kubeconfig)
	if len(gvrs) == 0 {
		// No F5 CRDs resolved: either BNK is not installed or the CRDs are
		// already gone. Either way there is nothing to drain.
		return
	}

	// utilsNS may equal floNS — the single-namespace topology is a supported
	// deployment, so the list is de-duplicated rather than assumed distinct.
	for _, ns := range dedupeNonEmpty(floNS, utilsNS) {
		deleted, remaining := drainBNKCustomResources(ctx, dc, gvrs, ns, bnkCRDrainTimeout, bnkCRDrainPoll, bnkCRRefusalGrace, w)
		switch {
		case deleted == 0 && remaining == 0:
			// Nothing there. Silent: a namespace with no BNK CRs is the normal
			// state for a workspace that never installed, and saying so on every
			// teardown is noise.
		case remaining == 0:
			fmt.Fprintf(w, "  Drained %d BNK custom resource(s) from %s while FLO could still finalize them.\n", deleted, ns)
		default:
			fmt.Fprintf(w, "  ⚠ %d BNK custom resource(s) in %s did not finalize within %s.\n"+
				"    The destroy continues; a namespace left Terminating is repaired afterwards.\n",
				remaining, ns, bnkCRDrainTimeout)
		}
	}
}

// drainBNKCustomResources deletes the BNK CRs in one namespace and waits for
// them to actually go away, returning how many were deleted and how many were
// still present when the deadline expired.
//
// THE ORDER IS THE POINT, and it is the 2.4 guide's (see UninstallOrder): every
// other F5 CR first, confirmed gone, and only then the CNEInstance. The guide is
// explicit that IPAM and IPAMRange must be verified absent before the CNEInstance
// is removed, "to avoid any leftover state that might cause issues during product
// reinstallation" — they are controller-generated, so deleting the CNEInstance
// first takes away the very thing that would have cleaned them up. Deleting
// everything in one pass would reproduce that bug inside the fix for it.
//
// Takes a dynamic.Interface rather than a kubeconfig so the wait loop can be
// exercised against a fake client: the failure this fixes is entirely about
// ordering and waiting, which a test that never runs the loop cannot observe.
func drainBNKCustomResources(
	ctx context.Context,
	dc dynamic.Interface,
	gvrs []schema.GroupVersionResource,
	ns string,
	timeout, poll, refusalGrace time.Duration,
	w io.Writer,
) (deleted, remaining int) {
	// ONE deadline for both phases, not one each. The budget is a total: it
	// exists to stay under the kubernetes provider's own namespace-delete
	// timeout, so spending it twice would defeat the purpose. A leaf phase that
	// consumes almost all of it therefore leaves the CNEInstance phase very
	// little, and the CNEInstance is then reported as not finalized — which is
	// the honest answer, and the repair path handles it.
	deadline := time.Now().Add(timeout)

	leaves, roots := splitCNEInstance(gvrs)
	for _, phase := range [][]schema.GroupVersionResource{leaves, roots} {
		if len(phase) == 0 {
			continue
		}
		d, left := drainPhase(ctx, dc, phase, ns, deadline, poll, refusalGrace, w)
		deleted += d
		if left > 0 {
			// The leaf phase did not drain, so the CNEInstance is deliberately
			// NOT deleted: removing it now would orphan exactly the objects
			// still waiting, which is the bug being fixed. Leave them for the
			// repair path.
			return deleted, left
		}
	}
	return deleted, 0
}

// drainPhase deletes one phase's objects and waits for them to go, RE-ISSUING
// the deletes on every poll.
//
// THE RETRY IS THE FIX (#241). The previous version called deleteAllIn exactly
// once and then polled only for the objects to disappear. So a delete refused on
// that single pass was never attempted again: the drain would sit for its whole
// four-minute budget waiting for objects it had asked to delete once, at the one
// moment the API server happened to be refusing. That is precisely the window
// FLO opens when it re-creates the validating webhook ten seconds into the drain
// — the deletes are refused for about half a minute and then would have
// succeeded, but nothing asked again.
//
// Re-issuing is safe. deleteAllIn skips objects that already carry a
// deletionTimestamp, so an object accepted on pass one is not deleted twice; the
// retries only ever reach objects the API server has not accepted yet.
//
// GIVING UP IS STILL POSSIBLE, but on evidence rather than on one sample. The
// condition is: objects present, NONE of them marked for deletion, and every
// delete refused. That means no finalizer is running and nothing in the cluster
// is working toward this — #235's insight, and still correct. What #235 got wrong
// is that one such pass proves it, so it now has to hold for refusalGrace.
func drainPhase(
	ctx context.Context,
	dc dynamic.Interface,
	phase []schema.GroupVersionResource,
	ns string,
	deadline time.Time,
	poll, refusalGrace time.Duration,
	w io.Writer,
) (deleted, remaining int) {
	var refusingSince time.Time
	warned := false
	// Owned here, not package-level: a global would carry names between
	// namespaces and between tests, and would need a mutex it does not have.
	logged := map[string]bool{}

	for {
		p := deleteAllIn(ctx, dc, phase, ns, w, logged)
		deleted += p.accepted

		if p.present == 0 && p.unknown == 0 {
			return deleted, 0
		}

		// Nothing accepted, nothing already finalizing, and refusals: no part of
		// the cluster is making progress on this.
		if p.accepted == 0 && p.marked == 0 && p.refused > 0 && p.unknown == 0 {
			if refusingSince.IsZero() {
				refusingSince = time.Now()
			}
			if time.Since(refusingSince) >= refusalGrace {
				if w != nil {
					fmt.Fprintf(w, "  ⚠ %s: every delete was refused for %s, so waiting cannot help — skipping the rest of the %s budget.\n"+
						"    The reason is above, one line per object. A validating webhook whose backend is\n"+
						"    already gone is the usual cause; the destroy continues and the namespace is repaired after.\n",
						ns, refusalGrace, time.Until(deadline).Round(time.Second))
				}
				return deleted, p.present + p.unknown
			}
			if !warned && w != nil {
				warned = true
				fmt.Fprintf(w, "  every delete in %s is being refused; retrying for up to %s in case it is the webhook being re-created.\n", ns, refusalGrace)
			}
		} else {
			// Any progress at all resets the clock: a refusal that alternates
			// with acceptances is the controller cycling, not a dead end.
			refusingSince = time.Time{}
		}

		if !time.Now().Before(deadline) {
			present, unknown := countIn(ctx, dc, phase, ns)
			return deleted, present + unknown
		}

		select {
		case <-ctx.Done():
			present, unknown := countIn(ctx, dc, phase, ns)
			return deleted, present + unknown
		case <-time.After(poll):
		}
	}
}

// splitCNEInstance separates the CNEInstance resource from everything else.
func splitCNEInstance(gvrs []schema.GroupVersionResource) (leaves, roots []schema.GroupVersionResource) {
	for _, g := range gvrs {
		if g.Resource == cneInstanceResource {
			roots = append(roots, g)
			continue
		}
		leaves = append(leaves, g)
	}
	return leaves, roots
}

// deletePass is what one pass over a phase actually observed. The counts are
// separate because they mean different things to the caller: `accepted` and
// `marked` are both progress, `refused` is the API server saying no, and only
// "present, none marked, all refused" means nothing in the cluster is working
// toward the delete.
//
// #235 collapsed this into (accepted, present) and inferred a refusal from
// accepted==0. That is ambiguous — a phase where every object is already
// finalizing also has accepted==0 — and once the drain retries (#241) it is the
// NORMAL state of every pass after the first.
type deletePass struct {
	accepted int // delete calls the API server took this pass
	present  int // objects seen this pass
	refused  int // delete calls that came back with a real error
	marked   int // objects already carrying a deletionTimestamp
	unknown  int // kinds whose contents could not be established this pass
}

// deleteAllIn issues a delete for every object of every resource in one
// namespace that is not already being deleted, and reports what happened.
//
// It is called once per poll, not once per drain, so it must be idempotent:
// objects with a deletionTimestamp are skipped, which means a retry only ever
// reaches objects the API server has not accepted yet.
//
// DeleteCollection is not used: it is not implemented by every apiserver for
// every custom resource, and a single unsupported verb would silently skip a
// whole kind. Listing and deleting by name is slower and always works.
func deleteAllIn(ctx context.Context, dc dynamic.Interface, gvrs []schema.GroupVersionResource, ns string, w io.Writer, logged map[string]bool) deletePass {
	var p deletePass
	for _, gvr := range gvrs {
		list, err := dc.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			// A kind whose CRD is already gone lists as an error, and that IS
			// drained. Any other list failure — an etcd timeout, an apiserver
			// restart, an RBAC change mid-teardown — is not: the objects may
			// well still be there. Treating the two the same would report a
			// namespace drained whose contents nobody could see, which is the
			// one answer the destroy must not be given.
			if !typeIsGone(err) {
				p.unknown++
			}
			continue
		}
		if list == nil {
			p.unknown++
			continue
		}
		p.present += len(list.Items)
		for i := range list.Items {
			item := list.Items[i]
			if item.GetDeletionTimestamp() != nil {
				// Already marked: the API server took the delete on an earlier
				// pass and a finalizer is holding it. Counting it as accepted
				// again would inflate the total, but it IS progress, so it is
				// counted separately rather than not at all.
				p.marked++
				continue
			}
			err := dc.Resource(gvr).Namespace(ns).Delete(ctx, item.GetName(), metav1.DeleteOptions{})
			if err == nil {
				p.accepted++
				continue
			}
			if apierrors.IsNotFound(err) {
				continue
			}
			p.refused++
			// Logged on the FIRST pass only. The drain now retries every few
			// seconds for up to four minutes, and one line per object per pass
			// would bury the rest of the teardown output.
			key := gvr.Resource + "/" + item.GetName()
			if w != nil && !logged[key] {
				logged[key] = true
				fmt.Fprintf(w, "    could not delete %s/%s: %v\n", gvr.Resource, item.GetName(), err)
			}
		}
	}
	return p
}

// countIn totals the objects of the given resources present in one namespace,
// separating those it could count from those it could not.
//
// A LIST ERROR IS NOT ZERO OBJECTS. Counting every failure as "nothing there"
// meant an unreachable API server read as a successful drain, and the teardown
// then announced "drained N resources" having verified nothing — the same
// cries-wolf logging this change exists to remove, in the other direction.
//
// But fail-closed is wrong too: a kind whose CRD is already gone also errors on
// list, and waiting for it would add the full timeout to every teardown. So the
// two are told apart. NotFound and NoMatch mean the type itself is gone and
// there is nothing left to wait for; anything else is genuinely unknown and is
// reported as such rather than as drained.
func countIn(ctx context.Context, dc dynamic.Interface, gvrs []schema.GroupVersionResource, ns string) (present, unknown int) {
	for _, gvr := range gvrs {
		list, err := dc.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			if typeIsGone(err) {
				continue
			}
			unknown++
			continue
		}
		if list == nil {
			continue
		}
		present += len(list.Items)
	}
	return present, unknown
}

// typeIsGone reports whether an error means the RESOURCE TYPE no longer exists,
// as opposed to the request having failed.
//
// Both forms occur: the API server answers NotFound once a CRD is deleted, and
// the RESTMapper answers NoMatch once discovery has caught up. Either way there
// is nothing left to wait for.
func typeIsGone(err error) bool {
	return apierrors.IsNotFound(err) || meta.IsNoMatchError(err) ||
		runtime.IsNotRegisteredError(err)
}

// discoverBNKNamespacedGVRs resolves the namespaced, listable, deletable
// resources belonging to the BNK API groups.
//
// DISCOVERED, not listed — the same decision freeF5Finalizers records: a
// hardcoded list had three entries while the live 2.4 capture shows sixteen
// finalizer-bearing F5 CRs, so a list would sweep three and report success.
// Both callers share BNKCRDGroups, which is the part that would otherwise drift.
//
// The client call and the SELECTION are separated on purpose. Selection decides
// what this code deletes — the only part of the sweep that can destroy something
// it was never meant to touch — so it is a pure function with its own tests,
// while this wrapper is the untestable half that merely fetches.
func discoverBNKNamespacedGVRs(kubeconfig []byte) []schema.GroupVersionResource {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil
	}
	lists, err := disc.ServerPreferredNamespacedResources()
	// Returns whatever resolved on a PARTIAL discovery failure. CRDs are being
	// torn down while this runs, so a group that fails to resolve is expected;
	// giving up on all of them because one was mid-deletion would skip the sweep
	// exactly when it is needed.
	if lists == nil && err != nil {
		return nil
	}
	return selectBNKResources(lists)
}

// selectBNKResources picks the resources the drain is allowed to delete.
//
// THIS FUNCTION IS THE BLAST RADIUS. Everything else in this file operates on
// whatever it returns, so a wrong answer here means deleting custom resources
// that belong to something else that happens to live in the BNK namespace.
//
// Three conditions, all required:
//
//   - the API group is one of BNKCRDGroups — F5's own, nothing else;
//   - the resource can be listed, or the drain cannot tell when it is gone;
//   - the resource can be deleted, or selecting it only produces noise.
//
// Verbs are matched exactly. A substring check over the joined verb list accepts
// "delete" inside "deletecollection" and selects resources the drain cannot
// actually delete one at a time.
func selectBNKResources(lists []*metav1.APIResourceList) []schema.GroupVersionResource {
	groups := make(map[string]bool, len(BNKCRDGroups))
	for _, g := range BNKCRDGroups {
		groups[g] = true
	}

	var out []schema.GroupVersionResource
	for _, rl := range lists {
		if rl == nil {
			continue
		}
		gv, perr := schema.ParseGroupVersion(rl.GroupVersion)
		if perr != nil || !groups[gv.Group] {
			continue
		}
		for _, r := range rl.APIResources {
			if !hasVerb(r.Verbs, "list") || !hasVerb(r.Verbs, "delete") {
				continue
			}
			out = append(out, gv.WithResource(r.Name))
		}
	}
	// Stable order so the log reads the same way twice and tests do not depend
	// on discovery's map iteration.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Resource < out[j].Resource
	})
	return out
}

// hasVerb reports whether the verb list contains an exact match.
//
// The predecessor of this check was strings.Contains on the joined verb list,
// which matches "list" inside "deletecollection"'s neighbours and would accept a
// resource that cannot be listed at all.
func hasVerb(verbs []string, want string) bool {
	for _, v := range verbs {
		if v == want {
			return true
		}
	}
	return false
}

// dedupeNonEmpty returns the distinct non-empty values, in the order given.
//
// The single-namespace topology (flo == utils) is a customer requirement, not a
// degenerate case, so the caller cannot assume two namespaces.
func dedupeNonEmpty(vals ...string) []string {
	seen := make(map[string]bool, len(vals))
	var out []string
	for _, v := range vals {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
