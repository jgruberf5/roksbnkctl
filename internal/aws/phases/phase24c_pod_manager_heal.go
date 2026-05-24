// Package phases — phase24c_pod_manager_heal.go
//
// Phase 24c: f5-tmm-pod-manager cold-start race heal.
//
// Targets Finding #4 from docs/audits/2026-05-24-live-e2e-round-2-findings.md.
// Memory anchor: project_pod_manager_image_regression.
//
// BNK 2.3 ships pod-manager v1.6.x as a sidecar inside f5-cne-controller.
// On a cold node, pod-manager calls the EKS API server via the ClusterIP
// (172.20.0.1) before kube-proxy has finished programming iptables rules,
// causing the container to enter CrashLoopBackOff.  The pod self-heals in
// ~5 min but Phase 25 times out at 9 min before CNEControllerAvailable
// propagates to True.
//
// Mitigation: detect the wedged sidecar and trigger a rollout-restart of the
// f5-cne-controller Deployment (annotation patch), which re-schedules the pod
// once kube-proxy is ready.  This is a best-effort recovery action — every
// code path returns nil and this phase is NOT a verification gate.
//
// No new state keys are written.
// D-005: CheckAuthOrDie called at entry.

package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

const (
	// h4MaxIter is the number of poll iterations before giving up.
	// At h4PollInterval=30s this gives ~5 minutes of watch time.
	h4MaxIter = 10

	// h4PollInterval is the sleep between pod-manager readiness polls.
	h4PollInterval = 30 * time.Second

	// h4PostRestartWait is the additional sleep after kicking the rollout,
	// allowing the new pod to begin scheduling before we re-poll.
	h4PostRestartWait = 30 * time.Second

	// h4RestartThreshold is the restart count at or above which the pod is
	// considered wedged even without an explicit CrashLoopBackOff waiting
	// reason.  Empirically v1.6.x hits this within 2 crash cycles.
	// ADR D-011 pattern: hardcoded, not user-tunable.
	h4RestartThreshold = int32(2)

	// h4ContainerName is the sidecar container we are watching.
	h4ContainerName = "f5-tmm-pod-manager"

	// h4DeploymentName is the Deployment that owns the pod-manager sidecar.
	h4DeploymentName = "f5-cne-controller"
)

