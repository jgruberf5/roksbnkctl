package phases

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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

	// dssmLabelSelector targets all f5-dssm pods for the post-patch bounce.
	dssmLabelSelector = "app=f5-dssm"

	// dssmInsecureMarker is the string we check for (and inject) in readiness_probe.sh.
	dssmInsecureMarker = "--tls --insecure"

	// dssmTLSReplace is the old substring to replace (with a trailing space to avoid
	// partial matches on --tls-cert or similar flags).
	dssmTLSReplace = " --tls "

	// dssmTLSInsecureReplace is the replacement that adds --insecure immediately after --tls.
	dssmTLSInsecureReplace = " --tls --insecure "

	// dssmProbeKey is the ConfigMap data key that holds the readiness_probe.sh content.
	dssmProbeKey = "readiness_probe.sh"
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

	// Wait for the f5-dssm ConfigMap — FLO creates it after CNEInstance reconciliation.
	var probeScript string
	waitErr := wait.PollUntilContextTimeout(ctx, 10*time.Second, phase24bConfigMapWait, true,
		func(ctx context.Context) (bool, error) {
			cm, err := clients.K8s.CoreV1().ConfigMaps(InstanceNamespace).Get(ctx, dssmConfigMapName, metav1.GetOptions{})
			if err != nil {
				if k8serrors.IsNotFound(err) {
					fmt.Fprintf(os.Stderr, "[phase 24b] waiting for ConfigMap %s/%s to appear...\n",
						InstanceNamespace, dssmConfigMapName)
					return false, nil
				}
				return false, err
			}
			probeScript = cm.Data[dssmProbeKey]
			return true, nil
		})
	if waitErr != nil {
		return fmt.Errorf("phase 24b: timed out waiting for ConfigMap %s/%s: %w",
			InstanceNamespace, dssmConfigMapName, waitErr)
	}

	// Idempotency: if already patched, skip update and bounce.
	if strings.Contains(probeScript, dssmInsecureMarker) {
		fmt.Fprintln(os.Stderr, "[phase 24b] f5-dssm ConfigMap already patched (--tls --insecure present) — skipping")
		return nil
	}

	// Apply sed-equivalent: replace all " --tls " with " --tls --insecure ".
	patched := strings.ReplaceAll(probeScript, dssmTLSReplace, dssmTLSInsecureReplace)
	if patched == probeScript {
		// --tls not found — unexpected; log and continue (don't fail — the
		// BNK image may have changed the probe script).
		fmt.Fprintln(os.Stderr, "[phase 24b] warning: '--tls' not found in readiness_probe.sh — ConfigMap not modified")
		return nil
	}

	// Fetch the current ConfigMap for the typed Update call.
	cm, err := clients.K8s.CoreV1().ConfigMaps(InstanceNamespace).Get(ctx, dssmConfigMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("phase 24b: re-fetch ConfigMap %s/%s: %w", InstanceNamespace, dssmConfigMapName, err)
	}
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[dssmProbeKey] = patched

	_, err = clients.K8s.CoreV1().ConfigMaps(InstanceNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("phase 24b: update ConfigMap %s/%s: %w", InstanceNamespace, dssmConfigMapName, err)
	}
	fmt.Fprintln(os.Stderr, "[phase 24b] patched f5-dssm ConfigMap (added --insecure to redis-cli --tls invocations)")

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
