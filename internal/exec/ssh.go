package exec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/remote"
)

// Exit codes for the SSH backend, aligned with PRD 03 §"Backend interface".
const (
	sshExitFailedToStart     = 127 // backend couldn't connect or bootstrap couldn't reach repo
	sshExitStartedThenFailed = 126 // session established but exec couldn't spawn the wrapped process
)

// toolPackages maps argv[0] tool names to the apt package + repo
// metadata the SSH bootstrap path needs. Sprint 4 covers iperf3 and
// ibmcloud; future tools register here.
//
// IBMRepo=true triggers the "add IBM apt repo + GPG key" pre-step
// before the install. The Sprint 0 jumphost integration tests pinned
// `jammy` (Ubuntu 22.04); we keep that for v1 and bump in a later
// sprint when noble (24.04) becomes the default ROKS jumphost image.
type toolPackage struct {
	Name    string
	IBMRepo bool
}

var toolPackages = map[string]toolPackage{
	"iperf3":   {Name: "iperf3", IBMRepo: false},
	"ibmcloud": {Name: "ibmcloud-cli", IBMRepo: true},
}

// SSHBackendOpts are runtime knobs the SSH backend reads via package-
// level setters (analogous to k8s.go's SetK8sInit). Keeps the cobra
// layer out of the exec package.
type SSHBackendOpts struct {
	// Bootstrap toggles the "auto-install missing tools via apt" path.
	// Default false (PRD 03 §"open questions" recommendation: opt-in).
	// CLI plumbs `--bootstrap` here.
	Bootstrap bool

	// Workspace + Target plumbing — backend resolves target via
	// remote.LoadTarget(workspace, name). Set by the CLI dispatcher
	// before invoking Run; left blank for tests that wire a synthetic
	// target via SetSSHTargetResolver.
	Workspace string

	// InsecureHostKey mirrors the persistent --insecure-host-key flag.
	InsecureHostKey bool
}

// sshOpts is the package-level SSH backend config. CLI calls
// SetSSHOpts before dispatch; tests set fields directly via the var.
var sshOpts SSHBackendOpts

// SetSSHOpts is the seam the CLI layer uses to push --bootstrap and
// related flags into the backend without an import cycle. Safe to call
// repeatedly; the latest call wins.
func SetSSHOpts(opts SSHBackendOpts) { sshOpts = opts }

// sshTargetResolver is the seam tests use to inject a synthetic target
// without touching the workspace config loader. Production callers
// leave it nil; the default falls through to remote.LoadTarget.
var sshTargetResolver func(workspace, name string) (*remote.Target, map[string][]byte, error)

// SetSSHTargetResolver overrides the default (workspace-config-backed)
// target resolver. Called from internal/cli (and tests).
func SetSSHTargetResolver(fn func(workspace, name string) (*remote.Target, map[string][]byte, error)) {
	sshTargetResolver = fn
}

// remoteClient is the minimum subset of *remote.Client surface that
// SSHBackend invokes. Extracting it as an interface lets tests inject
// a fake client that captures wrapper-script content + simulates
// bootstrap-failure modes (sudo failure, non-Ubuntu, repo
// unreachable) without dialing a live sshd.
//
// Sprint 4 validator Issue 3 carry-over: the production *remote.Client
// satisfies this surface natively (its Run + Close signatures match);
// tests replace `sshClientFactory` with a mock that captures the
// argv + stdin streams that the SSHBackend's wrapper-script path
// emits.
type remoteClient interface {
	Run(ctx context.Context, argv []string, opts remote.RunOpts) (int, error)
	Close() error
}

// sshClientFactory is the package-level seam for swapping the
// `remote.Connect` constructor. Production = nil → default falls
// through to remote.Connect; tests assign a func returning a mock
// remoteClient.
//
// Mirrors the existing Sprint 4 SetSSHTargetResolver pattern (PRD 03
// §"SSH" §"open questions" + resolved_sprint4_validator.md Issue 3).
var sshClientFactory func(ctx context.Context, target *remote.Target) (remoteClient, error)

// SetSSHClientFactory wires a custom remote.Client constructor for
// tests. Production callers leave the factory unset; the SSHBackend
// falls through to remote.Connect.
//
// Test usage:
//
//	exec.SetSSHClientFactory(func(_ context.Context, _ *remote.Target) (remoteClient, error) {
//	    return &fakeRemoteClient{...}, nil
//	})
//	defer exec.SetSSHClientFactory(nil)
//	// ... exercise the backend; assert against the fake's captures ...
func SetSSHClientFactory(fn func(ctx context.Context, target *remote.Target) (remoteClient, error)) {
	sshClientFactory = fn
}