// Phase24cPodManagerHeal polls the f5-cne-controller Deployment for a
// wedged f5-tmm-pod-manager sidecar (CrashLoopBackOff or restart count ≥ 2)
// and, if detected, triggers a rollout-restart via an annotation patch.
// Returns nil regardless of outcome — this is a best-effort recovery action,
// not a verification gate.
//
// No new state keys are written (this is a recovery action, not a checkpoint).
// D-005: CheckAuthOrDie called at entry.
// Finding #4 mitigation: pod-manager v1.6.x cold-start race vs kube-proxy.
func Phase24cPodManagerHeal(ctx context.Context, _ *intent.Cluster, _ *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintln(os.Stderr, "[phase 24c] f5-tmm-pod-manager cold-start race heal")

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 24c] dry-run: would rollout-restart f5-cne-controller deploy if f5-tmm-pod-manager container wedges")
		return nil
	}

	if clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 24c] warning: K8s client not available, skipping pod-manager heal")
		return nil
	}

	restarted := false
	start := time.Now()

	for i := 0; i < h4MaxIter; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				fmt.Fprintln(os.Stderr, "[phase 24c] context cancelled, stopping pod-manager heal loop")
				return nil //nolint:nilerr // best-effort, no error propagation
			case <-time.After(h4PollInterval):
			}
		}

		elapsed := int(time.Since(start).Seconds())

		// Fetch the f5-cne-controller Deployment.
		deploy, err := clients.K8s.AppsV1().Deployments(InstanceNamespace).Get(ctx, h4DeploymentName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				fmt.Fprintf(os.Stderr,
					"[phase 24c] deployment %s/%s not found yet (FLO may not have created it) — retrying\n",
					InstanceNamespace, h4DeploymentName)
				continue
			}
			fmt.Fprintf(os.Stderr, "[phase 24c] warning: getting deployment %s: %v — retrying\n", h4DeploymentName, err)
			continue
		}

		// Build pod label selector from the Deployment's spec selector.
		if deploy.Spec.Selector == nil {
			fmt.Fprintln(os.Stderr, "[phase 24c] warning: deployment has nil spec.selector — retrying")
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[phase 24c] warning: converting label selector: %v — retrying\n", err)
			continue
		}

		pods, err := clients.K8s.CoreV1().Pods(InstanceNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: sel.String(),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[phase 24c] warning: listing pods: %v — retrying\n", err)
			continue
		}
		if len(pods.Items) == 0 {
			fmt.Fprintf(os.Stderr, "[phase 24c] no pods found for %s (t=%ds) — retrying\n", h4DeploymentName, elapsed)
			continue
		}

		// Inspect each pod for the pod-manager container state.
		anyWedged := false
		var wedgedRestarts int32
		var wedgedReason string

		for i := range pods.Items {
			pod := &pods.Items[i]
			found, ready, restartCount, waitingReason := podManagerStatus(pod)
			if !found {
				continue
			}
			if ready {
				fmt.Fprintf(os.Stderr, "[phase 24c] f5-tmm-pod-manager Ready in pod %s (no heal needed)\n", pod.Name)
				return nil
			}
			// Pod is wedged if it has CrashLoopBackOff or has restarted enough times.
			if waitingReason == "CrashLoopBackOff" || restartCount >= h4RestartThreshold {
				anyWedged = true
				wedgedRestarts = restartCount
				wedgedReason = waitingReason
			}
		}

		if !restarted && anyWedged {
			// Trigger a rollout-restart via annotation patch on the Deployment template.
			patchBody := []byte(fmt.Sprintf(
				`{"spec":{"template":{"metadata":{"annotations":{"awsbnkctl.io/restartedAt":%q}}}}}`,
				time.Now().UTC().Format(time.RFC3339),
			))
			_, patchErr := clients.K8s.AppsV1().Deployments(InstanceNamespace).Patch(
				ctx,
				h4DeploymentName,
				types.StrategicMergePatchType,
				patchBody,
				metav1.PatchOptions{},
			)
			if patchErr != nil {
				fmt.Fprintf(os.Stderr, "[phase 24c] warning: patch deployment %s: %v — retrying\n", h4DeploymentName, patchErr)
				continue
			}
			fmt.Fprintf(os.Stderr,
				"[phase 24c] kicked f5-cne-controller rollout (pod-manager wedged: restartCount=%d reason=%q) — sleeping %s\n",
				wedgedRestarts, wedgedReason, h4PostRestartWait)
			restarted = true
			select {
			case <-ctx.Done():
				return nil //nolint:nilerr
			case <-time.After(h4PostRestartWait):
			}
			continue
		}

		// Pod-manager container found but not yet wedged enough; keep watching.
		// Also handles the case where restarted=true and we are waiting for the
		// new pod to become Ready.
		// Log the first pod's state as representative.
		var logRestarts int32
		var logReason string
		for i := range pods.Items {
			found, _, rc, wr := podManagerStatus(&pods.Items[i])
			if found {
				logRestarts = rc
				logReason = wr
				break
			}
		}
		fmt.Fprintf(os.Stderr,
			"[phase 24c] pod-manager not yet Ready (restartCount=%d reason=%q, t=%ds) — continuing\n",
			logRestarts, logReason, elapsed)
	}

	fmt.Fprintln(os.Stderr, "[phase 24c] heal iterations exhausted — proceeding (heuristic, not a gate)")
	return nil
}

// podManagerStatus extracts the readiness state of the f5-tmm-pod-manager
// container from the given pod's ContainerStatuses.
//
// Returns:
//
//	found        — true if the container was present in the status list
//	ready        — true if the container is Ready
//	restartCount — number of times the container has restarted
//	waitingReason — cs.State.Waiting.Reason if the container is waiting, else ""
func podManagerStatus(pod *corev1.Pod) (found bool, ready bool, restartCount int32, waitingReason string) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == h4ContainerName {
			reason := ""
			if cs.State.Waiting != nil {
				reason = cs.State.Waiting.Reason
			}
			return true, cs.Ready, cs.RestartCount, reason
		}
	}
	return false, false, 0, ""
}

// Phase24cPodManagerHealDown is a no-op — the pod-manager heal has no
// resources to clean up (the rollout-restart annotation is harmless).
func Phase24cPodManagerHealDown(_ context.Context, _ *intent.Cluster, _ *state.State, _ *Clients) error {
	return nil
}
