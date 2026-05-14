package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
)

// Namespaces and well-known names used by the k8s backend. Mirror the
// values baked into k8s_install.yaml — the backend assumes ops install
// has already provisioned these.
const (
	K8sOpsNamespace  = "roksbnkctl-ops"
	K8sTestNamespace = "roksbnkctl-test"
	K8sOpsPodName    = "roksbnkctl-ops"
	K8sOpsSecretName = "roksbnkctl-ibm-creds"

	// k8sJobReadyTimeout is how long we wait for an ephemeral Job's pod
	// to reach Running before streaming logs. Image pulls on a cold
	// node can chew up most of this budget; 3m matches the iperf3
	// fixture's defaultReadyTimeout in internal/k8s/iperf3.go.
	k8sJobReadyTimeout = 3 * time.Minute

	// k8sExitFailedToStart maps to PRD 03's 127 — backend couldn't
	// reach the cluster, ops pod missing, etc.
	k8sExitFailedToStart = 127

	// k8sExitStartedThenFailed maps to PRD 03's 126 — ops pod present
	// but the exec stream errored, Job created but pod failed to come up.
	k8sExitStartedThenFailed = 126
)

// jobNameSanitizer maps docker-ref characters that are invalid in k8s
// label values (`:`, `/`, `@`) to `-`. Hit when argv[0] is a literal
// image ref via the toolImages-fallback test path; production callers
// pass tool names from toolImages and aren't affected.
var jobNameSanitizer = strings.NewReplacer(":", "-", "/", "-", "@", "-")

// ibmcloudLoginWrapScript is the sh -c body for the ops-pod ibmcloud
// login wrap. Branches on `$IAM_PROFILE_ID`:
//
//   - Set: trusted-profile path. Three attempts at `ibmcloud login
//     -a https://cloud.ibm.com --cr-token @/var/run/secrets/tokens/token
//     --profile "$IAM_PROFILE_ID"`, 20s apart, to absorb the cluster's
//     OIDC-issuer propagation window (30-60s after `ops install`).
//     The `--cr-token` path reads the projected SA-token volume
//     mounted at /var/run/secrets/tokens/token by k8s_install.yaml
//     (audience: iam, expirationSeconds: 3600) — the projected token
//     carries the IAM-acceptable audience so IBM IAM's ROKS_SA link
//     (`internal/ibm/trusted_profile.go::ensureLink`) accepts it. The
//     `-a https://cloud.ibm.com` is required on the cold ops pod
//     (no persisted `ibmcloud api` setting).
//     On all-three-fail, prints the final attempt's stderr to the
//     caller's stderr before exec'ing argv (so the user sees a real
//     diagnostic, not a silent missing-token).
//   - Empty: v1.0.x static-key path. Single `--apikey` login;
//     unchanged behaviour.
//
// Sprint 10 / validator Issue 1 closure: replaced the non-existent
// `--trusted-profile-id` flag (Sprint 10 initial wrap) with the
// `--cr-token @<path> --profile <id>` pair documented in `ibmcloud
// login --help` for `ibmcloud 2.43.0`.
//
// Exported as a package-level var so unit tests can assert the script
// shape without re-deriving it (Sprint 10 staff Issue 2 / k8s_test.go
// branching-wrap test).
const ibmcloudLoginWrapScript = `if [ -n "$IAM_PROFILE_ID" ]; then
  attempt=1
  last_err=""
  while [ "$attempt" -le 3 ]; do
    last_err="$(ibmcloud login -a https://cloud.ibm.com --cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID" -r "${IBMCLOUD_REGION:-us-south}" --quiet 2>&1 > /dev/null)"
    if [ $? -eq 0 ]; then break; fi
    if [ "$attempt" -lt 3 ]; then sleep 20; fi
    attempt=$((attempt + 1))
  done
  if [ "$attempt" -gt 3 ]; then
    printf '%s\n' "trusted-profile login failed after 3 attempts: $last_err" >&2
  fi
else
  ibmcloud login -a https://cloud.ibm.com -r "${IBMCLOUD_REGION:-us-south}" --apikey "$IBMCLOUD_API_KEY" --quiet > /dev/null 2>&1
fi
exec ibmcloud "$@"`

