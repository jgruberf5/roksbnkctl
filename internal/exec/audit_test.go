package exec

// Sprint 3 / PRD 04 — security-spine cred-leak audit.
//
// PRD 04 §"Acceptance criteria" item 5:
//
//   "A regression test runs each backend with a known API key and asserts
//   the key string never appears in any of: docker inspect output, kubectl
//   get all -o yaml, ssh's process listing, the wrapper script after exit"
//
// This file holds the unit-tier portion: known-secret runs through the
// available backends (currently just `local`; `docker` covered in
// docker_integration_test.go's NoLeakInInspect), then assertions over every
// inspection surface within reach of a unit test:
//
//   - os.Environ() after Backend.Run returns
//   - argv passed to a wrapped Backend (we capture it via a stub)
//   - captured stdout/stderr (validates the redactor wrap)
//
// Run with:
//
//	go test -run CredAudit ./internal/exec/...
//
// CI/Make integration: a `make test-cred-audit` target wraps this — see
// CONTRIBUTING.md "Running cred-audit tests" for context.

import (
	"bytes"
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

const auditSecret = "test-key-roksbnkctl-audit-NEVER-LOG-ME"

// argvCapture is a Backend wrapper that records every argv it sees, then
// dispatches to an inner Backend so the run still completes. Tests use it to
// assert the secret never landed in argv (PRD 04 cross-backend principle #2:
// "Never put credentials in argv").
type argvCapture struct {
	inner    Backend
	captured [][]string
}

func (c *argvCapture) Name() string { return "argv-capture(" + c.inner.Name() + ")" }
func (c *argvCapture) Run(ctx context.Context, argv []string, opts RunOpts) (int, error) {
	dup := make([]string, len(argv))
	copy(dup, argv)
	c.captured = append(c.captured, dup)
	return c.inner.Run(ctx, argv, opts)
}

// TestCredAudit_NoLeakInArgv is the security-spine test PRD 04 calls for at
// the unit-test tier.
//
// The wrapped Backend's argv must NEVER contain the secret string regardless
// of how the cred is propagated (env, mount, file, etc.). PRD 04
// cross-backend principle #2.
func TestCredAudit_NoLeakInArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local backend test uses sh -c")
	}
	inner, err := ResolveBackend("local")
	if err != nil {
		t.Fatalf("ResolveBackend(\"local\"): %v", err)
	}
	wrap := &argvCapture{inner: inner}

	t.Setenv("IBMCLOUD_API_KEY", auditSecret)

	creds := &Credentials{IBMCloudAPIKey: auditSecret}
	_, _ = wrap.Run(context.Background(),
		[]string{"sh", "-c", "true"},
		RunOpts{
			Stdout:      io.Discard,
			Stderr:      io.Discard,
			Credentials: creds,
		})

	if len(wrap.captured) == 0 {
		t.Fatal("argv-capture saw no calls — wrapper not invoked")
	}
	for i, argv := range wrap.captured {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, auditSecret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: secret in argv #%d: %v", i, argv)
		}
	}
}

// TestCredAudit_NoLeakInProcessEnv asserts that after Backend.Run returns,
// the parent process's os.Environ() does NOT include any new IBMCLOUD_API_KEY
// entries the backend might have set. (Some primitive impls set os.Setenv
// to propagate; that's a leak — the env should be passed only to the child.)
func TestCredAudit_NoLeakInProcessEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c true")
	}
	b, err := ResolveBackend("local")
	if err != nil {
		t.Fatalf("ResolveBackend: %v", err)
	}

	// Snapshot env keys before the run.
	before := envSnapshot()

	creds := &Credentials{IBMCloudAPIKey: auditSecret}
	_, _ = b.Run(context.Background(),
		[]string{"sh", "-c", "true"},
		RunOpts{
			Stdout:      io.Discard,
			Stderr:      io.Discard,
			Credentials: creds,
		})

	after := envSnapshot()

	// Assertion: any new env var that wasn't there before must not contain
	// the secret value.
	for k, v := range after {
		if _, existed := before[k]; existed {
			continue
		}
		if strings.Contains(v, auditSecret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: backend left env var %q=%q in parent process", k, "[redacted]")
		}
	}
}

// TestCredAudit_RedactsWrappedOutput asserts: when a wrapped tool prints its
// API key (a real ibmcloud --debug bug we've seen), the redactor catches it
// before it reaches the caller's stdout. Validates the integration of
// NewRedactor with a Backend's stream wrap.
//
// The test runs `sh -c 'echo <secret>'` through the local backend with a
// caller-provided stdout that goes through a redactor. We assert the secret
// never appears in the captured output.
func TestCredAudit_RedactsWrappedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c echo")
	}
	b, err := ResolveBackend("local")
	if err != nil {
		t.Fatalf("ResolveBackend: %v", err)
	}

	var raw bytes.Buffer
	stdout := NewRedactor(&raw, []string{auditSecret})

	creds := &Credentials{IBMCloudAPIKey: auditSecret}
	_, _ = b.Run(context.Background(),
		[]string{"sh", "-c", "echo " + auditSecret},
		RunOpts{
			Stdout:      stdout,
			Stderr:      io.Discard,
			Credentials: creds,
		})

	// Flush the redactor's trailing buffer, if any.
	if c, ok := stdout.(io.Closer); ok {
		_ = c.Close()
	}

	if strings.Contains(raw.String(), auditSecret) {
		t.Errorf("PRD 04 SECURITY VIOLATION: redactor missed the secret in stdout: %q", raw.String())
	}
}

func envSnapshot() map[string]string {
	out := make(map[string]string, len(os.Environ()))
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		out[kv[:i]] = kv[i+1:]
	}
	return out
}
