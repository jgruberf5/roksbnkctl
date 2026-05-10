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
// PRD 03 §"Docker (internal/exec/docker.go)" + PRD 04 §"Docker
// container" jointly drive the implementation:
//
//   - Per-tool image lookup (toolImages); the GH Actions workflow
//     publishes :dev tags on tag releases.
//
//   - Cred propagation: --env IBMCLOUD_API_KEY (no =value) so
//     `docker inspect` can never leak the value; bind-mount the SINGLE
//     kubeconfig file (not parent dir) read-only at /root/.kube/config.
//
//   - Stream redaction: stdout/stderr passed through NewRedactor
//     (defense-in-depth) before reaching the caller.
//
//   - Cleanup: AutoRemove + ctx-cancel triggers ContainerKill so the
//     container doesn't outlive its parent.
type DockerBackend struct {
	// clientOnce + clientErr lazy-init the docker API client. We don't
	// connect at registration time because that would force every
	// `roksbnkctl --help` invocation to dial the docker socket.
	clientOnce sync.Once
	client     *dockerclient.Client
	clientErr  error
}

// toolImages maps argv[0] tool names to their bundled docker images.
// Image tags are resolved from the binary's version (set by ldflags at
// link time) — see toolImageTag below — so a tag-released binary
// (v0.10.0) pulls v0.10.0 images, and a `dev` build pulls :dev.
//
// PRD 03 §"Docker (internal/exec/docker.go)" §"Tool migration plan" +
// Sprint 3 tech-writer Issue 8 carry-over (the :dev hard-code broke
// `go install ./cmd/roksbnkctl` on a fresh host because CI doesn't
// publish :dev). Sprint 4 fixes this by pinning to the binary's version.
//
// Populated lazily via the tool-image accessor below; the var keeps
// the same shape so existing tests using `toolImages["iperf3"]`
// continue to work.
var toolImages = func() map[string]string {
	tag := toolImageTag()
	return map[string]string{
		"ibmcloud":   "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:" + tag,
		"iperf3":     "ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:" + tag,
		"terraform":  "hashicorp/terraform:1.5.7",
		"roksbnkctl": "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:" + tag,
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
		"ibmcloud":   "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:" + tag,
		"iperf3":     "ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:" + tag,
		"terraform":  "hashicorp/terraform:1.5.7",
		"roksbnkctl": "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:" + tag,
	}
}

// Name implements Backend.
func (*DockerBackend) Name() string { return "docker" }