// K8sBackend executes argv either by exec'ing into a long-lived ops pod
// (for ibmcloud + ad-hoc shells) or by spawning a one-shot Job (for
// iperf3 client + terraform).
//
// PRD 03 §"K8s" is the design spec. The two paths share a single Run
// entrypoint and dispatch on RunOpts.LongLivedExec — true for the
// ops-pod exec path, false (default) for the Job path.
//
// `roksbnkctl ops install` provisions the namespaces, ServiceAccount,
// Secret, ClusterRole, and ops Pod this backend assumes exist. The
// backend doesn't try to bootstrap on first call (would race; would
// surprise users). Callers see a clear "ops not installed" error
// (rc=127) and the install command in the message.
type K8sBackend struct {
	// once-init plumbing for client + config so a `--help` invocation
	// doesn't dial the apiserver. Mirror DockerBackend's lazy-init.
	mu     sync.Mutex
	client kubernetes.Interface
	config *rest.Config
	initFn func() (kubernetes.Interface, *rest.Config, error)
}

// Name implements Backend.
func (b *K8sBackend) Name() string { return "k8s" }

// k8sLongLivedKey is a sentinel set on RunOpts.Env when callers want
// the long-lived ops-pod exec path instead of the Job path. Since
// RunOpts.Env is the only "free" extension point on the public Backend
// interface today (adding a LongLivedExec bool would require an API
// change), we encode the bit as an env entry and strip it before the
// child sees it.
//
// Future cleanup: bump RunOpts to carry a LongLivedExec field directly
// once the integrator is ready for an API change.
const k8sLongLivedKey = "ROKSBNKCTL_K8S_LONG_LIVED=1"

// extractLongLivedFlag pulls the sentinel out of env and returns
// (longLived, filteredEnv). Callers pass the filtered env on to the pod
// so the wrapped tool doesn't see internal plumbing.
func extractLongLivedFlag(env []string) (bool, []string) {
	out := make([]string, 0, len(env))
	longLived := false
	for _, kv := range env {
		if kv == k8sLongLivedKey {
			longLived = true
			continue
		}
		out = append(out, kv)
	}
	return longLived, out
}

// Run implements Backend. Dispatches to runOnOpsPod (long-lived exec)
// or runAsJob (one-shot Job) per the sentinel in opts.Env.
func (b *K8sBackend) Run(ctx context.Context, argv []string, opts RunOpts) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("argv is empty")
	}

	cs, restCfg, err := b.ensureClient()
	if err != nil {
		return k8sExitFailedToStart, fmt.Errorf("k8s backend: %w (run `roksbnkctl ops install` to provision the ops pod)", err)
	}

	longLived, filteredEnv := extractLongLivedFlag(opts.Env)
	opts.Env = filteredEnv

	if longLived {
		return b.runOnOpsPod(ctx, cs, restCfg, argv, opts)
	}
	return b.runAsJob(ctx, cs, argv, opts)
}

// ensureClient lazy-builds the client + REST config. Reuses the
// integrator's k8s package conventions (DefaultKubeconfigPath +
// rest.InClusterConfig fallback). The initFn hook lets tests substitute
// a fake clientset without touching the real loader.
func (b *K8sBackend) ensureClient() (kubernetes.Interface, *rest.Config, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil && b.config != nil {
		return b.client, b.config, nil
	}
	if b.initFn != nil {
		cs, cfg, err := b.initFn()
		if err != nil {
			return nil, nil, err
		}
		b.client, b.config = cs, cfg
		return cs, cfg, nil
	}
	cs, cfg, err := defaultK8sInit()
	if err != nil {
		return nil, nil, err
	}
	b.client, b.config = cs, cfg
	return cs, cfg, nil
}

// defaultK8sInit is the package-level seam mirroring internal/k8s.
// Exposed as a var so tests can stub the kubeconfig discovery without
// importing internal/k8s (which would create a backend → k8s package
// cycle if we ever added the reverse import).
var defaultK8sInit = func() (kubernetes.Interface, *rest.Config, error) {
	return nil, nil, errors.New("k8s backend: no client initialiser registered; call SetK8sInit from internal/cli before dispatch")
}

// SetK8sInit lets the CLI layer wire its kubeconfig-loading logic into
// the backend without forcing a circular import. internal/cli/k_root.go
// (or wherever feels natural) calls this in init().
func SetK8sInit(fn func() (kubernetes.Interface, *rest.Config, error)) {
	defaultK8sInit = fn
}

