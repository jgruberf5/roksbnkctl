package orchestration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
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

	// Watching for the LOSS runs alongside the attempt to win. Deliberately a
	// second goroutine rather than work folded into the sweep tick: the sweep
	// must keep its 5s cadence to be useful at all, and the repair polls a
	// different object on a slower one (#96).
	repairDone := make(chan struct{})
	go func() {
		defer close(repairDone)
		watchAndRepairCRDInstaller(sctx, dc, floNamespaceOf(cctx), utilsNamespaceOf(cctx), 15*time.Second)
	}()

	return func() {
		cancel()
		<-done
		<-repairDone
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

// ── crd-installer repair (#96) ───────────────────────────────────────────────
//
// The sweep above is a RACE, and the happy path is probabilistic. The FLO
// crd-installer runs for about 1.3 seconds; the ingress operator recreates the
// admission policy roughly every minute; our delete lands every 5s. When the
// crd-installer's ~200ms CRD create falls inside a window where the policy is
// live, it is denied:
//
//	CRDInstallerAvailable=False  Gateway API CRD "backendtlspolicies…" creation
//	blocked by platform admission policy … requires Gateway API "1.4.1 standard"
//
// Losing is TERMINAL without this, because the crd-installer is a Job that runs
// ONCE and reports Complete 1/1 even when the CRD install failed. Nothing
// retries it, so `tfx wait` burns its full 15 minutes on CNEControllerAvailable
// and then reports the condition name rather than the cause.
//
// So: stop relying on winning the race, and repair the loss. This runs in the
// same goroutine window as the sweep, which means the repair lands WHILE tfx
// wait is still waiting — the apply then succeeds outright rather than failing
// and needing a re-run.
//
// The repair is the sequence the BNK 2.4 install guide prescribes for OCP >=
// 4.19, and which was confirmed by hand on a cluster in this state: clear the
// policy, drop the failed Job, restart FLO so the Job is recreated.
var (
	crdInstallerJobGVR = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
	deploymentGVR      = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	cneInstanceGVR     = schema.GroupVersionResource{Group: "k8s.f5.com", Version: "v1", Resource: "cneinstances"}
)

// crdInstallerBlocked reports whether the CNEInstance is stuck specifically on
// the gateway-api admission policy — not merely un-Available, which is the
// normal state for most of an install. Matching the MESSAGE is deliberate: a
// broad "CRDInstallerAvailable=False" would fire during ordinary startup and
// bounce FLO for no reason.
func crdInstallerBlocked(obj map[string]any) bool {
	conds, found, _ := unstructured.NestedSlice(obj, "status", "conditions")
	if !found {
		return false
	}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t != "CRDInstallerAvailable" {
			continue
		}
		if s, _ := m["status"].(string); s != "False" {
			return false
		}
		msg, _ := m["message"].(string)
		return strings.Contains(msg, "admission policy")
	}
	return false
}

// repairCRDInstaller clears the admission policy, deletes the failed Job and
// restarts FLO so the Job is recreated. Best-effort throughout: every step is
// reported, and a failure to do one does not stop the others — a partial repair
// still improves the odds, and the apply's own error remains the source of truth.
func repairCRDInstaller(ctx context.Context, dc dynamic.Interface, floNS, utilsNS string) {
	fmt.Fprintln(os.Stderr, "  ⚠ FLO crd-installer was blocked by the gateway-api admission policy — repairing")

	for _, gvr := range admissionSweepGVRs {
		_ = dc.Resource(gvr).Delete(ctx, admissionSweepName, metav1.DeleteOptions{})
	}
	if err := dc.Resource(crdInstallerJobGVR).Namespace(utilsNS).
		Delete(ctx, "crd-installer", metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		fmt.Fprintf(os.Stderr, "    · could not delete the failed crd-installer Job: %v\n", err)
	}

	// The same thing `kubectl rollout restart` does: stamp the pod template so
	// the Deployment rolls, which makes FLO recreate the crd-installer Job.
	//
	// A JSON merge patch, not the strategic merge `kubectl` uses. RFC 7386
	// merges nested objects recursively, so this adds the one annotation and
	// leaves the rest of the pod template alone — identical effect here, and
	// unlike strategic merge it needs no Go struct, so it works against
	// unstructured objects on both the real API and the fake client in tests.
	patch := fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"roksbnkctl/crd-installer-repair":%q}}}}}`,
		time.Now().UTC().Format(time.RFC3339))
	if _, err := dc.Resource(deploymentGVR).Namespace(floNS).Patch(
		ctx, "flo-f5-lifecycle-operator", types.MergePatchType, []byte(patch), metav1.PatchOptions{},
	); err != nil {
		fmt.Fprintf(os.Stderr, "    · could not restart flo-f5-lifecycle-operator: %v\n", err)
		fmt.Fprintln(os.Stderr, "      → repair it by hand: oc rollout restart deployment flo-f5-lifecycle-operator -n "+floNS)
		return
	}
	fmt.Fprintln(os.Stderr, "  → restarted flo-f5-lifecycle-operator; the crd-installer will run again")
}

// floNamespaceOf / utilsNamespaceOf resolve the install namespaces, defaulting
// the way the terraform module does. Both are configurable (bnk.flo_namespace /
// bnk.flo_utils_namespace) and may be the SAME namespace — a supported
// single-namespace install — so neither is assumed distinct.
func floNamespaceOf(cctx *config.Context) string {
	if cctx != nil && cctx.Workspace != nil {
		if ns := strings.TrimSpace(cctx.Workspace.BNK.FLONamespace); ns != "" {
			return ns
		}
	}
	return "f5-bnk"
}

func utilsNamespaceOf(cctx *config.Context) string {
	if cctx != nil && cctx.Workspace != nil {
		if ns := strings.TrimSpace(cctx.Workspace.BNK.FLOUtilsNamespace); ns != "" {
			return ns
		}
	}
	return "f5-utils"
}

// watchAndRepairCRDInstaller polls the CNEInstance and repairs the crd-installer
// the first time it reports the admission block.
//
// ONCE, not on every tick: the repair restarts FLO, and a loop that fires
// repeatedly would bounce the operator while it is trying to converge — turning
// a recoverable race into a crash loop of our own making. If one restart does
// not clear it, the cause is not the race and the apply's error should stand.
func watchAndRepairCRDInstaller(ctx context.Context, dc dynamic.Interface, floNS, utilsNS string, interval time.Duration) {
	name := floNS + "-f5-cne-controller"
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		u, err := dc.Resource(cneInstanceGVR).Namespace(floNS).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			continue // not created yet, or unreachable — nothing to judge
		}
		if crdInstallerBlocked(u.Object) {
			repairCRDInstaller(ctx, dc, floNS, utilsNS)
			return
		}
	}
}
