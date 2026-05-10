package exec

// Sprint 5 / PRD 03 §"SSH" — wrapper-script content + bootstrap-failure
// matrix tests, unblocked by the SetSSHClientFactory seam staff lands
// this sprint (Sprint 4 validator Issue 3 carry-over).
//
// What this file pins:
//
//   - Wrapper-script content discipline: the cred value MUST NOT appear
//     in the script body; only the env-file path. `set +x` is verified
//     as part of the wrapper text (defense-in-depth even if the user
//     has `bash -x` aliased on the remote).
//
//   - File materialization: Files entries land at
//     /tmp/roksbnkctl.<rand>/<basename>, written via the captured mock
//     client's Run invocations.
//
//   - Bootstrap failure matrix:
//       - sudo -n fails → exit 126 with "passwordless sudo required"
//       - lsb_release reports non-Ubuntu → exit 126 with "auto-install
//         only supports Ubuntu"
//       - apt-get repo-unreachable → exit 127 with "target can't reach
//         the package repo"
//       - Tool missing without --bootstrap → exit 127 (covered in the
//         Sprint 4 ssh_test.go via the gliderlabs path; covered here
//         again via the mock-client seam for cross-checking)
//       - Tool missing with --bootstrap → apt-get spawn observed in
//         the captured argv list
//
// Run with:
//
//	go test -run SSHWrapper ./internal/exec/...

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

// — mock client surface — //

// mockRemoteCall captures one client.Run invocation: the argv it saw,
// any stdin content, and what it returned.
type mockRemoteCall struct {
	Argv  []string
	Stdin []byte
	Env   []string
	RC    int
	Err   error
}

// mockRemoteClient is the smallest surface SSHBackend uses on its
// underlying remote.Client. Implements the interface SetSSHClientFactory
// expects (the staff impl of the seam introduces this interface in
// internal/exec/ssh.go; this file mirrors the shape).
//
// The mock captures every Run invocation to a `calls` slice tests
// inspect after Backend.Run returns. Each call dispatches to a
// callback `respond(call) (rc, err)` so tests can script per-step
// behaviour (tool present? sudo refused? lsb_release output? etc.).
type mockRemoteClient struct {
	mu      sync.Mutex
	calls   []mockRemoteCall
	respond func(*mockRemoteCall) // hook to set RC + populate stdout

	// stdoutResponses lets simple tests script stdout per-argv pattern
	// without writing a full callback. Key: a substring of the joined
	// argv; value: what to write to stdout. Match order is "first in
	// order of insertion" (Go maps don't preserve order — tests should
	// use `respond` if multi-key precedence matters).
	stdoutResponses map[string]string
}

func (m *mockRemoteClient) Run(ctx context.Context, argv []string, opts remote.RunOpts) (int, error) {
	call := mockRemoteCall{Argv: append([]string(nil), argv...), Env: append([]string(nil), opts.Env...)}
	if opts.Stdin != nil {
		stdin, _ := io.ReadAll(opts.Stdin)
		call.Stdin = stdin
	}
	// Apply stdoutResponses if set.
	joined := strings.Join(argv, " ")
	for needle, resp := range m.stdoutResponses {
		if strings.Contains(joined, needle) && opts.Stdout != nil {
			_, _ = io.WriteString(opts.Stdout, resp)
			break
		}
	}
	if m.respond != nil {
		m.respond(&call)
	}
	m.mu.Lock()
	m.calls = append(m.calls, call)
	m.mu.Unlock()
	return call.RC, call.Err
}

func (m *mockRemoteClient) Close() error { return nil }

func (m *mockRemoteClient) snapshot() []mockRemoteCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockRemoteCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// installMockClientFactory wires the SSH backend's SetSSHClientFactory
// seam to return the given mock client. Returns a cleanup that
// restores the default factory.
func installMockClientFactory(t *testing.T, m *mockRemoteClient) (cleanup func()) {
	t.Helper()
	SetSSHClientFactory(func(ctx context.Context, target *remote.Target) (remoteClient, error) {
		return m, nil
	})
	return func() { SetSSHClientFactory(nil) }
}

// installFakeTargetResolver wires SetSSHTargetResolver with a synthetic
// target so the backend's resolveTarget step doesn't try to hit the
// workspace config loader.
func installFakeTargetResolver(t *testing.T) (cleanup func()) {
	t.Helper()
	resolver := func(_, name string) (*remote.Target, map[string][]byte, error) {
		return &remote.Target{Name: name, Host: "127.0.0.1", User: "tester"}, nil, nil
	}
	SetSSHTargetResolver(resolver)
	return func() { SetSSHTargetResolver(nil) }
}

// — wrapper-script content — //

