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
		"ibmcloud": "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:" + tag,
		// iperf3: use the public networkstatic/iperf3 image instead of the
		// bundled one. The bundled image at ghcr.io/jgruberf5/roksbnkctl-
		// tools-iperf3 is private (no public-read on the GitHub Container
		// Registry package) so ROKS workers can't pull it without an
		// image-pull-secret. networkstatic/iperf3 is the same image the
		// throughput server pod uses, public on Docker Hub, and known to
		// work with the iperf3 client argv (`-c <host> -J`).
		//
		// v1.x can switch back to a bundled image once we either flip
		// the ghcr package to public or wire an image-pull-secret per
		// PRD 03 §"K8s backend image pull".
		"iperf3":     "networkstatic/iperf3:latest",
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
		"ibmcloud": "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:" + tag,
		// iperf3: use the public networkstatic/iperf3 image instead of the
		// bundled one. The bundled image at ghcr.io/jgruberf5/roksbnkctl-
		// tools-iperf3 is private (no public-read on the GitHub Container
		// Registry package) so ROKS workers can't pull it without an
		// image-pull-secret. networkstatic/iperf3 is the same image the
		// throughput server pod uses, public on Docker Hub, and known to
		// work with the iperf3 client argv (`-c <host> -J`).
		//
		// v1.x can switch back to a bundled image once we either flip
		// the ghcr package to public or wire an image-pull-secret per
		// PRD 03 §"K8s backend image pull".
		"iperf3":     "networkstatic/iperf3:latest",
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

	// Cred-shim wrap for tools that need IBMCLOUD_API_KEY in env but
	// aren't already wrapped via dockerImageBinary. Terraform is the
	// canonical case: the IBM provider reads `TF_VAR_ibmcloud_api_key`
	// from env, and the tmpfile-bind-mount pattern (PRD 04 §"Resolved
	// in Sprint 9") keeps the value out of container env metadata —
	// so we wrap terraform's invocation in a sh -c that sources the
	// key from the bind-mounted file (credShimScript) and then exec's
	// the image's terraform ENTRYPOINT with the user's argv.
	//
	// ibmcloud + roksbnkctl are NOT wrapped here — dockerImageBinary
	// already shim-wraps them. iperf3 + literal-image-ref shapes
	// (busybox in tests) don't need the API key.
	if needsCredShim(argv) && opts.Credentials != nil && opts.Credentials.IBMCloudAPIKey != "" {
		cmdArgv = wrapCmdWithCredShim(argv[0], cmdArgv)
	}

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
	// Append the cred-related env entries from buildMountsAndEnv.
	// Sprint 9 / PRD 04 §"Resolved in Sprint 9" §"Cred tmpfile-bind-
	// mount pattern": the only cred-related entry here is the bind-
	// mount path pointer (`IBMCLOUD_API_KEY_FILE=/run/secrets/...`).
	// The actual key value never enters cfg.Env — it lives in the
	// bind-mounted tempfile and is sourced inside the container by
	// the credShimScript at process-spawn time.
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

// credBindMountTarget is the in-container path the API-key tempfile is
// bind-mounted at. Stable name so the `IBMCLOUD_API_KEY_FILE` env var
// and the credShimScript both reference it. PRD 04 §"Resolved in
// Sprint 9" §"Cred tmpfile-bind-mount pattern" pins this convention.
const credBindMountTarget = "/run/secrets/ibmcloud_api_key"

// credEnvFileVar is the env var name the container sees pointing at
// the bind-mounted secret file. The credShimScript reads this and
// exports the actual API key into the shell scope before `exec`'ing
// the wrapped tool. Naming follows the `_FILE` convention popularised
// by docker-library images (postgres, mysql, etc.) for file-backed
// secrets.
const credEnvFileVar = "IBMCLOUD_API_KEY_FILE"

