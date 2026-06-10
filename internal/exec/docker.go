package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	dockerclient "github.com/moby/moby/client"
)

// DockerBackend executes argv inside a per-tool docker image. The
// image is selected by argv[0] from toolImages — tools without an entry
// fall back to argv[0] interpreted as a literal image reference (so
// callers can pass "busybox:latest" directly for tests).
//
//   - Per-tool image lookup (toolImages); the GH Actions workflow
//     publishes :dev tags on tag releases.
//
//   - Kubeconfig propagation: bind-mount the SINGLE kubeconfig file
//     (not parent dir) read-only at /root/.kube/config. AWS creds
//     reach the container via the standard provider env vars
//     (AWS_ACCESS_KEY_ID etc.) inherited from the caller's env.
//
//   - Stream redaction: stdout/stderr passed through NewRedactor
//     (defense-in-depth) before reaching the caller.
//
//   - Cleanup: AutoRemove + ctx-cancel triggers ContainerKill so the
//     container doesn't outlive its parent.
type DockerBackend struct {
	// clientOnce + clientErr lazy-init the docker API client. We don't
	// connect at registration time because that would force every
	// `awsbnkctl --help` invocation to dial the docker socket.
	clientOnce sync.Once
	client     *dockerclient.Client
	clientErr  error
}

// toolImages maps argv[0] tool names to their bundled docker images.
// Image tags are resolved from the binary's version (set by ldflags at
// link time) — see toolImageTag below — so a tag-released binary
// (v0.10.0) pulls v0.10.0 images, and a `dev` build pulls :dev.
//
// Image tags are resolved from the binary version — a tagged release
// (e.g., v0.10.0) pulls matching images; a `dev` build pulls :dev.
//
// Populated lazily via the tool-image accessor below; the var keeps
// the same shape so existing tests using `toolImages["iperf3"]`
// continue to work.
var toolImages = func() map[string]string {
	tag := toolImageTag()
	return map[string]string{
		// iperf3: use the public networkstatic/iperf3 image. Same image
		// the in-cluster throughput server pod uses, public on Docker
		// Hub, and works with the iperf3 client argv (`-c <host> -J`).
		// PSA-restricted workspaces should override via
		// `test.throughput.image` to a non-root iperf3 build — see
		// chapter 22 for the PSA contract.
		"iperf3":    "networkstatic/iperf3:latest",
		"awsbnkctl": "ghcr.io/JLCode-tech/awsbnkctl-tools:" + tag,
	}
}()

// toolImageTag returns the image tag the docker (and k8s) backends
// should pull for the bundled tools. Resolution:
//
//   - If the binary's version package value is non-empty and not "dev",
//     use that as the tag (e.g., "v0.10.0"). This matches the GH Actions
//     workflow's release publish behaviour.
//   - Otherwise fall back to ":dev" — what tools/docker/Makefile builds
//     locally. Note: a clean `go install` on a fresh host with no local
//     docker build will fail to pull on this path; users should either
//     install a tagged release or run `cd tools/docker && make build-all`.
//
// We read the version via a package-level seam (toolImageTagFn) so the
// CLI can wire its `Version` constant without an import cycle (the cli
// package imports exec, not the other way around).
func toolImageTag() string {
	if toolImageTagFn != nil {
		v := toolImageTagFn()
		if v != "" && v != "dev" {
			return v
		}
	}
	return "dev"
}

// toolImageTagFn is set by the CLI layer's init() to return its
// build-time Version. Left nil for tests that import only the exec
// package — those get the ":dev" fallback.
var toolImageTagFn func() string

// SetToolImageTag wires the CLI's Version through to the image-tag
// resolver. Called from internal/cli/root.go's init().
func SetToolImageTag(fn func() string) {
	toolImageTagFn = fn
	// Recompute the toolImages map with the new tag.
	tag := toolImageTag()
	toolImages = map[string]string{
		"iperf3":    "networkstatic/iperf3:latest",
		"awsbnkctl": "ghcr.io/JLCode-tech/awsbnkctl-tools:" + tag,
	}
}

// Name implements Backend.
func (*DockerBackend) Name() string { return "docker" }