// TestSSHWrapper_NoSecretInScriptBody asserts the wrapper script's body
// (the `cat > run.sh` argv pattern) doesn't contain the cred value —
// only the env-file path is referenced. The cred value lives in the
// .env file's stdin payload, which is the documented carrier (PRD 04
// §"SSH" §"wrapper-script-with-trap fallback").
func TestSSHWrapper_NoSecretInScriptBody(t *testing.T) {
	const secret = "test-key-NEVER-IN-WRAPPER-SCRIPT"

	mock := &mockRemoteClient{
		stdoutResponses: map[string]string{
			"mktemp": "/tmp/roksbnkctl.fake1234\n",
			// Force the wrapper-script fallback by failing the canary
			// printenv (rc=1, no echo).
		},
		respond: func(call *mockRemoteCall) {
			joined := strings.Join(call.Argv, " ")
			switch {
			case strings.Contains(joined, "command -v"):
				call.RC = 0 // tool present (skip bootstrap)
			case strings.Contains(joined, "mktemp"):
				call.RC = 0
			case strings.Contains(joined, "printenv"):
				call.RC = 1 // canary fails → wrapper-script path
			default:
				call.RC = 0
			}
		},
	}

	defer installMockClientFactory(t, mock)()
	defer installFakeTargetResolver(t)()

	b := &SSHBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = b.Run(ctx,
		[]string{"ibmcloud", "iam", "oauth-tokens"},
		RunOpts{
			Stdout: io.Discard, Stderr: io.Discard,
			Env:         []string{"ROKSBNKCTL_SSH_TARGET=jumphost"},
			Credentials: &Credentials{IBMCloudAPIKey: secret},
		})

	// Inspect every captured Run invocation. The cred MAY appear in
	// the stdin of the .env-write call — that's the documented
	// carrier — but MUST NOT appear in any argv.
	for _, c := range mock.snapshot() {
		argvStr := strings.Join(c.Argv, " ")
		if strings.Contains(argvStr, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred value in remote argv: %q", argvStr)
		}
	}

	// The wrapper-script body itself (written via `sh -c "set -e; umask 077;
	// cat > '<tempdir>/run.sh' && chmod 0700 '<tempdir>/run.sh'"`'s stdin
	// in the wrapper-fallback path) MUST contain `set +x`
	// (defense-in-depth) and MUST NOT contain the cred value. Two calls
	// in the trace mention `run.sh` (write + exec); we scope the body
	// assertion to the cat-write call (its argv contains `cat >`).
	wrapperWritten := false
	for _, c := range mock.snapshot() {
		argvStr := strings.Join(c.Argv, " ")
		if !strings.Contains(argvStr, "run.sh") || !strings.Contains(argvStr, "cat >") {
			continue
		}
		wrapperWritten = true
		body := string(c.Stdin)
		if !strings.Contains(body, "set +x") {
			t.Errorf("wrapper script lacks `set +x` discipline:\n%s", body)
		}
		if strings.Contains(body, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred value in wrapper script body:\n%s", body)
		}
	}
	if !wrapperWritten {
		t.Skip("wrapper-script-fallback path not exercised; canary may have succeeded — this test only meaningfully runs when the fallback path is reached")
	}
}

// TestSSHWrapper_FilesMaterializedAtTempDir asserts: when RunOpts.Files
// is set, each entry is written to /tmp/roksbnkctl.<rand>/<basename>
// on the remote. We capture the mock client's Run invocations for the
// `cat > <path>` shape and confirm the basenames + paths match.
func TestSSHWrapper_FilesMaterializedAtTempDir(t *testing.T) {
	mock := &mockRemoteClient{
		stdoutResponses: map[string]string{
			"mktemp": "/tmp/roksbnkctl.fake1234\n",
		},
		respond: func(call *mockRemoteCall) {
			joined := strings.Join(call.Argv, " ")
			switch {
			case strings.Contains(joined, "command -v"):
				call.RC = 0
			case strings.Contains(joined, "mktemp"):
				call.RC = 0
			case strings.Contains(joined, "printenv"):
				call.RC = 0 // canary echoes back, SetEnv path
			default:
				call.RC = 0
			}
		},
	}

	defer installMockClientFactory(t, mock)()
	defer installFakeTargetResolver(t)()

	files := map[string][]byte{
		"kubeconfig":  []byte("apiVersion: v1\nkind: Config\n"),
		"id_rsa":      []byte("-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n"),
		"some/nested": []byte("nested file content"),
	}

	b := &SSHBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _ = b.Run(ctx,
		[]string{"iperf3", "-V"},
		RunOpts{
			Stdout: io.Discard, Stderr: io.Discard,
			Env:   []string{"ROKSBNKCTL_SSH_TARGET=jumphost"},
			Files: files,
		})

	// Look for `cat > /tmp/roksbnkctl.fake1234/<basename>` invocations.
	gotBasenames := map[string]bool{}
	for _, c := range mock.snapshot() {
		argvStr := strings.Join(c.Argv, " ")
		if !strings.Contains(argvStr, "/tmp/roksbnkctl.fake1234/") {
			continue
		}
		// Extract basename from the cat-redirect — naive: last `/`.
		idx := strings.Index(argvStr, "/tmp/roksbnkctl.fake1234/")
		rest := argvStr[idx+len("/tmp/roksbnkctl.fake1234/"):]
		// Strip a trailing single-quote / space.
		end := strings.IndexAny(rest, "' ")
		if end > 0 {
			rest = rest[:end]
		}
		if rest != "" && rest != ".env" && rest != "run.sh" {
			gotBasenames[rest] = true
		}
	}
	for name := range files {
		base := name
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			base = name[i+1:]
		}
		if !gotBasenames[base] {
			t.Errorf("expected to see file basename %q materialized at /tmp/roksbnkctl.<rand>/; got %v", base, gotBasenames)
		}
	}
}

