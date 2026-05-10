//go:build integration

package exec

// Sprint 5 / PRD 03 §"terraform" — terraform docker integration tests.
//
// Live docker daemon round-trip against `hashicorp/terraform:<v>`:
//
//   - `terraform --version` reports cleanly (smoke check)
//   - bind-mount round-trip: write a marker file from inside the
//     container; assert the host can read it back (the PRD 05 §risks
//     UID/permission gotcha is what we're guarding against — if the
//     `--user $(id -u):$(id -g)` flag isn't set or is wrong, the file
//     ownership won't match and the host-side read in this test fails)
//   - UID match: the file written from inside is owned by the host's
//     `id -u` value (asserts the --user flag round-trip)
//
// Build tag: `integration && tfdocker` so the live-daemon path runs
// only when explicitly requested + after staff lands buildTerraformMounts.
//
// Run with:
//
//	go test -tags 'integration tfdocker' -timeout 5m -run 'IntegrationTerraform' ./internal/exec/...

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestIntegrationTerraform_DockerImage_VersionSmoke runs `terraform
// --version` inside the pinned terraform image. Mostly validates the
// image pulls + the entrypoint shape; if the daemon's offline or the
// image isn't pullable the test skips cleanly.
func TestIntegrationTerraform_DockerImage_VersionSmoke(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not reachable; skipping")
	}

	b, err := ResolveBackend("docker")
	if err != nil {
		t.Fatalf("ResolveBackend(\"docker\"): %v", err)
	}

	// Use the toolImages["terraform"] resolved literal pin.
	tfImage := toolImages["terraform"]
	if tfImage == "" {
		t.Fatal("toolImages[\"terraform\"] empty; staff hasn't landed the pin yet")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	rc, err := b.Run(ctx,
		[]string{tfImage, "--version"},
		RunOpts{Stdout: os.Stderr, Stderr: os.Stderr})
	if err != nil {
		t.Fatalf("terraform --version: %v", err)
	}
	if rc != 0 {
		t.Errorf("terraform --version exit code: got %d, want 0", rc)
	}
}

// TestIntegrationTerraform_DockerBindMount_RoundTrip is the Linux UID-
// match guard from PRD 05 §risks: write a file from inside the container
// (bind-mounted state dir), then read it back from the host. If --user
// isn't set (or is set wrong), the file ownership defaults to root and
// the host-side read fails.
func TestIntegrationTerraform_DockerBindMount_RoundTrip(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not reachable; skipping")
	}

	stateDir := t.TempDir()
	markerInside := "/state/marker.txt"

	b, err := ResolveBackend("docker")
	if err != nil {
		t.Fatalf("ResolveBackend: %v", err)
	}

	// The terraform image has `ENTRYPOINT ["terraform"]`, so it can't run
	// arbitrary shell commands. The bind-mount round-trip test is
	// image-agnostic — what we're actually asserting is the docker
	// backend's `--user $(id -u):$(id -g)` + HostMounts plumbing. Use
	// busybox (no entrypoint, `/bin/sh` available, also pre-loaded into
	// the kind cluster for the k8s integration job).
	const probeImage = "busybox:1.36"

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Use a shell argv to write the marker file. argv[0] is the literal
	// image (test-fallback path in resolveDockerImageAndArgv), argv[1:]
	// is the in-container cmd.
	// Staff's surface (Sprint 5): RunOpts.HostMounts + RunOpts.RunAsUser.
	// Mirror what internal/cli/lifecycle.go's terraform docker dispatch
	// builds:
	//   HostMounts: [{HostPath: stateDir, ContainerPath: "/state", ReadOnly: false}]
	//   RunAsUser:  "<uid>:<gid>"
	u, _ := user.Current()
	runAsUser := u.Uid + ":" + u.Gid
	rc, err := b.Run(ctx,
		[]string{probeImage, "/bin/sh", "-c", "echo round-trip > " + markerInside},
		RunOpts{
			Stdout:  os.Stderr,
			Stderr:  os.Stderr,
			WorkDir: "/state",
			HostMounts: []HostMount{{
				HostPath:      stateDir,
				ContainerPath: "/state",
				ReadOnly:      false,
			}},
			RunAsUser: runAsUser,
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rc != 0 {
		t.Errorf("rc: got %d, want 0", rc)
	}

	hostPath := filepath.Join(stateDir, "marker.txt")
	content, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("host-side ReadFile %s: %v (UID-mismatch? --user $(id -u):$(id -g) not set?)", hostPath, err)
	}
	if !strings.Contains(string(content), "round-trip") {
		t.Errorf("marker content: got %q, want 'round-trip'", string(content))
	}

	// UID-match assertion: the file should be owned by the running
	// host user, not by root (UID 0).
	st, err := os.Stat(hostPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	sys := st.Sys().(*syscall.Stat_t)
	wantUIDStr := u.Uid
	if !uidStringEqual(sys.Uid, wantUIDStr) {
		t.Errorf("file UID: got %d, want %s — --user $(id -u) didn't round-trip", sys.Uid, wantUIDStr)
	}
}

// uidStringEqual compares a numeric UID (from stat) against the string
// form (`os/user.User.Uid`).
func uidStringEqual(num uint32, s string) bool {
	// Cheap, no strconv import; we only ever expect numeric Uid strings.
	got := []byte{}
	for n := num; ; n /= 10 {
		got = append([]byte{byte('0' + n%10)}, got...)
		if n < 10 {
			break
		}
	}
	return string(got) == s
}
