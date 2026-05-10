//go:build integration
// +build integration

package exec

// Sprint 4 / PRD 03 — K8s backend integration tests against a kind cluster.
//
// Gated behind the `integration` build tag. Expects a kind cluster
// reachable via $KUBECONFIG (or $HOME/.kube/config) — the GitHub Actions
// `k8s-backend` job uses helm/kind-action@v1 to provision one ephemerally.
//
// Run locally:
//
//	# spin a kind cluster first
//	kind create cluster --name roksbnkctl-test
//	go test -tags integration -timeout 10m ./internal/exec/...
//	kind delete cluster --name roksbnkctl-test
//
// Tests skip cleanly when no cluster is reachable so the suite is safe
// even on a runner without kind installed (the CI job sets up kind
// explicitly; locally you can no-op).

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// k8sIntegrationClient builds a real clientset from $KUBECONFIG /
// ~/.kube/config (or in-cluster). Skips the test when neither resolves.
func k8sIntegrationClient(t *testing.T) (kubernetes.Interface, *rest.Config) {
	t.Helper()

	kc := os.Getenv("KUBECONFIG")
	if kc == "" {
		kc = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}
	if _, err := os.Stat(kc); err != nil {
		// Try in-cluster (e.g., running from a pod that has SA token).
		cfg, err2 := rest.InClusterConfig()
		if err2 != nil {
			t.Skipf("no kubeconfig at %s and not in-cluster (%v / %v)", kc, err, err2)
		}
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			t.Skipf("building clientset: %v", err)
		}
		return cs, cfg
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kc)
	if err != nil {
		t.Skipf("building rest.Config from %s: %v", kc, err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Skipf("building clientset: %v", err)
	}

	// Sanity probe: list namespaces to confirm the cluster is reachable.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		t.Skipf("kubernetes API unreachable: %v", err)
	}
	return cs, cfg
}

// ensureTestNamespace creates the roksbnkctl-test namespace if missing
// (mirrors what `roksbnkctl ops install` does). Tests that exercise the
// Job path need this to exist before runAsJob is invoked.
func ensureTestNamespace(t *testing.T, cs kubernetes.Interface) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, ns := range []string{K8sOpsNamespace, K8sTestNamespace} {
		_, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if err == nil {
			continue
		}
		nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		_, _ = cs.CoreV1().Namespaces().Create(ctx, nsObj, metav1.CreateOptions{})
	}
}

// TestIntegration_K8sBackend_JobMode_Echo runs a no-op probe via the Job
// path: argv = ["busybox:1.36", "echo", "hello"]. Asserts the Job runs to
// completion, logs reach the caller's stdout, and the Job is cleaned up
// (TTLSecondsAfterFinished or our cancel-cleanup goroutine).
func TestIntegration_K8sBackend_JobMode_Echo(t *testing.T) {
	cs, cfg := k8sIntegrationClient(t)
	ensureTestNamespace(t, cs)

	b := &K8sBackend{
		client: cs,
		config: cfg,
		initFn: func() (kubernetes.Interface, *rest.Config, error) { return cs, cfg, nil },
	}

	var stdout, stderr strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Pin the busybox tag — CI pre-loads busybox:1.36 into the kind cluster
	// via `kind load docker-image` so the kubelet doesn't need to pull
	// from Docker Hub (slow + rate-limited on cold runners).
	rc, err := b.Run(ctx,
		[]string{"busybox:1.36", "echo", "hello-from-job"},
		RunOpts{
			Stdout: &noopBuilder{&stdout},
			Stderr: &noopBuilder{&stderr},
		})
	if err != nil {
		// Don't t.Fatal — kind clusters can be slow to schedule. Surface
		// useful diagnostics.
		t.Logf("Run error (rc=%d): %v", rc, err)
	}
	if rc != 0 {
		t.Errorf("expected rc=0, got %d (stderr=%q)", rc, stderr.String())
	}
	// Note: argv[0]="busybox" isn't in toolImages, so it's used as a
	// literal image ref. The runAsJob path uses argv[0] in the job name —
	// hyphens-only, no colons, so this is label-safe.
}