// runOnOpsPod kubectl-execs argv into the ops pod via SPDY.
//
// The wrapped tool's stdout/stderr stream live through opts; we wrap
// both with the redactor as defense-in-depth (the ibmcloud CLI in
// --debug mode is the obvious leak risk).
func (b *K8sBackend) runOnOpsPod(ctx context.Context, cs kubernetes.Interface, cfg *rest.Config, argv []string, opts RunOpts) (int, error) {
	// Verify the ops pod exists + is ready. A clear error here is much
	// better than an opaque SPDY upgrade failure.
	pod, err := cs.CoreV1().Pods(K8sOpsNamespace).Get(ctx, K8sOpsPodName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return k8sExitFailedToStart, fmt.Errorf("ops pod %s/%s not found; run `roksbnkctl ops install`", K8sOpsNamespace, K8sOpsPodName)
		}
		return k8sExitFailedToStart, fmt.Errorf("looking up ops pod: %w", err)
	}
	if pod.Status.Phase != corev1.PodRunning || !podReady(pod) {
		return k8sExitFailedToStart, fmt.Errorf("ops pod %s/%s not Ready (phase=%s)", K8sOpsNamespace, K8sOpsPodName, pod.Status.Phase)
	}

	// Build the exec request. PodExecOptions.Command is exec'd
	// **directly** inside the running container's filesystem; the
	// image's ENTRYPOINT does NOT prepend (that only applies at
	// container start, and the ops pod's `command:` already overrides
	// it to `sleep infinity` per k8s_install.yaml). So argv flows
	// through verbatim — `["ibmcloud", "iam", "oauth-tokens"]` runs
	// `ibmcloud iam oauth-tokens` in the pod, no entrypoint double-up.
	//
	// Sprint 4 validator Issue 7 carry-over (interim resolution):
	// the original concern was that argv[0] would double up against
	// the image's ENTRYPOINT. Verified that exec doesn't prepend
	// ENTRYPOINT, so this is a no-op risk for the long-lived path.
	// For the one-shot Job path (runAsJob), where Container.Command
	// DOES override Docker ENTRYPOINT, we use the
	// `jobToolCmdOverride` map for tools (like `roksbnkctl`) that
	// need to bypass the bundled tools-image's `ibmcloud`
	// entrypoint. See `buildJobSpecWithArgs` for the Args plumbing.
	cmd := argv

	// v1.0.2: ibmcloud needs `ibmcloud login` before stateful subcommands
	// (iam, ks, account, target, …) — the ops pod's container starts
	// cold with no $HOME/.bluemix session. Wrap argv with a sh -c
	// login-then-exec dance so any ibmcloud invocation gets primed.
	// Same shape as docker.go's dockerImageBinary["ibmcloud"] wrap.
	// Skip if argv[0] != "ibmcloud" or if the user is explicitly running
	// `ibmcloud login`/`logout` (no double-login).
	//
	// Sprint 10 / PRD 04 §"Resolved in Sprint 9" closure (staff Issue 2
	// from Sprint 9): when the ops pod's SA carries the trusted-profile
	// annotation, the manifest renderer injects `IAM_PROFILE_ID=<id>`
	// into the pod env. The wrap branches on `$IAM_PROFILE_ID` presence
	// at runtime: when set, `ibmcloud login --cr-token @<path>
	// --profile "$IAM_PROFILE_ID"` is used (no static API key in the
	// cluster at rest; the projected SA-token volume at
	// /var/run/secrets/tokens/token, audience `iam`, supplies the JWT
	// IAM validates against the ROKS_SA link); when empty (the v1.0.x
	// static-key path), the existing `--apikey` form runs.
	//
	// Brief retry on the trusted-profile path: the cluster's OIDC issuer
	// URL takes 30-60s to propagate through IBM IAM after `ops install`
	// returns. The first `--cr-token` attempt may fail with "failed to
	// assume trusted profile" during this window. Three attempts with
	// 20s backoff; if all three fail, the wrap surfaces the final
	// attempt's error before exec'ing the user's argv (so the caller
	// sees a useful diagnostic, not a silent missing-token).
	// The static-key path doesn't need this (no OIDC dependency).
	if len(cmd) >= 1 && cmd[0] == "ibmcloud" {
		if len(cmd) < 2 || (cmd[1] != "login" && cmd[1] != "logout") {
			wrap := []string{
				"sh", "-c",
				ibmcloudLoginWrapScript,
				"--",
			}
			cmd = append(wrap, cmd[1:]...)
		}
	}

	req := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(K8sOpsPodName).
		Namespace(K8sOpsNamespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: cmd,
			Stdin:   opts.Stdin != nil,
			Stdout:  true,
			Stderr:  true,
			TTY:     opts.TTY,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return k8sExitStartedThenFailed, fmt.Errorf("building SPDY executor: %w", err)
	}

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

	streamErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  opts.Stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    opts.TTY,
	})
	if streamErr == nil {
		return 0, nil
	}

	if ctx.Err() != nil {
		return 137, ctx.Err()
	}

	// SPDY's CodeExitError carries the wrapped command's exit code.
	var ee utilexec.CodeExitError
	if errors.As(streamErr, &ee) {
		// In-pod tool exited non-zero. The PRD 03 split says 126/127
		// are reserved for backend faults; if the in-pod process
		// genuinely exited with 126/127 we still pass it through
		// (the user's tool said so).
		return ee.ExitStatus(), nil
	}

	// Anything else is a transport / SPDY error — backend started but
	// the exec stream errored mid-flight. PRD 03 §"Backend interface"
	// 126 split.
	return k8sExitStartedThenFailed, fmt.Errorf("k8s exec stream: %w", streamErr)
}