// connectViaFactory dispatches to either the test factory (when set)
// or the production remote.Connect path. Returns the remoteClient
// interface so the rest of SSHBackend.Run can stay shape-agnostic.
func connectViaFactory(ctx context.Context, t *remote.Target) (remoteClient, error) {
	if sshClientFactory != nil {
		return sshClientFactory(ctx, t)
	}
	c, err := remote.Connect(ctx, t)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// SSHBackend wraps internal/remote.Client with the surface PRD 03 specifies:
// pre-flight tool check + apt bootstrap, file materialization in a
// per-run tempdir on the remote, env propagation via SetEnv with a
// wrapper-script-with-trap fallback, TTY pass-through, and trap-on-EXIT
// cleanup.
//
// One SSHBackend instance handles all targets — the per-Run target name
// comes from the spec ("ssh:<target>") parsed by ResolveBackend.
//
// PRD 03 §"SSH" + PRD 04 §"SSH" jointly drive the design.
type SSHBackend struct{}

// Name implements Backend.
func (*SSHBackend) Name() string { return "ssh" }

// Run implements Backend. The target name is conventionally embedded
// in opts.Env via the sentinel ROKSBNKCTL_SSH_TARGET=<name> set by the
// CLI dispatch layer (mirrors k8s_long_lived_key); the registry's
// ResolveBackend("ssh:<target>") path also stamps the same key.
func (b *SSHBackend) Run(ctx context.Context, argv []string, opts RunOpts) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("argv is empty")
	}

	target, env := extractSSHTarget(opts.Env)
	opts.Env = env
	if target == "" {
		return sshExitFailedToStart, errors.New("ssh backend: no target specified (use --backend ssh:<target>)")
	}

	// Resolve target → connect-ready remote.Target.
	resolveFn := sshTargetResolver
	if resolveFn == nil {
		resolveFn = defaultSSHTargetResolver
	}
	t, _, err := resolveFn(sshOpts.Workspace, target)
	if err != nil {
		return sshExitFailedToStart, fmt.Errorf("ssh target %q: %w", target, err)
	}

	client, err := connectViaFactory(ctx, t)
	if err != nil {
		return sshExitFailedToStart, fmt.Errorf("ssh connect %s: %w", t.Name, err)
	}
	defer client.Close()

	// Pre-flight tool check + (optional) bootstrap.
	tool := argv[0]
	if rc, err := b.ensureTool(ctx, client, tool); err != nil {
		return rc, err
	}

	// Allocate a per-run tempdir on the remote.
	tempdir, err := b.makeRemoteTempdir(ctx, client)
	if err != nil {
		return sshExitStartedThenFailed, fmt.Errorf("ssh tempdir: %w", err)
	}
	// Trap-on-EXIT in the wrapper script handles cleanup on the remote
	// even if our ctx cancels mid-exec; we belt-and-braces with an
	// explicit rm here for happy-path tidy-up.
	defer func() {
		_, _ = client.Run(context.Background(), []string{"rm", "-rf", tempdir}, remote.RunOpts{
			Stdout: io.Discard, Stderr: io.Discard,
		})
	}()

	// Materialise Files into the tempdir.
	if err := b.writeFiles(ctx, client, tempdir, opts.Files); err != nil {
		return sshExitStartedThenFailed, fmt.Errorf("ssh file materialisation: %w", err)
	}

	// Build the env-passing strategy. PRD 04 §"SSH": prefer SetEnv,
	// fall back to wrapper-script-with-trap when the remote's
	// AcceptEnv silently drops our values.
	mergedEnv := mergeSSHEnv(opts.Env, opts.Credentials)

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

	// First attempt: SetEnv path. Sentinel-canary check decides
	// whether to fall back. The canary uses an ephemeral env var name
	// the wrapper can read back without leaking real secrets.
	canaryName, canaryValue := makeCanary()
	envWithCanary := append(append([]string(nil), mergedEnv...), canaryName+"="+canaryValue)

	if accepted, err := b.canarySetEnvCheck(ctx, client, canaryName, canaryValue, envWithCanary); err != nil {
		return sshExitStartedThenFailed, fmt.Errorf("ssh env canary: %w", err)
	} else if accepted {
		// SetEnv path works — exec the tool with native SetEnv.
		rc, runErr := client.Run(ctx, argv, remote.RunOpts{
			Stdin:  opts.Stdin,
			Stdout: stdout,
			Stderr: stderr,
			Env:    mergedEnv,
			TTY:    opts.TTY,
		})
		if runErr != nil {
			return sshExitStartedThenFailed, fmt.Errorf("ssh run: %w", runErr)
		}
		return rc, nil
	}

	// Fallback: wrapper-script-with-trap. Writes KEY=VALUE entries to
	// `<tempdir>/.env`, sources it (silently — no `set -x`), execs
	// argv, then cleans up via trap on EXIT.
	rc, err := b.runViaWrapper(ctx, client, tempdir, argv, mergedEnv, opts, stdout, stderr)
	if err != nil {
		return sshExitStartedThenFailed, fmt.Errorf("ssh wrapper run: %w", err)
	}
	return rc, nil
}