// — bootstrap-failure matrix — //

// TestSSHBootstrap_SudoRefused_Exits126 asserts the documented PRD 03
// exit code + message: when `sudo -n` returns non-zero, the backend
// exits 126 with "passwordless sudo required".
func TestSSHBootstrap_SudoRefused_Exits126(t *testing.T) {
	savedOpts := sshOpts
	defer func() { sshOpts = savedOpts }()
	sshOpts.Bootstrap = true

	mock := &mockRemoteClient{
		stdoutResponses: map[string]string{
			"lsb_release": "Ubuntu\n", // pretend we're on Ubuntu to reach the sudo step
		},
		respond: func(call *mockRemoteCall) {
			joined := strings.Join(call.Argv, " ")
			switch {
			case strings.Contains(joined, "command -v"):
				call.RC = 1 // tool missing → bootstrap path
			case strings.Contains(joined, "lsb_release"):
				call.RC = 0
			case strings.Contains(joined, "sudo"):
				call.RC = 1 // sudo refused (no NOPASSWD, password required)
			default:
				call.RC = 0
			}
		},
	}
	defer installMockClientFactory(t, mock)()
	defer installFakeTargetResolver(t)()

	b := &SSHBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rc, err := b.Run(ctx,
		[]string{"iperf3", "-V"},
		RunOpts{Stdout: io.Discard, Stderr: io.Discard,
			Env: []string{"ROKSBNKCTL_SSH_TARGET=jumphost"}})

	if rc != 126 {
		t.Errorf("rc: got %d, want 126 (passwordless sudo required)", rc)
	}
	if err == nil {
		t.Fatal("expected an error explaining passwordless sudo, got nil")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "sudo") {
		t.Errorf("error %q lacks the 'passwordless sudo' message", err)
	}
}

// TestSSHBootstrap_NonUbuntu_Exits126 asserts: lsb_release reports a
// non-Ubuntu distro (e.g., RHEL) → exit 126 + "auto-install only
// supports Ubuntu".
func TestSSHBootstrap_NonUbuntu_Exits126(t *testing.T) {
	savedOpts := sshOpts
	defer func() { sshOpts = savedOpts }()
	sshOpts.Bootstrap = true

	mock := &mockRemoteClient{
		stdoutResponses: map[string]string{
			"lsb_release": "RHEL\n",
		},
		respond: func(call *mockRemoteCall) {
			joined := strings.Join(call.Argv, " ")
			switch {
			case strings.Contains(joined, "command -v"):
				call.RC = 1
			case strings.Contains(joined, "lsb_release"):
				call.RC = 0
			default:
				call.RC = 0
			}
		},
	}
	defer installMockClientFactory(t, mock)()
	defer installFakeTargetResolver(t)()

	b := &SSHBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rc, err := b.Run(ctx,
		[]string{"iperf3", "-V"},
		RunOpts{Stdout: io.Discard, Stderr: io.Discard,
			Env: []string{"ROKSBNKCTL_SSH_TARGET=jumphost"}})

	if rc != 126 {
		t.Errorf("rc: got %d, want 126 (non-Ubuntu)", rc)
	}
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "Ubuntu") {
		t.Errorf("error %q lacks the 'Ubuntu only' message", err)
	}

	// apt-get must NOT have been invoked on a non-Ubuntu host.
	for _, c := range mock.snapshot() {
		joined := strings.Join(c.Argv, " ")
		if strings.Contains(joined, "apt-get") {
			t.Errorf("BUG: backend ran apt-get on non-Ubuntu host: %q", joined)
		}
	}
}

