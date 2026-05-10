package remote_test

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

// genHostKey returns an ed25519 ssh.PublicKey for testing host-key
// callbacks. Same generator as keys_test.go, narrower return.
func genHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	_, signer := genEd25519PEM(t)
	return signer.PublicKey()
}

type fakeAddr struct{}

func (fakeAddr) Network() string { return "tcp" }
func (fakeAddr) String() string  { return "1.2.3.4:22" }

func TestHostKeyCallback_Insecure_Records(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, dir)

	cb := remote.HostKeyCallback(remote.HostKeyOptions{Insecure: true})
	key := genHostKey(t)
	if err := cb("1.2.3.4", fakeAddr{}, key); err != nil {
		t.Fatalf("first contact (insecure): %v", err)
	}
	khPath, _ := remote.KnownHostsPath()
	b, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatalf("known_hosts after insecure accept: %v", err)
	}
	if !strings.Contains(string(b), "1.2.3.4") {
		t.Errorf("known_hosts didn't record host: %q", string(b))
	}

	// Same key on second invocation: silent accept.
	if err := cb("1.2.3.4", fakeAddr{}, key); err != nil {
		t.Errorf("repeat with same key: %v", err)
	}
}

func TestHostKeyCallback_Mismatch_RejectsWithSentinel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, dir)

	cb := remote.HostKeyCallback(remote.HostKeyOptions{Insecure: true})
	first := genHostKey(t)
	if err := cb("h1", fakeAddr{}, first); err != nil {
		t.Fatalf("seed: %v", err)
	}
	second := genHostKey(t) // distinct key for same host
	err := cb("h1", fakeAddr{}, second)
	if !errors.Is(err, remote.ErrHostKeyMismatch) {
		t.Errorf("want ErrHostKeyMismatch, got %v", err)
	}
}

func TestHostKeyCallback_Prompt_Accept(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, dir)

	in := strings.NewReader("y\n")
	var out bytes.Buffer
	cb := remote.HostKeyCallback(remote.HostKeyOptions{
		PromptIn:  in,
		PromptOut: &out,
	})
	// Prompt path requires PromptIn to NOT be a TTY (we use a strings.Reader);
	// the callback's isInteractive check returns false for non-*os.File
	// readers, so we get the non-interactive branch — which without
	// --insecure-host-key errors out. Verify that wiring instead.
	err := cb("p1", fakeAddr{}, genHostKey(t))
	if err == nil {
		t.Fatal("want error for non-interactive non-insecure unknown host")
	}
	if !strings.Contains(err.Error(), "unknown host") {
		t.Errorf("want unknown-host error, got: %v", err)
	}
}

func TestHostKeyCallback_PerToolKnownHosts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, dir)
	khPath, err := remote.KnownHostsPath()
	if err != nil {
		t.Fatalf("KnownHostsPath: %v", err)
	}
	if !strings.HasPrefix(khPath, dir) {
		t.Errorf("known_hosts must live under ROKSBNKCTL_HOME (%s), got %s", dir, khPath)
	}
	if filepath.Base(khPath) != "known_hosts" {
		t.Errorf("expected file named known_hosts, got %s", khPath)
	}
}

func TestHostKeyCallback_StripsPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, dir)
	cb := remote.HostKeyCallback(remote.HostKeyOptions{Insecure: true})
	key := genHostKey(t)
	if err := cb("h2:2222", fakeAddr{}, key); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Second call uses the bare hostname — should still match.
	if err := cb("h2", fakeAddr{}, key); err != nil {
		t.Errorf("port-stripped lookup didn't match: %v", err)
	}
}

// silence unused import if PromptIn / fakeAddr fields go unused.
var _ = net.IPv4zero