// jobToolCmdOverride declares the in-container binary `runAsJob` should
// exec when argv[0] is a known tool name. Entries here mirror the
// docker backend's `dockerImageBinary` map (see `internal/exec/docker.go`)
// so the k8s Job path and the docker container path resolve the same
// tool→binary mapping.
//
// Two situations make an entry necessary:
//
//  1. The tool's image has NO ENTRYPOINT (e.g. the bundled tools-
//     ibmcloud image post-Sprint 6 — see `issues/resolved_sprint5_staff.md`
//     Issue 1). Without an override, `Container.Command = argv[1:]`
//     would run with no binary name and the kube node would surface
//     ErrPullBackOff-shaped failures pointing at a nonexistent
//     command.
//  2. The tool's image HAS an ENTRYPOINT but the caller wants a
//     different binary inside the same image (e.g. roksbnkctl is
//     bundled into the tools-ibmcloud image; the dns-probe re-exec
//     path needs `roksbnkctl`, not whatever the image's ENTRYPOINT
//     used to be).
//
// runAsJob picks the entry up, sets `Container.Command` to the
// override, and `Container.Args` to argv[1:].
//
// Tools NOT in this map keep the legacy shape
// (`Container.Command = argv[1:]`, image's ENTRYPOINT picks the
// binary) — `iperf3` (image's ENTRYPOINT="iperf3") and `terraform`
// (upstream `hashicorp/terraform` image's ENTRYPOINT="terraform")
// continue to work without an entry.
var jobToolCmdOverride = map[string][]string{
	"ibmcloud":   {"ibmcloud"},
	"roksbnkctl": {"/usr/local/bin/roksbnkctl"},
}

