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
	"fmt"
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

// (namespace helpers below)
// (mirrors what `roksbnkctl ops install` does). Tests that exercise the
// Job path need this to exist before runAsJob is invoked.
// ensureOpsNamespace creates K8sOpsNamespace if missing.
//
// The ops-pod path CANNOT be hermetic the way the Job path is: the long-lived
// ops pod is a singleton by design — `roksbnkctl ops install` puts it in a fixed
// namespace and `doctor` looks for it there — so the namespace is part of the
// contract rather than an implementation detail. The test compensates by owning
// and deleting the POD it creates, which is the state that actually leaks.
func ensureOpsNamespace(t *testing.T, cs kubernetes.Interface) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := cs.CoreV1().Namespaces().Get(ctx, K8sOpsNamespace, metav1.GetOptions{}); err == nil {
		return
	}
	_, _ = cs.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: K8sOpsNamespace}}, metav1.CreateOptions{})
}

// hermeticNamespace creates a namespace unique to THIS test run and deletes it
// afterwards, returning its name.
//
// The suite used to share two fixed namespaces (K8sOpsNamespace,
// K8sTestNamespace), creating them if absent and reusing them if present — so
// nothing was ever cleaned up and one run's leftovers changed the next run's
// result. The visible symptom was that the suite passed on a fresh kind cluster
// and failed on a reused one with `mkdir /home/runner/.bluemix: permission
// denied`, three layers from the cause (#73).
//
// That mattered more than an ordinary flake because reuse is the DOCUMENTED
// path: scripts/integration-test.sh reuses an existing cluster by design and
// KEEP_KIND=1 exists to encourage it, so the debug loop was the one that broke.
// And this runs as step 5 of `make release`, where a failure unrelated to the
// code teaches whoever is cutting the release to reach for
// SKIP_INTEGRATION_TEST=1.
//
// Named from the test name plus a nanosecond stamp so parallel runs, and reruns
// against a cluster whose previous namespace is still terminating, cannot
// collide.
func hermeticNamespace(t *testing.T, cs kubernetes.Interface) string {
	t.Helper()
	safe := strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	if len(safe) > 40 {
		safe = safe[:40]
	}
	ns := fmt.Sprintf("rbk-it-%s-%d", safe, time.Now().UnixNano()%1e9)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := cs.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating hermetic namespace %s: %v", ns, err)
	}

	t.Cleanup(func() {
		// Background context: t's deadline may already have expired, and a
		// namespace that outlives the run is the very thing this exists to
		// prevent. Best-effort — a failed delete must not fail a passing test,
		// but it is reported so it does not vanish silently.
		delCtx, delCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer delCancel()
		if err := cs.CoreV1().Namespaces().Delete(delCtx, ns, metav1.DeleteOptions{}); err != nil {
			t.Logf("WARNING: could not delete hermetic namespace %s: %v — a later run on this cluster may be affected", ns, err)
		}
	})
	return ns
}

