//go:build live
// +build live

// Golden-file byte-equivalence tests for `roksbnkctl k get -o yaml`.
//
// These tests run against a real Kubernetes cluster (KUBECONFIG must be
// set). They compare the output of `roksbnkctl k get <resource> -o yaml`
// against `kubectl get <resource> -o yaml` and assert byte equivalence
// modulo timestamp + version fields that are necessarily transient.
//
// Run with:
//
//	make test-live
//	# or
//	go test -tags live -timeout 5m ./internal/k8s/...
//
// The tests skip cleanly (rather than fail) when:
//
//   - $KUBECONFIG (or ~/.kube/config) doesn't exist
//   - kubectl isn't on PATH (we need it for the comparison side)
//   - the cluster isn't reachable
//
// PRD 02 §Acceptance criteria requires byte equivalence for Node, Pod,
// Service, ConfigMap. We expose one TestGolden_* per resource so a
// single flake is easy to localise.

package k8s

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const liveTimeout = 60 * time.Second

// goldenSetup checks preconditions and returns the kubeconfig path. It
// SkipNow's the test rather than failing if the prerequisites for a
// live run aren't met — golden tests are intended for the integrator
// pre-tag, not for every contributor's local laptop.
func goldenSetup(t *testing.T) string {
	t.Helper()
	kubeconfig := DefaultKubeconfigPath()
	if kubeconfig == "" {
		t.Skip("no kubeconfig (set $KUBECONFIG or run on a host with ~/.kube/config)")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not on PATH (required for the comparison side)")
	}
	return kubeconfig
}

// runRoksbnkctl invokes the bin under test (assumed on PATH or in
// $ROKSBNKCTL) and returns its stdout. Returns an error on non-zero
// exit so the caller can surface the cluster's error message.
func runRoksbnkctl(ctx context.Context, args ...string) (string, error) {
	bin := os.Getenv("ROKSBNKCTL")
	if bin == "" {
		bin = "roksbnkctl"
	}
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return "", fmt.Errorf("running %s %v: %w", bin, args, err)
	}
	return string(out), nil
}

func runKubectl(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "kubectl", args...).Output()
	if err != nil {
		return "", fmt.Errorf("running kubectl %v: %w", args, err)
	}
	return string(out), nil
}

