package phases

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

const (
	// dssmConfigMapName is the FLO-created ConfigMap that holds the readiness probe script.
	dssmConfigMapName = "f5-dssm"

	// dssmLabelSelector targets the real dssm pods for the post-patch bounce.
	// Live-verified 2026-05-28: pods are labeled app=f5-dssm-db and app=f5-dssm-sentinel;
	// the old "app=f5-dssm" selector matched zero pods and made the bounce a silent no-op.
	dssmLabelSelector = "app in (f5-dssm-db,f5-dssm-sentinel)"

	// dssmReadinessSelector is the label selector used to list dssm pods for the
	// readiness guard. Identical to dssmLabelSelector — named separately for clarity.
	dssmReadinessSelector = "app in (f5-dssm-db,f5-dssm-sentinel)"

	// dssmInsecureMarker is the string we check for (and inject) in readiness_probe.sh.
	dssmInsecureMarker = "--tls --insecure"

	// dssmTLSReplace is the old substring to replace (with a trailing space to avoid
	// partial matches on --tls-cert or similar flags).
	dssmTLSReplace = " --tls "

	// dssmTLSInsecureReplace is the replacement that adds --insecure immediately after --tls.
	dssmTLSInsecureReplace = " --tls --insecure "
)

// phase24bConfigMapWait is the maximum time we wait for the f5-dssm ConfigMap to appear.
// FLO creates it after CNEInstance reconciliation; on a cold cluster this can be 2+ min.
// Exported as a var (not const) so tests can override it with a short deadline.
var phase24bConfigMapWait = 3 * time.Minute

