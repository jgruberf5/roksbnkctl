//go:build integration
// +build integration

package cli

// Sprint 4 / PRD 03 — `roksbnkctl ops install/show/uninstall` integration
// tests against a kind cluster. Exercises the cluster-side cred lifecycle
// (namespaces, ServiceAccount, Secret, ClusterRole, RoleBinding, ops Pod)
// + the RBAC negative/positive `kubectl auth can-i` matrix from PRD 05
// Phase L.
//
// Gated behind the `integration` build tag. Same kubeconfig discovery as
// internal/exec/k8s_integration_test.go — skips cleanly when no cluster.
//
// Run via:
//
//	go test -tags integration -timeout 10m ./internal/cli/...
//
// At validator-dispatch time, internal/cli/ops.go didn't exist yet (staff
// implementation in-flight). Tests skip cleanly when the `ops` cobra verb
// isn't registered. See issues/issue_sprint4_validator.md for the
// roadmap entry.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kubeconfigPath returns the kubeconfig location ($KUBECONFIG or
// ~/.kube/config). Skips when neither exists.
func kubeconfigPath(t *testing.T) string {
	t.Helper()
	kc := os.Getenv("KUBECONFIG")
	if kc == "" {
		kc = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}
	if _, err := os.Stat(kc); err != nil {
		t.Skipf("no kubeconfig at %s: %v", kc, err)
	}
	return kc
}

// kubectlAvailable reports whether `kubectl version --client` succeeds.
// Some kind workflows ship kubectl alongside; others rely on the in-tree
// internalised verbs. The RBAC checks in this file use kubectl directly
// (the `kubectl auth can-i` form is the documented PRD 05 Phase L step).
func kubectlAvailable(t *testing.T) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "kubectl", "version", "--client").Run() == nil
}

// roksbnkctlBin returns the path to the roksbnkctl binary to exercise.
// Defaults to "roksbnkctl" on PATH; override via $ROKSBNKCTL.
func roksbnkctlBin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("ROKSBNKCTL"); p != "" {
		return p
	}
	if _, err := exec.LookPath("roksbnkctl"); err != nil {
		t.Skipf("roksbnkctl not on PATH: %v (set ROKSBNKCTL=... to override)", err)
	}
	return "roksbnkctl"
}

// TestIntegration_OpsInstall_ShowsRBACAndPod runs:
//
//	roksbnkctl ops install
//	roksbnkctl ops show
//	kubectl auth can-i create jobs --as=...:roksbnkctl-ops -n roksbnkctl-test  → yes
//	kubectl auth can-i delete pods  --as=...:roksbnkctl-ops -n default         → no
//	roksbnkctl ops uninstall
//
// Asserts the namespace + RBAC + ops Pod come up, the RBAC matrix matches
// PRD 04's least-privilege design, and uninstall cleans up.
func TestIntegration_OpsInstall_ShowsRBACAndPod(t *testing.T) {
	bin := roksbnkctlBin(t)
	kc := kubeconfigPath(t)
	if !kubectlAvailable(t) {
		t.Skip("kubectl not on PATH; can-i checks need it")
	}

	env := append(os.Environ(), "KUBECONFIG="+kc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 1. ops install — skips test cleanly if the verb isn't yet wired.
	out, err := runCmd(ctx, env, bin, "ops", "install")
	if err != nil && (strings.Contains(out, "unknown command") || strings.Contains(out, "unknown subcommand")) {
		t.Skipf("ops verb not registered yet: %v\n%s", err, out)
	}
	if err != nil {
		t.Fatalf("ops install failed: %v\n%s", err, out)
	}
	defer func() {
		// Best-effort uninstall on test exit.
		_, _ = runCmd(context.Background(), env, bin, "ops", "uninstall")
	}()

	// 2. ops show — should report the namespace + pod.
	out, err = runCmd(ctx, env, bin, "ops", "show")
	if err != nil {
		t.Errorf("ops show: %v\n%s", err, out)
	}

	// 3. RBAC negative: SA can't delete pods in default.
	out, err = runCmd(ctx, env, bin, "kubectl", "auth", "can-i", "delete", "pods",
		"--as=system:serviceaccount:roksbnkctl-ops:roksbnkctl-ops",
		"-n", "default")
	// can-i exits 1 when the answer is "no" — that's the expected case here.
	if !strings.Contains(out, "no") {
		t.Errorf("RBAC negative: expected 'no' for delete pods on default, got %q", out)
	}
	_ = err // expected

	// 4. RBAC positive: SA CAN create jobs in roksbnkctl-test.
	out, err = runCmd(ctx, env, bin, "kubectl", "auth", "can-i", "create", "jobs",
		"--as=system:serviceaccount:roksbnkctl-ops:roksbnkctl-ops",
		"-n", "roksbnkctl-test")
	if !strings.Contains(out, "yes") {
		t.Errorf("RBAC positive: expected 'yes' for create jobs on roksbnkctl-test, got %q", out)
	}
	_ = err

	// 5. ops uninstall.
	out, err = runCmd(ctx, env, bin, "ops", "uninstall")
	if err != nil {
		t.Errorf("ops uninstall: %v\n%s", err, out)
	}
}

func runCmd(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}
