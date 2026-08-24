package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

// All three resource types are swept because the OpenShift ingress operator has
// used more than one to express the same block. 4.18 ships it as a
// ValidatingAdmissionPolicy + binding; the BNK 2.4 install guide instructs
// operators on OCP >= 4.19 to delete a ValidatingWebhookConfiguration of the
// same name. Which one exists is a function of the cluster's OCP version, not of
// anything we control, so sweeping all three is the only version-independent
// answer. Deleting a name that does not exist is a no-op, so the extra entry
// costs a 4.18 cluster nothing.
var admissionSweepGVRs = []schema.GroupVersionResource{
	{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicybindings"},
	{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicies"},
	{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"},
}

const admissionSweepName = "openshift-ingress-operator-gatewayapi-crd-admission"

// admissionSweepWouldDelete reports whether the sweep, as it is written, would
// delete obj.
//
// It exists because the Gateway API bundle (#185) ships an admission policy OF
// ITS OWN — "safe-upgrades.gateway.networking.k8s.io" — and that bundle is
// applied while this sweep is running. The two objects are different and must
// stay different: a sweep that widened to a prefix, a label selector or a
// delete-collection would remove what the bundle had just installed, and the
// failure would be silent. Nothing errors, nothing is denied; the policy is
// simply absent afterwards and the install looks as though the bundle never
// applied.
//
// Derived from admissionSweepGVRs and admissionSweepName rather than restating
// them, so it cannot answer for a sweep the loop no longer performs.
func admissionSweepWouldDelete(obj *unstructured.Unstructured) bool {
	if obj == nil || obj.GetName() != admissionSweepName {
		return false
	}
	group := obj.GroupVersionKind().Group
	for _, gvr := range admissionSweepGVRs {
		if gvr.Group == group {
			return true
		}
	}
	return false
}

// applyBNKWithAdmissionSweep runs the BNK-phase apply with the admission-policy
// sweep goroutine alive for its duration (started before, stopped after) —
// covering the crd-installer window. The sweep is best-effort; the apply's result
// is what's returned.
func applyBNKWithAdmissionSweep(ctx context.Context, cctx *config.Context, tfws *tf.Workspace, varFiles []string) error {
	return applyBNKInSweepWindow(
		admissionSweepNeeded(cctx),
		func() func() { return startAdmissionPolicySweep(ctx, cctx, tfws) },
		func() error { return applyGatewayAPIBundle(ctx, cctx, tfws, os.Stderr) },
		func() error { return applyWithRetry(ctx, tfws, varFiles) },
	)
}

// applyBNKInSweepWindow is the ORDER, separated from what each step does.
//
// The order is the whole point and none of it is unit-testable against a real
// cluster, so the three steps arrive as functions and this decides when each
// runs: start the sweep, install the Gateway API bundle, run the apply, stop the
// sweep. A test can then observe the sequence directly instead of a guard having
// to read the source and hope.
//
// Why this sequence and not another:
//
//   - The bundle goes on INSIDE the window. The OpenShift ingress operator's
//     ValidatingAdmissionPolicy blocks third-party writes to the Gateway API
//     CRDs and is recreated within about a minute of each delete, so the sweep
//     running is the only condition under which the apply survives.
//   - It goes on BEFORE terraform. The CRDs have to exist before the FLO
//     crd-installer's own window, which the terraform apply opens.
//   - A bundle failure stops the run. An mTLS install without its Gateway API
//     bundle does not fail loudly later; the CNE controller is simply configured
//     for a Gateway API the cluster does not carry.
func applyBNKInSweepWindow(sweepNeeded bool, startSweep func() func(), installBundle, apply func() error) error {
	if !sweepNeeded {
		// No sweep means no bundle either: the two are gated on the same
		// question, so a bundle installed here would go on with nothing holding
		// the admission policy open.
		return apply()
	}
	stop := startSweep()
	defer stop()
	if err := installBundle(); err != nil {
		return err
	}
	return apply()
}

// admissionSweepNeeded reports whether this install has to fight the OpenShift
// ingress-operator for the Gateway API CRDs (#170).
//
// 2.3 always does: its FLO crd-installer forces the CRDs and is blocked by the
// platform's ValidatingAdmissionPolicy without the sweep. The symptom is not a
// CRD error — it is CNEControllerAvailable never appearing, fifteen minutes
// later, which is why the sweep announces itself loudly when it cannot run.
//
// 2.4 usually does NOT. Its crd-installer logs a graceful skip and leaves the
// cluster on whatever bundle OpenShift ships, which is correct for a base
// install. Only mTLS needs Gateway API 1.5.0 standard, and installing that means
// deleting the policy and its binding — which the platform recreates quickly,
// the race the sweep wins.
//
// So the sweep is conditional on 2.4, not redundant. An earlier reading of the
// graceful-skip log concluded it could be removed; that would have handled the
// policy by giving up, and left an mTLS install without the bundle it requires.
func admissionSweepNeeded(cctx *config.Context) bool {
	if cctx == nil || cctx.Workspace == nil {
		return true // unknown workspace: keep today's behaviour
	}
	// An unreadable or unrecognised line keeps the 2.3 behaviour. Running the
	// sweep when it was not needed costs a background goroutine that finds
	// nothing; skipping it when it was needed costs a fifteen-minute timeout with
	// no useful error.
	if cctx.Workspace.BNKLineOrEmpty() != "2.4" {
		return true
	}
	// On 2.4 the sweep runs for exactly one reason: to clear the way for the
	// Gateway API bundle. Asking the same question the bundle asks — rather than
	// re-deriving it here — is what stops the two drifting into a build that
	// sweeps without installing anything, or installs the bundle into a window
	// nothing is holding open.
	return cctx.Workspace.GatewayAPIBundleNeeded()
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
	body, err := clusterKubeconfigBytes(ctx, cctx, tfws)
	if err != nil {
		return nil, err
	}
	dc, err := k8s.DynamicFromKubeconfigBytes(body)
	if err != nil {
		return nil, fmt.Errorf("building k8s client from kubeconfig: %w", err)
	}
	return dc, nil
}

// clusterKubeconfigBytes resolves the workspace's cluster identity and fetches
// its admin kubeconfig from the IBM Cloud container-service API — the same
// endpoint the terraform providers use. Shared by the admission-policy sweep
// and the air-gap registry-CA trust, both of which need a live client against
// the cluster during the BNK apply.
func clusterKubeconfigBytes(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) ([]byte, error) {
	if cctx == nil || cctx.Workspace == nil {
		return nil, fmt.Errorf("no workspace context")
	}
	cluster, byID := resolveClusterIdentity(ctx, cctx, tfws)
	if cluster == "" {
		return nil, fmt.Errorf("could not resolve the cluster identity (no cluster-outputs.json cluster_id and no roks_cluster_id output)")
	}
	if !byID {
		fmt.Fprintf(os.Stderr, "  ⚠ resolving cluster by NAME %q (no recorded cluster_id) — ambiguous if a duplicate-named cluster exists.\n", cluster)
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
	return body, nil
}
