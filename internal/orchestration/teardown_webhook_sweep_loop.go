package orchestration

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
	"k8s.io/client-go/kubernetes"
)

// teardownWebhookSweepInterval is how often the sweep re-checks for a
// re-created webhook. FLO put one back 10s after the one-shot sweep in #241 and
// its endpoint was not ready for a further 24s, so the whole window in which
// deletes are refused is tens of seconds. Three seconds keeps that window to a
// few percent of itself without meaningfully loading the API server: one LIST of
// a cluster-scoped collection that is normally a handful of objects.
const teardownWebhookSweepInterval = 3 * time.Second

// startTeardownWebhookSweep removes the admission webhook served from the BNK
// namespace and KEEPS it removed for as long as the returned stop func is
// uncalled (#208, #235, #241).
//
// WHAT IT IS FOR (#208). BNK installs f5validate-<ns> pointing at
// f5-validation-svc INSIDE the BNK namespace. Destroying the namespace deletes
// that service first, and every further attempt to remove the namespace's
// content then calls a webhook that cannot answer:
//
//	Failed to delete all resource types, 2 remaining: Internal error occurred:
//	failed calling webhook "f5validate.f5net.com": ... service
//	"f5-validation-svc" not found
//
// The namespace then sits in Terminating indefinitely. Nothing retries its way
// out, because the service is never coming back — the only exit is deleting the
// configuration by hand, which is what this saves the operator from.
//
// WHY A LOOP AND NOT A SINGLE SWEEP. #208 added the sweep and #235 moved it
// before the drain, and both treated the webhook as something you remove once.
// It is not. f5validate-<ns> is owned by the f5-cne-controller, which is owned by
// the F5 Lifecycle Operator, and FLO is still running during the drain -- that is
// the whole point of #217's ordering, because FLO is what finalizes the custom
// resources. So the drain's first deletes make FLO reconcile, FLO re-creates the
// controller Deployment, and the new controller stands the webhook back up:
//
//	05:46:34  sweep removes f5validate-f5-bnk
//	05:46:44  FLO re-creates f5-cne-controller; the new controller re-creates the
//	          ValidatingWebhookConfiguration with failurePolicy: Fail
//	05:47:08  its endpoint finally becomes ready
//
// Between 05:46:44 and 05:47:08 the API server has a webhook it must call and no
// endpoint to call it on, so every DELETE of a k8s.f5.com object is refused with
// "no endpoints available for service f5-validation-svc" -- which is the exact
// error #208 and #235 each set out to eliminate, arriving 10 seconds after the
// fix for it ran.
//
// The one-shot sweep was not wrong about WHAT to remove. It was wrong that
// removing it once is enough while the thing that creates it is deliberately
// still alive.
//
// This is the same shape as startAdmissionPolicySweep, which holds the OpenShift
// gateway-api admission policy out of the way for the duration of an apply for
// the same reason: an operator is reconciling it back.
//
// THE FIRST SWEEP IS SYNCHRONOUS, before this returns. Callers rely on the
// webhook being gone before the drain issues its first delete, and a goroutine
// that has not been scheduled yet provides no such guarantee.
//
// Best-effort throughout, like the sweep it replaces: this exists to make a
// destroy succeed, so refusing to destroy because the cleanup could not start
// would invert the point.
func startTeardownWebhookSweep(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, w io.Writer) func() {
	if cctx == nil || cctx.Workspace == nil {
		return func() {}
	}
	floNS, _ := cctx.Workspace.BNKNamespaces()
	if floNS == "" {
		return func() {}
	}

	// Resolved ONCE. clusterKubeconfigBytes is a network round-trip against IBM
	// Cloud, and doing it every 3s for the length of a destroy would be both slow
	// and a good way to get rate-limited.
	//
	// The kubeconfig comes from clusterKubeconfigBytes, NOT the ambient one. The
	// forge kubeconfig is shared between workspaces AND sessions, so an ambient
	// client can point at a different cluster than the one being destroyed, and
	// deleting admission webhooks on the wrong cluster is a considerably worse
	// bug than the one being fixed.
	kubeconfig, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		// A cluster already gone, or credentials that no longer resolve, are both
		// ordinary here — there is nothing left to deadlock.
		return func() {}
	}
	cl, err := k8s.NewFromKubeconfigBytes(kubeconfig)
	if err != nil {
		return func() {}
	}

	return runTeardownWebhookSweep(ctx, cl.Clientset(), floNS, teardownWebhookSweepInterval, w)
}