// stripVolatileFields removes YAML lines that necessarily differ
// between two snapshots of the same resource: managedFields,
// resourceVersion, creationTimestamp, generation, uid. These aren't
// what byte equivalence is testing — we want to catch real divergence
// (different field ordering, extra fields, formatting).
//
// PRD 02's "(modulo timestamps)" wording is realised here.
func stripVolatileFields(s string) string {
	var out []string
	skipBlock := false
	skipBlockIndent := -1

	scanner := bufio.NewScanner(strings.NewReader(s))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()

		// Determine indent (number of leading spaces).
		indent := 0
		for indent < len(line) && line[indent] == ' ' {
			indent++
		}

		// If we're inside a managedFields block, skip until the indent
		// drops back to the block's parent.
		if skipBlock {
			if strings.TrimSpace(line) == "" {
				continue
			}
			if indent > skipBlockIndent {
				continue
			}
			skipBlock = false
			// fall through and re-evaluate this line
		}

		trimmed := strings.TrimSpace(line)
		// Strip top-level volatile keys.
		if strings.HasPrefix(trimmed, "managedFields:") {
			skipBlock = true
			skipBlockIndent = indent
			continue
		}
		if strings.HasPrefix(trimmed, "resourceVersion:") ||
			strings.HasPrefix(trimmed, "creationTimestamp:") ||
			strings.HasPrefix(trimmed, "generation:") ||
			strings.HasPrefix(trimmed, "uid:") ||
			strings.HasPrefix(trimmed, "selfLink:") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// assertGoldenEqual is the shared comparator. Failure dumps both
// outputs to t.Logf for ease of diffing.
func assertGoldenEqual(t *testing.T, label, kubectlOut, roksOut string) {
	t.Helper()
	a := stripVolatileFields(kubectlOut)
	b := stripVolatileFields(roksOut)
	if a != b {
		t.Errorf("%s: byte-equivalence diff (volatile fields stripped)", label)
		// Surface a small diff hint — full streams to the test log.
		t.Logf("kubectl output (first 800 bytes):\n%s", truncate(a, 800))
		t.Logf("roksbnkctl output (first 800 bytes):\n%s", truncate(b, 800))
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n…(truncated)\n"
	}
	return s
}

// TestGolden_GetNodes_YAML — PRD 02 acceptance criterion #1.
func TestGolden_GetNodes_YAML(t *testing.T) {
	_ = goldenSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	kub, err := runKubectl(ctx, "get", "nodes", "-o", "yaml")
	if err != nil {
		t.Skipf("kubectl get nodes failed (cluster unreachable?): %v", err)
	}
	rok, err := runRoksbnkctl(ctx, "k", "get", "nodes", "-o", "yaml")
	if err != nil {
		t.Fatalf("roksbnkctl k get nodes failed: %v", err)
	}
	assertGoldenEqual(t, "nodes -o yaml", kub, rok)
}

// TestGolden_GetPods_YAML — covers namespaced resource.
func TestGolden_GetPods_YAML(t *testing.T) {
	_ = goldenSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	// Use kube-system as a namespace that's almost always present and
	// has at least one pod (kube-proxy, coredns, etc.).
	kub, err := runKubectl(ctx, "get", "pods", "-n", "kube-system", "-o", "yaml")
	if err != nil {
		t.Skipf("kubectl get pods -n kube-system failed: %v", err)
	}
	rok, err := runRoksbnkctl(ctx, "k", "get", "pods", "-n", "kube-system", "-o", "yaml")
	if err != nil {
		t.Fatalf("roksbnkctl k get pods failed: %v", err)
	}
	assertGoldenEqual(t, "pods -n kube-system -o yaml", kub, rok)
}

// TestGolden_GetServices_YAML
func TestGolden_GetServices_YAML(t *testing.T) {
	_ = goldenSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	kub, err := runKubectl(ctx, "get", "services", "-n", "default", "-o", "yaml")
	if err != nil {
		t.Skipf("kubectl get services failed: %v", err)
	}
	rok, err := runRoksbnkctl(ctx, "k", "get", "services", "-n", "default", "-o", "yaml")
	if err != nil {
		t.Fatalf("roksbnkctl k get services failed: %v", err)
	}
	assertGoldenEqual(t, "services -n default -o yaml", kub, rok)
}

// TestGolden_GetConfigMaps_YAML
func TestGolden_GetConfigMaps_YAML(t *testing.T) {
	_ = goldenSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	kub, err := runKubectl(ctx, "get", "configmaps", "-n", "kube-system", "-o", "yaml")
	if err != nil {
		t.Skipf("kubectl get configmaps failed: %v", err)
	}
	rok, err := runRoksbnkctl(ctx, "k", "get", "configmaps", "-n", "kube-system", "-o", "yaml")
	if err != nil {
		t.Fatalf("roksbnkctl k get configmaps failed: %v", err)
	}
	assertGoldenEqual(t, "configmaps -n kube-system -o yaml", kub, rok)
}

// TestGolden_GetNodes_Name verifies the -o name format matches
// kubectl's "<resource>/<name>" line shape.
func TestGolden_GetNodes_Name(t *testing.T) {
	_ = goldenSetup(t)
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	kub, err := runKubectl(ctx, "get", "nodes", "-o", "name")
	if err != nil {
		t.Skipf("kubectl get nodes -o name failed: %v", err)
	}
	rok, err := runRoksbnkctl(ctx, "k", "get", "nodes", "-o", "name")
	if err != nil {
		t.Fatalf("roksbnkctl k get nodes -o name failed: %v", err)
	}
	if strings.TrimSpace(kub) != strings.TrimSpace(rok) {
		t.Errorf("nodes -o name diff:\nkubectl:\n%s\nroksbnkctl:\n%s", kub, rok)
	}
}
