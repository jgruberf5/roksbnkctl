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

// buildReadyDSSMPod returns a dssm pod with a Ready=True condition.
func buildReadyDSSMPod(name, appLabel string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: InstanceNamespace,
			Labels:    map[string]string{"app": appLabel},
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

// buildNotReadyDSSMPod returns a dssm pod with no Ready condition (cold-start state).
func buildNotReadyDSSMPod(name, appLabel string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: InstanceNamespace,
			Labels:    map[string]string{"app": appLabel},
		},
	}
}

// ─── Test 1: HappyPath — unpatched CM gets patched and pods bounced ───────────
//
// Pods are NOT ready (no Ready condition) so the readiness guard falls through.
// The CM is unpatched so the phase patches it and issues a DeleteCollection.

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

	// Seed a not-ready pod using the REAL labels — readiness guard lists these and
	// finds them not Ready, so the phase falls through to patch+bounce.
	pod := buildNotReadyDSSMPod("f5-dssm-db-0", "f5-dssm-db")
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

	// Assert a DeleteCollection action was issued for the correct pod selector.
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
		t.Error("expected DeleteCollection action for dssm pods, none found")
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

// ─── Test 8: ReadinessGuard — all dssm pods Ready → no mutations ─────────────
//
// This is the primary idempotency fix: on a healthy warm cluster, the phase must
// return nil immediately without touching the ConfigMap or bouncing pods.

func TestPhase24b_ReadinessGuard_AllReady_NoMutations(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	// Seed both db and sentinel pods as Ready.
	dbPod := buildReadyDSSMPod("f5-dssm-db-0", "f5-dssm-db")
	sentinelPod := buildReadyDSSMPod("f5-dssm-sentinel-0", "f5-dssm-sentinel")
	// Also seed an unpatched CM — if the guard fails, this would get mutated.
	unpatchedScript := "redis-cli -p 6379 --tls --cert /etc/dssm/tls.crt ping\n"
	cm := buildDSSMConfigMap(unpatchedScript)

	cs := k8sfake.NewSimpleClientset(cm, dbPod, sentinelPod)
	clients := &Clients{K8s: cs, Profile: "test"}

	if err := Phase24bDSSMInsecureOverlay(context.Background(), dssmHostDeviceCluster(), st, clients, false); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlay with all-Ready pods: %v", err)
	}

	// Assert NO mutating K8s calls were made (no ConfigMap update, no pod bounce).
	for _, action := range cs.Actions() {
		verb := action.GetVerb()
		if verb == "update" || verb == "delete-collection" || verb == "delete" || verb == "patch" {
			t.Errorf("readiness guard: unexpected mutating action %q — phase should have returned immediately", verb)
		}
	}
}

// ─── Test 9: ReadinessGuard — one pod not Ready → falls through to patch+bounce

func TestPhase24b_ReadinessGuard_OneNotReady_FallsThrough(t *testing.T) {
	awsmw.ResetForTest()
	orig := phase24bConfigMapWait
	phase24bConfigMapWait = 5 * time.Second
	defer func() { phase24bConfigMapWait = orig }()

	dir := t.TempDir()
	st, _ := state.Load(dir)

	// One db pod Ready, one sentinel pod NOT Ready — guard must fall through.
	dbPod := buildReadyDSSMPod("f5-dssm-db-0", "f5-dssm-db")
	sentinelPod := buildNotReadyDSSMPod("f5-dssm-sentinel-0", "f5-dssm-sentinel")
	unpatchedScript := "redis-cli -p 6379 --tls --cert /etc/dssm/tls.crt ping\n"
	cm := buildDSSMConfigMap(unpatchedScript)

	cs := k8sfake.NewSimpleClientset(cm, dbPod, sentinelPod)
	clients := &Clients{K8s: cs, Profile: "test"}

	if err := Phase24bDSSMInsecureOverlay(context.Background(), dssmHostDeviceCluster(), st, clients, false); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlay with one-not-Ready: %v", err)
	}

	// Must have updated the ConfigMap.
	var foundUpdate bool
	for _, action := range cs.Actions() {
		if action.GetVerb() == "update" && action.GetResource().Resource == "configmaps" {
			foundUpdate = true
		}
	}
	if !foundUpdate {
		t.Error("expected ConfigMap update when not all dssm pods are Ready")
	}

	// Must have issued a DeleteCollection for pods.
	var foundDelete bool
	for _, action := range cs.Actions() {
		if action.GetVerb() == "delete-collection" && action.GetResource().Resource == "pods" {
			foundDelete = true
		}
	}
	if !foundDelete {
		t.Error("expected DeleteCollection for pods when dssm not Ready, none found")
	}
}