// extractSSHTarget pulls the target sentinel out of env and returns
// (target, filteredEnv). Mirrors k8s.go's extractLongLivedFlag pattern.
func extractSSHTarget(env []string) (string, []string) {
	out := make([]string, 0, len(env))
	target := ""
	const prefix = "ROKSBNKCTL_SSH_TARGET="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			target = kv[len(prefix):]
			continue
		}
		out = append(out, kv)
	}
	return target, out
}

// defaultSSHTargetResolver loads the workspace target by name. The
// signer + host-key callback are populated identically to
// internal/cli/remote.go's dispatchRemote so the SSH backend doesn't
// regress that path's semantics.
//
// We deliberately don't import internal/cli here (would cycle); the CLI
// layer registers its richer resolver via SetSSHTargetResolver, which
// can pull tf-output-derived signers + insecure-host-key plumbing. This
// default exists for tests + early integration; the CLI override is
// the production path.
func defaultSSHTargetResolver(workspace, name string) (*remote.Target, map[string][]byte, error) {
	t, err := remote.LoadTarget(workspace, name)
	if err != nil {
		return nil, nil, err
	}
	signer, err := remote.ResolveSigner(t, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving signer for %s: %w", name, err)
	}
	t.Signer = signer
	t.HostKeyCallback = remote.HostKeyCallback(remote.HostKeyOptions{Insecure: sshOpts.InsecureHostKey})
	return t, nil, nil
}

