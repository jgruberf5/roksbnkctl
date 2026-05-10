package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Exit codes used by the --on flow. Match the conventions in PRD 01:
//
//	127 — "command not found" analog: connect failure / unreachable target
//	126 — "permission denied" analog: auth failure or host-key mismatch
//
// Remote command exit codes flow through unchanged when Run returns
// without err.
const (
	ExitConnectFailed = 127
	ExitAuthFailed    = 126
)

// connectTimeout is the per-attempt TCP+handshake budget. Short enough
// that a misconfigured target fails fast; long enough that a tunnel /
// VPN handshake completes on a slow link.
const connectTimeout = 15 * time.Second

// Client wraps an *ssh.Client and the resolved target it was opened for.
// One Client maps to one TCP connection. Callers create as many as they
// need (one per command invocation today; Phase 3 may pool).
type Client struct {
	target *Target
	conn   *ssh.Client
}

// RunOpts controls a single Run invocation. Stdin/Stdout/Stderr are
// streamed live — no buffering of the whole output. Env entries are
// "KEY=VALUE" strings; the remote sshd's AcceptEnv config decides which
// pass through.
type RunOpts struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Env            []string
	TTY            bool
}

// ShellOpts controls Shell. Stdin/Stdout/Stderr are required; the caller
// owns making them a TTY (raw mode + Resize) at the host side.
type ShellOpts struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Connect dials target's host:port, performs the SSH handshake, and
// returns a Client ready for Run / Shell. The caller must Close.
//
// Context cancellation aborts both the TCP dial and the handshake.
// Connect failures wrap a sentinel so callers can map them to the
// 127 / 126 exit codes documented in PRD 01.
func Connect(ctx context.Context, target *Target) (*Client, error) {
	if target == nil {
		return nil, errors.New("nil target")
	}
	if target.Host == "" {
		return nil, errors.New("target has no host")
	}
	if target.Signer == nil {
		return nil, fmt.Errorf("target %q has no signer; resolve a key first", target.Name)
	}
	port := target.Port
	if port == 0 {
		port = 22
	}
	user := target.User
	if user == "" {
		user = "root"
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(target.Signer)},
		HostKeyCallback: target.HostKeyCallback,
		Timeout:         connectTimeout,
	}
	if cfg.HostKeyCallback == nil {
		// Defensive — every Target should set one. ssh.NewClientConn
		// errors out instead of silently using an insecure default,
		// matching PRD 01 host-key requirements.
		return nil, errors.New("target has no HostKeyCallback (refusing to connect)")
	}

	addr := net.JoinHostPort(target.Host, strconv.Itoa(port))

	// Dial with context so SIGINT / parent ctx cancel aborts a hanging
	// TCP handshake (e.g., firewall dropping packets vs. RSTing).
	d := net.Dialer{Timeout: connectTimeout}
	tcpConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	// SSH handshake. ssh.NewClientConn doesn't accept a context, so we
	// race it against ctx via a goroutine + Close-on-cancel. On
	// cancellation, Close kicks the in-flight handshake out with an
	// error within a few hundred ms.
	type result struct {
		c   ssh.Conn
		ch  <-chan ssh.NewChannel
		req <-chan *ssh.Request
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, ch, req, herr := ssh.NewClientConn(tcpConn, addr, cfg)
		done <- result{c: c, ch: ch, req: req, err: herr}
	}()
	select {
	case <-ctx.Done():
		_ = tcpConn.Close()
		<-done // drain
		return nil, ctx.Err()
	case r := <-done:
		if r.err != nil {
			_ = tcpConn.Close()
			return nil, fmt.Errorf("ssh handshake to %s: %w", addr, r.err)
		}
		client := ssh.NewClient(r.c, r.ch, r.req)
		return &Client{target: target, conn: client}, nil
	}
}