// buildMountsAndEnv translates RunOpts into docker container mounts +
// the list of `KEY=VALUE` env entries the container should carry.
//
// PRD 04 §"Resolved in Sprint 9" §"Cred tmpfile-bind-mount pattern":
// the IBM Cloud API key is NEVER materialised into the container's
// stored env (which `docker inspect` would expose). Instead the key
// value is written to a per-run `0600` tempfile under `tempDir`, the
// file is bind-mounted read-only at `/run/secrets/ibmcloud_api_key`,
// and only the bind-mount-target path is set as
// `IBMCLOUD_API_KEY_FILE` in container env. The shim script in
// `dockerImageBinary["ibmcloud"]` (and any tool that reads
// `IBMCLOUD_API_KEY` directly via the `credShimScript` prefix)
// `cat`s the file at process-spawn time and exports the value into
// the shell scope only — the value never appears in `docker inspect`.
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

	// Cred propagation.
	//
	// Tmpfile-bind-mount pattern (PRD 04 §"Resolved in Sprint 9"): the
	// API key value lands in a 0600 tempfile under `tempDir/creds/api-key`,
	// the file is bind-mounted at `/run/secrets/ibmcloud_api_key` read-only,
	// and we set ONLY `IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key`
	// in container env. The container's shell shim (credShimScript)
	// reads the file and exports the key into shell scope at process-
	// spawn time — the value never appears in `docker inspect`.
	//
	// Replaces:
	//   - the pre-v1.0.2 BARE-NAME form (`Env: ["IBMCLOUD_API_KEY"]`,
	//     silently broken on the docker SDK path because bare names
	//     land as defined-but-empty); and
	//   - the v1.0.2 KEY=VALUE form (worked on the SDK path but
	//     leaked the value to `docker inspect`).
	//
	// Kubeconfig propagation (unchanged): the SINGLE kubeconfig file
	// is bind-mounted read-only at /root/.kube/config. PRD 04 §"Docker
	// container" §"Anti-patterns" — never mount the parent .kube dir.
	if opts.Credentials != nil {
		if opts.Credentials.IBMCloudAPIKey != "" {
			credsDir := filepath.Join(tempDir, "creds")
			if err := os.MkdirAll(credsDir, 0o700); err != nil {
				cleanup()
				return nil, nil, nil, fmt.Errorf("creating creds dir: %w", err)
			}
			keyPath := filepath.Join(credsDir, "api-key")
			if err := os.WriteFile(keyPath, []byte(opts.Credentials.IBMCloudAPIKey), 0o600); err != nil {
				cleanup()
				return nil, nil, nil, fmt.Errorf("materialising api key tempfile: %w", err)
			}
			// Bind the SINGLE file (not the parent dir) read-only.
			// PRD 04 §"Docker container" §"Anti-patterns" — same
			// pattern as kubeconfig; expose only what the container
			// needs.
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeBind,
				Source:   keyPath,
				Target:   credBindMountTarget,
				ReadOnly: true,
			})
			// Container env: ONLY the path pointer. `docker inspect`
			// shows this value, not the secret.
			env = append(env, credEnvFileVar+"="+credBindMountTarget)
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

	return mounts, env, cleanup, nil
}

// buildContainerEnv translates RunOpts.Env (KEY=VALUE strings) into
// the slice docker's container.Config.Env expects. Skips entries with
// no '=' separator (silently — the local backend does the same).
// buildContainerEnv copies KEY=VALUE pairs from the caller's env into
// the container env, EXCEPT for host-specific vars that would confuse
// programs running inside the container. The caller's HOME, PATH,
// USER, SHELL, PWD etc. refer to host filesystem paths that don't
// exist inside the container — the bundled ibmcloud image puts its
// plugins at /root/.bluemix/plugins/ which only resolves correctly
// when the container's $HOME stays at the image's default (/root).
// Letting the host's HOME=/home/<user> leak through means ibmcloud
// looks for plugins at /home/<user>/.bluemix/plugins/ which doesn't
// exist, and the plugin list comes back empty.
//
// v1.0.2 fix — pre-v1.0.2 this function blindly forwarded everything,
// and the docker backend's ibmcloud plugin (ks) was silently broken.
// The bare-name env-propagation issue masked this because the api
// key was also empty, so ibmcloud never got far enough to consult
// plugins.
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
// credShimPrefix is the POSIX sh -c shim that sources the IBM Cloud
// API key from the bind-mounted tempfile and exports it (plus the
// IC_API_KEY / TF_VAR_ibmcloud_api_key aliases) BEFORE exec'ing the
// real tool. PRD 04 §"Resolved in Sprint 9" §"Cred tmpfile-bind-mount
// pattern" — the export-then-exec means the env var is only set
// inside the container's shell scope and never appears in the
// container metadata visible to `docker inspect` (which records
// only the `IBMCLOUD_API_KEY_FILE` pointer + the bind mount entry).
//
// Constructed once at package load so callers don't re-allocate. The
// shim is idempotent: when `$IBMCLOUD_API_KEY_FILE` is unset (no
// creds passed) the read fails silently and the exec'd tool sees
// the parent env unchanged.
const credShimScript = `if [ -n "$IBMCLOUD_API_KEY_FILE" ] && [ -r "$IBMCLOUD_API_KEY_FILE" ]; then ` +
	`IBMCLOUD_API_KEY="$(cat "$IBMCLOUD_API_KEY_FILE")"; ` +
	`export IBMCLOUD_API_KEY IC_API_KEY="$IBMCLOUD_API_KEY" TF_VAR_ibmcloud_api_key="$IBMCLOUD_API_KEY"; ` +
	`fi; `

