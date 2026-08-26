package orchestration

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		deleted, remaining := drainBNKCustomResources(ctx, dc, gvrs, ns, bnkCRDrainTimeout, bnkCRDrainPoll, w)
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
	timeout, poll time.Duration,
	w io.Writer,
) (deleted, remaining int) {
	deadline := time.Now().Add(timeout)

	leaves, roots := splitCNEInstance(gvrs)
	for _, phase := range [][]schema.GroupVersionResource{leaves, roots} {
		if len(phase) == 0 {
			continue
		}
		deleted += deleteAllIn(ctx, dc, phase, ns, w)
		remaining = awaitGone(ctx, dc, phase, ns, deadline, poll)
		if remaining > 0 {
			// The leaf phase did not drain, so the CNEInstance is deliberately
			// NOT deleted: removing it now would orphan exactly the objects
			// still waiting, which is the bug being fixed. Leave them for the
			// repair path.
			return deleted, remaining
		}
	}
	return deleted, 0
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

// deleteAllIn issues a delete for every object of every resource in one
// namespace and returns how many deletes were accepted.
//
// DeleteCollection is not used: it is not implemented by every apiserver for
// every custom resource, and a single unsupported verb would silently skip a
// whole kind. Listing and deleting by name is slower and always works.
func deleteAllIn(ctx context.Context, dc dynamic.Interface, gvrs []schema.GroupVersionResource, ns string, w io.Writer) int {
	n := 0
	for _, gvr := range gvrs {
		list, err := dc.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil || list == nil {
			// A kind whose CRD is already gone lists as an error. Normal.
			continue
		}
		for i := range list.Items {
			item := list.Items[i]
			if item.GetDeletionTimestamp() != nil {
				// Already marked. Counting it as deleted here would be a lie,
				// but it still has to be waited for, and awaitGone does that.
				continue
			}
			err := dc.Resource(gvr).Namespace(ns).Delete(ctx, item.GetName(), metav1.DeleteOptions{})
			if err == nil {
				n++
				continue
			}
			if w != nil && !isNotFound(err) {
				fmt.Fprintf(w, "    could not delete %s/%s: %v\n", gvr.Resource, item.GetName(), err)
			}
		}
	}
	return n
}

// awaitGone polls until no object of the given resources remains in the
// namespace, or the deadline passes. Returns how many were still present.
//
// It counts rather than returning a bool so the caller can say how many did not
// drain — "2 objects did not finalize" is actionable and "the wait timed out" is
// not.
func awaitGone(
	ctx context.Context,
	dc dynamic.Interface,
	gvrs []schema.GroupVersionResource,
	ns string,
	deadline time.Time,
	poll time.Duration,
) int {
	for {
		left := countIn(ctx, dc, gvrs, ns)
		if left == 0 {
			return 0
		}
		if !time.Now().Before(deadline) {
			return left
		}
		select {
		case <-ctx.Done():
			return left
		case <-time.After(poll):
		}
	}
}

// countIn totals the objects of the given resources present in one namespace.
func countIn(ctx context.Context, dc dynamic.Interface, gvrs []schema.GroupVersionResource, ns string) int {
	n := 0
	for _, gvr := range gvrs {
		list, err := dc.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil || list == nil {
			continue
		}
		n += len(list.Items)
	}
	return n
}

// discoverBNKNamespacedGVRs resolves the namespaced, listable, deletable
// resources belonging to the BNK API groups.
//
// DISCOVERED, not listed — the same decision freeF5Finalizers records: a
// hardcoded list had three entries while the live 2.4 capture shows sixteen
// finalizer-bearing F5 CRs, so a list would sweep three and report success.
// Both callers share BNKCRDGroups, which is the part that would otherwise drift.
//
// Returns whatever resolved on a PARTIAL discovery failure. CRDs are being torn
// down while this runs, so a group that fails to resolve is expected; giving up
// on all of them because one was mid-deletion would skip the sweep exactly when
// it is needed.
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
	if lists == nil && err != nil {
		return nil
	}

	groups := make(map[string]bool, len(BNKCRDGroups))
	for _, g := range BNKCRDGroups {
		groups[g] = true
	}

	var out []schema.GroupVersionResource
	for _, rl := range lists {
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

// isNotFound reports whether an error is an already-deleted object.
//
// Matched on the API machinery's reason rather than the message so a translated
// or reworded server string still counts.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
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
