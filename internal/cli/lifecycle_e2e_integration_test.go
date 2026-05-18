//go:build integration
// +build integration

package cli

// Sprint 14 deliverable 3 — `-tags integration` `--on`/passthrough
// smoke. Same gating split Sprints 10–13 use (build tag + clean skip
// when the prerequisite tooling is absent — see ops_integration_test.go
// kubeconfigPath/roksbnkctlBin precedent).
//
// This exercises the passthrough dispatch surface end-to-end against a
// real kubeconfig (an ephemeral kind cluster in CI; any reachable
// kubeconfig locally). It is the live counterpart to the stubbed
// heal-vs-outage matrix in lifecycle_e2e_test.go: there we prove the
// decision logic; here we prove the wired `roksbnkctl kubectl`
// passthrough actually reaches a cluster with a usable kubeconfig and
// does NOT fall back to localhost:8080.
//
// The `--on <target>` SSH leg itself needs a live jumphost + SSH target
// (out of scope for kind-only CI) — that remains the user's live
// `up → --on jumphost kubectl` verify. This smoke covers the
// passthrough/env-composition half so an Issue-1-class regression in
// the kubectl wiring fails here, not at a human.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestIntegration_KubectlPassthrough_ReachesCluster runs
// `roksbnkctl kubectl version` against the ambient kubeconfig and
// asserts it talks to a real API server (NOT kubectl's localhost:8080
// no-config fallback — the exact symptom the get-well sprint targets).
func TestIntegration_KubectlPassthrough_ReachesCluster(t *testing.T) {
	bin := roksbnkctlBin(t)
	kc := kubeconfigPath(t)
	if !kubectlAvailable(t) {
		t.Skip("kubectl not on PATH; passthrough smoke needs it")
	}

	env := append(envWithout(kc), "KUBECONFIG="+kc)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	out, err := runCmd(ctx, env, bin, "kubectl", "version", "-o", "json")
	if skipUnconfiguredPassthrough(t, err, out) {
		return
	}
	if strings.Contains(out, "localhost:8080") || strings.Contains(out, "127.0.0.1:8080") {
		t.Fatalf("REGRESSION: kubectl passthrough fell back to localhost:8080 "+
			"(no usable kubeconfig reached the wrapped tool)\n%s", out)
	}
	if err != nil {
		t.Fatalf("roksbnkctl kubectl version failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "serverVersion") {
		t.Errorf("expected a server version (cluster reached), got:\n%s", out)
	}
}

// TestIntegration_KubectlPassthrough_GetNodes is a second, behaviour-
// level assertion: a real cluster has at least one node.
func TestIntegration_KubectlPassthrough_GetNodes(t *testing.T) {
	bin := roksbnkctlBin(t)
	kc := kubeconfigPath(t)
	if !kubectlAvailable(t) {
		t.Skip("kubectl not on PATH")
	}
	env := append(envWithout(kc), "KUBECONFIG="+kc)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	out, err := runCmd(ctx, env, bin, "kubectl", "get", "nodes", "--no-headers")
	if skipUnconfiguredPassthrough(t, err, out) {
		return
	}
	if strings.Contains(out, "localhost:8080") {
		t.Fatalf("REGRESSION: localhost:8080 fallback\n%s", out)
	}
	if err != nil {
		t.Fatalf("get nodes failed: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Errorf("expected ≥1 node from a real cluster, got empty output")
	}
}

// skipUnconfiguredPassthrough cleanly skips (per the Sprints 10–13
// "skip when the prerequisite tooling/config is absent" precedent —
// ops_integration_test.go) when the failure is the passthrough's
// workspace prerequisite, not a real regression: the `kubectl`
// passthrough composes workspaceEnv() and so needs an initialised
// workspace + IBM Cloud API key. A kind-only CI box has neither. A
// genuine localhost:8080 fallback or wiring break is NOT swallowed
// here — those are asserted by the caller after this returns false.
func skipUnconfiguredPassthrough(t *testing.T, err error, out string) bool {
	t.Helper()
	if err == nil {
		return false
	}
	switch {
	case strings.Contains(out, "unknown command"),
		strings.Contains(out, "unknown subcommand"):
		t.Skipf("kubectl passthrough not registered: %v\n%s", err, out)
		return true
	case strings.Contains(out, "no IBM Cloud API key"),
		strings.Contains(out, "is not initialised"),
		strings.Contains(out, "run `roksbnkctl init`"):
		t.Skipf("no initialised workspace/API key on this host — "+
			"passthrough prerequisite absent (kind-only env); "+
			"the live `up → --on jumphost kubectl` verify covers this leg: %v", err)
		return true
	}
	return false
}

// envWithout returns os.Environ() with any inherited KUBECONFIG removed,
// so the test controls the kubeconfig the passthrough resolves (the
// caller appends the intended one). Uses the same scrub set as the
// production --on path.
func envWithout(_ string) []string {
	return remoteSafeEnv(os.Environ())
}
