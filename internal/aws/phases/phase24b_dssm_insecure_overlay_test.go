//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig codegen
package phases

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// buildDSSMConfigMap builds a corev1.ConfigMap simulating the FLO-created f5-dssm CM.
func buildDSSMConfigMap(probeContent string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dssmConfigMapName,
			Namespace: InstanceNamespace,
		},
		Data: map[string]string{
			"readiness_probe.sh": probeContent,
		},
	}
}

// dssmHostDeviceCluster returns a minimal *intent.Cluster with pattern=host-device
// for testing the DSSM overlay path.
func dssmHostDeviceCluster() *intent.Cluster {
	return &intent.Cluster{Pattern: "host-device"}
}

// ─── Test 1: HappyPath — unpatched CM gets patched and pods bounced ───────────

func TestPhase24b_HappyPath(t *testing.T) {
	awsmw.ResetForTest()
	// Override the wait timeout for tests so we don't block 3 min.
	orig := phase24bConfigMapWait
	phase24bConfigMapWait = 5 * time.Second
	defer func() { phase24bConfigMapWait = orig }()

	dir := t.TempDir()
	st, _ := state.Load(dir)

	// Pre-seed: CM with unpatched readiness_probe.sh.
	unpatchedScript := "redis-cli -p 6379 --tls --cert /etc/dssm/tls.crt ping\n"
	cm := buildDSSMConfigMap(unpatchedScript)

	// Also seed a pod so DeleteCollection has something to act on.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dssm-db-1",
			Namespace: InstanceNamespace,
			Labels:    map[string]string{"app": "f5-dssm"},
		},
	}
	cs := k8sfake.NewSimpleClientset(cm, pod)
	clients := &Clients{K8s: cs, Profile: "test"}

	if err := Phase24bDSSMInsecureOverlay(context.Background(), dssmHostDeviceCluster(), st, clients, false); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlay: %v", err)
	}

	// Assert CM was updated to contain --tls --insecure.
	updatedCM, err := cs.CoreV1().ConfigMaps(InstanceNamespace).Get(context.Background(), dssmConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated ConfigMap: %v", err)
	}
	gotScript := updatedCM.Data["readiness_probe.sh"]
	if !strings.Contains(gotScript, "--tls --insecure") {
		t.Errorf("expected '--tls --insecure' in updated ConfigMap, got:\n%s", gotScript)
	}

	// Assert a DeleteCollection action was issued for app=f5-dssm.
	var foundDelete bool
	for _, action := range cs.Actions() {
		if action.GetVerb() == "delete-collection" &&
			action.GetResource().Resource == "pods" &&
			action.GetNamespace() == InstanceNamespace {
			foundDelete = true
			break
		}
	}
	if !foundDelete {
		t.Error("expected DeleteCollection action for pods with app=f5-dssm, none found")
	}

	// Assert state key set.
	if v := st.Get("DSSM_INSECURE_OVERLAY_APPLIED_AT"); v == "" || v == "dry-run" {
		t.Errorf("expected DSSM_INSECURE_OVERLAY_APPLIED_AT to be an RFC3339 timestamp, got %q", v)
	}
}

// ─── Test 2: Idempotent — already-patched CM → no update, no pod bounce ──────

func TestPhase24b_Idempotent(t *testing.T) {
	awsmw.ResetForTest()
	orig := phase24bConfigMapWait
	phase24bConfigMapWait = 5 * time.Second
	defer func() { phase24bConfigMapWait = orig }()

	dir := t.TempDir()
	st, _ := state.Load(dir)

	// Pre-seed: CM already has --tls --insecure.
	alreadyPatched := "redis-cli -p 6379 --tls --insecure --cert /etc/dssm/tls.crt ping\n"
	cm := buildDSSMConfigMap(alreadyPatched)
	cs := k8sfake.NewSimpleClientset(cm)
	clients := &Clients{K8s: cs, Profile: "test"}

	if err := Phase24bDSSMInsecureOverlay(context.Background(), dssmHostDeviceCluster(), st, clients, false); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlay idempotent: %v", err)
	}

	// Assert NO update action was issued.
	for _, action := range cs.Actions() {
		if action.GetVerb() == "update" && action.GetResource().Resource == "configmaps" {
			t.Error("expected no ConfigMap update for already-patched CM, but got one")
		}
	}

	// Assert NO delete-collection action was issued.
	for _, action := range cs.Actions() {
		if action.GetVerb() == "delete-collection" && action.GetResource().Resource == "pods" {
			t.Error("expected no pod DeleteCollection for already-patched CM, but got one")
		}
	}
}

// ─── Test 3: WaitForCM — missing CM times out with clear error ────────────────

func TestPhase24b_WaitForCM_Timeout(t *testing.T) {
	awsmw.ResetForTest()
	// Very short timeout so the test completes quickly.
	orig := phase24bConfigMapWait
	phase24bConfigMapWait = 500 * time.Millisecond
	defer func() { phase24bConfigMapWait = orig }()

	dir := t.TempDir()
	st, _ := state.Load(dir)

	// No ConfigMap pre-seeded.
	cs := k8sfake.NewSimpleClientset()
	clients := &Clients{K8s: cs, Profile: "test"}

	err := Phase24bDSSMInsecureOverlay(context.Background(), dssmHostDeviceCluster(), st, clients, false)
	if err == nil {
		t.Fatal("expected error on missing ConfigMap timeout, got nil")
	}
	if !strings.Contains(err.Error(), dssmConfigMapName) {
		t.Errorf("expected error to mention %q, got: %v", dssmConfigMapName, err)
	}
}

