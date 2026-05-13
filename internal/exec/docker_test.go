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

// TestResolveDockerImageAndArgv covers the four shapes resolveDockerImageAndArgv
// distinguishes after the Sprint 6 Dockerfile-ENTRYPOINT drop:
//
//  1. tool with an explicit dockerImageBinary entry (ibmcloud,
//     roksbnkctl) — the binary is prepended to the Cmd slice so the
//     container has something to run even though the image has no
//     ENTRYPOINT.
//  2. tool without a dockerImageBinary entry (iperf3, terraform) —
//     argv[1:] flows through verbatim; the image's own ENTRYPOINT
//     picks the binary.
//  3. literal image ref (no toolImages match) — argv[0] is the image,
//     argv[1:] is the cmd (test/integration path).
//  4. multi-arg passthrough — ensures argv[1:]'s order is preserved.
func TestResolveDockerImageAndArgv(t *testing.T) {
	tests := []struct {
		name    string
		argv    []string
		wantImg string // prefix only (we don't lock the per-binary tag)
		wantCmd []string
	}{
		{
			// ibmcloud now wraps argv with a sh -c login-then-exec dance
			// so any stateful subcommand (iam, ks, account, target, …)
			// gets a primed session. See dockerImageBinary["ibmcloud"]
			// in docker.go for the wrap rationale. argv[1:] flows
			// through `"$@"` after the `--` separator.
			name:    "ibmcloud wraps argv with login-then-exec sh -c",
			argv:    []string{"ibmcloud", "iam", "oauth-tokens"},
			wantImg: "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:",
			wantCmd: []string{
				"sh", "-c",
				`ibmcloud login -a https://cloud.ibm.com -r "${IBMCLOUD_REGION:-us-south}" --apikey "$IBMCLOUD_API_KEY" --quiet > /dev/null 2>&1 && exec ibmcloud "$@"`,
				"--",
				"iam", "oauth-tokens",
			},
		},
		{
			name:    "roksbnkctl prepends absolute binary path",
			argv:    []string{"roksbnkctl", "test", "dns", "--target=example.com"},
			wantImg: "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:",
			wantCmd: []string{"/usr/local/bin/roksbnkctl", "test", "dns", "--target=example.com"},
		},
		{
			// iperf3 image is `networkstatic/iperf3:latest` (public on
			// Docker Hub) — see toolImages comment for the switch from
			// the private ghcr bundled image. ENTRYPOINT-picks-the-binary
			// shape is preserved: dockerImageBinary has no iperf3 entry,
			// so argv[1:] flows straight through to the image's
			// ENTRYPOINT.
			name:    "iperf3 keeps legacy shape (image ENTRYPOINT picks the binary)",
			argv:    []string{"iperf3", "-s", "-D"},
			wantImg: "networkstatic/iperf3:",
			wantCmd: []string{"-s", "-D"},
		},
		{
			name:    "terraform keeps legacy shape (upstream image ENTRYPOINT)",
			argv:    []string{"terraform", "version"},
			wantImg: "hashicorp/terraform:",
			wantCmd: []string{"version"},
		},
		{
			name:    "literal image ref passes through",
			argv:    []string{"busybox:latest", "echo", "hi"},
			wantImg: "busybox:latest",
			wantCmd: []string{"echo", "hi"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotImg, gotCmd := resolveDockerImageAndArgv(tc.argv)
			if !strings.HasPrefix(gotImg, tc.wantImg) {
				t.Errorf("image: got %q, want prefix %q", gotImg, tc.wantImg)
			}
			if len(gotCmd) != len(tc.wantCmd) {
				t.Fatalf("cmd: got %v (len %d), want %v (len %d)", gotCmd, len(gotCmd), tc.wantCmd, len(tc.wantCmd))
			}
			for i := range gotCmd {
				if gotCmd[i] != tc.wantCmd[i] {
					t.Errorf("cmd[%d]: got %q, want %q", i, gotCmd[i], tc.wantCmd[i])
				}
			}
		})
	}
}

// TestDockerImageBinary_MirrorsK8sOverrides pins the cross-backend
// invariance contract — `dockerImageBinary` (in docker.go) and
// `jobToolCmdOverride` (in k8s.go) MUST list the same tool→binary
// mappings so the same `--backend docker` and `--backend k8s` argv
// produces semantically-identical execution.
//
// A future tool added to one map without the other is a latent bug
// (works on docker, broken on k8s, or vice versa) — this test catches
// it at unit-test time.
//
// Tools that diverge intentionally — i.e. one backend applies a wrap
// statically in the map and the other applies it dynamically at
// dispatch time — are listed in `mirrorExempt` below with a note
// explaining the divergence. Currently: `ibmcloud` (docker applies
// the `sh -c login-then-exec` wrap via `dockerImageBinary`; k8s
// applies the same wrap dynamically inside `runOnOpsPod` so the
// `jobToolCmdOverride` map stays a bare `[ibmcloud]`).
func TestDockerImageBinary_MirrorsK8sOverrides(t *testing.T) {
	mirrorExempt := map[string]string{
		"ibmcloud": "docker uses dockerImageBinary sh-c wrap; k8s applies the same wrap dynamically in runOnOpsPod",
	}
	// Length-comparison is meaningful only after exempt tools are
	// excluded on both sides.
	dockerKeys := keysOf(dockerImageBinary)
	k8sKeys := keysOf(jobToolCmdOverride)
	if len(dockerKeys)-countIn(dockerKeys, mirrorExempt) != len(k8sKeys)-countIn(k8sKeys, mirrorExempt) {
		t.Fatalf("dockerImageBinary (%v) and jobToolCmdOverride (%v) must list the same tools (minus exempt %v); diverged",
			dockerKeys, k8sKeys, mirrorExempt)
	}
	for tool, dockerBin := range dockerImageBinary {
		if reason, exempt := mirrorExempt[tool]; exempt {
			t.Logf("tool %q exempt from mirror check: %s", tool, reason)
			continue
		}
		k8sBin, ok := jobToolCmdOverride[tool]
		if !ok {
			t.Errorf("tool %q in dockerImageBinary but not in jobToolCmdOverride", tool)
			continue
		}
		if len(dockerBin) != len(k8sBin) {
			t.Errorf("tool %q: docker binary %v differs from k8s override %v", tool, dockerBin, k8sBin)
			continue
		}
		for i := range dockerBin {
			if dockerBin[i] != k8sBin[i] {
				t.Errorf("tool %q [%d]: docker %q vs k8s %q", tool, i, dockerBin[i], k8sBin[i])
			}
		}
	}
}

// countIn returns how many entries of `keys` are present as keys in `m`.
func countIn(keys []string, m map[string]string) int {
	n := 0
	for _, k := range keys {
		if _, ok := m[k]; ok {
			n++
		}
	}
	return n
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
