package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/pkg/bnk"
)

const (
	// phase25InitialSleep is the initial wait before the first poll iteration.
	// Gives the CNEInstance controller time to start reconciling the License CR.
	phase25InitialSleep = 30 * time.Second

	// phase25MaxIter is the maximum number of poll iterations.
	phase25MaxIter = 18

	// phase25PollInterval is the sleep between iterations (18 × 30s = 9 min cap).
	phase25PollInterval = 30 * time.Second
)

// Phase25ActivationPoll polls the CNEInstance and License CRs until both reach
// their ready states (CNEInstance.status.state ∈ {Ready, Running} and
// License.status.state = Active). Reports pod counts each iteration.
//
// If skipPoll is true, the function returns nil immediately (for reviewer use
// to avoid burning a real license per round).
//
// On success: sets CNEINSTANCE_READY_AT in state.
// On timeout (9 min): dumps pod diagnostics to stderr then returns a hard error.
//
// D-005: CheckAuthOrDie called at entry.
func Phase25ActivationPoll(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool, skipPoll bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	crName := cl.Metadata.Name + "-bnk"
	fmt.Fprintf(os.Stderr, "[phase 25] activation poll: cluster=%s cr=%s\n", cl.Metadata.Name, crName)

	if skipPoll {
		fmt.Fprintln(os.Stderr, "[phase 25] skipping activation poll (--skip-activation-poll)")
		return nil
	}

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 25] dry-run: would poll CNEInstance + License up to 9 min")
		return nil
	}

	if clients.Dynamic == nil {
		return fmt.Errorf("phase25: Clients.Dynamic is nil — call clients.AttachK8s(kubeconfigPath) first")
	}
	if clients.K8s == nil {
		return fmt.Errorf("phase25: Clients.K8s is nil — call clients.AttachK8s(kubeconfigPath) first")
	}

	// Initial sleep: give FLO + License controller time to reconcile.
	fmt.Fprintf(os.Stderr, "[phase 25] initial sleep %s before first poll\n", phase25InitialSleep)
	select {
	case <-ctx.Done():
		return fmt.Errorf("phase25: context cancelled during initial sleep: %w", ctx.Err())
	case <-time.After(phase25InitialSleep):
	}

	var lastCNEState, lastLicState string
	var kicked bool

	for i := 1; i <= phase25MaxIter; i++ {
		if i > 1 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("phase25: context cancelled while polling: %w", ctx.Err())
			case <-time.After(phase25PollInterval):
			}
		}

		// Read CNEInstance status.state and derive functional readiness.
		// H1: we deliberately do NOT use the rollup `Available` condition (or
		// status.state == "Ready"/"Running" as the sole gate). Sydney aws-syd-test
		// runs in healthy production for 33+ days with Available=False because
		// f5-dssm-db-1 is Pending — the rollup aggregates DSSM HA replicas which
		// are NOT on the BNK data path. The right gate for "can traffic flow" is
		// the sub-conditions F5TmmAvailable + CNEControllerAvailable. See:
		//   docs/audits/2026-05-24-live-e2e-round-2-findings.md (H1)
		//   memory: project_sydney_reference_baseline
		// We still treat status.state ∈ {Ready,Running} as ready for
		// backward-compatibility with FLO versions that set the field.
		cneState := ""
		cneReady := false
		cneObj, cneErr := clients.Dynamic.Resource(cneinstanceGVR).Namespace(InstanceNamespace).Get(ctx, crName, metav1.GetOptions{})
		if cneErr == nil {
			cneState, _, _ = unstructured.NestedString(cneObj.Object, "status", "state")
			cneReady = isCNEReady(cneState) || cneFunctionallyReady(cneObj.Object)
		}
		lastCNEState = cneState

		// Read License status.state.
		licState := ""
		licObj, licErr := clients.Dynamic.Resource(licenseGVR).Namespace(OperatorNamespace).Get(ctx, licenseCRName, metav1.GetOptions{})
		if licErr == nil {
			licState, _, _ = unstructured.NestedString(licObj.Object, "status", "state")
		}
		lastLicState = licState

		// Read pod counts in f5-cne-system.
		running, pending, failed, total := podCounts(ctx, clients, InstanceNamespace)

		fmt.Fprintf(os.Stderr,
			"[phase 25] [%d/%d] cne=state=%q,ready=%v lic=%s pods running=%d pending=%d failed=%d total=%d\n",
			i, phase25MaxIter, cneState, cneReady, licState, running, pending, failed, total)

		// Success: CNEInstance is functionally ready AND License is Active.
		if cneReady && licState == "Active" {
			fmt.Fprintf(os.Stderr, "[phase 25] activation complete: cne=state=%q,ready=true lic=%s\n", cneState, licState)
			st.Set("CNEINSTANCE_READY_AT", time.Now().UTC().Format(time.RFC3339))
			return st.Save()
		}

		// After iter 6 (~3 min of polling), if all pods are Running but the
		// CNEInstance sub-conditions are still stale, kick the cne-controller
		// once via a harmless annotation patch. Same reconcile-lag pattern as
		// project_pool_member_sync_root_cause / project_cne_controller_available_lag.
		if !kicked && i >= 6 && running == total && total > 0 && !cneReady {
			kicked = true
			elapsed := phase25InitialSleep + time.Duration(i-1)*phase25PollInterval
			fmt.Fprintf(os.Stderr,
				"[phase 25] all pods Running but CNE Available=False after %s — kicking cne-controller via no-op annotation patch (see project_cne_controller_available_lag)\n",
				elapsed.Round(time.Second),
			)
			if err := bnk.ResyncCNEInstance(ctx, clients.Dynamic, InstanceNamespace, crName); err != nil {
				fmt.Fprintf(os.Stderr, "[phase 25] warn: cne-controller kick failed (continuing): %v\n", err)
			}
		}
	}

	dumpPodDiagnostics(ctx, clients, InstanceNamespace)
	return fmt.Errorf("phase25: timeout after %d iterations (9 min): last cne=%q lic=%q — see [phase 25] FAIL diag lines above for stuck pod state",
		phase25MaxIter, lastCNEState, lastLicState)
}

