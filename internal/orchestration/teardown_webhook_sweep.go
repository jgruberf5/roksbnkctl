package orchestration

import (
	"context"
	"fmt"
	"io"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// sweepTeardownWebhooks removes the admission webhook served from the BNK
// namespace BEFORE terraform destroys that namespace (#208).
//
// BNK installs f5validate-f5-bnk pointing at f5-validation-svc INSIDE the BNK
// namespace. Destroying the namespace deletes the service first, and every
// further attempt to remove the namespace's content then calls a webhook that
// cannot answer:
//
//	Failed to delete all resource types, 2 remaining: Internal error occurred:
//	failed calling webhook "f5validate.f5net.com": ... service
//	"f5-validation-svc" not found
//
// The namespace then sits in Terminating indefinitely. Nothing retries its way
// out, because the service is never coming back — the only exit is deleting the
// configuration by hand, which is what this saves the operator from.
//
// The kubeconfig comes from clusterKubeconfigBytes, NOT the ambient one. The
// forge kubeconfig is shared between workspaces, so an ambient client can point
// at a different cluster than the one being destroyed, and deleting admission
// webhooks on the wrong cluster is a considerably worse bug than the one being
// fixed.
//
// Best-effort throughout. This exists to make a destroy succeed; refusing to
// destroy because the cleanup could not run would invert the point.
func sweepTeardownWebhooks(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, w io.Writer) {
	if cctx == nil || cctx.Workspace == nil {
		return
	}
	floNS, _ := cctx.Workspace.BNKNamespaces()
	if floNS == "" {
		return
	}
	kubeconfig, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		// A cluster already gone, or credentials that no longer resolve, are
		// both ordinary here — there is nothing left to deadlock.
		return
	}
	cl, err := k8s.NewFromKubeconfigBytes(kubeconfig)
	if err != nil {
		return
	}
	deleted, err := k8s.DeleteOrphanedAdmissionWebhooks(ctx, cl.Clientset(), floNS, w)
	if err != nil {
		fmt.Fprintf(w, "  ⚠ could not remove the admission webhook served from %s (%v).\n"+
			"    If the namespace hangs in Terminating, delete its ValidatingWebhookConfiguration by hand.\n", floNS, err)
		return
	}
	if len(deleted) > 0 {
		fmt.Fprintf(w, "  Removed %d admission webhook(s) served from %s so its deletion can complete.\n", len(deleted), floNS)
	}
}
