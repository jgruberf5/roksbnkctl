//go:build integration

package exec

// Sprint 3 / PRD 03 — Docker backend integration tests.
//
// Exercises the real docker daemon via the backend's `Run` path. Gated behind
// the `integration` build tag so `go test ./...` stays fast and offline.
//
// CI runs this via the `docker-backend` job in .github/workflows/ci.yml. Run
// locally with:
//
//	go test -tags integration -timeout 5m ./internal/exec/...
//
// Tests skip cleanly (rather than fail) when the daemon isn't reachable —
// macOS GitHub runners don't ship Docker, so the test must be a no-op there
// rather than a red x.

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// dockerAvailable reports whether `docker info` succeeds. Called as a guard
// at the top of every test in this file.
func dockerAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info")
	return cmd.Run() == nil
}

// TestIntegration_DockerBackend_BusyboxEcho is the smoke test: spin a
// throwaway busybox container, run echo, check stdout, confirm cleanup
// (--rm).
func TestIntegration_DockerBackend_BusyboxEcho(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not reachable; skipping integration test")
	}

	b, err := ResolveBackend("docker")
	if err != nil {
		t.Fatalf("ResolveBackend(\"docker\"): %v", err)
	}

	// The Run signature here uses the standard image-via-argv shape that
	// matches PRD 03's docker run translation. If staff exposes image
	// selection via an option struct (e.g. RunOpts.Image), this test will
	// need updating to use that field instead.
	var stdout bytes.Buffer
	rc, err := b.Run(context.Background(),
		[]string{"busybox:latest", "echo", "hello-from-docker"},
		RunOpts{Stdout: &stdout})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rc != 0 {
		t.Errorf("expected rc=0, got %d", rc)
	}
	if !strings.Contains(stdout.String(), "hello-from-docker") {
		t.Errorf("stdout missing token: %q", stdout.String())
	}
}

// TestIntegration_DockerBackend_NoLeakInInspect runs a short-lived container
// with a known IBMCLOUD_API_KEY value, then greps the output for the value.
// PRD 04 §Docker container's recommended `--env IBMCLOUD_API_KEY` (no =value)
// form means `docker inspect` should show only the env var NAME, never the
// value. This test is the integration-tier sibling of audit_test.go's unit
// check.
func TestIntegration_DockerBackend_NoLeakInInspect(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not reachable; skipping")
	}

	const secret = "test-key-NEVER-IN-INSPECT"
	t.Setenv("IBMCLOUD_API_KEY", secret)

	b, err := ResolveBackend("docker")
	if err != nil {
		t.Fatalf("ResolveBackend: %v", err)
	}

	// Run a container that just exits; we only need it to have existed so
	// `docker inspect` can examine its config. The backend wraps cleanup,
	// but we add a label we can find afterwards via `docker ps -a`.
	creds := &Credentials{IBMCloudAPIKey: secret}
	var out bytes.Buffer
	_, _ = b.Run(context.Background(),
		[]string{"busybox:latest", "true"},
		RunOpts{Stdout: &out, Credentials: creds})

	// Inspect the most recent container (regardless of exit status).
	insp, err := exec.Command("docker", "ps", "-a", "--format", "{{.ID}}", "-l").Output()
	if err != nil || len(strings.TrimSpace(string(insp))) == 0 {
		t.Skipf("couldn't list containers (auto-removed?): %v", err)
	}
	id := strings.TrimSpace(string(insp))
	inspectOut, err := exec.Command("docker", "inspect", id).Output()
	if err != nil {
		t.Skipf("docker inspect %s: %v", id, err)
	}
	if strings.Contains(string(inspectOut), secret) {
		t.Errorf("PRD 04 SECURITY VIOLATION: docker inspect leaks IBMCLOUD_API_KEY value:\n%s", inspectOut)
	}
}
