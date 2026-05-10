package exec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// LocalBackend executes argv in a child process on the host running
// roksbnkctl. Wraps `os/exec` with the credential-propagation +
// stream-redaction behaviour the Backend interface requires.
//
// Behaviour matches the pre-Sprint-3 passthrough callsites in
// internal/cli/cluster.go (runWithEnv): host env is inherited; Env
// entries from RunOpts.Env are appended (later wins for duplicates,
// per os/exec's documented semantics); cred env vars come from
// Credentials.EnvVars().
//
// The local backend never materialises Files into the parent host's
// filesystem at arbitrary paths — that would surprise users. Instead,
// when Files is non-empty, the backend writes them inside RunOpts.WorkDir
// (or a tempdir if WorkDir is empty, returning that as cwd). Sprint 3
// ibmcloud passthrough doesn't need Files for local; the path exists
// for symmetry with docker / k8s / ssh backends.
type LocalBackend struct{}

// Name implements Backend.
func (LocalBackend) Name() string { return "local" }

// Run implements Backend.
//
// Exit-code semantics (PRD 03 §"Backend interface", 126/127 split):
//
//   - argv[0] not on PATH → returns (127, error). Matches POSIX shell
//     "command not found" convention; PRD 03 reserves 127 for
//     backend-side failed-to-start, and "binary not on PATH" is the
//     local-backend analog (no daemon to be unreachable, no SSH to fail
//     to connect). The 126 ("started then failed") case doesn't apply
//     to the local backend — there's no backend-startup phase distinct
//     from process-spawn, so we never split that direction.
//
//   - Child runs and exits non-zero → returns (childExit, *exec.ExitError).
//     Caller can ignore the error (rc is the source of truth) or
//     surface the wrap.
//
//   - Child runs and exits 0 → returns (0, nil).
//
//   - ctx cancelled mid-run → SIGKILL the child, return (137, ctx.Err())
//     (137 = 128 + SIGKILL, the Linux convention).
func (LocalBackend) Run(ctx context.Context, argv []string, opts RunOpts) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("argv is empty")
	}

	bin, err := exec.LookPath(argv[0])
	if err != nil {
		// Fall back to the literal path — exec.CommandContext
		// tolerates absolute paths that LookPath rejects (e.g., /bin/sh
		// when the host's PATH is sanitised). If both fail, the
		// CommandContext below surfaces the clearer error.
		if filepath.IsAbs(argv[0]) {
			bin = argv[0]
		} else {
			return 127, err
		}
	}

	// Resolve effective env: host env + caller Env + cred env vars from
	// the (optional) Credentials struct. Caller Env wins over host env
	// for duplicates because os/exec.Cmd.Env appends in order and the
	// kernel uses the last entry. Credentials.EnvVars() comes last so
	// they override anything the caller might have shadowed.
	env := os.Environ()
	env = append(env, opts.Env...)
	if opts.Credentials != nil {
		env = append(env, opts.Credentials.EnvVars()...)
	}

	// Materialise Files into WorkDir (or a per-exec tempdir).
	workDir := opts.WorkDir
	var cleanupTemp func()
	if len(opts.Files) > 0 && workDir == "" {
		td, terr := os.MkdirTemp("", "roksbnkctl-local-")
		if terr != nil {
			return 0, terr
		}
		workDir = td
		cleanupTemp = func() { _ = os.RemoveAll(td) }
	}
	if cleanupTemp != nil {
		defer cleanupTemp()
	}
	for name, content := range opts.Files {
		path := filepath.Join(workDir, name)
		if derr := os.MkdirAll(filepath.Dir(path), 0o755); derr != nil {
			return 0, derr
		}
		if werr := os.WriteFile(path, content, 0o600); werr != nil {
			return 0, werr
		}
	}

	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Env = env
	if workDir != "" {
		cmd.Dir = workDir
	}

	// Wrap stdout/stderr through the redactor so a wrapped tool that
	// accidentally prints the IBM API key (e.g., `ibmcloud --debug`)
	// gets caught before the bytes reach the caller. PRD 04 §"Cross-
	// backend principles" #1 — defense-in-depth.
	stdout, stdoutClose := wrapForRedaction(opts.Stdout, opts.Credentials)
	stderr, stderrClose := wrapForRedaction(opts.Stderr, opts.Credentials)
	defer func() {
		if stdoutClose != nil {
			_ = stdoutClose()
		}
		if stderrClose != nil {
			_ = stderrClose()
		}
	}()

	cmd.Stdin = opts.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// CommandContext + a non-nil Cancel callback makes ctx cancellation
	// SIGKILL the process group. For Sprint 3 we use the default Cancel
	// (sends os.Kill); a per-pgid kill that catches grandchildren can
	// land in a Sprint 4 polish pass if any tool starts misbehaving.
	runErr := cmd.Run()
	if runErr == nil {
		return 0, nil
	}

	// Distinguish ctx-cancelled (137) from generic exit error.
	if ctx.Err() != nil {
		return 137, ctx.Err()
	}

	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		return ee.ExitCode(), runErr
	}
	return 0, runErr
}

// wrapForRedaction returns w wrapped through NewRedactor when the
// secrets list is non-empty, plus a Close() function the caller defers
// (nil if no wrap was applied or w is nil).
//
// w == nil means "callee doesn't want this stream" — we pass io.Discard
// through, so the wrapped child can still write without blocking on a
// nil writer.
func wrapForRedaction(w io.Writer, creds *Credentials) (io.Writer, func() error) {
	if w == nil {
		w = io.Discard
	}
	if creds == nil || creds.IBMCloudAPIKey == "" {
		return w, nil
	}
	r := NewRedactor(w, []string{creds.IBMCloudAPIKey})
	if c, ok := r.(io.Closer); ok {
		return r, c.Close
	}
	return r, nil
}

func init() {
	Register("local", LocalBackend{})
}
