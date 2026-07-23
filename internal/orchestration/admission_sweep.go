package orchestration

import (
	"context"
	"fmt"
	"os"
	"time"

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
// it to drain. A no-op stop when the cluster can't be reached.
func startAdmissionPolicySweep(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) func() {
	dc := admissionSweepClient(ctx, cctx, tfws)
	if dc == nil {
		fmt.Fprintln(os.Stderr, "  (gateway-api admission-policy sweep skipped: cluster not reachable yet — best-effort)")
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

// runAdmissionSweepLoop deletes the policy + binding (if present) every interval
// until ctx is cancelled. Deletes are best-effort (NotFound is the normal case).
func runAdmissionSweepLoop(ctx context.Context, dc dynamic.Interface, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		for _, gvr := range admissionSweepGVRs {
			_ = dc.Resource(gvr).Delete(ctx, admissionSweepName, metav1.DeleteOptions{})
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// admissionSweepClient fetches a fresh admin kubeconfig for the workspace's cluster
// and builds a dynamic client. Returns nil (skip the sweep) on any failure — the
// sweep is defensive, so a transient cluster-access issue must never fail the apply.
func admissionSweepClient(ctx context.Context, cctx *config.Context, tfws *tf.Workspace) dynamic.Interface {
	if cctx == nil || cctx.Workspace == nil {
		return nil
	}
	cluster := resolveClusterIdentity(ctx, cctx, tfws)
	if cluster == "" {
		return nil
	}
	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		return nil
	}
	ic, err := ibm.New(apiKey, cctx.Workspace.IBMCloud.Region)
	if err != nil {
		return nil
	}
	body, err := ic.FetchClusterConfig(ctx, cluster)
	if err != nil {
		return nil
	}
	dc, err := k8s.DynamicFromKubeconfigBytes(body)
	if err != nil {
		return nil
	}
	return dc
}