// Phase24bDSSMInsecureOverlay patches the FLO-created f5-dssm ConfigMap to add
// --insecure to all redis-cli --tls invocations in readiness_probe.sh, then bounces
// the dssm pods so they re-mount the patched ConfigMap.
//
// Root cause: redis-cli 8.6.0 in the f5-dssm image strict-verifies TLS hostname, but
// the dssm cert SAN is "DNS:dssm-svc, IP:0.0.0.0" and the probe connects to 127.0.0.1
// — hostname-check fails. This blocks dssm-db-1 (replica) startup for 12+ min on a
// cold cluster, which in turn blocks CNEInstance Available=True and Phase 25.
//
// Mirrors aws-gpu-setup/deploy-bnk.sh:263-282.
//
// Idempotency: if --tls --insecure already appears in readiness_probe.sh, the
// ConfigMap is not updated and no pods are bounced.
//
// Skipped silently when cl.Pattern != "host-device".
//
// D-005: CheckAuthOrDie called at entry.
func Phase24bDSSMInsecureOverlay(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintln(os.Stderr, "[phase 24b] DSSM --insecure readiness probe overlay")

	if cl != nil && cl.Pattern != "host-device" {
		fmt.Fprintf(os.Stderr, "[phase 24b] skipped: pattern=%q (host-device only)\n", cl.Pattern)
		return nil
	}

	if dryRun {
		st.Set("DSSM_INSECURE_OVERLAY_APPLIED_AT", "dry-run")
		fmt.Fprintln(os.Stderr, "[phase 24b] dry-run: would patch f5-dssm ConfigMap and bounce dssm pods")
		return nil
	}

	if clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 24b] warning: K8s client not available, skipping DSSM overlay")
		return nil
	}

	// Readiness guard: skip the entire patch+bounce when dssm is already healthy.
	// The FLO operator reverts the ConfigMap marker on every reconcile, so re-patching
	// on a warm cluster is perpetual churn with no effect (dssm stays healthy without the
	// patch present — live-confirmed 2026-05-28 on syd-tracer at 27h uptime). The patch
	// only matters during initial cold-start to unblock the redis-cli TLS hostname-verify
	// bug; once pods are Ready the fix has already been applied and the pods are healthy.
	if allDSSMPodsReady(ctx, clients) {
		fmt.Fprintln(os.Stderr, "[phase 24b] dssm already Ready — skipping overlay (cold-start only)")
		return nil
	}

	// Wait for the f5-dssm ConfigMap — FLO creates it after CNEInstance reconciliation.
	var cm *corev1.ConfigMap
	waitErr := wait.PollUntilContextTimeout(ctx, 10*time.Second, phase24bConfigMapWait, true,
		func(ctx context.Context) (bool, error) {
			got, err := clients.K8s.CoreV1().ConfigMaps(InstanceNamespace).Get(ctx, dssmConfigMapName, metav1.GetOptions{})
			if err != nil {
				if k8serrors.IsNotFound(err) {
					fmt.Fprintf(os.Stderr, "[phase 24b] waiting for ConfigMap %s/%s to appear...\n",
						InstanceNamespace, dssmConfigMapName)
					return false, nil
				}
				return false, err
			}
			cm = got
			return true, nil
		})
	if waitErr != nil {
		return fmt.Errorf("phase 24b: timed out waiting for ConfigMap %s/%s: %w",
			InstanceNamespace, dssmConfigMapName, waitErr)
	}

	// Patch every probe script that uses --tls. BNK 2.3 f5-dssm ConfigMap ships 7
	// such scripts: db probes (readiness/liveness/startup), sentinel probes
	// (readiness/liveness/startup), and init.sh. Phase 24b's original
	// readiness_probe.sh-only patch (mirror of aws-gpu-setup/deploy-bnk.sh:263-282)
	// missed the sentinel probes, which kept f5-dssm-sentinel-2 stuck at 2/3
	// blocking CNE Available indefinitely (live-validated on syd-tracer 2026-05-24).
	if cm.Data == nil {
		fmt.Fprintln(os.Stderr, "[phase 24b] warning: ConfigMap data is empty — nothing to patch")
		return nil
	}

	patchedKeys := []string{}
	skippedKeys := []string{}
	for k, v := range cm.Data {
		if !strings.Contains(v, dssmTLSReplace) {
			continue
		}
		if strings.Contains(v, dssmInsecureMarker) {
			skippedKeys = append(skippedKeys, k)
			continue
		}
		cm.Data[k] = strings.ReplaceAll(v, dssmTLSReplace, dssmTLSInsecureReplace)
		patchedKeys = append(patchedKeys, k)
	}

	if len(patchedKeys) == 0 {
		if len(skippedKeys) > 0 {
			fmt.Fprintf(os.Stderr, "[phase 24b] f5-dssm ConfigMap already patched (--tls --insecure present in %d scripts) — skipping\n",
				len(skippedKeys))
		} else {
			fmt.Fprintln(os.Stderr, "[phase 24b] warning: no '--tls' scripts found in f5-dssm ConfigMap — nothing to patch")
		}
		return nil
	}

	_, err := clients.K8s.CoreV1().ConfigMaps(InstanceNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("phase 24b: update ConfigMap %s/%s: %w", InstanceNamespace, dssmConfigMapName, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 24b] patched %d scripts in f5-dssm ConfigMap (added --insecure to redis-cli --tls invocations): %s\n",
		len(patchedKeys), strings.Join(patchedKeys, ", "))

	// Bounce dssm pods so they re-mount the patched ConfigMap.
	if err := clients.K8s.CoreV1().Pods(InstanceNamespace).DeleteCollection(
		ctx,
		metav1.DeleteOptions{},
		metav1.ListOptions{LabelSelector: dssmLabelSelector},
	); err != nil {
		// Log but don't fail — pods will pick up the new CM on next restart anyway.
		fmt.Fprintf(os.Stderr, "[phase 24b] warning: bounce dssm pods: %v\n", err)
	} else {
		fmt.Fprintln(os.Stderr, "[phase 24b] bounced f5-dssm pods to re-mount patched ConfigMap")
	}

	st.Set("DSSM_INSECURE_OVERLAY_APPLIED_AT", time.Now().UTC().Format(time.RFC3339))
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "[phase 24b] warning: save state: %v\n", err)
	}
	return nil
}

// allDSSMPodsReady returns true when at least one dssm pod exists AND every
// dssm pod (db + sentinel) in InstanceNamespace has a Ready condition of True.
// Zero pods (very early cold-start) returns false so the patch path runs.
func allDSSMPodsReady(ctx context.Context, clients *Clients) bool {
	pods, err := clients.K8s.CoreV1().Pods(InstanceNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: dssmReadinessSelector,
	})
	if err != nil {
		// List failure → can't confirm Ready; fall through to patch path.
		fmt.Fprintf(os.Stderr, "[phase 24b] warning: listing dssm pods for readiness check: %v — assuming not Ready\n", err)
		return false
	}
	if len(pods.Items) == 0 {
		// No pods yet (very early cold-start).
		return false
	}
	for i := range pods.Items {
		if !isPodReady(&pods.Items[i]) {
			return false
		}
	}
	return true
}

// isPodReady reports whether the pod has a Ready condition with status True.
func isPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Phase24bDSSMInsecureOverlayDown is a no-op — the f5-dssm ConfigMap and pods
// are removed as part of FLO/CNEInstance teardown (Phase 22 down / Phase 14 down).
// We only clear the state key.
func Phase24bDSSMInsecureOverlayDown(_ context.Context, _ *intent.Cluster, st *state.State, _ *Clients) error {
	st.Set("DSSM_INSECURE_OVERLAY_APPLIED_AT", "")
	if err := st.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "[phase 24b] warning: save state on down: %v\n", err)
	}
	return nil
}
