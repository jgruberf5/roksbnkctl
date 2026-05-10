//go:build integration
// +build integration

// Package remote integration tests — exercise the SSH client against a
// real openssh-server container via testcontainers-go.
//
// Gated behind the `integration` build tag so the default `go test ./...`
// suite stays fast and Docker-free. Run with:
//
//	go test -tags integration ./internal/remote/...
//	# or
//	make test-integration
//
// Each test stands up a fresh sshd container, generates an ed25519 keypair,
// installs the public key into the container's authorized_keys, and connects
// via the staff-engineer's `internal/remote.Client`. Containers are torn
// down via t.Cleanup; a leak indicates a test bug, not a Docker problem.
//
// Requires Docker daemon access. On Linux GitHub runners this is provided by
// default; on macOS/Windows runners it is not (see ci.yml — integration job
// is Linux-only).
//
// API expectations: this file references the package's `Connect`, `Client`,
// `RunOpts`, and `Target` per the PRD 01 contract. If staff's final API
// names differ (e.g. `Dial` vs `Connect`, `ExecOpts` vs `RunOpts`), this
// file is the single point of update.
package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"golang.org/x/crypto/ssh"
)

// sshFixture is one running openssh-server container plus the keys/target
// needed to talk to it. Shared by every test case.
type sshFixture struct {
	container testcontainers.Container
	host      string
	port      int
	user      string
	signer    ssh.Signer
	keyPath   string // PEM file on disk; populated for tests that need a key file
}

// startSSHContainer spins up `linuxserver/openssh-server`, generates a fresh
// ed25519 keypair, and configures the container to accept it for the chosen
// user. Returns a fixture with the port mapping resolved + the key written
// to a tempfile (so callers can build a Target from disk if they prefer).
func startSSHContainer(ctx context.Context, t *testing.T) *sshFixture {
	t.Helper()

	// Generate ed25519 keypair on the fly. Each test gets a fresh key —
	// no shared fixtures, no pollution from previous runs.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh.NewPublicKey: %v", err)
	}
	authorizedKey := strings.TrimRight(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")

	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("ssh.NewSignerFromKey: %v", err)
	}

	// Marshal the private half to PEM so tests that exercise the
	// file-key-source path have something on disk to point at.
	pemBlock, err := ssh.MarshalPrivateKey(priv, "roksbnkctl-integration")
	if err != nil {
		t.Fatalf("ssh.MarshalPrivateKey: %v", err)
	}
	keyPEM := pem.EncodeToMemory(pemBlock)
	keyFile := fmt.Sprintf("%s/test_ed25519", t.TempDir())
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	// linuxserver/openssh-server reads PUBLIC_KEY (and PUBLIC_KEY_FILE)
	// at startup; whatever we hand in becomes authorized_keys for USER_NAME.
	// PASSWORD_ACCESS=false enforces key-only auth — matches what `ibmcloud`
	// jumphosts ship and what we want to validate against.
	user := "testuser"
	req := testcontainers.ContainerRequest{
		Image:        "lscr.io/linuxserver/openssh-server:latest",
		ExposedPorts: []string{"2222/tcp"},
		Env: map[string]string{
			"PUID":            "1000",
			"PGID":            "1000",
			"TZ":              "Etc/UTC",
			"USER_NAME":       user,
			"USER_PASSWORD":   "ignored-key-only",
			"PASSWORD_ACCESS": "false",
			"SUDO_ACCESS":     "false",
			"PUBLIC_KEY":      authorizedKey,
		},
		WaitingFor: wait.ForListeningPort("2222/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start container: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort cleanup; if termination fails, t.Cleanup logs are
		// the user's only signal — don't t.Fatal here (would shadow the
		// real test failure).
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = c.Terminate(shutdownCtx)
	})

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}

	return &sshFixture{
		container: c,
		host:      host,
		port:      int(mapped.Num()),
		user:      user,
		signer:    signer,
		keyPath:   keyFile,
	}
}

// target builds the *Target struct (per staff's targets.go shape) the SSH
// client expects. Constructed in-memory rather than via LoadTarget() — these
// tests are about the connect/run path, not the workspace YAML round-trip
// (covered by targets_test.go in the unit suite).
//
// Wires an Insecure-mode HostKeyCallback so the first connect against a
// fresh container is silent (the container's host key was just generated
// at startup; no operator can have pre-pinned it). Each test gets its own
// known_hosts file via t.Setenv(ROKSBNKCTL_HOME, t.TempDir()) — see
// callers below.
func (f *sshFixture) target() *Target {
	return &Target{
		Name:            "integration",
		Host:            f.host,
		Port:            f.port,
		User:            f.user,
		Signer:          f.signer,
		HostKeyCallback: HostKeyCallback(HostKeyOptions{Insecure: true}),
	}
}

