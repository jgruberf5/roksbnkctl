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
)

const (
	// phase25InitialSleep is the initial wait before the first poll iteration.
	// Gives the CNEInstance controller time to start reconciling the License CR.
	phase25InitialSleep = 30 * time.Second

	// phase25MaxIter is the maximum number of poll iterations.
	phase25MaxIter = 40

	// phase25PollInterval is the sleep between iterations (40 × 30s = 20 min cap).
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
// On timeout: returns a hard error with last-seen states and diagnostic hint.
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
		fmt.Fprintln(os.Stderr, "[phase 25] dry-run: would poll CNEInstance + License up to 20 min")
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

	for i := 1; i <= phase25MaxIter; i++ {
		if i > 1 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("phase25: context cancelled while polling: %w", ctx.Err())
			case <-time.After(phase25PollInterval):
			}
		}

		// Read CNEInstance status.state AND fall back to status.conditions[Available]==True.
		// BNK 2.3.0 leaves .status.state empty; readiness signal moved to the conditions
		// list (verified live 2026-05-23 on syd-tracer — Available=True with state="").
		cneState := ""
		cneObj, cneErr := clients.Dynamic.Resource(cneinstanceGVR).Namespace(InstanceNamespace).Get(ctx, crName, metav1.GetOptions{})
		if cneErr == nil {
			cneState, _, _ = unstructured.NestedString(cneObj.Object, "status", "state")
			if cneState == "" {
				// Fallback: derive readiness from conditions[].
				if cneAvailableFromConditions(cneObj.Object) {
					cneState = "Available"
				}
			}
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
			"[phase 25] [%d/%d] cne=%s lic=%s pods running=%d pending=%d failed=%d total=%d\n",
			i, phase25MaxIter, cneState, licState, running, pending, failed, total)

		// Success: CNEInstance is Ready/Running AND License is Active.
		if isCNEReady(cneState) && licState == "Active" {
			fmt.Fprintf(os.Stderr, "[phase 25] activation complete: cne=%s lic=%s\n", cneState, licState)
			st.Set("CNEINSTANCE_READY_AT", time.Now().UTC().Format(time.RFC3339))
			return st.Save()
		}
	}

	return fmt.Errorf("phase25: timeout after %d iterations (20 min): last cne=%q lic=%q — run `kubectl get cneinstance %s -n %s -o yaml` for diagnosis",
		phase25MaxIter, lastCNEState, lastLicState, crName, InstanceNamespace)
}

// isCNEReady returns true if state is "Ready", "Running", or "Available"
// (the latter is the BNK 2.3 fallback derived from .status.conditions).
func isCNEReady(state string) bool {
	return state == "Ready" || state == "Running" || state == "Available"
}

// cneAvailableFromConditions returns true if the CNEInstance object's
// .status.conditions list contains {type:"Available", status:"True"}.
// BNK 2.3.0 reports readiness via this condition rather than the older
// top-level .status.state field.
func cneAvailableFromConditions(obj map[string]interface{}) bool {
	conds, found, err := unstructured.NestedSlice(obj, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, c := range conds {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		t, _ := cm["type"].(string)
		s, _ := cm["status"].(string)
		if t == "Available" && s == "True" {
			return true
		}
	}
	return false
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

// Phase25ActivationPollDown is a no-op — polling has no resources to clean up.
func Phase25ActivationPollDown(_ context.Context, _ *intent.Cluster, _ *state.State, _ *Clients) error {
	return nil
}