// Run implements Backend.
//
// argv[0] selects the image (via toolImages or literal); argv[1:] is
// passed as the container's command. The entrypoint baked into the
// image (e.g., `iperf3` for the iperf3 image) handles the argv[1:]
// by default — callers don't need to repeat the binary name.
func (b *DockerBackend) Run(ctx context.Context, argv []string, opts RunOpts) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("argv is empty")
	}

	cli, err := b.dockerClient()
	if err != nil {
		// 127 == backend failed to start (daemon unreachable, equivalent
		// of "command not found").
		return 127, fmt.Errorf("docker daemon unreachable; is dockerd running? (%w)", err)
	}

	// Resolve image. If argv[0] is a known tool, use its image and
	// pass argv[1:] as the command. Otherwise treat argv[0] as a
	// literal image reference and argv[1:] as the command (the
	// integration test path: ResolveBackend("docker") + Run with
	// argv=["busybox:latest", "echo", "hi"]).
	image, cmdArgv := resolveDockerImageAndArgv(argv)

	// AWS credentials reach containers via the standard AWS provider
	// env vars (AWS_ACCESS_KEY_ID etc.), inherited from the caller's
	// environment via the docker-run --env passthrough in
	// buildContainerEnv. No cred-shim wrap is needed.

	// Materialise creds + Files into a per-run tempdir.
	tempDir, err := os.MkdirTemp("", "awsbnkctl-docker-")
	if err != nil {
		return 0, fmt.Errorf("creating tempdir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	mounts, env, credsCleanup, err := b.buildMountsAndEnv(opts, tempDir)
	if credsCleanup != nil {
		defer credsCleanup()
	}
	if err != nil {
		return 0, err
	}

	// Append caller-supplied HostMounts: the host directories bind-mount
	// at the specified container paths read-write so state persists
	// across runs.
	for _, hm := range opts.HostMounts {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   hm.HostPath,
			Target:   hm.ContainerPath,
			ReadOnly: hm.ReadOnly,
		})
	}

	// Container config.
	cfg := &container.Config{
		Image:        image,
		Cmd:          cmdArgv,
		Env:          buildContainerEnv(opts.Env),
		AttachStdin:  opts.Stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
		OpenStdin:    opts.Stdin != nil,
		StdinOnce:    opts.Stdin != nil,
		Tty:          opts.TTY,
		WorkingDir:   opts.WorkDir,
		User:         opts.RunAsUser,
	}
	hostCfg := &container.HostConfig{
		AutoRemove: true,
		Mounts:     mounts,
	}
	// Append any cred-related env entries from buildMountsAndEnv. AWS
	// retarget: this slice is now always empty (kubeconfig propagates
	// via bind-mount, not env), but the append-an-empty-slice shape
	// stays so future cred-related env additions slot in here.
	cfg.Env = append(cfg.Env, env...)

	// Pull the image lazily — if it's already cached, this is a noop;
	// if not, we surface the pull failure as a 127 so callers can
	// distinguish "image not available" from "tool exited 1".
	if perr := b.ensureImage(ctx, cli, image); perr != nil {
		return 127, perr
	}

	created, err := cli.ContainerCreate(ctx, dockerclient.ContainerCreateOptions{
		Config:     cfg,
		HostConfig: hostCfg,
	})
	if err != nil {
		// 126 == backend started but the wrapped invocation couldn't
		// spawn (daemon up + image pulled, but `containerCreate`
		// rejected — bad spec, image arch mismatch, etc.).
		return 126, fmt.Errorf("docker create: %w", err)
	}
	cid := created.ID

	// Wire up an attach so we can stream stdout/stderr through the
	// redactor. ContainerWait below blocks until the container exits;
	// the StdCopy goroutine drains the multiplexed stream until the
	// container's stdout/stderr close.
	hijack, err := cli.ContainerAttach(ctx, cid, dockerclient.ContainerAttachOptions{
		Stream: true,
		Stdin:  opts.Stdin != nil,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		// 126: container created but the attach (which runs before
		// we can exec the wrapped tool) errored.
		return 126, fmt.Errorf("docker attach: %w", err)
	}
	defer hijack.Close()

	// Wrap stdout/stderr through the redactor.
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

	// Stream the multiplexed docker output. TTY mode collapses
	// stdout/stderr into a single stream; non-TTY uses stdcopy
	// framing.
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		if opts.TTY {
			_, _ = io.Copy(stdout, hijack.Reader)
			return
		}
		_, _ = stdcopy.StdCopy(stdout, stderr, hijack.Reader)
	}()

	// Stdin: pump caller's stdin into the hijacked connection if set.
	if opts.Stdin != nil {
		go func() {
			_, _ = io.Copy(hijack.Conn, opts.Stdin)
			_ = hijack.CloseWrite()
		}()
	}

	// Start the container and wait for exit.
	waitC := cli.ContainerWait(ctx, cid, dockerclient.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})

	if _, err := cli.ContainerStart(ctx, cid, dockerclient.ContainerStartOptions{}); err != nil {
		// 126: created, attached, but start failed (wrapped process
		// couldn't be spawned in the container).
		return 126, fmt.Errorf("docker start: %w", err)
	}

	// ctx cancellation triggers ContainerKill so the container
	// doesn't run on after the parent CLI exits. Use a fresh context
	// for the kill itself so the kill request isn't itself cancelled.
	cancelDone := make(chan struct{})
	// #nosec G118 -- this goroutine intentionally uses a fresh context.Background(); the parent ctx is what just got cancelled and we still need to kill the container
	go func() {
		select {
		case <-ctx.Done():
			killCtx, killCancel := context.WithTimeoutCause(context.Background(), 0, nil) // #nosec G118
			defer killCancel()
			_, _ = cli.ContainerKill(killCtx, cid, dockerclient.ContainerKillOptions{Signal: "SIGKILL"})
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	var rc int
	select {
	case res := <-waitC.Result:
		rc = int(res.StatusCode)
	case werr := <-waitC.Error:
		<-streamDone
		// 126: backend started (container running), but Wait errored
		// mid-flight — backend-level failure, not the tool's exit code.
		return 126, fmt.Errorf("docker wait: %w", werr)
	case <-ctx.Done():
		<-streamDone
		return 137, ctx.Err()
	}
	<-streamDone

	return rc, nil
}

// dockerClient lazy-inits the docker API client. Subsequent calls
// return the same client (or its cached error).
func (b *DockerBackend) dockerClient() (*dockerclient.Client, error) {
	b.clientOnce.Do(func() {
		// Use client.New (the modern constructor); API-version negotiation
		// is now enabled by default, so the legacy WithAPIVersionNegotiation
		// option isn't needed.
		c, err := dockerclient.New(dockerclient.FromEnv)
		if err != nil {
			b.clientErr = err
			return
		}
		b.client = c
	})
	return b.client, b.clientErr
}

// ensureImage pulls image if it isn't already present in the daemon's
// image cache. A missing image is the most common new-user failure
// mode; pulling lazily means `awsbnkctl test throughput --backend
// docker` just-works on first run instead of producing an opaque
// "no such image" error.
//
// Pull progress is surfaced to stderr (best-effort: we render the
// status field of each JSON message) so users see "Pulling fs layer"
// and friends rather than a multi-minute silence. Errors mid-stream
// (e.g., "manifest unknown" for an unpublished :dev tag) bubble up
// through Wait().
func (b *DockerBackend) ensureImage(ctx context.Context, cli *dockerclient.Client, image string) error {
	// Try to inspect first; only pull on miss.
	if _, err := cli.ImageInspect(ctx, image); err == nil {
		return nil
	}
	resp, err := cli.ImagePull(ctx, image, dockerclient.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("docker pull %s: %w", image, err)
	}
	defer resp.Close()
	// Drain the pull progress to stderr so users get visible feedback.
	if _, err := io.Copy(os.Stderr, resp); err != nil {
		return fmt.Errorf("docker pull %s: %w", image, err)
	}
	if err := resp.Wait(ctx); err != nil {
		return fmt.Errorf("docker pull %s: %w", image, err)
	}
	return nil
}

// buildMountsAndEnv translates RunOpts into docker container mounts +
// the list of `KEY=VALUE` env entries the container should carry.
//
// AWS credentials reach the container via the standard provider env
// vars passed through buildContainerEnv. No tempfile-bind-mount
// cred-pattern: env-var creds don't carry the same long-lived-secret
// risk as an IBM Cloud API key did. Kubeconfig propagation (the only
// cred surface this backend still owns) bind-mounts the single file
// read-only at /root/.kube/config.
func (b *DockerBackend) buildMountsAndEnv(opts RunOpts, tempDir string) ([]mount.Mount, []string, func(), error) {
	var mounts []mount.Mount
	var env []string
	cleanupFns := []func(){}
	cleanup := func() {
		for _, f := range cleanupFns {
			f()
		}
	}

	// Materialise Files into tempDir/files/ then bind-mount each.
	if len(opts.Files) > 0 {
		filesDir := filepath.Join(tempDir, "files")
		if err := os.MkdirAll(filesDir, 0o750); err != nil {
			cleanup()
			return nil, nil, nil, fmt.Errorf("creating files dir: %w", err)
		}
		for name, content := range opts.Files {
			path := filepath.Join(filesDir, filepath.Base(name))
			if err := os.WriteFile(path, content, 0o600); err != nil {
				cleanup()
				return nil, nil, nil, fmt.Errorf("writing file %q: %w", name, err)
			}
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeBind,
				Source:   path,
				Target:   filepath.Join("/work", filepath.Base(name)),
				ReadOnly: true,
			})
		}
	}

	// Kubeconfig propagation: the SINGLE kubeconfig file is bind-
	// mounted read-only at /root/.kube/config — never mount the
	// parent .kube dir.
	if opts.Credentials != nil && len(opts.Credentials.KubeconfigBytes) > 0 {
		path := filepath.Join(tempDir, "kubeconfig")
		if err := os.WriteFile(path, opts.Credentials.KubeconfigBytes, 0o600); err != nil {
			cleanup()
			return nil, nil, nil, fmt.Errorf("materialising kubeconfig: %w", err)
		}
		cleanupFns = append(cleanupFns, func() { _ = os.Remove(path) })
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   path,
			Target:   "/root/.kube/config",
			ReadOnly: true,
		})
	}

	return mounts, env, cleanup, nil
}

