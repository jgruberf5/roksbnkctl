package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

const (
	// cwcRestartThreshold is the restart count at or above which we force-delete
	// the cwc pod to break its DNS-warmup crash loop.
	// ADR D-011: hardcoded at 3 based on empirical syd-test-lab 2026-05-19 data.
	// Not a user-tunable knob — see .agent/DECISIONS.md D-011.
	cwcRestartThreshold = int32(3)

	// cwcMaxIter is the number of poll iterations (each 15 s apart) before giving up.
	cwcMaxIter = 12

	// cwcPollInterval is the sleep between cwc readiness polls.
	cwcPollInterval = 15 * time.Second

	// cwcPostDeleteWait is the additional sleep after force-deleting a pod.
	cwcPostDeleteWait = 20 * time.Second

	// cwcContainerName is the name of the f5-spk-cwc container within the cwc pod.
	cwcContainerName = "f5-spk-cwc"

	// cwcLabelSelector targets pods with app=cwc.
	cwcLabelSelector = "app=cwc"
)

// Phase24CWCHeal applies a heuristic DNS-warmup heal for the cwc pod in
// f5-cne-core: if the pod's restart count reaches cwcRestartThreshold (3),
// it is force-deleted to break the crash loop. Returns nil regardless of
// outcome — this is a best-effort recovery action, not a verification gate.
//
// No new state keys are written (this is a recovery action, not a checkpoint).
// D-005: CheckAuthOrDie called at entry.
// ADR D-011: restart threshold hardcoded at 3.
func Phase24CWCHeal(ctx context.Context, _ *intent.Cluster, _ *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintln(os.Stderr, "[phase 24] CWC DNS-warmup heal")

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 24] dry-run: would auto-bounce cwc on restart >= 3")
		return nil
	}

	if clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 24] warning: K8s client not available, skipping CWC heal")
		return nil
	}

	start := time.Now()
	for i := 0; i < cwcMaxIter; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				fmt.Fprintln(os.Stderr, "[phase 24] context cancelled, stopping CWC heal loop")
				return nil //nolint:nilerr // best-effort, no error propagation
			case <-time.After(cwcPollInterval):
			}
		}

		elapsed := int(time.Since(start).Seconds())

		// Read the first cwc pod in f5-cne-core.
		pods, err := clients.K8s.CoreV1().Pods(OperatorNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: cwcLabelSelector,
			Limit:         1,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[phase 24] warning: listing cwc pods: %v — retrying\n", err)
			continue
		}
		if len(pods.Items) == 0 {
			fmt.Fprintf(os.Stderr, "[phase 24] no cwc pods found in %s (t=%ds) — retrying\n",
				OperatorNamespace, elapsed)
			continue
		}

		pod := &pods.Items[0]
		ready, restartCount := cwcStatus(pod)

		if ready {
			fmt.Fprintf(os.Stderr, "[phase 24] cwc Ready (no heal needed, restartCount=%d)\n", restartCount)
			return nil
		}

		if restartCount >= cwcRestartThreshold {
			fmt.Fprintf(os.Stderr,
				"[phase 24] cwc not Ready: restartCount=%d >= threshold=%d — force-deleting pod %s\n",
				restartCount, cwcRestartThreshold, pod.Name)
			gracePeriod := int64(0)
			delErr := clients.K8s.CoreV1().Pods(OperatorNamespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriod,
			})
			if delErr != nil {
				fmt.Fprintf(os.Stderr, "[phase 24] warning: force-delete pod %s: %v\n", pod.Name, delErr)
			} else {
				fmt.Fprintf(os.Stderr, "[phase 24] force-deleted pod %s — sleeping %s\n",
					pod.Name, cwcPostDeleteWait)
			}
			select {
			case <-ctx.Done():
				return nil //nolint:nilerr
			case <-time.After(cwcPostDeleteWait):
			}
			continue
		}

		fmt.Fprintf(os.Stderr,
			"[phase 24] cwc not yet Ready (restartCount=%d, t=%ds) — continuing\n",
			restartCount, elapsed)
	}

	// Exhausted iterations — log and return nil (best-effort, not a gate).
	fmt.Fprintln(os.Stderr, "[phase 24] CWC heal iterations exhausted — proceeding (heuristic heal, not a gate)")
	return nil
}

// cwcStatus extracts ready and restartCount from the f5-spk-cwc container in
// the given pod. If the container is not found, returns false, 0.
func cwcStatus(pod *corev1.Pod) (ready bool, restartCount int32) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == cwcContainerName {
			return cs.Ready, cs.RestartCount
		}
	}
	return false, 0
}

// Phase24CWCHealDown is a no-op — CWC heal has no resources to clean up.
func Phase24CWCHealDown(_ context.Context, _ *intent.Cluster, _ *state.State, _ *Clients) error {
	return nil
}