// TestIntegration_K8sBackend_JobMode_Echo runs a no-op probe via the
// Job path. Sprint 9 / PRD 04 §"Resolved in Sprint 9" §"Trusted-profile
// auto-provisioning" carry-over (Option 1 from the v1.0.2 TODO): the
// test image switched from busybox:1.36 (USER root, fails runAsJob's
// strict `RunAsNonRoot: true` admission on clusters without an SCC
// mutating webhook) to the tools-ibmcloud image. That image carries
// `USER 1000` (Sprint 9 Dockerfile addition), so runAsJob's existing
// SecurityContext passes admission unchanged.
//
// argv shape: ["ibmcloud", "--version"]. The runAsJob path resolves
// argv[0]="ibmcloud" → toolImages["ibmcloud"] (the tools-ibmcloud
// image) and prepends the `ibmcloud` binary via jobToolCmdOverride.
// `ibmcloud --version` runs to completion in <1s and prints a
// stable banner: `ibmcloud <semver> (<commit>-<date>)` followed by
// `Copyright IBM Corp. <year>` — both stable across releases.
func TestIntegration_K8sBackend_JobMode_Echo(t *testing.T) {
	cs, cfg := k8sIntegrationClient(t)
	ns := hermeticNamespace(t, cs)

	b := &K8sBackend{
		client:       cs,
		config:       cfg,
		jobNamespace: ns,
		initFn:       func() (kubernetes.Interface, *rest.Config, error) { return cs, cfg, nil },
	}

	var stdout, stderr strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Use the tools-ibmcloud image (USER 1000 — passes admission with
	// runAsNonRoot=true) and the most stable subcommand (`--version`)
	// — no network round-trip, no IAM call, exits 0 in <1s.
	rc, err := b.Run(ctx,
		[]string{"ibmcloud", "--version"},
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
	// `ibmcloud --version` output shape is stable across releases:
	// `ibmcloud <semver> (<commit>-<date>)\nCopyright IBM Corp. <year>\n`.
	// Spot-check both halves so a banner-shape change in either line is
	// caught explicitly (the v1.2.1 cycle hit a one-word assertion drift
	// when the previous shape `"ibmcloud version <v>"` was assumed but
	// the real banner has no "version" word).
	got := stdout.String()
	if !strings.HasPrefix(got, "ibmcloud ") {
		t.Errorf("stdout missing 'ibmcloud <ver>' banner prefix: %q", got)
	}
	if !strings.Contains(got, "Copyright IBM") {
		t.Errorf("stdout missing 'Copyright IBM' banner second-line: %q", got)
	}
}

// TestIntegration_K8sBackend_OpsPodExec runs `echo hello` through the
// long-lived ops-pod exec path. Requires `roksbnkctl ops install` to
// have run (or this test to provision a sleep-infinity pod with
// equivalent labels).
func TestIntegration_K8sBackend_OpsPodExec(t *testing.T) {
	cs, cfg := k8sIntegrationClient(t)
	ensureOpsNamespace(t, cs)

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

// errIntegrationTimeout is a sentinel for the ensureOpsPodForTest poll
// loop. Inline error rather than fmt.Errorf to avoid the fmt import.
var errIntegrationTimeout = ioErrTimeout("ops pod not Ready within timeout")

type ioErrTimeout string

func (e ioErrTimeout) Error() string { return string(e) }

// Silence unused-import warnings if the file's bodies are skip-only on
// some runners.
var _ = io.Discard

// ptrInt64 is colocated with its sole caller (the SecurityContext
// builder above) so it lives under the `integration` build tag. The
// sibling ptrBool helper in k8s.go is used by production code; this
// one is used by tests only — keeping it here means staticcheck on
// the default build doesn't flag it as U1000 unused.
func ptrInt64(i int64) *int64 { return &i }

// Review of #155, finding 7. opts.Files has no production caller, so the
// per-Job Secret and its owner-ref had zero coverage against a real API server
// — which is exactly how a patch that OpenShift rejects could sit here
// unnoticed. A fake clientset would not have caught it either: fakes do not run
// admission plugins.
//
// This drives the real path end to end: Secret created, owner-ref patched by a
// real apiserver, and the Secret collected when the Job goes away.
func TestIntegration_K8sBackend_FilesSecretIsOwnedByItsJob(t *testing.T) {
	cs, cfg := k8sIntegrationClient(t)
	ns := hermeticNamespace(t, cs)

	b := &K8sBackend{
		client:       cs,
		config:       cfg,
		jobNamespace: ns,
		initFn:       func() (kubernetes.Interface, *rest.Config, error) { return cs, cfg, nil },
	}

	var stdout, stderr strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rc, err := b.Run(ctx,
		[]string{"ibmcloud", "--version"},
		RunOpts{
			Stdout: &noopBuilder{&stdout},
			Stderr: &noopBuilder{&stderr},
			Files:  map[string][]byte{"creds.json": []byte(`{"apikey":"not-a-real-key"}`)},
		})
	if err != nil {
		t.Logf("Run error (rc=%d): %v", rc, err)
	}
	if rc != 0 {
		t.Errorf("expected rc=0, got %d (stderr=%q)", rc, stderr.String())
	}

	// The owner-ref is the whole point: without it nothing deletes this Secret
	// on a successful run. A warning on the caller's stderr means the patch was
	// rejected — the failure mode this test exists to catch.
	if strings.Contains(stderr.String(), "could not owner-ref secret") {
		t.Errorf("the API server rejected the owner-ref patch:\n%s", stderr.String())
	}

	secrets, lerr := cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "roksbnkctl.io/job",
	})
	if lerr != nil {
		t.Fatalf("listing files secrets: %v", lerr)
	}
	for _, s := range secrets.Items {
		if len(s.OwnerReferences) == 0 {
			t.Errorf("secret %s/%s has no owner reference, so nothing will ever delete it",
				s.Namespace, s.Name)
			continue
		}
		or := s.OwnerReferences[0]
		if or.Kind != "Job" {
			t.Errorf("secret %s is owned by a %s, not a Job", s.Name, or.Kind)
		}
		// blockOwnerDeletion is what OpenShift's
		// OwnerReferencesPermissionEnforcement rejects, and it is not needed
		// for the Secret to be collected.
		if or.BlockOwnerDeletion != nil && *or.BlockOwnerDeletion {
			t.Errorf("secret %s sets blockOwnerDeletion; it is unnecessary here and is "+
				"refused on OpenShift without jobs/finalizers access", s.Name)
		}
	}
}