// ─── Test 4: NonHostDevice — phase skips silently ────────────────────────────

func TestPhase24b_NonHostDevice_Skipped(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	cs := k8sfake.NewSimpleClientset()
	clients := &Clients{K8s: cs, Profile: "test"}
	cl := &intent.Cluster{Pattern: "some-other-pattern"}

	if err := Phase24bDSSMInsecureOverlay(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlay non-host-device: %v", err)
	}

	// Assert no K8s actions taken.
	if len(cs.Actions()) != 0 {
		t.Errorf("expected no K8s actions for non-host-device pattern, got %d", len(cs.Actions()))
	}
}

// ─── Test 5: DryRun — no mutations ────────────────────────────────────────────

func TestPhase24b_DryRun_NoMutation(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	unpatchedScript := "redis-cli -p 6379 --tls --cert /etc/dssm/tls.crt ping\n"
	cm := buildDSSMConfigMap(unpatchedScript)
	cs := k8sfake.NewSimpleClientset(cm)
	clients := &Clients{K8s: cs, Profile: "test"}

	if err := Phase24bDSSMInsecureOverlay(context.Background(), dssmHostDeviceCluster(), st, clients, true); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlay dry-run: %v", err)
	}

	// No updates or deletes in dry-run.
	for _, action := range cs.Actions() {
		verb := action.GetVerb()
		if verb == "update" || verb == "delete-collection" || verb == "delete" {
			t.Errorf("dry-run: unexpected mutation action %q", verb)
		}
	}

	// State key should be set to "dry-run".
	if v := st.Get("DSSM_INSECURE_OVERLAY_APPLIED_AT"); v != "dry-run" {
		t.Errorf("expected state DSSM_INSECURE_OVERLAY_APPLIED_AT=dry-run, got %q", v)
	}
}

// ─── Test 6: Down — clears state key ─────────────────────────────────────────

func TestPhase24b_Down_ClearsStateKey(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("DSSM_INSECURE_OVERLAY_APPLIED_AT", "2026-05-24T00:00:00Z")

	if err := Phase24bDSSMInsecureOverlayDown(context.Background(), nil, st, nil); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlayDown: %v", err)
	}

	if v := st.Get("DSSM_INSECURE_OVERLAY_APPLIED_AT"); v != "" {
		t.Errorf("expected state key cleared on down, got %q", v)
	}
}

// ─── Test 7: AllSevenScripts — every --tls script in the CM gets patched ─────
//
// Regression test for the live-validated bug on syd-tracer 2026-05-24 where
// Phase 24b's original readiness_probe.sh-only patch left 6 other probe scripts
// (sentinel_readiness, sentinel_startup, etc.) unpatched, blocking sentinel-2.

func TestPhase24b_AllTLSScriptsPatched(t *testing.T) {
	awsmw.ResetForTest()
	orig := phase24bConfigMapWait
	phase24bConfigMapWait = 5 * time.Second
	defer func() { phase24bConfigMapWait = orig }()

	dir := t.TempDir()
	st, _ := state.Load(dir)

	// BNK 2.3 f5-dssm ConfigMap ships 7 scripts using --tls: db + sentinel
	// readiness/liveness/startup probes plus init.sh. All must be patched.
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dssmConfigMapName,
			Namespace: InstanceNamespace,
		},
		Data: map[string]string{
			"readiness_probe.sh":          "redis-cli -p 6379 --tls --cert x ping\n",
			"liveness_probe.sh":           "redis-cli -p 6379 --tls --cert x ping\n",
			"startup_probe.sh":            "redis-cli -p 6379 --tls --cert x ping\n",
			"sentinel_readiness_probe.sh": "redis-cli -p 26379 --tls --cert x ping\n",
			"sentinel_liveness_probe.sh":  "redis-cli -p 26379 --tls --cert x ping\n",
			"sentinel_startup_probe.sh":   "redis-cli -p 26379 --tls --cert x ping\n",
			"init.sh":                     "redis-cli -p 6379 --tls --cert x INFO\n",
			"f5log_lib.sh":                "# helper functions; no probe calls; must not be modified\n",
		},
	}
	cs := k8sfake.NewSimpleClientset(cm)
	clients := &Clients{K8s: cs, Profile: "test"}

	if err := Phase24bDSSMInsecureOverlay(context.Background(), dssmHostDeviceCluster(), st, clients, false); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlay: %v", err)
	}

	updatedCM, err := cs.CoreV1().ConfigMaps(InstanceNamespace).Get(context.Background(), dssmConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated CM: %v", err)
	}

	wantPatched := []string{
		"readiness_probe.sh", "liveness_probe.sh", "startup_probe.sh",
		"sentinel_readiness_probe.sh", "sentinel_liveness_probe.sh", "sentinel_startup_probe.sh",
		"init.sh",
	}
	for _, k := range wantPatched {
		if !strings.Contains(updatedCM.Data[k], "--tls --insecure") {
			t.Errorf("script %q missing --tls --insecure after patch: %s", k, updatedCM.Data[k])
		}
	}
	// f5log_lib.sh has no --tls and must be untouched.
	if updatedCM.Data["f5log_lib.sh"] != "# helper functions; no probe calls; must not be modified\n" {
		t.Errorf("f5log_lib.sh should not have been modified, got: %s", updatedCM.Data["f5log_lib.sh"])
	}
}

// ─── Verify k8stesting import is used (prevents unused-import compile error) ──
var _ k8stesting.Action // referenced only for package import validation