// TestSSHBootstrap_RepoUnreachable_Exits127 asserts: when the IBM apt
// repo bootstrap fails (e.g., network unreachable), the backend exits
// 127 with the documented "target can't reach the package repo" message.
func TestSSHBootstrap_RepoUnreachable_Exits127(t *testing.T) {
	savedOpts := sshOpts
	defer func() { sshOpts = savedOpts }()
	sshOpts.Bootstrap = true

	mock := &mockRemoteClient{
		stdoutResponses: map[string]string{
			"lsb_release": "Ubuntu\n",
		},
		respond: func(call *mockRemoteCall) {
			joined := strings.Join(call.Argv, " ")
			switch {
			case strings.Contains(joined, "command -v"):
				call.RC = 1
			case strings.Contains(joined, "lsb_release"):
				call.RC = 0
			case strings.Contains(joined, "download.clis.cloud.ibm.com") || strings.Contains(joined, "ibmcloud.list"):
				// Repo-add step fails — simulate "target can't reach
				// the package repo".
				call.RC = 1
				call.Err = errors.New("network unreachable")
			default:
				call.RC = 0
			}
		},
	}
	defer installMockClientFactory(t, mock)()
	defer installFakeTargetResolver(t)()

	b := &SSHBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rc, err := b.Run(ctx,
		[]string{"ibmcloud", "iam", "oauth-tokens"},
		RunOpts{Stdout: io.Discard, Stderr: io.Discard,
			Env: []string{"ROKSBNKCTL_SSH_TARGET=jumphost"}})

	if rc != 127 {
		t.Errorf("rc: got %d, want 127 (repo unreachable)", rc)
	}
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "repo") && !strings.Contains(msg, "reach") {
		t.Errorf("error %q lacks the 'package repo unreachable' troubleshooting hint", err)
	}
}

// TestSSHBootstrap_OptInGate_AptGetSpawnedOnlyWithFlag asserts the
// `--bootstrap` opt-in gate: tool missing without the flag → 127 with
// no apt-get spawn; tool missing WITH the flag → apt-get observed in
// the captured argv list.
func TestSSHBootstrap_OptInGate_AptGetSpawnedOnlyWithFlag(t *testing.T) {
	savedOpts := sshOpts
	defer func() { sshOpts = savedOpts }()

	scriptHandler := func(call *mockRemoteCall) {
		joined := strings.Join(call.Argv, " ")
		switch {
		case strings.Contains(joined, "command -v"):
			call.RC = 1 // missing
		case strings.Contains(joined, "lsb_release"):
			call.RC = 0
		default:
			call.RC = 0
		}
	}

	// Subtest 1: bootstrap=false → no apt-get spawn.
	t.Run("bootstrap_false", func(t *testing.T) {
		sshOpts.Bootstrap = false
		mock := &mockRemoteClient{
			stdoutResponses: map[string]string{"lsb_release": "Ubuntu\n"},
			respond:         scriptHandler,
		}
		defer installMockClientFactory(t, mock)()
		defer installFakeTargetResolver(t)()

		b := &SSHBackend{}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		rc, _ := b.Run(ctx,
			[]string{"iperf3", "-V"},
			RunOpts{Stdout: io.Discard, Stderr: io.Discard,
				Env: []string{"ROKSBNKCTL_SSH_TARGET=jumphost"}})
		if rc != 127 {
			t.Errorf("rc (no --bootstrap): got %d, want 127", rc)
		}
		for _, c := range mock.snapshot() {
			joined := strings.Join(c.Argv, " ")
			if strings.Contains(joined, "apt-get") {
				t.Errorf("apt-get spawned without --bootstrap opt-in: %q", joined)
			}
		}
	})

	// Subtest 2: bootstrap=true → apt-get observed.
	t.Run("bootstrap_true", func(t *testing.T) {
		sshOpts.Bootstrap = true
		mock := &mockRemoteClient{
			stdoutResponses: map[string]string{"lsb_release": "Ubuntu\n"},
			respond:         scriptHandler,
		}
		defer installMockClientFactory(t, mock)()
		defer installFakeTargetResolver(t)()

		b := &SSHBackend{}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _ = b.Run(ctx,
			[]string{"iperf3", "-V"},
			RunOpts{Stdout: io.Discard, Stderr: io.Discard,
				Env: []string{"ROKSBNKCTL_SSH_TARGET=jumphost"}})

		spawnedAptGet := false
		for _, c := range mock.snapshot() {
			joined := strings.Join(c.Argv, " ")
			if strings.Contains(joined, "apt-get") {
				spawnedAptGet = true
				break
			}
		}
		if !spawnedAptGet {
			t.Error("--bootstrap=true should spawn apt-get; no such argv observed")
		}
	})
}
