package remote_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

// genEd25519PEM returns an OpenSSH-format ED25519 private key as PEM
// bytes plus the parsed Signer for assertion. Crypto-stdlib only — no
// external test fixture deps.
func genEd25519PEM(t *testing.T) ([]byte, ssh.Signer) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("MarshalPrivateKey: %v", err)
	}
	pemBytes := pem.EncodeToMemory(block)
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	return pemBytes, signer
}

func TestResolveSigner_KeyPath(t *testing.T) {
	dir := t.TempDir()
	pem, want := genEd25519PEM(t)
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, pem, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	target := &remote.Target{Name: "t", KeyPath: keyPath}
	got, err := remote.ResolveSigner(target, nil)
	if err != nil {
		t.Fatalf("ResolveSigner: %v", err)
	}
	if got.PublicKey().Type() != want.PublicKey().Type() {
		t.Errorf("type mismatch: got %s want %s", got.PublicKey().Type(), want.PublicKey().Type())
	}
}

func TestResolveSigner_TFOutput(t *testing.T) {
	pem, _ := genEd25519PEM(t)
	target := &remote.Target{Name: "t", KeySource: "tf-output:jumphost"}
	signer, err := remote.ResolveSigner(target, map[string]string{"jumphost": string(pem)})
	if err != nil {
		t.Fatalf("ResolveSigner tf-output: %v", err)
	}
	if signer == nil {
		t.Fatal("nil signer")
	}
}

func TestResolveSigner_TFOutput_Missing(t *testing.T) {
	target := &remote.Target{Name: "t", KeySource: "tf-output:nope"}
	_, err := remote.ResolveSigner(target, map[string]string{})
	if err == nil {
		t.Fatal("want error for missing tf output")
	}
}

func TestResolveSigner_Unsupported(t *testing.T) {
	target := &remote.Target{Name: "t", KeySource: "vault:secret"}
	_, err := remote.ResolveSigner(target, nil)
	if err == nil {
		t.Fatal("want error for unsupported scheme")
	}
}

func TestResolveSigner_Empty(t *testing.T) {
	target := &remote.Target{Name: "t"}
	_, err := remote.ResolveSigner(target, nil)
	if err == nil {
		t.Fatal("want error when neither key_path nor key_source set")
	}
}

func TestResolveSigner_Agent_Skipped(t *testing.T) {
	if os.Getenv("SSH_AUTH_SOCK") == "" {
		t.Skip("SSH_AUTH_SOCK unset; skipping agent path")
	}
	target := &remote.Target{Name: "t", KeySource: "agent"}
	signer, err := remote.ResolveSigner(target, nil)
	if err != nil {
		// Real test envs may have an agent that's reachable but with no
		// keys loaded — still a valid pass for "agent dispatch worked".
		if !strings.Contains(err.Error(), "no keys") {
			t.Fatalf("agent: %v", err)
		}
		return
	}
	if signer == nil {
		t.Fatal("agent returned nil signer with no error")
	}
}