// buildContainerEnv translates RunOpts.Env (KEY=VALUE strings) into
// the slice docker's container.Config.Env expects. Skips entries with
// no '=' separator (silently — the local backend does the same).
// buildContainerEnv copies KEY=VALUE pairs from the caller's env into
// the container env, EXCEPT for host-specific vars that would confuse
// programs running inside the container. The caller's HOME, PATH,
// USER, SHELL, PWD etc. refer to host filesystem paths that don't
// exist inside the container; tools that look up plugin / config
// directories under $HOME (kubectl plugins, awscli profiles, etc.)
// expect the container's image-default $HOME, not the host user's.
func buildContainerEnv(env []string) []string {
	// hostOnly is the set of env vars whose values refer to host paths
	// or identities and should NEVER be propagated to a container.
	hostOnly := map[string]bool{
		"HOME":     true,
		"USER":     true,
		"USERNAME": true,
		"LOGNAME":  true,
		"SHELL":    true,
		"PWD":      true,
		"OLDPWD":   true,
		"PATH":     true,
		"TMPDIR":   true,
		"TERM":     true,
		"LANG":     true,
		"LC_ALL":   true,
	}
	var out []string
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		if hostOnly[kv[:eq]] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// dockerImageBinary maps argv[0] tool names to the in-container binary
// they invoke when the image has no ENTRYPOINT. Used by
// resolveDockerImageAndArgv to prepend the binary name explicitly to
// the container's `Cmd` slice.
//
// Tools NOT in this map keep the legacy shape — argv[1:] is passed
// verbatim as the container Cmd, relying on the image's own ENTRYPOINT
// to pick the binary (e.g. `iperf3`, whose image has ENTRYPOINT="iperf3").
//
// Why a per-tool map instead of "always prepend argv[0]"? Because
// the iperf3 image carries its own ENTRYPOINT directive; prepending
// the binary name would double-invoke (`iperf3 iperf3 --version`).
//
// The awsbnkctl tools image's `awsbnkctl` alias maps to
// `/usr/local/bin/awsbnkctl` so a `--backend docker` invocation of
// awsbnkctl-as-tool (the dns-probe re-exec path, etc.) lands on the
// right binary.
var dockerImageBinary = map[string][]string{
	"awsbnkctl": {"/usr/local/bin/awsbnkctl"},
}

// resolveDockerImageAndArgv picks the docker image and the in-container
// argv from the caller's argv.
//
//   - If argv[0] is a known tool with an entry in dockerImageBinary,
//     its image is looked up AND the in-container Cmd is prepended
//     with the tool's binary name (so the image can have no
//     ENTRYPOINT and still run the right binary).
//   - If argv[0] is a known tool WITHOUT an entry in dockerImageBinary
//     (e.g. iperf3), its image is looked up and argv[1:] is passed
//     verbatim — the image's own ENTRYPOINT picks the binary.
//   - Otherwise argv[0] is treated as a literal image reference and
//     argv[1:] is the in-container command — useful for tests and
//     ad-hoc shapes.
func resolveDockerImageAndArgv(argv []string) (image string, cmdArgv []string) {
	if img, ok := toolImages[argv[0]]; ok {
		if bin, hasBin := dockerImageBinary[argv[0]]; hasBin {
			cmd := make([]string, 0, len(bin)+len(argv)-1)
			cmd = append(cmd, bin...)
			cmd = append(cmd, argv[1:]...)
			return img, cmd
		}
		return img, argv[1:]
	}
	return argv[0], argv[1:]
}

func init() {
	Register("docker", &DockerBackend{})
}