// runTeardownWebhookSweep does the first sweep synchronously, then keeps
// sweeping every interval until the returned stop func is called. Split from
// startTeardownWebhookSweep so the loop can be exercised against a fake
// clientset: the failure this fixes is entirely about what happens on the
// SECOND and later passes, which a test that only calls the sweep once cannot
// observe.
func runTeardownWebhookSweep(
	ctx context.Context,
	cs kubernetes.Interface,
	floNS string,
	interval time.Duration,
	w io.Writer,
) func() {
	// Errors are reported ONCE, not once per tick. At 3s intervals across a
	// destroy that can run ten minutes, an unreachable API server would otherwise
	// print the same warning two hundred times and bury the rest of the teardown.
	var errLogged bool

	total := sweepOnce(ctx, cs, floNS, w, &errLogged)

	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-tick.C:
				total += sweepOnce(sctx, cs, floNS, w, &errLogged)
			}
		}
	}()

	return func() {
		cancel()
		<-done
		// More than one removal is the #241 signature: the webhook was put back
		// while the teardown was running. Saying so turns an invisible race into
		// a line in the log, so the next person to see a refused delete can tell
		// whether the sweep was outrun or never ran at all.
		if total > 1 && w != nil {
			fmt.Fprintf(w, "  The admission webhook served from %s was re-created %d time(s) during teardown and removed each time.\n", floNS, total-1)
		}
	}
}

// sweepOnce removes any validating webhook served from floNS, returning how many
// it removed. Errors are never returned: the loop must keep running through a
// transient API error, because the condition it is holding open lasts as long as
// the teardown does. errLogged makes the report once rather than once per tick.
func sweepOnce(ctx context.Context, cs kubernetes.Interface, floNS string, w io.Writer, errLogged *bool) int {
	deleted, err := k8s.DeleteOrphanedAdmissionWebhooks(ctx, cs, floNS, nil)
	if err != nil {
		if ctx.Err() == nil && w != nil && !*errLogged {
			*errLogged = true
			fmt.Fprintf(w, "  ⚠ could not remove the admission webhook served from %s (%v).\n"+
				"    If the namespace hangs in Terminating, delete its ValidatingWebhookConfiguration by hand.\n", floNS, err)
		}
		return len(deleted)
	}
	if len(deleted) > 0 && w != nil {
		fmt.Fprintf(w, "  Removed %d admission webhook(s) served from %s so its deletion can complete.\n", len(deleted), floNS)
	}
	return len(deleted)
}

// webhookNeutraliser returns the callback the CR drain uses to clear a webhook
// that has just refused a delete (#241).
//
// It is built from the SAME kubeconfig bytes the drain resolved, not a fresh
// lookup: the forge kubeconfig is shared between workspaces and sessions, and
// deleting admission webhooks on the wrong cluster is a considerably worse bug
// than the stall being fixed. Reusing the bytes the caller already proved point
// at the cluster being destroyed removes the chance of the two disagreeing.
//
// Returns nil when a client cannot be built, which deleteAllIn treats as "nothing
// to try" -- the drain then behaves exactly as it did before.
func webhookNeutraliser(kubeconfig []byte, floNS string) neutraliseFunc {
	if floNS == "" {
		return nil
	}
	cl, err := k8s.NewFromKubeconfigBytes(kubeconfig)
	if err != nil {
		return nil
	}
	return func(ctx context.Context) bool {
		deleted, err := k8s.DeleteOrphanedAdmissionWebhooks(ctx, cl.Clientset(), floNS, nil)
		return err == nil && len(deleted) > 0
	}
}