// isCNEReady returns true if state is "Ready" or "Running". Kept for
// backward-compatibility with FLO versions that set .status.state; in
// BNK 2.3.x the field is typically empty and readiness is derived from
// cneFunctionallyReady() instead.
func isCNEReady(state string) bool {
	return state == "Ready" || state == "Running"
}

// cneFunctionallyReady returns true when both F5TmmAvailable and
// CNEControllerAvailable sub-conditions are True. These are the conditions
// that actually matter for traffic to flow through BNK.
//
// We deliberately do NOT use the rollup `Available` condition: the
// aws-syd-test reference cluster runs in healthy production for 33+ days
// with Available=False because f5-dssm-db-1 is Pending (Insufficient cpu)
// — the rollup aggregates DSSM HA replicas which are not on the data path.
// See docs/audits/2026-05-24-live-e2e-round-2-findings.md (H1) and
// memory: project_sydney_reference_baseline.
func cneFunctionallyReady(obj map[string]interface{}) bool {
	conds, found, err := unstructured.NestedSlice(obj, "status", "conditions")
	if err != nil || !found {
		return false
	}
	tmmReady := false
	ctrlReady := false
	for _, c := range conds {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		t, _ := cm["type"].(string)
		s, _ := cm["status"].(string)
		switch t {
		case "F5TmmAvailable":
			tmmReady = (s == "True")
		case "CNEControllerAvailable":
			ctrlReady = (s == "True")
		}
	}
	return tmmReady && ctrlReady
}

// podCounts returns running/pending/failed/total pod counts in the given namespace.
func podCounts(ctx context.Context, clients *Clients, ns string) (running, pending, failed, total int) {
	pods, err := clients.K8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, 0, 0, 0
	}
	total = len(pods.Items)
	for _, p := range pods.Items {
		switch p.Status.Phase {
		case "Running":
			running++
		case "Pending":
			pending++
		case "Failed":
			failed++
		}
	}
	return
}

// dumpPodDiagnostics prints per-pod phase/reason and recent events for any
// non-Running pod in ns to stderr. Best-effort: errors are printed, not returned.
func dumpPodDiagnostics(ctx context.Context, clients *Clients, ns string) {
	pods, err := clients.K8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[phase 25] FAIL diag: %v\n", err)
		return
	}
	for _, pod := range pods.Items {
		reason := pod.Status.Reason
		if reason == "" && len(pod.Status.ContainerStatuses) > 0 {
			if w := pod.Status.ContainerStatuses[0].State.Waiting; w != nil {
				reason = w.Reason
			}
		}
		fmt.Fprintf(os.Stderr, "[phase 25] FAIL diag: pod=%s phase=%s reason=%s\n",
			pod.Name, pod.Status.Phase, reason)

		if pod.Status.Phase == "Running" {
			continue
		}
		evts, evtErr := clients.K8s.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
			FieldSelector: "involvedObject.name=" + pod.Name,
		})
		if evtErr != nil {
			fmt.Fprintf(os.Stderr, "[phase 25] FAIL diag: events for %s: %v\n", pod.Name, evtErr)
			continue
		}
		items := evts.Items
		if len(items) > 10 {
			items = items[len(items)-10:]
		}
		for _, ev := range items {
			fmt.Fprintf(os.Stderr, "[phase 25] FAIL diag:   event: %s %s\n", ev.Reason, ev.Message)
		}
	}
}

// Phase25ActivationPollDown is a no-op — polling has no resources to clean up.
func Phase25ActivationPollDown(_ context.Context, _ *intent.Cluster, _ *state.State, _ *Clients) error {
	return nil
}