// runAsJob spawns a one-shot Job in roksbnkctl-test, materialises Files
// + creds via projected Secret(s), waits for Running, streams logs,
// then waits for completion + cleanup.
//
// argv[0] picks the per-tool image (mirrors DockerBackend.toolImages);
// argv[1:] is the in-container command, EXCEPT for tools listed in
// jobToolCmdOverride which get a full `Command + Args` shape that
// bypasses the image's ENTRYPOINT.
func (b *K8sBackend) runAsJob(ctx context.Context, cs kubernetes.Interface, argv []string, opts RunOpts) (int, error) {
	tool := argv[0]
	image, ok := toolImages[tool]
	if !ok {
		// Test path: argv[0] is a literal image ref + argv[1:] is
		// the in-container command. Mirrors docker.go's fallback.
		image = tool
	}

	// Job + per-Job files Secret share a randomised suffix for trivial
	// teardown via owner refs. Sanitise tool name into k8s-label-safe
	// shape: docker-style refs ("busybox:latest", "myrepo/img@sha256:…")
	// surface in the test fallback above and would otherwise trip
	// label-validation regex on Job creation.
	suffix := rand.String(6)
	safeTool := jobNameSanitizer.Replace(tool)
	jobName := "roksbnkctl-" + safeTool + "-" + suffix
	if len(jobName) > 60 {
		jobName = jobName[:60]
	}
	filesSecretName := jobName + "-files"

	// Files Secret (per-Job, owned by the Job for auto-delete).
	var filesSecretCreated bool
	if len(opts.Files) > 0 {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      filesSecretName,
				Namespace: K8sTestNamespace,
				Labels:    map[string]string{"roksbnkctl.io/job": jobName},
			},
			Type: corev1.SecretTypeOpaque,
			Data: opts.Files,
		}
		if _, err := cs.CoreV1().Secrets(K8sTestNamespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			return k8sExitFailedToStart, fmt.Errorf("creating files secret: %w", err)
		}
		filesSecretCreated = true
	}

	// Cmd + Args translation:
	//   - For entrypoint-bypass tools, prepend the override's argv as
	//     Command (overrides image's ENTRYPOINT) and pass argv[1:] as
	//     Args (replaces image's CMD).
	//   - Otherwise pass argv[1:] as Args so the image's ENTRYPOINT
	//     stays in place and the supplied args flow to it. Setting
	//     Command in the no-override path would OVERRIDE the image's
	//     ENTRYPOINT, causing the kubelet to try exec'ing argv[1]
	//     directly as a binary (e.g., "-c" for iperf3 → exec /-c →
	//     CreateContainerError). This was the v1.0.2 fix for the
	//     L2 throughput Job's CreateContainerError; pre-fix the
	//     comment claimed "image's ENTRYPOINT picks the binary"
	//     which contradicts actual k8s Container.Command semantics.
	var cmdArgv []string
	var argsArgv []string
	if override, hasOverride := jobToolCmdOverride[tool]; hasOverride {
		cmdArgv = append([]string(nil), override...)
		argsArgv = argv[1:]
	} else {
		argsArgv = argv[1:]
	}

	job := buildJobSpecWithArgs(jobName, image, cmdArgv, argsArgv, opts, filesSecretCreated, filesSecretName)

	created, err := cs.BatchV1().Jobs(K8sTestNamespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		if filesSecretCreated {
			_ = cs.CoreV1().Secrets(K8sTestNamespace).Delete(context.Background(), filesSecretName, metav1.DeleteOptions{})
		}
		return k8sExitFailedToStart, fmt.Errorf("creating job: %w", err)
	}

	// Owner-ref the files Secret to the Job so it auto-deletes on Job
	// cleanup. Done after Create so we have the Job's UID.
	if filesSecretCreated {
		_ = setSecretOwnerRef(ctx, cs, filesSecretName, created)
	}

	// Cleanup goroutine: ctx cancel → delete Job + Secret. Job's
	// ttlSecondsAfterFinished handles the happy-path cleanup.
	cancelDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			pp := metav1.DeletePropagationForeground
			_ = cs.BatchV1().Jobs(K8sTestNamespace).Delete(cleanCtx, jobName, metav1.DeleteOptions{PropagationPolicy: &pp})
			if filesSecretCreated {
				_ = cs.CoreV1().Secrets(K8sTestNamespace).Delete(cleanCtx, filesSecretName, metav1.DeleteOptions{})
			}
		case <-cancelDone:
		}
	}()
	defer close(cancelDone)

	// Wait for the Job's pod to be Running.
	pod, err := waitForJobPodRunning(ctx, cs, jobName, k8sJobReadyTimeout)
	if err != nil {
		return k8sExitStartedThenFailed, fmt.Errorf("waiting for job pod: %w", err)
	}

	// Stream logs.
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		stdout, stdoutClose := wrapForRedaction(opts.Stdout, opts.Credentials)
		defer func() {
			if stdoutClose != nil {
				_ = stdoutClose()
			}
		}()
		stream, lerr := cs.CoreV1().Pods(K8sTestNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Follow: true,
		}).Stream(ctx)
		if lerr != nil {
			return
		}
		defer stream.Close()
		_, _ = io.Copy(stdout, stream)
	}()

	// Wait for Job completion.
	rc, werr := waitForJobCompletion(ctx, cs, jobName)
	<-streamDone
	if werr != nil {
		return k8sExitStartedThenFailed, werr
	}
	return rc, nil
}

