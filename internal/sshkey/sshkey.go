// Package sshkey generates ed25519 SSH keypairs and writes them to disk for the
// testing jumphosts (the IBM Cloud VPC SSH key roksbnkctl uploads + manages).
package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Generate creates an ed25519 SSH keypair. It returns the public key in OpenSSH
// authorized_keys form (one line, no trailing newline) and the private key as an
// OpenSSH-format PEM block.
func Generate() (publicOpenSSH string, privatePEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", nil, fmt.Errorf("generating ed25519 key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return "", nil, fmt.Errorf("marshaling private key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", nil, fmt.Errorf("marshaling public key: %w", err)
	}
	pubLine := strings.TrimRight(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")
	return pubLine, pem.EncodeToMemory(block), nil
}

// Write atomically writes the keypair into dir: <name> (private, 0600) and
// <name>.pub (public, 0644). dir is created at 0700 if absent. Returns the
// private-key path.
func Write(dir, name string, privatePEM []byte, publicOpenSSH string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	privPath := filepath.Join(dir, name)
	if err := writeAtomic(privPath, privatePEM, 0o600); err != nil {
		return "", err
	}
	if err := writeAtomic(privPath+".pub", []byte(publicOpenSSH+"\n"), 0o644); err != nil {
		return "", err
	}
	return privPath, nil
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
