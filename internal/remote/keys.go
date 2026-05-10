package remote

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// ResolveSigner picks a Signer based on a target's KeyPath / KeySource.
// Dispatch order:
//
//   - KeyPath != ""             → read PEM file, parse via ssh.ParsePrivateKey
//   - KeySource == "agent"      → first signer from $SSH_AUTH_SOCK
//   - KeySource == "tf-output:<name>" → bytes from tfOutputs[<name>], parsed as PEM
//
// tfOutputs is supplied by the caller (the cli layer pulls them from
// tf.Workspace.Output before Connect). Keeping the lookup at the call
// site means this package never imports internal/tf — so internal/tf
// can keep using internal/remote without a cycle.
func ResolveSigner(target *Target, tfOutputs map[string]string) (ssh.Signer, error) {
	if target == nil {
		return nil, errors.New("nil target")
	}

	switch {
	case target.KeyPath != "":
		return signerFromFile(target.KeyPath)

	case target.KeySource == "agent":
		return signerFromAgent()

	case strings.HasPrefix(target.KeySource, "tf-output:"):
		name := strings.TrimPrefix(target.KeySource, "tf-output:")
		if name == "" {
			return nil, fmt.Errorf("target %q: tf-output: source has no output name", target.Name)
		}
		pem, ok := tfOutputs[name]
		if !ok || pem == "" {
			return nil, fmt.Errorf("target %q: tf output %q is empty or missing", target.Name, name)
		}
		signer, err := ssh.ParsePrivateKey([]byte(pem))
		if err != nil {
			return nil, fmt.Errorf("target %q: parsing tf-output:%s: %w", target.Name, name, err)
		}
		return signer, nil

	case target.KeySource == "":
		return nil, fmt.Errorf("target %q: neither key_path nor key_source set", target.Name)

	default:
		return nil, fmt.Errorf("target %q: unsupported key_source %q", target.Name, target.KeySource)
	}
}

func signerFromFile(path string) (ssh.Signer, error) {
	expanded, err := expandHome(path)
	if err != nil {
		return nil, err
	}
	pem, err := os.ReadFile(expanded)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", expanded, err)
	}
	signer, err := ssh.ParsePrivateKey(pem)
	if err != nil {
		// crypto/ssh's parser distinguishes a passphrase-protected key
		// via PassphraseMissingError. We don't support encrypted keys
		// in v0.7 (PRD scope says keys + agent only); surface the
		// situation clearly so users know to run `ssh-add` instead.
		var miss *ssh.PassphraseMissingError
		if errors.As(err, &miss) {
			return nil, fmt.Errorf("%s is passphrase-protected — load it via ssh-agent and use key_source: agent", expanded)
		}
		return nil, fmt.Errorf("parsing %s: %w", expanded, err)
	}
	return signer, nil
}

// signerFromAgent connects to $SSH_AUTH_SOCK and returns the first
// signer the agent exposes. Errors clearly when the socket is unset
// (Windows, or just no agent running).
func signerFromAgent() (ssh.Signer, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("SSH_AUTH_SOCK is unset; start ssh-agent and ssh-add a key, or use key_path")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("dial ssh-agent at %s: %w", sock, err)
	}
	a := agent.NewClient(conn)
	signers, err := a.Signers()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("listing agent signers: %w", err)
	}
	if len(signers) == 0 {
		_ = conn.Close()
		return nil, errors.New("ssh-agent has no keys; run `ssh-add ~/.ssh/id_ed25519`")
	}
	// We deliberately leak the conn for the lifetime of the process —
	// signers from agent.Client hold a reference to it and panic if it
	// closes mid-handshake. The socket FD is small; a roksbnkctl
	// invocation doesn't accumulate them.
	return signers[0], nil
}

// expandHome turns a leading "~/" into $HOME-prefixed. No shell-style
// `~user/` expansion — keeps the surface tiny and predictable.
func expandHome(p string) (string, error) {
	if !strings.HasPrefix(p, "~/") && p != "~" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home dir: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}
