package remote_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh"

	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

// startTestServer spins up an in-process SSH server bound to a random
// localhost port. The server accepts any public-key auth and dispatches
// the single-command shape via handler. Returns the host:port the
// client should dial, the server's signer (for the callback's known
// key), and a cleanup func.
func startTestServer(t *testing.T, handler gssh.Handler) (host string, port int, hostKey ssh.PublicKey, cleanup func()) {
	t.Helper()

	// Server host key.
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(hostPriv, "")
	if err != nil {
		t.Fatalf("MarshalPrivateKey: %v", err)
	}
	hostSigner, err := ssh.ParsePrivateKey(pem.EncodeToMemory(block))
	if err != nil {
		t.Fatalf("ParsePrivateKey host: %v", err)
	}

	srv := &gssh.Server{
		Handler: handler,
		PublicKeyHandler: func(_ gssh.Context, _ gssh.PublicKey) bool {
			return true
		},
	}
	srv.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	go func() { _ = srv.Serve(ln) }()
	cleanup = func() { _ = srv.Close() }
	return addr.IP.String(), addr.Port, hostSigner.PublicKey(), cleanup
}

// allowKeyCallback returns a HostKeyCallback that accepts only the
// expected host key. Avoids the production TOFU code path so SSH-only
// tests don't touch ROKSBNKCTL_HOME.
func allowKeyCallback(want ssh.PublicKey) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, got ssh.PublicKey) error {
		if want.Type() != got.Type() ||
			!bytes.Equal(want.Marshal(), got.Marshal()) {
			return fmt.Errorf("unexpected host key")
		}
		return nil
	}
}

// clientSigner builds a client-side signer the test server's
// any-key-allowed PublicKeyHandler will accept.
func clientSigner(t *testing.T) ssh.Signer {
	_, signer := genEd25519PEM(t)
	return signer
}

func TestRun_ExitCodeZero_StdoutCaptured(t *testing.T) {
	host, port, hostKey, cleanup := startTestServer(t, func(s gssh.Session) {
		_, _ = io.WriteString(s, "hello\n")
		_ = s.Exit(0)
	})
	defer cleanup()

	target := &remote.Target{
		Name: "t", Host: host, Port: port, User: "tester",
		Signer: clientSigner(t), HostKeyCallback: allowKeyCallback(hostKey),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := remote.Connect(ctx, target)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	var out bytes.Buffer
	code, err := c.Run(ctx, []string{"true"}, remote.RunOpts{Stdout: &out})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit: want 0, got %d", code)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("stdout: %q", out.String())
	}
}

func TestRun_ExitCodeNonZero_PassesThrough(t *testing.T) {
	host, port, hostKey, cleanup := startTestServer(t, func(s gssh.Session) {
		_ = s.Exit(42)
	})
	defer cleanup()

	target := &remote.Target{
		Name: "t", Host: host, Port: port, User: "tester",
		Signer: clientSigner(t), HostKeyCallback: allowKeyCallback(hostKey),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := remote.Connect(ctx, target)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	code, err := c.Run(ctx, []string{"false"}, remote.RunOpts{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 42 {
		t.Errorf("exit: want 42, got %d", code)
	}
}

func TestRun_StreamsStdinStdoutStderr(t *testing.T) {
	host, port, hostKey, cleanup := startTestServer(t, func(s gssh.Session) {
		// echo stdin → stdout, fixed marker → stderr
		_, _ = io.Copy(s, s)
		_, _ = io.WriteString(s.Stderr(), "ERRMARK")
		_ = s.Exit(0)
	})
	defer cleanup()

	target := &remote.Target{
		Name: "t", Host: host, Port: port, User: "tester",
		Signer: clientSigner(t), HostKeyCallback: allowKeyCallback(hostKey),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := remote.Connect(ctx, target)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	var out, errOut bytes.Buffer
	code, err := c.Run(ctx, []string{"cat"}, remote.RunOpts{
		Stdin:  strings.NewReader("payload-data"),
		Stdout: &out,
		Stderr: &errOut,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit: %d", code)
	}
	if got := out.String(); !strings.Contains(got, "payload-data") {
		t.Errorf("stdout did not stream stdin: %q", got)
	}
	if got := errOut.String(); !strings.Contains(got, "ERRMARK") {
		t.Errorf("stderr stream: %q", got)
	}
}

func TestConnect_ContextCancellation(t *testing.T) {
	// Listen but never accept — Connect should give up when ctx cancels.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)

	target := &remote.Target{
		Name: "t", Host: addr.IP.String(), Port: addr.Port, User: "tester",
		Signer:          clientSigner(t),
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = remote.Connect(ctx, target)
	if err == nil {
		t.Fatal("want error from cancelled connect")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Connect didn't honor ctx cancel; elapsed=%v", elapsed)
	}
}

func TestConnect_NoSigner_Errors(t *testing.T) {
	target := &remote.Target{Name: "t", Host: "127.0.0.1", Port: 22, User: "u"}
	_, err := remote.Connect(context.Background(), target)
	if err == nil {
		t.Fatal("want error when Signer is nil")
	}
}
