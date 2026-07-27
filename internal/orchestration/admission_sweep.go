package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// The one imperative case from the Windows-native tfx migration (see the PRD
// docs/prd/native-windows-tfx.md §"The one imperative case"): the OpenShift ingress
// operator recreates its gateway-api ValidatingAdmissionPolicy + binding within
// ~1m, and the FLO crd-installer must see them GONE during its window (~1-3m into
// the CNEInstance reconcile). The terraform module did this with a detached
// `nohup` bash loop — which doesn't port to Windows.
//
// Instead, roksbnkctl (the parent process already shelling to `terraform apply` for
// the BNK phase) runs the sweep as a GOROUTINE for the duration of the apply:
// identical on Windows/Linux (no detached process, no SysProcAttr), reusing the Go
// k8s delete. Best-effort — if the cluster can't be reached the sweep is skipped
// (the module's loop was best-effort too).

var admissionSweepGVRs = []schema.GroupVersionResource{
	{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicybindings"},
	{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicies"},
}

const admissionSweepName = "openshift-ingress-operator-gatewayapi-crd-admission"

// applyBNKWithAdmissionSweep runs the BNK-phase apply with the admission-policy
// sweep goroutine alive for its duration (started before, stopped after) —
// covering the crd-installer window. The sweep is best-effort; the apply's result
// is what's returned.
func applyBNKWithAdmissionSweep(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, varFiles []string) error {
	stop := startAdmissionPolicySweep(ctx, cctx, tfws)
	defer stop()
	return applyWithRetry(ctx, tfws, varFiles)
}

// startAdmissionPolicySweep resolves a dynamic client (fresh admin kubeconfig) and
// launches the sweep goroutine, returning a stop func that cancels it and waits for
// it to drain. A no-op stop when the client can't be built — but LOUDLY, because a
// silent skip here is exactly how a misdirected sweep hid a CNEInstance timeout: the
// FLO crd-installer stayed blocked by the gateway-api admission policy and the only
// visible symptom, 15m later, was `CNEControllerAvailable` never appearing.
func startAdmissionPolicySweep(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) func() {
	dc, err := admissionSweepClient(ctx, cctx, tfws)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ gateway-api admission-policy sweep NOT running: %v\n", err)
		fmt.Fprintln(os.Stderr, "    → the FLO crd-installer may be blocked by the OpenShift ingress-operator gateway-api")
		fmt.Fprintln(os.Stderr, "      admission policy; if the CNEInstance later times out on CNEControllerAvailable, this is why.")
		return func() {}
	}
	fmt.Fprintln(os.Stderr, "→ gateway-api admission-policy sweep running for the duration of the apply (crd-installer window)")
	sctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAdmissionSweepLoop(sctx, dc, 5*time.Second)
	}()
	return func() {
		cancel()
		<-done
	}
}

// runAdmissionSweepLoop deletes the policy + binding (if present) every interval until
// ctx is cancelled. A NotFound is the normal, healthy case (the binding is already
// gone); any OTHER error means the deletes are not landing (wrong/dead cluster, RBAC)
// — logged once per resource so a broken sweep is visible instead of silent. On stop
// it reports how many deletes actually succeeded: zero is the red flag that the sweep
// swept nothing (e.g. an unreachable cluster), the precise failure this replaces.
func runAdmissionSweepLoop(ctx context.Context, dc dynamic.Interface, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	deletes := 0
	loggedErr := map[string]bool{}
	for {
		for _, gvr := range admissionSweepGVRs {
			err := dc.Resource(gvr).Delete(ctx, admissionSweepName, metav1.DeleteOptions{})
			switch {
			case err == nil:
				deletes++
			case apierrors.IsNotFound(err):
				// healthy — nothing to delete this tick
			case !loggedErr[gvr.Resource]:
				loggedErr[gvr.Resource] = true
				fmt.Fprintf(os.Stderr, "  ⚠ admission-policy sweep: deleting %s/%s is failing (will keep retrying): %v\n", gvr.Resource, admissionSweepName, err)
			}
		}
		select {
		case <-ctx.Done():
			if deletes == 0 {
				fmt.Fprintln(os.Stderr, "  ⚠ gateway-api admission-policy sweep landed ZERO deletes — it swept nothing reachable; the FLO crd-installer was likely NOT unblocked.")
			} else {
				fmt.Fprintf(os.Stderr, "→ gateway-api admission-policy sweep stopped (%d deletes landed over the apply)\n", deletes)
			}
			return
		case <-tick.C:
		}
	}
}

// admissionSweepClient fetches a fresh admin kubeconfig for the workspace's cluster
// and builds a dynamic client. Returns a descriptive error (never fails the apply —
// the caller logs and continues) so a skipped sweep is explained, not silent. Resolves
// the cluster by ID wherever possible: a bare cluster NAME is not unique in an IBM
// account, so a duplicate-named (or orphaned) cluster can otherwise misdirect every
// delete to the wrong endpoint — which is exactly how the sweep landed zero deletes.
func admissionSweepClient(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) (dynamic.Interface, error) {
	if cctx == nil || cctx.Workspace == nil {
		return nil, fmt.Errorf("no workspace context")
	}
	cluster, byID := resolveClusterIdentity(ctx, cctx, tfws)
	if cluster == "" {
		return nil, fmt.Errorf("could not resolve the cluster identity (no cluster-outputs.json cluster_id and no roks_cluster_id output)")
	}
	if !byID {
		fmt.Fprintf(os.Stderr, "  ⚠ admission-policy sweep resolving cluster by NAME %q (no recorded cluster_id) — ambiguous if a duplicate-named cluster exists.\n", cluster)
	}
	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving IBM Cloud API key: %w", err)
	}
	ic, err := ibm.New(apiKey, cctx.Workspace.IBMCloud.Region)
	if err != nil {
		return nil, fmt.Errorf("building IBM client: %w", err)
	}
	body, err := ic.FetchClusterConfig(ctx, cluster)
	if err != nil {
		return nil, fmt.Errorf("fetching admin kubeconfig for cluster %q: %w", cluster, err)
	}
	dc, err := k8s.DynamicFromKubeconfigBytes(body)
	if err != nil {
		return nil, fmt.Errorf("building k8s client from kubeconfig: %w", err)
	}
	return dc, nil
}
