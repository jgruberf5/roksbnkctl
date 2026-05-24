//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig codegen
package phases

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// buildCneControllerDeploy returns a minimal f5-cne-controller Deployment
// with a matchLabels selector targeting app=f5-cne-controller.
func buildCneControllerDeploy() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      h4DeploymentName,
			Namespace: InstanceNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "f5-cne-controller"},
			},
		},
	}
}

// buildCneControllerPod builds a corev1.Pod simulating an f5-cne-controller
// pod with the f5-tmm-pod-manager sidecar in the given state.
//
//   - name          — pod name
//   - podManagerReady — whether the f5-tmm-pod-manager container is Ready
//   - restartCount  — container restart count
//   - waitingReason — cs.State.Waiting.Reason (empty string = not waiting)
func buildCneControllerPod(name string, podManagerReady bool, restartCount int32, waitingReason string) *corev1.Pod {
	cs := corev1.ContainerStatus{
		Name:         h4ContainerName,
		Ready:        podManagerReady,
		RestartCount: restartCount,
	}
	if waitingReason != "" {
		cs.State = corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: waitingReason},
		}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: InstanceNamespace,
			Labels:    map[string]string{"app": "f5-cne-controller"},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{cs},
		},
	}
}

// ─── Test 1: Ready on first iter → no patch ──────────────────────────────────

func TestPhase24c_ReadyOnFirstIter(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	deploy := buildCneControllerDeploy()
	pod := buildCneControllerPod("cne-ctrl-0", true, 0, "")
	cs := k8sfake.NewSimpleClientset(deploy, pod)
	clients := &Clients{K8s: cs, Profile: "test"}

	// Use a cancelled ctx so the loop exits after the first iter without
	// blocking on h4PollInterval for subsequent iters.
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- Phase24cPodManagerHeal(ctx, nil, st, clients, false)
	}()

	// Give the first iter time to run, then cancel so the loop drains.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-errCh

	// Verify no patch was applied (annotation must be absent).
	d, err := cs.AppsV1().Deployments(InstanceNamespace).Get(context.Background(), h4DeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	if ann := d.Spec.Template.ObjectMeta.Annotations; ann != nil {
		if _, ok := ann["awsbnkctl.io/restartedAt"]; ok {
			t.Error("expected no restartedAt annotation on Ready pod, but found one")
		}
	}
}

// ─── Test 2: CrashLoopBackOff triggers rollout-restart ───────────────────────

func TestPhase24c_CrashloopTriggersRestart(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	deploy := buildCneControllerDeploy()
	pod := buildCneControllerPod("cne-ctrl-crashloop", false, 1, "CrashLoopBackOff")
	cs := k8sfake.NewSimpleClientset(deploy, pod)
	clients := &Clients{K8s: cs, Profile: "test"}

	// Run with a context that will be cancelled to avoid blocking on
	// h4PostRestartWait after the patch is applied.
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- Phase24cPodManagerHeal(ctx, nil, st, clients, false)
	}()
	cancel() // cancels the post-restart sleep so the goroutine exits
	<-errCh  // drain (best-effort — nil expected)

	// Verify the deployment now has the restartedAt annotation.
	d, err := cs.AppsV1().Deployments(InstanceNamespace).Get(context.Background(), h4DeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	ann := d.Spec.Template.ObjectMeta.Annotations
	if ann == nil {
		t.Fatal("expected restartedAt annotation on deployment, got nil annotations")
	}
	if _, ok := ann["awsbnkctl.io/restartedAt"]; !ok {
		t.Errorf("expected awsbnkctl.io/restartedAt annotation, annotations=%v", ann)
	}
}

// ─── Test 3: High restart count (no waiting reason) triggers restart ─────────

func TestPhase24c_HighRestartCount_TriggersRestart(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	deploy := buildCneControllerDeploy()
	// restartCount == h4RestartThreshold, no CrashLoopBackOff reason.
	pod := buildCneControllerPod("cne-ctrl-highrestart", false, h4RestartThreshold, "")
	cs := k8sfake.NewSimpleClientset(deploy, pod)
	clients := &Clients{K8s: cs, Profile: "test"}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- Phase24cPodManagerHeal(ctx, nil, st, clients, false)
	}()
	cancel()
	<-errCh

	d, err := cs.AppsV1().Deployments(InstanceNamespace).Get(context.Background(), h4DeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	ann := d.Spec.Template.ObjectMeta.Annotations
	if ann == nil {
		t.Fatal("expected restartedAt annotation on deployment, got nil annotations")
	}
	if _, ok := ann["awsbnkctl.io/restartedAt"]; !ok {
		t.Errorf("expected awsbnkctl.io/restartedAt annotation, annotations=%v", ann)
	}
}

// ─── Test 4: Below threshold, no reason → not wedged → no patch ──────────────

