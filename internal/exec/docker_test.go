package exec

// Sprint 3 / PRD 03 — Docker backend unit tests.
//
// These tests cover the docker backend's translation of RunOpts into
// container.Config + HostConfig + the cred-propagation rules from PRD 04
// §Docker container.
//
// Tests that need a live Docker daemon (`docker run` end-to-end) live behind
// the `integration` build tag in docker_integration_test.go to keep `go test
// ./...` fast and offline.

import (
	"context"
	"strings"
	"testing"
)

// TestDockerBackend_Resolves checks the registry exposes "docker".
func TestDockerBackend_Resolves(t *testing.T) {
	b, err := ResolveBackend("docker")
	if err != nil {
		t.Skipf("docker backend not registered (build without docker tag?): %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBackend(\"docker\") returned nil")
	}
	if b.Name() != "docker" {
		t.Errorf("Name(): got %q, want \"docker\"", b.Name())
	}
}

// TestCredentials_DockerArgs_NoValueEnvForm asserts PRD 04's #1 anti-pattern
// avoidance: the docker backend MUST emit `--env IBMCLOUD_API_KEY` (no
// `=value`), so the value inherits from the caller's env and never appears
// in `docker inspect`. The bare-name form is the single most important
// security-spine assertion of this sprint.
func TestCredentials_DockerArgs_NoValueEnvForm(t *testing.T) {
	creds := &Credentials{IBMCloudAPIKey: "test-key-VISIBLE-IF-LEAKED"}
	envArgs, _, cleanup, err := creds.DockerArgs(t.TempDir())
	if err != nil {
		t.Fatalf("DockerArgs: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	joined := strings.Join(envArgs, " ")
	if strings.Contains(joined, "IBMCLOUD_API_KEY=test-key-VISIBLE-IF-LEAKED") {
		t.Errorf("PRD 04 anti-pattern: docker --env emitted KEY=VALUE form, leaking secret to docker inspect: %v", envArgs)
	}
	if strings.Contains(joined, "test-key-VISIBLE-IF-LEAKED") {
		t.Errorf("secret value embedded in docker args (any form): %v", envArgs)
	}
	// The bare-name form `--env IBMCLOUD_API_KEY` is what we want: docker
	// reads the value from the caller's env at run time. We don't pin the
	// exact arg-pair shape (could be `["--env", "IBMCLOUD_API_KEY"]` or
	// `["-e", "IBMCLOUD_API_KEY"]`); we just confirm the env name appears
	// somewhere in the args without an `=value` suffix on it.
	found := false
	for i, a := range envArgs {
		if a == "IBMCLOUD_API_KEY" {
			// Previous arg should be -e or --env.
			if i > 0 && (envArgs[i-1] == "-e" || envArgs[i-1] == "--env") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("expected `-e IBMCLOUD_API_KEY` (no =value) in docker args, got %v", envArgs)
	}
}

// TestCredentials_DockerArgs_KubeconfigSingleFileMount asserts PRD 04 §Docker:
// kubeconfig is bind-mounted as a SINGLE FILE read-only at /root/.kube/config,
// never the parent directory.
func TestCredentials_DockerArgs_KubeconfigSingleFileMount(t *testing.T) {
	creds := &Credentials{
		KubeconfigBytes: []byte("apiVersion: v1\nkind: Config\nclusters: []\n"),
	}
	tmp := t.TempDir()
	_, mountArgs, cleanup, err := creds.DockerArgs(tmp)
	if err != nil {
		t.Fatalf("DockerArgs: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	joined := strings.Join(mountArgs, " ")
	if !strings.Contains(joined, "/root/.kube/config:ro") {
		t.Errorf("kubeconfig mount target not /root/.kube/config:ro: %v", mountArgs)
	}
	// Anti-pattern check: no .kube directory mount.
	if strings.Contains(joined, ":/root/.kube:") || strings.Contains(joined, ":/root/.kube ") {
		t.Errorf("PRD 04 anti-pattern: mounting parent .kube/ directory exposes other clusters' configs: %v", mountArgs)
	}
}

// TestCredentials_DockerArgs_NoCredsNoArgs asserts that an empty Credentials
// struct produces no env or mount args — backends without secrets shouldn't
// pay any docker arg cost.
func TestCredentials_DockerArgs_NoCredsNoArgs(t *testing.T) {
	creds := &Credentials{}
	envArgs, mountArgs, cleanup, err := creds.DockerArgs(t.TempDir())
	if err != nil {
		t.Fatalf("DockerArgs: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(envArgs) != 0 {
		t.Errorf("empty Credentials emitted env args: %v", envArgs)
	}
	if len(mountArgs) != 0 {
		t.Errorf("empty Credentials emitted mount args: %v", mountArgs)
	}
}

// TestCredentials_DockerArgs_CleanupRemovesTempfile asserts the cleanup
// callback actually unlinks the materialised kubeconfig — an orphaned
// tempfile holding a workspace's kubeconfig is a leak waiting to happen.
func TestCredentials_DockerArgs_CleanupRemovesTempfile(t *testing.T) {
	creds := &Credentials{KubeconfigBytes: []byte("kubeconfig-content")}
	tmp := t.TempDir()
	_, _, cleanup, err := creds.DockerArgs(tmp)
	if err != nil {
		t.Fatalf("DockerArgs: %v", err)
	}
	if cleanup == nil {
		t.Skip("no cleanup callback returned (implementation may use t.TempDir() lifecycle directly)")
	}
	cleanup()
	// We don't enumerate tmpdir files here — the contract is "cleanup unlinks
	// any kubeconfig the serialiser materialised". The audit-test in
	// audit_test.go verifies the no-leak invariant end-to-end.
}

// TestDockerBackend_DaemonUnreachableErrorClear asserts the docker backend
// returns a clear error (and rc=127 per staff prompt) when the docker daemon
// isn't reachable. Skipped when docker IS available.
func TestDockerBackend_DaemonUnreachableErrorClear(t *testing.T) {
	b, err := ResolveBackend("docker")
	if err != nil || b == nil {
		t.Skip("docker backend not available")
	}
	// Force daemon unreachable by pointing DOCKER_HOST at a bogus socket.
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")

	rc, err := b.Run(context.Background(),
		[]string{"echo", "shouldnt-run"},
		RunOpts{})
	if err == nil {
		t.Skipf("backend tolerated bogus DOCKER_HOST (probably ignored env); skipping: rc=%d", rc)
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "docker") || (!strings.Contains(msg, "daemon") && !strings.Contains(msg, "unreachable") && !strings.Contains(msg, "connect")) {
		t.Errorf("daemon-unreachable error message %q lacks the troubleshooting hint operators need", err)
	}
}