// ─── Test 10: ReadinessGuard — zero pods (very early cold-start) → falls through

func TestPhase24b_ReadinessGuard_ZeroPods_FallsThrough(t *testing.T) {
	awsmw.ResetForTest()
	orig := phase24bConfigMapWait
	phase24bConfigMapWait = 5 * time.Second
	defer func() { phase24bConfigMapWait = orig }()

	dir := t.TempDir()
	st, _ := state.Load(dir)

	// No pods at all — guard must treat this as not Ready (very early cold-start).
	unpatchedScript := "redis-cli -p 6379 --tls --cert /etc/dssm/tls.crt ping\n"
	cm := buildDSSMConfigMap(unpatchedScript)

	cs := k8sfake.NewSimpleClientset(cm)
	clients := &Clients{K8s: cs, Profile: "test"}

	if err := Phase24bDSSMInsecureOverlay(context.Background(), dssmHostDeviceCluster(), st, clients, false); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlay with zero dssm pods: %v", err)
	}

	// Must have updated the ConfigMap (fell through to patch path).
	var foundUpdate bool
	for _, action := range cs.Actions() {
		if action.GetVerb() == "update" && action.GetResource().Resource == "configmaps" {
			foundUpdate = true
		}
	}
	if !foundUpdate {
		t.Error("expected ConfigMap update when zero dssm pods exist (very early cold-start)")
	}
}

// ─── Test 11: CorrectBounceSelector — DeleteCollection uses the real pod labels ─
//
// Verifies that the bounce selector is "app in (f5-dssm-db,f5-dssm-sentinel)"
// and NOT the old dead "app=f5-dssm" that matched zero pods.

func TestPhase24b_CorrectBounceSelector(t *testing.T) {
	awsmw.ResetForTest()
	orig := phase24bConfigMapWait
	phase24bConfigMapWait = 5 * time.Second
	defer func() { phase24bConfigMapWait = orig }()

	dir := t.TempDir()
	st, _ := state.Load(dir)

	// Seed an unpatched CM and a not-ready dssm pod so we reach the bounce.
	unpatchedScript := "redis-cli -p 6379 --tls --cert /etc/dssm/tls.crt ping\n"
	cm := buildDSSMConfigMap(unpatchedScript)
	pod := buildNotReadyDSSMPod("f5-dssm-db-0", "f5-dssm-db")

	cs := k8sfake.NewSimpleClientset(cm, pod)
	clients := &Clients{K8s: cs, Profile: "test"}

	if err := Phase24bDSSMInsecureOverlay(context.Background(), dssmHostDeviceCluster(), st, clients, false); err != nil {
		t.Fatalf("Phase24bDSSMInsecureOverlay: %v", err)
	}

	// Find the DeleteCollection action and inspect its label selector.
	// The fake clientset records this as k8stesting.DeleteCollectionActionImpl.
	var selectorStr string
	for _, action := range cs.Actions() {
		if action.GetVerb() != "delete-collection" || action.GetResource().Resource != "pods" {
			continue
		}
		if da, ok := action.(k8stesting.DeleteCollectionActionImpl); ok {
			selectorStr = da.ListRestrictions.Labels.String()
		}
	}

	if selectorStr == "" {
		t.Fatal("no DeleteCollection action recorded for pods")
	}

	// The selector must reference f5-dssm-db and f5-dssm-sentinel, NOT the old dead "app=f5-dssm".
	if selectorStr == "app=f5-dssm" {
		t.Errorf("DeleteCollection used old dead selector %q — must target real dssm pod labels (f5-dssm-db + f5-dssm-sentinel)", selectorStr)
	}
	if !strings.Contains(selectorStr, "f5-dssm-db") || !strings.Contains(selectorStr, "f5-dssm-sentinel") {
		t.Errorf("DeleteCollection selector %q does not reference both f5-dssm-db and f5-dssm-sentinel", selectorStr)
	}
}

// ─── Verify k8stesting import is used (prevents unused-import compile error) ──
var _ k8stesting.Action // referenced only for package import validation