// Run implements Backend.
//
// argv[0] selects the image (via toolImages or literal); argv[1:] is
// passed as the container's command. The entrypoint baked into the
// image (e.g., `ibmcloud` for the ibmcloud-cli image) handles the
// argv[1:] by default — callers don't need to repeat the binary name.
func (b *DockerBackend) Run(ctx context.Context, argv []string, opts RunOpts) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("argv is empty")
	}

	cli, err := b.dockerClient()
	if err != nil {
		// PRD 03 §"Backend interface": 127 == backend failed to start
		// (daemon unreachable, equivalent of "command not found").
		return 127, fmt.Errorf("docker daemon unreachable; is dockerd running? (%w)", err)
	}

	// Resolve image. If argv[0] is a known tool, use its image and
	// pass argv[1:] as the command. Otherwise treat argv[0] as a
	// literal image reference and argv[1:] as the command (the
	// integration test path: ResolveBackend("docker") + Run with
	// argv=["busybox:latest", "echo", "hi"]).
	image, cmdArgv := resolveDockerImageAndArgv(argv)

	// Materialise creds + Files into a per-run tempdir.
	tempDir, err := os.MkdirTemp("", "roksbnkctl-docker-")
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

	// Append caller-supplied HostMounts (Sprint 5 terraform path).
	// PRD 03 §"terraform" §"Docker container": the workspace state
	// directory bind-mounts at /state read-write so terraform's local
	// backend persists state across runs.
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
	// Stash the propagated env names (no values) on the container env
	// list. PRD 04 §Docker forbids the =value form, so we only emit
	// the bare name; the docker daemon picks up the value from the
	// caller's environment at container-start time.
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
		// PRD 03 §"Backend interface": 126 == backend started but the
		// wrapped invocation couldn't spawn (daemon up + image pulled,
		// but `containerCreate` rejected — bad spec, image arch
		// mismatch, etc.).
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
		// we can exec the wrapped tool) errored. PRD 03 split.
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
		// couldn't be spawned in the container). PRD 03 split.
		return 126, fmt.Errorf("docker start: %w", err)
	}

	// ctx cancellation triggers ContainerKill so the container
	// doesn't run on after the parent CLI exits. Use a fresh context
	// for the kill itself so the kill request isn't itself cancelled.
	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killCtx, killCancel := context.WithTimeoutCause(context.Background(), 0, nil)
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
// mode; pulling lazily means `roksbnkctl ibmcloud --backend docker ...`
// just-works on first run instead of producing an opaque "no such
// image" error.
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
// the list of env-var names whose values should be inherited from the
// caller's environment.
//
// Values for the env-name list come from os.Environ() at container-
// start time (the Docker daemon copies them in if the container's
// `Env` field has the bare-name form — same semantics as docker CLI's
// `--env IBMCLOUD_API_KEY` no-value form).
func (b *DockerBackend) buildMountsAndEnv(opts RunOpts, tempDir string) ([]mount.Mount, []string, func(), error) {
	var mounts []mount.Mount
	var envNames []string
	cleanupFns := []func(){}
	cleanup := func() {
		for _, f := range cleanupFns {
			f()
		}
	}

	// Materialise Files into tempDir/files/ then bind-mount each.
	if len(opts.Files) > 0 {
		filesDir := filepath.Join(tempDir, "files")
		if err := os.MkdirAll(filesDir, 0o755); err != nil {
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

	// Cred propagation. PRD 04 §Docker container: bind-mount the
	// SINGLE kubeconfig FILE read-only at /root/.kube/config; pass
	// IBMCLOUD_API_KEY by name only (no value) so it inherits from the
	// docker daemon's view of the caller env.
	if opts.Credentials != nil {
		if opts.Credentials.IBMCloudAPIKey != "" {
			// Bare names — these tell docker "inherit value from caller
			// env". Setting these on the daemon connection is the
			// caller's responsibility (the os.Environ() of the
			// roksbnkctl process is what the daemon sees through the
			// docker API).
			//
			// TF_VAR_ibmcloud_api_key is included for the terraform
			// docker backend (Sprint 5; PRD 03 §"terraform"): the
			// terraform image reads it as the `ibmcloud_api_key`
			// variable. Same bare-name pattern as IBMCLOUD_API_KEY so
			// `docker inspect` never sees the value.
			envNames = append(envNames, "IBMCLOUD_API_KEY", "IC_API_KEY", "TF_VAR_ibmcloud_api_key")
			// Make sure IC_API_KEY is set in our env if only IBMCLOUD_API_KEY is.
			if os.Getenv("IC_API_KEY") == "" {
				_ = os.Setenv("IC_API_KEY", opts.Credentials.IBMCloudAPIKey)
				cleanupFns = append(cleanupFns, func() { _ = os.Unsetenv("IC_API_KEY") })
			}
			// Likewise make sure IBMCLOUD_API_KEY is set, in case
			// the resolver returned the value but the host env doesn't
			// carry it (resolver came from keychain/config).
			if os.Getenv("IBMCLOUD_API_KEY") == "" {
				_ = os.Setenv("IBMCLOUD_API_KEY", opts.Credentials.IBMCloudAPIKey)
				cleanupFns = append(cleanupFns, func() { _ = os.Unsetenv("IBMCLOUD_API_KEY") })
			}
			if os.Getenv("TF_VAR_ibmcloud_api_key") == "" {
				_ = os.Setenv("TF_VAR_ibmcloud_api_key", opts.Credentials.IBMCloudAPIKey)
				cleanupFns = append(cleanupFns, func() { _ = os.Unsetenv("TF_VAR_ibmcloud_api_key") })
			}
		}
		if len(opts.Credentials.KubeconfigBytes) > 0 {
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
	}

	return mounts, envNames, cleanup, nil
}

// buildContainerEnv translates RunOpts.Env (KEY=VALUE strings) into
// the slice docker's container.Config.Env expects. Skips entries with
// no '=' separator (silently — the local backend does the same).
func buildContainerEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		if strings.IndexByte(kv, '=') > 0 {
			out = append(out, kv)
		}
	}
	return out
}

// dockerImageBinary maps argv[0] tool names to the in-container binary
// they invoke when the image has no ENTRYPOINT. Used by
// resolveDockerImageAndArgv to prepend the binary name explicitly to
// the container's `Cmd` slice. Entries here mirror tools whose images
// no longer carry an ENTRYPOINT line (e.g., the bundled tools-ibmcloud
// image after Sprint 6's Dockerfile change).
//
// Tools NOT in this map keep the legacy shape — argv[1:] is passed
// verbatim as the container Cmd, relying on the image's own ENTRYPOINT
// to pick the binary (`iperf3`, `terraform`).
//
// Why a per-tool map instead of "always prepend argv[0]"? Because
// `iperf3` and `terraform` images still carry their own ENTRYPOINT
// directives (the upstream `hashicorp/terraform` image and our own
// `tools/docker/iperf3/Dockerfile`); prepending the binary name in
// those cases would double-invoke (`iperf3 iperf3 --version`).
//
// The ibmcloud image's `roksbnkctl` alias maps to `/usr/local/bin/
// roksbnkctl` so a `--backend docker` invocation of roksbnkctl-as-tool
// (the dns-probe re-exec path, etc.) lands on the right binary.
var dockerImageBinary = map[string][]string{
	"ibmcloud":   {"ibmcloud"},
	"roksbnkctl": {"/usr/local/bin/roksbnkctl"},
}

// resolveDockerImageAndArgv picks the docker image and the in-container
// argv from the caller's argv.
//
//   - If argv[0] is a known tool with an entry in dockerImageBinary,
//     its image is looked up AND the in-container Cmd is prepended
//     with the tool's binary name (so the image can have no
//     ENTRYPOINT and still run the right binary).
//   - If argv[0] is a known tool WITHOUT an entry in dockerImageBinary
//     (iperf3, terraform), its image is looked up and argv[1:] is
//     passed verbatim — the image's own ENTRYPOINT picks the binary.
//   - Otherwise argv[0] is treated as a literal image reference and
//     argv[1:] is the in-container command — useful for tests and
//     ad-hoc shapes.
//
// Sprint 6 — Dockerfile ENTRYPOINT drop landed for the bundled
// tools-ibmcloud image; this function adapts the dispatch
// accordingly. PRD 03 §"Docker (internal/exec/docker.go)".
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