// Close releases the SSH connection. Idempotent.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// Run executes argv on the remote host, streams I/O, and returns the
// remote process's exit code.
//
// argv is joined into a shell command line (sshd's exec channel takes a
// string, not an argv slice — the remote shell parses it). Args are
// quoted with single quotes; embedded single quotes use the canonical
// close-quote / escaped-quote / re-open trick.
//
// Context cancellation closes the session, which signals the remote
// process and unblocks the io.Copy goroutines.
func (c *Client) Run(ctx context.Context, argv []string, opts RunOpts) (int, error) {
	if c == nil || c.conn == nil {
		return 0, errors.New("client is closed")
	}
	if len(argv) == 0 {
		return 0, errors.New("argv is empty")
	}

	sess, err := c.conn.NewSession()
	if err != nil {
		return 0, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	for _, kv := range opts.Env {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			continue
		}
		// Best-effort: many sshd configs reject Setenv unless AcceptEnv
		// matches. We don't fail the whole run on a Setenv reject — the
		// caller's --env-passing strategy is up to them.
		_ = sess.Setenv(kv[:idx], kv[idx+1:])
	}

	if opts.TTY {
		// Reasonable defaults; callers that want resize handling can
		// build their own session via Shell.
		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}
		if err := sess.RequestPty("xterm-256color", 40, 120, modes); err != nil {
			return 0, fmt.Errorf("request pty: %w", err)
		}
	}

	if opts.Stdin != nil {
		sess.Stdin = opts.Stdin
	}
	if opts.Stdout != nil {
		sess.Stdout = opts.Stdout
	}
	if opts.Stderr != nil {
		sess.Stderr = opts.Stderr
	}

	cmdline := joinArgv(argv)

	// Wire ctx cancellation to session teardown so Ctrl-C unblocks Run.
	// Closing the session alone is insufficient: ssh.Session.Run calls
	// Wait, which blocks until the remote process exits — closing the
	// session does not signal the remote. Send SIGKILL first so the
	// remote process exits, then close as a backstop.
	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Signal(ssh.SIGKILL)
			_ = sess.Close()
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	runErr := sess.Run(cmdline)
	if runErr == nil {
		return 0, nil
	}

	// Remote process exited non-zero — pass the code through unchanged.
	var ee *ssh.ExitError
	if errors.As(runErr, &ee) {
		return ee.ExitStatus(), nil
	}

	// Anything else (transport error, signal kill) is a Run error,
	// not a remote exit code. Caller decides exit semantics.
	return 0, runErr
}

// Shell opens an interactive PTY shell on the remote host. Blocks until
// the remote shell exits or the context is cancelled.
func (c *Client) Shell(ctx context.Context, opts ShellOpts) error {
	if c == nil || c.conn == nil {
		return errors.New("client is closed")
	}
	sess, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty("xterm-256color", 40, 120, modes); err != nil {
		return fmt.Errorf("request pty: %w", err)
	}

	if opts.Stdin != nil {
		sess.Stdin = opts.Stdin
	}
	if opts.Stdout != nil {
		sess.Stdout = opts.Stdout
	}
	if opts.Stderr != nil {
		sess.Stderr = opts.Stderr
	}

	if err := sess.Shell(); err != nil {
		return fmt.Errorf("start shell: %w", err)
	}

	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Close()
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	if err := sess.Wait(); err != nil {
		// A clean remote exit also surfaces as *ssh.ExitError on Wait;
		// treat any non-zero shell exit as not-an-error here (the user
		// just typed `exit 1`). Transport errors flow through.
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			return nil
		}
		return err
	}
	return nil
}

// joinArgv quotes each element so the remote shell sees it as one token.
// Single-quote everything; escape embedded single quotes by closing the
// quoted run, emitting a backslash-quoted apostrophe, and reopening.
func joinArgv(argv []string) string {
	var b strings.Builder
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(shellQuote(a))
	}
	return b.String()
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Bare token — only safe if it's a strict subset of shell-safe chars.
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '_', c == '-', c == '/', c == '.', c == ':', c == ',', c == '@', c == '+', c == '=':
			// safe
		default:
			safe = false
		}
		if !safe {
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