// buildJobSpec renders the per-Job spec. SCC-clean (matches the iperf3
// SCC fix). Mounts the files Secret at /work read-only when present;
// envFroms the cred Secret in roksbnkctl-ops (cross-namespace). Note:
// projected Secret crossing namespaces requires the SA in roksbnkctl-test
// to read the cred Secret; we sidestep that by referencing the cred
// Secret only from the ops pod (long-lived path) and using a fresh
// envFrom-style projection out of a per-Job Secret for the Job path.
//
// For Sprint 4 simplicity, the Job path env-injects IBMCLOUD_API_KEY
// from RunOpts.Credentials (via opts.Credentials.EnvVars()). The shape
// matches the docker / local backends — the cred is materialised by the
// caller, not pre-staged in a cluster-wide Secret.
//
// buildJobSpec is preserved for the legacy single-argv shape (image
// ENTRYPOINT picks the binary; cmd is argv[1:]). The Sprint 5 shim
// `buildJobSpecWithArgs` adds an explicit args slice for tools like
// `roksbnkctl` that need to bypass the image's ENTRYPOINT.
func buildJobSpec(jobName, image string, cmd []string, opts RunOpts, hasFilesSecret bool, filesSecretName string) *batchv1.Job {
	return buildJobSpecWithArgs(jobName, image, cmd, nil, opts, hasFilesSecret, filesSecretName)
}

// buildJobSpecWithArgs is buildJobSpec extended for the entrypoint-
// bypass shape. When `args` is non-nil, the rendered container has
// `Command=cmd, Args=args`; this overrides the image's Docker
// ENTRYPOINT and runs `cmd[0] cmd[1:] ...args` instead. For tools that
// keep the legacy "image ENTRYPOINT picks the binary" shape, pass
// args=nil.
//
// Sprint 4 validator Issue 7 carry-over: the dns-probe Job sets
// `cmd=["/usr/local/bin/roksbnkctl"]` + `args=["test","dns",...]` so
// the tools image's `ibmcloud` ENTRYPOINT doesn't override the binary
// the dns probe wants to run. PRD 03 §"DNS probe" §"K8s shape".
func buildJobSpecWithArgs(jobName, image string, cmd, args []string, opts RunOpts, hasFilesSecret bool, filesSecretName string) *batchv1.Job {
	envVars := buildJobEnv(opts)
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount
	if hasFilesSecret {
		volumes = append(volumes, corev1.Volume{
			Name: "files",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: filesSecretName,
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "files",
			MountPath: "/work",
			ReadOnly:  true,
		})
	}

	workDir := opts.WorkDir
	if workDir == "" && hasFilesSecret {
		workDir = "/work"
	}

	ttl := int32(60)
	backoffLimit := int32(0)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: K8sTestNamespace,
			Labels:    map[string]string{"roksbnkctl.io/managed": "true", "app": jobName},
		},
		Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: &ttl,
			BackoffLimit:            &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": jobName},
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: ptrBool(true),
						// Do NOT pin RunAsUser to a specific value.
						// OpenShift's restricted-v2 SCC assigns a UID
						// from the namespace's allowed range (e.g.,
						// 1000680000-1000689999); pinning 65532 collides
						// and the Job is rejected at admission with
						// "Invalid value: 65532: must be in the ranges
						// [...]" — see PRD 05 §"Risks" + Sprint 5 staff
						// Issue 2 carry-over. Leaving RunAsUser unset
						// lets the SCC mutating-admission webhook pick
						// a valid UID per namespace.
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Volumes: volumes,
					Containers: []corev1.Container{{
						Name:       "tool",
						Image:      image,
						Command:    cmd,
						Args:       args,
						Env:        envVars,
						WorkingDir: workDir,
						SecurityContext: &corev1.SecurityContext{
							AllowPrivilegeEscalation: ptrBool(false),
							RunAsNonRoot:             ptrBool(true),
							// RunAsUser unset — see PodSecurityContext
							// comment above. Container-level pinning
							// to 65532 also collides with restricted-v2's
							// dynamic UID range.
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{"ALL"},
							},
						},
						VolumeMounts: mounts,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1000m"),
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
						},
					}},
				},
			},
		},
	}
}

