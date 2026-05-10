package exec

// Sprint 3 / PRD 03 — Local backend unit tests.
//
// Asserts the contract from staff.md Priority 5: the local backend wraps
// `os/exec` and is byte-identical to today's behaviour (it's a refactor so
// docker/k8s/ssh backends share a single Backend interface).
//
// The exec.LocalBackend{} value-or-pointer form is whatever staff exports.
// These tests construct via the registry's "local" name when possible so
// they survive minor refactors in the constructor signature.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// resolveLocal returns the local backend via the registry. If the registry
// shape changes, this is the single place to update.
func resolveLocal(t *testing.T) Backend {
	t.Helper()
	b, err := ResolveBackend("local")
	if err != nil {
		t.Fatalf("ResolveBackend(\"local\"): %v", err)
	}
	if b == nil {
		t.Fatal("ResolveBackend returned nil backend")
	}
	if got := b.Name(); got != "local" {
		t.Errorf("backend Name(): got %q, want %q", got, "local")
	}
	return b
}

// TestLocalBackend_RunEcho covers the simplest case: argv → stdout via the
// `echo` builtin (Linux/macOS).
func TestLocalBackend_RunEcho(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local backend echo test uses /bin/echo; run on Linux/macOS")
	}
	b := resolveLocal(t)

	var stdout, stderr bytes.Buffer
	rc, err := b.Run(context.Background(),
		[]string{"sh", "-c", "echo hello-from-local"},
		RunOpts{Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rc != 0 {
		t.Errorf("expected exit 0, got %d (stderr=%q)", rc, stderr.String())
	}
	if !strings.Contains(stdout.String(), "hello-from-local") {
		t.Errorf("stdout missing expected token: %q", stdout.String())
	}
}

// TestLocalBackend_ExitCodePropagation asserts the backend mirrors the child's
// exit code. PRD 03 §"Backend interface" reserves 126/127 for backend-side
// failures, so any non-reserved code (here 7) must come straight from the
// child.
func TestLocalBackend_ExitCodePropagation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c exit 7")
	}
	b := resolveLocal(t)

	rc, err := b.Run(context.Background(),
		[]string{"sh", "-c", "exit 7"},
		RunOpts{Stdout: io.Discard, Stderr: io.Discard})
	// `err` may be non-nil (os/exec returns *exec.ExitError for non-zero
	// exits); the contract is that the *exit code* is faithfully reported
	// regardless of whether err is nil or an ExitError. Backends that wrap
	// the error in a custom type are fine; the rc is what callers act on.
	_ = err
	if rc != 7 {
		t.Errorf("exit code propagation broken: got %d, want 7", rc)
	}
}

// TestLocalBackend_EnvPropagation asserts caller-set Env values reach the
// child process.
func TestLocalBackend_EnvPropagation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c printenv")
	}
	b := resolveLocal(t)

	var stdout bytes.Buffer
	rc, err := b.Run(context.Background(),
		[]string{"sh", "-c", "printenv ROKSBNKCTL_TEST_VAR"},
		RunOpts{
			Stdout: &stdout,
			Stderr: io.Discard,
			Env:    []string{"ROKSBNKCTL_TEST_VAR=propagated"},
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rc != 0 {
		t.Fatalf("printenv exit %d", rc)
	}
	if got := strings.TrimSpace(stdout.String()); got != "propagated" {
		t.Errorf("env var not propagated: stdout=%q", got)
	}
}

// TestLocalBackend_StdinPipe asserts stdin is wired through.
func TestLocalBackend_StdinPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c cat")
	}
	b := resolveLocal(t)

	var stdout bytes.Buffer
	rc, err := b.Run(context.Background(),
		[]string{"cat"},
		RunOpts{
			Stdin:  strings.NewReader("piped input\n"),
			Stdout: &stdout,
			Stderr: io.Discard,
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rc != 0 {
		t.Fatalf("cat exit %d", rc)
	}
	if !strings.Contains(stdout.String(), "piped input") {
		t.Errorf("stdin not piped: stdout=%q", stdout.String())
	}
}

// TestLocalBackend_ContextCancel asserts that ctx cancellation terminates
// the child process within a few seconds. PRD 03 §"Backend interface": "ctx
// cancellation must terminate the remote process within a few seconds."
//
// Without proper ctx wiring, a `sleep 30` would block the test for 30
// seconds. We assert the test returns within 5s — generous slack for slow
// CI runners.
func TestLocalBackend_ContextCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sleep")
	}
	b := resolveLocal(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := b.Run(ctx,
		[]string{"sleep", "30"},
		RunOpts{Stdout: io.Discard, Stderr: io.Discard})
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("ctx cancel didn't kill child fast enough: elapsed=%v", elapsed)
	}
	// ctx cancel SHOULD produce some kind of error (context.Canceled or an
	// ExitError reflecting a signal). Don't assert the exact form.
	if err == nil && !errors.Is(ctx.Err(), context.Canceled) {
		// Tolerate the case where the sleep happened to be killed cleanly
		// and the backend reports rc=0; the elapsed-time check above is
		// the real assertion.
		t.Logf("note: ctx-cancelled run returned err=nil (acceptable if backend swallows the signal)")
	}
	_ = os.Stderr // silence unused import on Windows skip path
}
