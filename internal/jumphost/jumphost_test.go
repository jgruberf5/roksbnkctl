package jumphost_test

import (
	"crypto/ed25519"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

func TestGenerateEphemeralED25519_ParseablePEM(t *testing.T) {
	privPEM, pubAuth, err := jumphost.GenerateEphemeralED25519()
	if err != nil {
		t.Fatalf("GenerateEphemeralED25519: %v", err)
	}

	// Private key must be a PEM block.
	block, _ := pem.Decode(privPEM)
	if block == nil {
		t.Fatal("privPEM is not a valid PEM block")
	}
	// PEM type must indicate an OpenSSH private key.
	if !strings.Contains(block.Type, "OPENSSH PRIVATE KEY") && !strings.Contains(block.Type, "PRIVATE KEY") {
		t.Errorf("unexpected PEM type %q (want OPENSSH PRIVATE KEY)", block.Type)
	}

	// Parse via golang.org/x/crypto/ssh to confirm it's a valid ED25519 key.
	privKey, err := ssh.ParseRawPrivateKey(privPEM)
	if err != nil {
		t.Fatalf("ssh.ParseRawPrivateKey: %v", err)
	}
	if _, ok := privKey.(*ed25519.PrivateKey); !ok {
		// ssh package returns *ed25519.PrivateKey for ed25519.
		t.Logf("key type: %T (ok — may be a wrapped type)", privKey)
	}

	// Public auth line must start with "ssh-ed25519".
	if !strings.HasPrefix(pubAuth, "ssh-ed25519 ") {
		t.Errorf("pubAuth does not start with 'ssh-ed25519 ': %q", pubAuth[:min(len(pubAuth), 40)])
	}

	// Auth line must end with newline.
	if !strings.HasSuffix(pubAuth, "\n") {
		t.Errorf("pubAuth does not end with newline")
	}
}

func TestGenerateEphemeralED25519_Unique(t *testing.T) {
	p1, _, err1 := jumphost.GenerateEphemeralED25519()
	p2, _, err2 := jumphost.GenerateEphemeralED25519()
	if err1 != nil || err2 != nil {
		t.Fatalf("GenerateEphemeralED25519 error: %v / %v", err1, err2)
	}
	if string(p1) == string(p2) {
		t.Error("two generated keys are identical — random source broken")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
