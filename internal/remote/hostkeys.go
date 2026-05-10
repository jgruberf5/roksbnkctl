package remote

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// ErrHostKeyMismatch is returned when a known host's stored key differs
// from the server's offered key. Callers map this to exit code 126 per
// PRD 01 (man-in-the-middle protection).
var ErrHostKeyMismatch = errors.New("host key mismatch")

// HostKeyOptions tunes the TOFU prompt + insecure-bypass behaviours.
//
//   - Insecure: just-add-the-key on first contact (no prompt). For CI.
//     Existing-but-mismatched keys still error: insecure means "trust
//     on first use without asking", not "ignore MITM warnings".
//   - PromptIn / PromptOut wire the y/N prompt to a non-stdin source for
//     tests. Default is os.Stdin / os.Stderr.
type HostKeyOptions struct {
	Insecure  bool
	PromptIn  io.Reader
	PromptOut io.Writer
}

// HostKeyCallback returns an ssh.HostKeyCallback that consults
// ~/.roksbnkctl/known_hosts. Behaviour matches PRD 01:
//
//   - Stored key matches: accept silently.
//   - Stored key differs: error wrapping ErrHostKeyMismatch.
//   - Unknown host: prompt the user (TTY) or accept-and-record (Insecure)
//     or error (no TTY, not insecure).
//
// On accept the new key is appended to known_hosts atomically.
func HostKeyCallback(opts HostKeyOptions) ssh.HostKeyCallback {
	return func(hostname string, addr net.Addr, key ssh.PublicKey) error {
		path, err := KnownHostsPath()
		if err != nil {
			return err
		}
		host := normalizedHost(hostname, addr)
		entries, err := readKnownHosts(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		for _, e := range entries {
			if e.host != host {
				continue
			}
			if keyEqual(e.key, key) {
				return nil
			}
			// Same host name, different key — MITM signal.
			return fmt.Errorf("%w: %s known with %s but server presented %s; if the host was rebuilt, edit %s",
				ErrHostKeyMismatch, host, fingerprint(e.key), fingerprint(key), path)
		}

		// Unknown host. Decide what to do.
		if opts.Insecure {
			return appendKnownHost(path, host, key)
		}
		in := opts.PromptIn
		if in == nil {
			in = os.Stdin
		}
		out := opts.PromptOut
		if out == nil {
			out = os.Stderr
		}
		if !isInteractive(in) {
			return fmt.Errorf("unknown host %s (key %s); pass --insecure-host-key to accept on first contact in non-interactive runs", host, fingerprint(key))
		}
		fmt.Fprintf(out, "Add %s's key (%s) to %s? [y/N]: ", host, fingerprint(key), path)
		reader := bufio.NewReader(in)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if !strings.HasPrefix(line, "y") {
			return fmt.Errorf("user rejected host key for %s", host)
		}
		return appendKnownHost(path, host, key)
	}
}

// KnownHostsPath returns ~/.roksbnkctl/known_hosts (per-tool, distinct
// from the user's ~/.ssh/known_hosts so we don't mutate their global
// trust store).
func KnownHostsPath() (string, error) {
	base, err := config.BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "known_hosts"), nil
}

// hostEntry is a parsed known_hosts line. We store one host per line in
// the simple `<host> <keytype> <key-b64>` shape — no hashed names, no
// CA markers, no @cert-authority. PRD 01 scope.
type hostEntry struct {
	host string
	key  ssh.PublicKey
}

func readKnownHosts(path string) ([]hostEntry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []hostEntry
	sc := bufio.NewScanner(f)
	// Public keys can be ~600 bytes for 4096-bit RSA; default scanner
	// buffer is 64K so we're fine, but bump explicitly for clarity.
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, _, pub, _, _, perr := ssh.ParseKnownHosts([]byte(line + "\n"))
		if perr != nil {
			// Skip malformed lines rather than error — preserves
			// forward-compat with future formats / hand edits.
			continue
		}
		// Recover the host token from the leading whitespace-delimited
		// field. ParseKnownHosts handles comma-lists internally; we
		// only ever write single-host entries so the field is the
		// host string.
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		out = append(out, hostEntry{host: fields[0], key: pub})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// appendKnownHost writes one entry, creating the file (and parent dir)
// if needed. Format matches `ssh-keyscan` output for easy diffing.
func appendKnownHost(path string, host string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	line := fmt.Sprintf("%s %s %s\n", host, key.Type(), base64.StdEncoding.EncodeToString(key.Marshal()))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// normalizedHost picks the host string we'll record. Prefer the
// hostname the caller passed (the value from Connect's addr arg), fall
// back to the resolved address. Strip the port — known_hosts entries
// for non-22 ports take the [host]:port form, but we only record one
// shape today; PRD 01 doesn't promise per-port granularity.
func normalizedHost(hostname string, addr net.Addr) string {
	h := hostname
	if h == "" && addr != nil {
		h = addr.String()
	}
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[:i], "]") {
		// host:port — strip port. Bracketed IPv6 (`[::1]:22`) handled
		// by leaving the brackets intact.
		h = h[:i]
	}
	return h
}

func keyEqual(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Type() != b.Type() {
		return false
	}
	am := a.Marshal()
	bm := b.Marshal()
	if len(am) != len(bm) {
		return false
	}
	for i := range am {
		if am[i] != bm[i] {
			return false
		}
	}
	return true
}

// fingerprint is the SHA256 base64 form OpenSSH uses
// (`SHA256:abcdef...`). Matches what the user would see in their
// existing tooling.
func fingerprint(k ssh.PublicKey) string {
	if k == nil {
		return ""
	}
	sum := sha256.Sum256(k.Marshal())
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}

func isInteractive(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
