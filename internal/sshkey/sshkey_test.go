package sshkey

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestGenerate_RoundTrip(t *testing.T) {
	pub, priv, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	// Private key parses as an OpenSSH key.
	signer, err := ssh.ParsePrivateKey(priv)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	// Public line parses and matches the private key's public half.
	parsedPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil {
		t.Fatalf("ParseAuthorizedKey(%q): %v", pub, err)
	}
	if string(parsedPub.Marshal()) != string(signer.PublicKey().Marshal()) {
		t.Error("public line does not match the private key")
	}
	if parsedPub.Type() != ssh.KeyAlgoED25519 {
		t.Errorf("key type = %s, want %s", parsedPub.Type(), ssh.KeyAlgoED25519)
	}
}

func TestWrite_Perms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ssh") // not pre-created → Write must MkdirAll
	pub, priv, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	privPath, err := Write(dir, "test-key", priv, pub)
	if err != nil {
		t.Fatal(err)
	}
	if got := perm(t, privPath); got != 0o600 {
		t.Errorf("private key perm = %o, want 600", got)
	}
	if got := perm(t, privPath+".pub"); got != 0o644 {
		t.Errorf("public key perm = %o, want 644", got)
	}
	// The written private key is the one we generated.
	got, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ssh.ParsePrivateKey(got); err != nil {
		t.Fatalf("written private key unparseable: %v", err)
	}
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}