func TestPhase24c_BelowThreshold_NoPatch(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	deploy := buildCneControllerDeploy()
	// restartCount=1 < h4RestartThreshold=2, no CrashLoopBackOff.
	pod := buildCneControllerPod("cne-ctrl-starting", false, 1, "")
	cs := k8sfake.NewSimpleClientset(deploy, pod)
	clients := &Clients{K8s: cs, Profile: "test"}

	// Cancel immediately so the loop exits after the first read without sleeping.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = Phase24cPodManagerHeal(ctx, nil, st, clients, false)

	d, err := cs.AppsV1().Deployments(InstanceNamespace).Get(context.Background(), h4DeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	ann := d.Spec.Template.ObjectMeta.Annotations
	if ann != nil {
		if _, ok := ann["awsbnkctl.io/restartedAt"]; ok {
			t.Error("expected no restartedAt annotation (below threshold), but found one")
		}
	}
}

// ─── Test 5: No deployment in fake → no-op ───────────────────────────────────

func TestPhase24c_NoDeployment_NoOp(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	// Empty fake client — no deployment exists.
	cs := k8sfake.NewSimpleClientset()
	clients := &Clients{K8s: cs, Profile: "test"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // exit after first iter

	if err := Phase24cPodManagerHeal(ctx, nil, st, clients, false); err != nil {
		t.Fatalf("expected nil (best-effort), got: %v", err)
	}
}

// ─── Test 6: Dry-run makes no mutations ──────────────────────────────────────

func TestPhase24c_DryRun_NoMutation(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	deploy := buildCneControllerDeploy()
	pod := buildCneControllerPod("cne-ctrl-0", false, 5, "CrashLoopBackOff")
	cs := k8sfake.NewSimpleClientset(deploy, pod)
	clients := &Clients{K8s: cs, Profile: "test"}

	if err := Phase24cPodManagerHeal(context.Background(), nil, st, clients, true); err != nil {
		t.Fatalf("Phase24cPodManagerHeal dry-run: %v", err)
	}

	d, err := cs.AppsV1().Deployments(InstanceNamespace).Get(context.Background(), h4DeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting deployment: %v", err)
	}
	ann := d.Spec.Template.ObjectMeta.Annotations
	if ann != nil {
		if _, ok := ann["awsbnkctl.io/restartedAt"]; ok {
			t.Error("expected no annotation in dry-run, but found one")
		}
	}
}

// ─── Test 7: ctx cancel returns nil ──────────────────────────────────────────

func TestPhase24c_CtxCancel(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	cs := k8sfake.NewSimpleClientset()
	clients := &Clients{K8s: cs, Profile: "test"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	if err := Phase24cPodManagerHeal(ctx, nil, st, clients, false); err != nil {
		t.Fatalf("expected nil on ctx cancel (best-effort), got: %v", err)
	}
}

// ─── Test 8: MaxBouncesCap — perpetually-wedged pod → at most h4MaxBounces patches ──

// TestPhase24c_MaxBouncesCap ensures we don't ping-pong bounce a perpetually-wedged pod.
//
// Strategy: install a PatchAction reactor on the fake client that atomically
// counts patch calls.  The pod is always in CrashLoopBackOff so every iter
// would want to bounce.  We run the phase in a goroutine and cancel the ctx
// once the counter hits h4MaxBounces (or a 2-second wall-clock guard fires).
// The phase must not issue more than h4MaxBounces patches total.
func TestPhase24c_MaxBouncesCap(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	deploy := buildCneControllerDeploy()
	pod := buildCneControllerPod("cne-ctrl-perpetual", false, 5, "CrashLoopBackOff")
	cs := k8sfake.NewSimpleClientset(deploy, pod)

	// Reactor counts patch calls on the deployments resource.
	var patchCount int64
	cs.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		atomic.AddInt64(&patchCount, 1)
		// Return false so the default object tracker also handles the patch
		// (keeps the fake store consistent).
		return false, nil, nil
	})

	clients := &Clients{K8s: cs, Profile: "test"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- Phase24cPodManagerHeal(ctx, nil, st, clients, false)
	}()

	// Poll until we observe h4MaxBounces patches or the 2-second guard fires.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&patchCount) >= int64(h4MaxBounces) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-errCh

	got := atomic.LoadInt64(&patchCount)
	if got > int64(h4MaxBounces) {
		t.Errorf("expected at most %d patches (h4MaxBounces), got %d", h4MaxBounces, got)
	}
	if got == 0 {
		t.Errorf("expected at least 1 patch for perpetually-wedged pod, got 0")
	}
}

// ─── Test 9a: podManagerStatus — container found ─────────────────────────────

func TestPodManagerStatus_ContainerFound(t *testing.T) {
	pod := buildCneControllerPod("cne-0", false, 3, "CrashLoopBackOff")
	found, ready, restartCount, waitingReason := podManagerStatus(pod)
	if !found {
		t.Error("expected found=true")
	}
	if ready {
		t.Error("expected ready=false")
	}
	if restartCount != 3 {
		t.Errorf("expected restartCount=3, got %d", restartCount)
	}
	if waitingReason != "CrashLoopBackOff" {
		t.Errorf("expected waitingReason=CrashLoopBackOff, got %q", waitingReason)
	}
}

// ─── Test 9b: podManagerStatus — container missing ───────────────────────────

func TestPodManagerStatus_ContainerMissing(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: InstanceNamespace},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "some-other-container", Ready: true, RestartCount: 1},
			},
		},
	}
	found, ready, restartCount, waitingReason := podManagerStatus(pod)
	if found {
		t.Error("expected found=false for missing container")
	}
	if ready || restartCount != 0 || waitingReason != "" {
		t.Errorf("expected zero values, got ready=%v restartCount=%d waitingReason=%q", ready, restartCount, waitingReason)
	}
}