// dockerImageBinary maps tool name → in-container Cmd prefix. Each
// entry's slice is prepended to argv[1:] when building the container's
// Cmd. For tools that need session priming (ibmcloud), the prefix is a
// sh -c wrap that runs the priming command first, then exec's the
// real binary with the user-supplied args.
//
// ibmcloud requires `ibmcloud login` before stateful subcommands (iam,
// ks, account, target, …) — without it the CLI errors with "No API
// endpoint set" or "Not logged in", since the container starts cold
// with no $HOME/.bluemix config cached. The wrap below:
//  1. Sources `IBMCLOUD_API_KEY` from the bind-mounted tempfile (see
//     `credShimScript`) — Sprint 9 / PRD 04 §"Cred tmpfile-bind-mount
//     pattern". The key value never appears in container env metadata.
//  2. Runs `ibmcloud login -a https://cloud.ibm.com -r <region>
//     --apikey "$IBMCLOUD_API_KEY" --quiet` (quiet suppresses banner)
//  3. Redirects login output to /dev/null so the caller sees only the
//     wrapped command's output (preserves "byte-identical to local
//     backend" promise from PRD 03)
//  4. `exec ibmcloud "$@"` runs the user's actual args with the
//     now-logged-in session
//
// $@ expands the positional args resolveDockerImageAndArgv appends
// after the wrap (i.e., argv[1:] = the user-supplied subcommand).
//
// Region defaults to us-south if IBMCLOUD_REGION isn't set. The
// bundled image's /bin/sh is dash (Ubuntu base); both ${VAR:-default}
// and exec are POSIX so this works there.
//
// `login` and `logout` skip the wrap (caller's explicit intent).
var dockerImageBinary = map[string][]string{
	"ibmcloud":   {"sh", "-c", credShimScript + `ibmcloud login -a https://cloud.ibm.com -r "${IBMCLOUD_REGION:-us-south}" --apikey "$IBMCLOUD_API_KEY" --quiet > /dev/null 2>&1 && exec ibmcloud "$@"`, "--"},
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

// needsCredShim reports whether the tool named by argv[0] needs the
// IBMCLOUD_API_KEY sourced from the bind-mounted tempfile via the
// credShimScript at process-spawn time.
//
// Tools that are ALREADY shim-wrapped via dockerImageBinary (ibmcloud)
// have credShimScript baked into their Cmd prefix; they return false
// here to avoid double-wrapping. Tools that consume the key in env
// (terraform's IBM provider reads TF_VAR_ibmcloud_api_key) but flow
// through the image's ENTRYPOINT return true so the Run path wraps
// them. Tools that don't consume the key (iperf3, literal image refs
// in tests) return false.
func needsCredShim(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch argv[0] {
	case "terraform":
		return true
	}
	return false
}

// wrapCmdWithCredShim takes the resolved in-container Cmd slice (from
// resolveDockerImageAndArgv) and produces a sh -c wrap that sources
// IBMCLOUD_API_KEY from the bind-mounted file via credShimScript, then
// exec's the original Cmd's binary with the original args.
//
// `tool` is argv[0] — used to pick the in-container binary name
// (e.g., "terraform" → the upstream image's ENTRYPOINT runs the
// terraform binary). For tools where the in-container binary name
// equals the tool name, the exec target is simply `tool`. The args
// slice from resolveDockerImageAndArgv is appended after the `--`
// separator so the shell positional `$@` expansion picks them up.
func wrapCmdWithCredShim(tool string, cmdArgv []string) []string {
	// cmdArgv at this point is the args (no binary prefix) for tools
	// that rely on image ENTRYPOINT. Wrap with sh -c that runs the
	// credShimScript then exec's the same binary `tool` provides.
	wrap := []string{
		"sh", "-c",
		credShimScript + `exec ` + tool + ` "$@"`,
		"--",
	}
	out := make([]string, 0, len(wrap)+len(cmdArgv))
	out = append(out, wrap...)
	out = append(out, cmdArgv...)
	return out
}

func init() {
	Register("docker", &DockerBackend{})
}