// TestIntegration_Connect_Whoami covers the happy-path round-trip:
// connect → run → output → exit zero. If this fails everything else is
// noise, so it's first.
func TestIntegration_Connect_Whoami(t *testing.T) {
	t.Setenv("ROKSBNKCTL_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fix := startSSHContainer(ctx, t)

	client, err := Connect(ctx, fix.target())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	var out, errb bytes.Buffer
	rc, err := client.Run(ctx, []string{"whoami"}, RunOpts{Stdout: &out, Stderr: &errb})
	if err != nil {
		t.Fatalf("Run: %v (stderr=%q)", err, errb.String())
	}
	if rc != 0 {
		t.Errorf("exit code = %d, want 0", rc)
	}
	got := strings.TrimSpace(out.String())
	if got != fix.user {
		t.Errorf("whoami = %q, want %q", got, fix.user)
	}
}

// TestIntegration_ExitCode_Propagates ensures non-zero exit codes from the
// remote process flow through Run unchanged. PRD 01's "remote command
// failed → pass through the remote process's exit code unchanged" clause.
func TestIntegration_ExitCode_Propagates(t *testing.T) {
	t.Setenv("ROKSBNKCTL_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fix := startSSHContainer(ctx, t)

	client, err := Connect(ctx, fix.target())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	rc, err := client.Run(ctx, []string{"sh", "-c", "exit 7"}, RunOpts{})
	if err != nil {
		// `ssh` clients typically return *ssh.ExitError on non-zero;
		// staff's wrapper should swallow that and return only the rc.
		// If err is non-nil here, the wrapper isn't doing its job.
		t.Fatalf("Run returned err for non-zero exit (should swallow): %v", err)
	}
	if rc != 7 {
		t.Errorf("exit code = %d, want 7", rc)
	}
}

// TestIntegration_Stdout_StreamsAllLines validates ordered streaming: 100
// numbered lines arrive in order, none dropped, none duplicated. Catches
// the worst SSH bugs (line-buffering misconfigurations, partial reads,
// goroutine reorderings) at low cost.
func TestIntegration_Stdout_StreamsAllLines(t *testing.T) {
	t.Setenv("ROKSBNKCTL_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fix := startSSHContainer(ctx, t)

	client, err := Connect(ctx, fix.target())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	var out bytes.Buffer
	rc, err := client.Run(ctx, []string{"seq", "1", "100"}, RunOpts{Stdout: &out})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rc != 0 {
		t.Fatalf("exit = %d, want 0", rc)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 100 {
		t.Fatalf("got %d lines, want 100", len(lines))
	}
	for i, line := range lines {
		want := fmt.Sprintf("%d", i+1)
		if line != want {
			t.Errorf("line %d = %q, want %q", i, line, want)
			break
		}
	}
}

// TestIntegration_StderrSeparation confirms the two streams stay isolated
// at the wire level. A bug here would show up as merged output in
// `roksbnkctl exec --on jumphost -- some-tool` and corrupt downstream
// pipelines that expect clean stderr/stdout.
func TestIntegration_StderrSeparation(t *testing.T) {
	t.Setenv("ROKSBNKCTL_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fix := startSSHContainer(ctx, t)

	client, err := Connect(ctx, fix.target())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	var out, errb bytes.Buffer
	rc, err := client.Run(ctx,
		[]string{"sh", "-c", "echo stdout-line; echo stderr-line >&2"},
		RunOpts{Stdout: &out, Stderr: &errb},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rc != 0 {
		t.Fatalf("exit = %d, want 0", rc)
	}
	if got := strings.TrimSpace(out.String()); got != "stdout-line" {
		t.Errorf("stdout = %q, want %q", got, "stdout-line")
	}
	if got := strings.TrimSpace(errb.String()); got != "stderr-line" {
		t.Errorf("stderr = %q, want %q", got, "stderr-line")
	}
}

// TestIntegration_HostKeyTOFU is intentionally deferred to a follow-up
// commit: PRD 01 §Host key handling specifies a TOFU flow against
// `~/.roksbnkctl/known_hosts` plus an `--insecure-host-key` flag, but
// the exact API shape staff exposes for *injecting* a custom known_hosts
// path and a per-call insecure override isn't pinned down in their spec
// (it could be on Target, on RunOpts, on a separate ConnectOpts, or only
// reachable via a global root flag). Rather than guess and constantly
// rewrite this test as staff iterates, the validator filed a follow-up
// in issues/issue_sprint1_validator.md to land this test once the
// surface is stable. The unit-tier `hostkeys_test.go` (staff's) covers
// the parsing logic; this integration test would only add MITM /
// second-connect-silent confidence.

// TestIntegration_ContextCancellation confirms a cancelled context tears
// down a long-running remote command within ~5s. Without this, ctrl-C
// during `roksbnkctl exec --on jumphost -- sleep 30` would hang for the
// full 30s — bad UX.
func TestIntegration_ContextCancellation(t *testing.T) {
	t.Setenv("ROKSBNKCTL_HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fix := startSSHContainer(ctx, t)

	client, err := Connect(ctx, fix.target())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	runCtx, runCancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := client.Run(runCtx, []string{"sleep", "30"}, RunOpts{})
		done <- err
	}()

	// Give the command time to actually start before yanking the context.
	// 2s is generous but cheap; sshd takes ~50ms to fork the sleep.
	time.Sleep(2 * time.Second)
	runCancel()

	select {
	case <-done:
		// Run returned within the 5s budget — that's the PRD 01
		// guarantee. The returned error can be context.Canceled wrapped,
		// an SSH-level session-closed error, OR nil (gliderlabs/testcontainers
		// sshd sometimes closes the session cleanly before propagating
		// the cancel — see resolved_sprint4_validator.md Issue 2). What
		// matters is the goroutine + TCP connection didn't leak past the
		// timeout.
	case <-time.After(5 * time.Second):
		// 5s budget aligns with PRD 01 §Implementation tasks 1: "Context
		// cancellation closes the session within a few seconds." If Run
		// hasn't returned by then, the SSH client is leaking a goroutine
		// + an open TCP connection on every Ctrl-C — bad UX, real bug.
		// See issue_sprint1_validator.md Issue 8.
		t.Fatal("Run did not return within 5s after context cancellation (PRD 01 §1 requires prompt teardown)")
	}
}