// ensureTool checks whether `tool` is on the remote PATH; if not, runs
// the apt-bootstrap path when --bootstrap is opted in. Returns
// (exitCode, error) following the PRD 03 split.
func (b *SSHBackend) ensureTool(ctx context.Context, client remoteClient, tool string) (int, error) {
	rc, _ := client.Run(ctx, []string{"sh", "-c", "command -v " + shellSingleQuote(tool)}, remote.RunOpts{
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if rc == 0 {
		return 0, nil
	}
	if !sshOpts.Bootstrap {
		return 127, fmt.Errorf("ssh: tool %q not found on remote PATH; rerun with --bootstrap to apt-install it", tool)
	}

	pkg, ok := toolPackages[tool]
	if !ok {
		return sshExitStartedThenFailed, fmt.Errorf("ssh: no apt package mapping for tool %q (pre-install on the target)", tool)
	}

	// Detect Ubuntu — PRD 03 only supports Ubuntu auto-install in v1.
	// The exit code is intentionally ignored: `lsb_release -is` may not
	// be installed on minimal images (the `|| true` shell-side fallback
	// keeps the command's exit at 0); we read the empty stdout in that
	// case and surface the "non-Ubuntu" message via the EqualFold below.
	var osIDOut bytes.Buffer
	_, _ = client.Run(ctx, []string{"sh", "-c", "lsb_release -is 2>/dev/null || true"}, remote.RunOpts{
		Stdout: &osIDOut, Stderr: io.Discard,
	})
	osID := strings.TrimSpace(osIDOut.String())
	if !strings.EqualFold(osID, "Ubuntu") {
		return sshExitStartedThenFailed, fmt.Errorf("ssh: --bootstrap auto-install only supports Ubuntu (got %q); pre-install %s on the target", osID, pkg.Name)
	}

	// IBM apt repo + GPG key for ibmcloud-cli.
	if pkg.IBMRepo {
		repoCmd := "set -e; " +
			"curl -fsSL https://download.clis.cloud.ibm.com/Linux/Ubuntu/repo.gpg | sudo -n apt-key add - && " +
			"echo 'deb https://download.clis.cloud.ibm.com/Linux/Ubuntu jammy main' | sudo -n tee /etc/apt/sources.list.d/ibmcloud.list >/dev/null"
		rc, err := client.Run(ctx, []string{"sh", "-c", repoCmd}, remote.RunOpts{
			Stdout: io.Discard, Stderr: io.Discard,
		})
		if err != nil {
			return sshExitFailedToStart, fmt.Errorf("ssh: adding IBM apt repo: %w", err)
		}
		if rc != 0 {
			return sshExitFailedToStart, fmt.Errorf("ssh: adding IBM apt repo failed (rc=%d) — target can't reach https://download.clis.cloud.ibm.com or sudo password required", rc)
		}
	}

	// apt-get update + install with sudo -n (passwordless required).
	rc, err := client.Run(ctx, []string{"sudo", "-n", "apt-get", "update", "-y"}, remote.RunOpts{
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil || rc != 0 {
		return 126, fmt.Errorf("ssh: `sudo -n apt-get update` failed (rc=%d). The SSH user needs passwordless sudo for apt-get. Configure `<user> ALL=(ALL) NOPASSWD: /usr/bin/apt-get` in /etc/sudoers, or pre-install %s manually", rc, pkg.Name)
	}
	rc, err = client.Run(ctx, []string{"sudo", "-n", "apt-get", "install", "-y", pkg.Name}, remote.RunOpts{
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil || rc != 0 {
		return 126, fmt.Errorf("ssh: `sudo -n apt-get install -y %s` failed (rc=%d). Pre-install on the target or fix sudo configuration", pkg.Name, rc)
	}
	return 0, nil
}

// makeRemoteTempdir creates `/tmp/roksbnkctl.<random>` on the remote
// and returns the path. Uses `mktemp -d` so we don't fight with
// concurrent invocations.
func (b *SSHBackend) makeRemoteTempdir(ctx context.Context, client remoteClient) (string, error) {
	var out bytes.Buffer
	rc, err := client.Run(ctx, []string{"mktemp", "-d", "/tmp/roksbnkctl.XXXXXXXX"}, remote.RunOpts{
		Stdout: &out, Stderr: io.Discard,
	})
	if err != nil {
		return "", err
	}
	if rc != 0 {
		return "", fmt.Errorf("mktemp -d returned rc=%d", rc)
	}
	dir := strings.TrimSpace(out.String())
	if dir == "" {
		return "", errors.New("mktemp returned empty path")
	}
	// chmod 0700 belt-and-braces — mktemp on most distros is 0700 by default.
	_, _ = client.Run(ctx, []string{"chmod", "0700", dir}, remote.RunOpts{Stdout: io.Discard, Stderr: io.Discard})
	return dir, nil
}

// writeFiles writes each Files entry into the remote tempdir. Uses a
// shell heredoc-via-base64 round-trip so binary content survives intact
// (heredoc plain-text would break on quoting; base64 sidesteps that).
func (b *SSHBackend) writeFiles(ctx context.Context, client remoteClient, tempdir string, files map[string][]byte) error {
	for name, content := range files {
		base := lastPathComponent(name)
		if base == "" || base == "." || base == ".." {
			return fmt.Errorf("invalid file basename %q", name)
		}
		dst := tempdir + "/" + base
		// base64-encode content; pipe through `base64 -d > <dst>` on the remote.
		// We use `set -e` so a broken pipe surfaces as non-zero rc.
		cmd := fmt.Sprintf("set -e; umask 077; base64 -d > %s", shellSingleQuote(dst))
		rc, err := client.Run(ctx, []string{"sh", "-c", cmd}, remote.RunOpts{
			Stdin:  bytes.NewReader([]byte(base64Encode(content))),
			Stdout: io.Discard, Stderr: io.Discard,
		})
		if err != nil {
			return fmt.Errorf("writing %s: %w", base, err)
		}
		if rc != 0 {
			return fmt.Errorf("writing %s: remote rc=%d", base, rc)
		}
	}
	return nil
}

// canarySetEnvCheck verifies the remote sshd accepts our SetEnv values.
// Runs a minimal `printenv <CANARY>` over a session that sets the
// canary; if the value comes back, native SetEnv works for this host
// + sshd configuration. Otherwise we fall back to the wrapper script.
//
// PRD 04 §"SSH" — the AcceptEnv-restricted-by-default behaviour means
// most hosts silently drop our env; the canary detects that without
// surfacing real secrets.
func (b *SSHBackend) canarySetEnvCheck(ctx context.Context, client remoteClient, name, value string, env []string) (bool, error) {
	var out bytes.Buffer
	rc, err := client.Run(ctx, []string{"printenv", name}, remote.RunOpts{
		Stdout: &out,
		Stderr: io.Discard,
		Env:    env,
	})
	if err != nil {
		// Transport error — the SetEnv path itself failed. Treat as
		// "fall back to wrapper" rather than fatal so a misconfigured
		// remote still works for the user.
		return false, nil
	}
	got := strings.TrimSpace(out.String())
	// rc==0 + canary echoed back ⇒ SetEnv works.
	return rc == 0 && got == value, nil
}

// runViaWrapper writes a wrapper script + .env file under tempdir,
// then execs the wrapper. The wrapper sources the .env silently
// (no `set -x`), traps EXIT for cleanup, and execs argv.
func (b *SSHBackend) runViaWrapper(ctx context.Context, client remoteClient, tempdir string, argv []string, env []string, opts RunOpts, stdout, stderr io.Writer) (int, error) {
	// Write .env file (one KEY=VALUE per line).
	envFile := tempdir + "/.env"
	envContent := strings.Builder{}
	for _, kv := range env {
		// Skip malformed entries to avoid wrapper-script parse errors.
		if !strings.Contains(kv, "=") {
			continue
		}
		// shell-quote the value — KEY='VALUE'. The shell-source path
		// then handles arbitrary characters in VALUE without issue.
		eq := strings.IndexByte(kv, '=')
		k, v := kv[:eq], kv[eq+1:]
		envContent.WriteString(k)
		envContent.WriteString("=")
		envContent.WriteString(shellSingleQuote(v))
		envContent.WriteString("\n")
	}
	writeCmd := fmt.Sprintf("set -e; umask 077; cat > %s", shellSingleQuote(envFile))
	rc, err := client.Run(ctx, []string{"sh", "-c", writeCmd}, remote.RunOpts{
		Stdin:  bytes.NewReader([]byte(envContent.String())),
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil || rc != 0 {
		return sshExitStartedThenFailed, fmt.Errorf("writing .env: rc=%d err=%v", rc, err)
	}

	// Build the wrapper. `set +x` keeps trace silent (defense-in-
	// depth — even if the user has `bash -x` aliased on the remote,
	// our wrapper explicitly disables it before sourcing the env).
	cmdline := joinArgvShell(argv)
	wrapper := strings.Builder{}
	wrapper.WriteString("#!/bin/sh\n")
	wrapper.WriteString("set +x\n")
	wrapper.WriteString("trap 'rm -rf ")
	wrapper.WriteString(shellSingleQuote(tempdir))
	wrapper.WriteString("' EXIT\n")
	wrapper.WriteString("set -a\n")
	wrapper.WriteString(". ")
	wrapper.WriteString(shellSingleQuote(envFile))
	wrapper.WriteString("\n")
	wrapper.WriteString("set +a\n")
	if opts.WorkDir != "" {
		wrapper.WriteString("cd ")
		wrapper.WriteString(shellSingleQuote(opts.WorkDir))
		wrapper.WriteString("\n")
	} else if len(opts.Files) > 0 {
		wrapper.WriteString("cd ")
		wrapper.WriteString(shellSingleQuote(tempdir))
		wrapper.WriteString("\n")
	}
	wrapper.WriteString("exec ")
	wrapper.WriteString(cmdline)
	wrapper.WriteString("\n")

	wrapperPath := tempdir + "/run.sh"
	wrapWriteCmd := fmt.Sprintf("set -e; umask 077; cat > %s && chmod 0700 %s",
		shellSingleQuote(wrapperPath), shellSingleQuote(wrapperPath))
	rc, err = client.Run(ctx, []string{"sh", "-c", wrapWriteCmd}, remote.RunOpts{
		Stdin:  bytes.NewReader([]byte(wrapper.String())),
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil || rc != 0 {
		return sshExitStartedThenFailed, fmt.Errorf("writing wrapper: rc=%d err=%v", rc, err)
	}

	// Exec the wrapper. Caller's stdin/stdout/stderr stream through.
	rc, err = client.Run(ctx, []string{"sh", wrapperPath}, remote.RunOpts{
		Stdin:  opts.Stdin,
		Stdout: stdout,
		Stderr: stderr,
		TTY:    opts.TTY,
	})
	if err != nil {
		return sshExitStartedThenFailed, err
	}
	return rc, nil
}

// mergeSSHEnv combines RunOpts.Env (caller-supplied) with creds.EnvVars()
// (resolver-derived). Late entries win.
//
// Local-user / local-session env vars are NOT propagated to the remote
// shell — they reference paths and identities that only exist on the
// caller's host (e.g. HOME=/home/jgruber on the local machine doesn't
// exist on a Ubuntu jumphost where the remote user is `ubuntu`). The
// remote shell starts with its own login env (HOME, USER, PATH, etc.)
// and the project-specific vars (IBMCLOUD_*, KUBECONFIG, ROKSBNKCTL_*,
// TERM, LANG) we deliberately propagate sit on top of that.
//
// Without this filter, the remote ibmcloud CLI tries to mkdir the
// caller's local HOME path and fails with "Configuration error: mkdir
// /home/jgruber: permission denied" (e2e Phase I2 surfaced this).
func mergeSSHEnv(env []string, creds *Credentials) []string {
	// Env keys that are per-user or per-session on the caller's host.
	// Stripping them lets the remote shell use its own login-derived
	// values. Match exact key names; substrings are NOT matched (we
	// don't want to accidentally drop IBMCLOUD_HOME or similar).
	localOnly := map[string]bool{
		"HOME":    true,
		"USER":    true,
		"LOGNAME": true,
		"PWD":     true,
		"OLDPWD":  true,
		"SHELL":   true,
		"PATH":    true,
		"TMPDIR":  true,
	}
	merged := make(map[string]string)
	order := []string{}
	add := func(kv string) {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			return
		}
		k := kv[:eq]
		if localOnly[k] {
			return
		}
		if _, ok := merged[k]; !ok {
			order = append(order, k)
		}
		merged[k] = kv[eq+1:]
	}
	for _, kv := range env {
		add(kv)
	}
	if creds != nil {
		for _, kv := range creds.EnvVars() {
			add(kv)
		}
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+merged[k])
	}
	return out
}

// makeCanary returns a unique env var name + random value used by the
// SetEnv probe. Both are short, ASCII, and non-secret.
func makeCanary() (string, string) {
	var b [8]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	return "ROKSBNKCTL_SETENV_CANARY_" + suffix, "v_" + suffix
}

// base64Encode is std-encoding base64 with no line wrapping. Inlined
// to avoid a heavy import for a one-liner.
func base64Encode(data []byte) string {
	const tab = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	out := make([]byte, 0, ((len(data)+2)/3)*4)
	for i := 0; i+3 <= len(data); i += 3 {
		v := uint32(data[i])<<16 | uint32(data[i+1])<<8 | uint32(data[i+2])
		out = append(out, tab[(v>>18)&0x3F], tab[(v>>12)&0x3F], tab[(v>>6)&0x3F], tab[v&0x3F])
	}
	rem := len(data) % 3
	if rem == 1 {
		v := uint32(data[len(data)-1]) << 16
		out = append(out, tab[(v>>18)&0x3F], tab[(v>>12)&0x3F], '=', '=')
	} else if rem == 2 {
		v := uint32(data[len(data)-2])<<16 | uint32(data[len(data)-1])<<8
		out = append(out, tab[(v>>18)&0x3F], tab[(v>>12)&0x3F], tab[(v>>6)&0x3F], '=')
	}
	return string(out)
}

// shellSingleQuote wraps s in single quotes, escaping embedded ones via
// the canonical close-quote / backslash-quote / re-open trick. Mirrors
// internal/remote/ssh.go::shellQuote, kept private here so the exec
// package doesn't need to export the helper.
func shellSingleQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// joinArgvShell shell-quotes each element and joins with spaces. The
// remote shell parses the resulting line as a single command.
func joinArgvShell(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellSingleQuote(a)
	}
	return strings.Join(parts, " ")
}

// lastPathComponent returns everything after the last '/' in p, or p
// itself if none. Avoids dragging path/filepath in for one helper.
func lastPathComponent(p string) string {
	idx := strings.LastIndexByte(p, '/')
	if idx < 0 {
		return p
	}
	return p[idx+1:]
}

func init() {
	Register("ssh", &SSHBackend{})
}