// buildJobEnv merges opts.Env (caller-supplied KEY=VALUE) with
// opts.Credentials.EnvVars() (resolver-derived). Late entries override
// earlier ones, mirroring the local backend's semantics.
func buildJobEnv(opts RunOpts) []corev1.EnvVar {
	merged := make(map[string]string)
	for _, kv := range opts.Env {
		k, v, ok := splitKV(kv)
		if !ok {
			continue
		}
		merged[k] = v
	}
	if opts.Credentials != nil {
		for _, kv := range opts.Credentials.EnvVars() {
			k, v, ok := splitKV(kv)
			if !ok {
				continue
			}
			merged[k] = v
		}
	}
	out := make([]corev1.EnvVar, 0, len(merged))
	for k, v := range merged {
		out = append(out, corev1.EnvVar{Name: k, Value: v})
	}
	return out
}

func splitKV(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], i > 0
		}
	}
	return "", "", false
}

// waitForJobPodRunning polls until the Job has a pod in Running phase.
// Returns the pod (so the caller can stream logs).
func waitForJobPodRunning(ctx context.Context, cs kubernetes.Interface, jobName string, timeout time.Duration) (*corev1.Pod, error) {
	deadline := time.Now().Add(timeout)
	pollInt := 1 * time.Second
	for {
		pods, err := cs.CoreV1().Pods(K8sTestNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=" + jobName,
		})
		if err == nil {
			for i := range pods.Items {
				p := &pods.Items[i]
				if p.Status.Phase == corev1.PodRunning || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
					return p, nil
				}
				// Surface terminal-ish waiting reasons early so we don't
				// burn the whole timeout on configs the kubelet will
				// never start (PSS rejects, runAsNonRoot mismatches,
				// missing images, crash loops).
				for _, st := range p.Status.ContainerStatuses {
					if st.State.Waiting != nil {
						switch st.State.Waiting.Reason {
						case "ImagePullBackOff", "ErrImagePull",
							"CrashLoopBackOff",
							"CreateContainerConfigError",
							"CreateContainerError",
							"RunContainerError",
							"InvalidImageName":
							return nil, fmt.Errorf("pod %s: %s (%s)", p.Name, st.State.Waiting.Reason, st.State.Waiting.Message)
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for job %s pod to be Running", jobName)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInt):
		}
	}
}

// waitForJobCompletion polls the Job until Complete or Failed. Returns
// the wrapped container's exit code (0 on success; the actual code on
// failure).
func waitForJobCompletion(ctx context.Context, cs kubernetes.Interface, jobName string) (int, error) {
	pollInt := 1 * time.Second
	for {
		j, err := cs.BatchV1().Jobs(K8sTestNamespace).Get(ctx, jobName, metav1.GetOptions{})
		if err == nil {
			for _, cond := range j.Status.Conditions {
				if cond.Status != corev1.ConditionTrue {
					continue
				}
				switch cond.Type {
				case batchv1.JobComplete:
					return 0, nil
				case batchv1.JobFailed:
					// Inspect the latest pod's container terminated state for
					// the wrapped tool's exit code.
					rc := jobFailureExitCode(ctx, cs, jobName)
					return rc, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return 137, ctx.Err()
		case <-time.After(pollInt):
		}
	}
}

// jobFailureExitCode pulls the most recent pod's tool-container
// terminated.exitCode. Returns 1 when the data isn't available — the
// caller treats any non-zero as failure.
func jobFailureExitCode(ctx context.Context, cs kubernetes.Interface, jobName string) int {
	pods, err := cs.CoreV1().Pods(K8sTestNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=" + jobName,
	})
	if err != nil || len(pods.Items) == 0 {
		return 1
	}
	for _, p := range pods.Items {
		for _, st := range p.Status.ContainerStatuses {
			if st.State.Terminated != nil {
				return int(st.State.Terminated.ExitCode)
			}
		}
	}
	return 1
}

// setSecretOwnerRef stamps the Job as the owner of the per-Job files
// Secret so kube garbage-collection cleans it up when the Job is
// deleted (TTL or explicit). Best-effort — failures don't break the run.
func setSecretOwnerRef(ctx context.Context, cs kubernetes.Interface, name string, owner *batchv1.Job) error {
	patch := []byte(fmt.Sprintf(`{"metadata":{"ownerReferences":[{"apiVersion":"batch/v1","kind":"Job","name":%q,"uid":%q,"controller":true,"blockOwnerDeletion":true}]}}`, owner.Name, owner.UID))
	_, err := cs.CoreV1().Secrets(K8sTestNamespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	return err
}

// podReady returns true when the pod has the Ready condition.
func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func ptrBool(b bool) *bool { return &b }

func init() {
	Register("k8s", &K8sBackend{})
}