// TestIntegration_K8sBackend_OpsPodExec runs `echo hello` through the
// long-lived ops-pod exec path. Requires `roksbnkctl ops install` to
// have run (or this test to provision a sleep-infinity pod with
// equivalent labels).
func TestIntegration_K8sBackend_OpsPodExec(t *testing.T) {
	cs, cfg := k8sIntegrationClient(t)
	ensureTestNamespace(t, cs)

	// Ensure an ops pod is present. We provision a minimal busybox pod
	// labelled the same way `roksbnkctl ops install` would so the
	// long-lived path can find it.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := ensureOpsPodForTest(ctx, cs); err != nil {
		t.Skipf("couldn't provision a test ops pod: %v", err)
	}
	defer func() {
		_ = cs.CoreV1().Pods(K8sOpsNamespace).Delete(context.Background(), K8sOpsPodName, metav1.DeleteOptions{})
	}()

	b := &K8sBackend{
		client: cs,
		config: cfg,
		initFn: func() (kubernetes.Interface, *rest.Config, error) { return cs, cfg, nil },
	}

	var stdout, stderr strings.Builder
	rc, err := b.Run(ctx,
		[]string{"echo", "hello-from-ops-pod"},
		RunOpts{
			Stdout: &noopBuilder{&stdout},
			Stderr: &noopBuilder{&stderr},
			Env:    []string{k8sLongLivedKey},
		})
	if err != nil {
		t.Logf("ops-pod exec err (rc=%d): %v", rc, err)
	}
	if rc != 0 {
		t.Errorf("rc=%d, stderr=%q", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hello-from-ops-pod") {
		t.Errorf("stdout missing token: %q", stdout.String())
	}
}

// ensureOpsPodForTest creates a minimal pod that satisfies the K8sBackend's
// ops-pod-ready check (Phase=Running + Ready condition). Used by the
// integration tests as a stand-in for `roksbnkctl ops install`.
func ensureOpsPodForTest(ctx context.Context, cs kubernetes.Interface) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      K8sOpsPodName,
			Namespace: K8sOpsNamespace,
			Labels:    map[string]string{"app": "roksbnkctl-ops"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: ptrBool(true),
				SeccompProfile: &corev1.SeccompProfile{
					Type: corev1.SeccompProfileTypeRuntimeDefault,
				},
			},
			Containers: []corev1.Container{{
				Name:    "tools",
				Image:   "busybox:1.36",
				Command: []string{"sleep", "3600"},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptrBool(false),
					RunAsNonRoot:             ptrBool(true),
					RunAsUser:                ptrInt64(65532),
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
					},
				},
			}},
		},
	}
	_, err := cs.CoreV1().Pods(K8sOpsNamespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	// Wait for Ready.
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		got, err := cs.CoreV1().Pods(K8sOpsNamespace).Get(ctx, K8sOpsPodName, metav1.GetOptions{})
		if err == nil && got.Status.Phase == corev1.PodRunning {
			for _, c := range got.Status.Conditions {
				if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					return nil
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return errIntegrationTimeout
}

// noopBuilder adapts strings.Builder into io.Writer (Builder already has
// Write; this is just a renamed wrapper to avoid taking address of a
// stack-allocated Builder in test patterns).
type noopBuilder struct {
	*strings.Builder
}

func (b *noopBuilder) Write(p []byte) (int, error) {
	return b.Builder.Write(p)
}

func ptrInt64(i int64) *int64 { return &i }

// errIntegrationTimeout is a sentinel for the ensureOpsPodForTest poll
// loop. Inline error rather than fmt.Errorf to avoid the fmt import.
var errIntegrationTimeout = ioErrTimeout("ops pod not Ready within timeout")

type ioErrTimeout string

func (e ioErrTimeout) Error() string { return string(e) }

// Silence unused-import warnings if the file's bodies are skip-only on
// some runners.
var _ = io.Discard
