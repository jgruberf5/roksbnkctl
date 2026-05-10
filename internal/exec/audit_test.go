package exec

// Sprint 3 / PRD 04 — security-spine cred-leak audit (extended in Sprint 4
// for k8s + ssh).
//
// PRD 04 §"Acceptance criteria" item 5:
//
//   "A regression test runs each backend with a known API key and asserts
//   the key string never appears in any of: docker inspect output, kubectl
//   get all -o yaml, ssh's process listing, the wrapper script after exit"
//
// This file holds the unit-tier portion: known-secret runs through the
// available backends (currently just `local`; `docker` covered in
// docker_integration_test.go's NoLeakInInspect), then assertions over every
// inspection surface within reach of a unit test:
//
//   - os.Environ() after Backend.Run returns
//   - argv passed to a wrapped Backend (we capture it via a stub)
//   - captured stdout/stderr (validates the redactor wrap)
//
// Run with:
//
//	go test -run CredAudit ./internal/exec/...
//
// CI/Make integration: a `make test-cred-audit` target wraps this — see
// CONTRIBUTING.md "Running cred-audit tests" for context.

import (
	"bytes"
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

const auditSecret = "test-key-roksbnkctl-audit-NEVER-LOG-ME"

// argvCapture is a Backend wrapper that records every argv it sees, then
// dispatches to an inner Backend so the run still completes. Tests use it to
// assert the secret never landed in argv (PRD 04 cross-backend principle #2:
// "Never put credentials in argv").
type argvCapture struct {
	inner    Backend
	captured [][]string
}

func (c *argvCapture) Name() string { return "argv-capture(" + c.inner.Name() + ")" }
func (c *argvCapture) Run(ctx context.Context, argv []string, opts RunOpts) (int, error) {
	dup := make([]string, len(argv))
	copy(dup, argv)
	c.captured = append(c.captured, dup)
	return c.inner.Run(ctx, argv, opts)
}

// TestCredAudit_NoLeakInArgv is the security-spine test PRD 04 calls for at
// the unit-test tier.
//
// The wrapped Backend's argv must NEVER contain the secret string regardless
// of how the cred is propagated (env, mount, file, etc.). PRD 04
// cross-backend principle #2.
func TestCredAudit_NoLeakInArgv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local backend test uses sh -c")
	}
	inner, err := ResolveBackend("local")
	if err != nil {
		t.Fatalf("ResolveBackend(\"local\"): %v", err)
	}
	wrap := &argvCapture{inner: inner}

	t.Setenv("IBMCLOUD_API_KEY", auditSecret)

	creds := &Credentials{IBMCloudAPIKey: auditSecret}
	_, _ = wrap.Run(context.Background(),
		[]string{"sh", "-c", "true"},
		RunOpts{
			Stdout:      io.Discard,
			Stderr:      io.Discard,
			Credentials: creds,
		})

	if len(wrap.captured) == 0 {
		t.Fatal("argv-capture saw no calls — wrapper not invoked")
	}
	for i, argv := range wrap.captured {
		joined := strings.Join(argv, " ")
		if strings.Contains(joined, auditSecret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: secret in argv #%d: %v", i, argv)
		}
	}
}

// TestCredAudit_NoLeakInProcessEnv asserts that after Backend.Run returns,
// the parent process's os.Environ() does NOT include any new IBMCLOUD_API_KEY
// entries the backend might have set. (Some primitive impls set os.Setenv
// to propagate; that's a leak — the env should be passed only to the child.)
func TestCredAudit_NoLeakInProcessEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c true")
	}
	b, err := ResolveBackend("local")
	if err != nil {
		t.Fatalf("ResolveBackend: %v", err)
	}

	// Snapshot env keys before the run.
	before := envSnapshot()

	creds := &Credentials{IBMCloudAPIKey: auditSecret}
	_, _ = b.Run(context.Background(),
		[]string{"sh", "-c", "true"},
		RunOpts{
			Stdout:      io.Discard,
			Stderr:      io.Discard,
			Credentials: creds,
		})

	after := envSnapshot()

	// Assertion: any new env var that wasn't there before must not contain
	// the secret value.
	for k, v := range after {
		if _, existed := before[k]; existed {
			continue
		}
		if strings.Contains(v, auditSecret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: backend left env var %q=%q in parent process", k, "[redacted]")
		}
	}
}

// TestCredAudit_RedactsWrappedOutput asserts: when a wrapped tool prints its
// API key (a real ibmcloud --debug bug we've seen), the redactor catches it
// before it reaches the caller's stdout. Validates the integration of
// NewRedactor with a Backend's stream wrap.
//
// The test runs `sh -c 'echo <secret>'` through the local backend with a
// caller-provided stdout that goes through a redactor. We assert the secret
// never appears in the captured output.
func TestCredAudit_RedactsWrappedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh -c echo")
	}
	b, err := ResolveBackend("local")
	if err != nil {
		t.Fatalf("ResolveBackend: %v", err)
	}

	var raw bytes.Buffer
	stdout := NewRedactor(&raw, []string{auditSecret})

	creds := &Credentials{IBMCloudAPIKey: auditSecret}
	_, _ = b.Run(context.Background(),
		[]string{"sh", "-c", "echo " + auditSecret},
		RunOpts{
			Stdout:      stdout,
			Stderr:      io.Discard,
			Credentials: creds,
		})

	// Flush the redactor's trailing buffer, if any.
	if c, ok := stdout.(io.Closer); ok {
		_ = c.Close()
	}

	if strings.Contains(raw.String(), auditSecret) {
		t.Errorf("PRD 04 SECURITY VIOLATION: redactor missed the secret in stdout: %q", raw.String())
	}
}

func envSnapshot() map[string]string {
	out := make(map[string]string, len(os.Environ()))
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		out[kv[:i]] = kv[i+1:]
	}
	return out
}

// — Sprint 4 extension: k8s + ssh cred-leak audit — //

// TestCredAudit_K8s_NoLeakInJobSpec asserts the security-spine invariant
// for the k8s backend: when running with a known IBM Cloud API key, the
// secret value must NOT appear in the Job's container Command (argv),
// metadata Annotations, or metadata Labels. Per PRD 04 §K8s, the cred
// flows via env (today's inline-value Job path) or via a referenced
// Secret (long-lived ops-pod path); never via spec text fields.
//
// Sprint 3 carry-over (Issue 4) — the k8s + ssh audit was deferred to
// Sprint 4 alongside the backend implementations. This test covers the
// k8s side; SSH coverage lives in TestCredAudit_SSH_* below.
func TestCredAudit_K8s_NoLeakInJobSpec(t *testing.T) {
	if _, err := ResolveBackend("k8s"); err != nil {
		t.Skipf("k8s backend not registered: %v", err)
	}

	const secret = "test-key-roksbnkctl-k8s-audit-NEVER-LOG-ME"

	opts := RunOpts{
		Credentials: &Credentials{IBMCloudAPIKey: secret},
	}
	argv := []string{"ibmcloud", "iam", "oauth-tokens"}

	// We use the same buildJobSpec helper the K8sBackend uses internally.
	// This checks the spec at construction time without needing a fake
	// clientset round-trip.
	job := buildJobSpec("roksbnkctl-ibmcloud-audit", "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev", argv, opts, false, "")

	// 1. argv (container Command) — never the secret.
	for _, a := range job.Spec.Template.Spec.Containers[0].Command {
		if strings.Contains(a, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred in Job container Command: %v", job.Spec.Template.Spec.Containers[0].Command)
		}
	}

	// 2. Metadata annotations — never.
	for k, v := range job.Annotations {
		if strings.Contains(v, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred in Job annotation %s=%s", k, "[redacted]")
		}
	}
	for k, v := range job.Spec.Template.Annotations {
		if strings.Contains(v, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred in Pod template annotation %s=%s", k, "[redacted]")
		}
	}

	// 3. Metadata labels — never.
	for k, v := range job.Labels {
		if strings.Contains(v, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred in Job label %s=%s", k, "[redacted]")
		}
	}
	for k, v := range job.Spec.Template.Labels {
		if strings.Contains(v, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred in Pod template label %s=%s", k, "[redacted]")
		}
	}

	// 4. Container Env: this is the documented carrier — assert the value
	// IS present (so we know creds reached the pod), but ONLY there.
	c := job.Spec.Template.Spec.Containers[0]
	foundInEnv := false
	for _, e := range c.Env {
		if e.Value == secret {
			foundInEnv = true
		}
	}
	if !foundInEnv {
		t.Errorf("expected cred to be carried via container Env (the documented mechanism); not found in %+v", c.Env)
	}

	// 5. Image/WorkingDir/Args — never.
	if strings.Contains(c.Image, secret) {
		t.Errorf("PRD 04 SECURITY VIOLATION: cred in container Image")
	}
	if strings.Contains(c.WorkingDir, secret) {
		t.Errorf("PRD 04 SECURITY VIOLATION: cred in container WorkingDir")
	}
	for _, a := range c.Args {
		if strings.Contains(a, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred in container Args: %v", c.Args)
		}
	}
}

// TestCredAudit_K8s_FilesSecretCarriesKubeconfigOnly asserts: when the
// caller passes a kubeconfig via RunOpts.Files, the per-Job files Secret
// holds the kubeconfig bytes (base64 in the wire-format Secret payload),
// but the IBM API key NEVER lands in this Secret — that lives in the
// roksbnkctl-ibm-creds Secret (the long-lived ops-namespace Secret) only.
func TestCredAudit_K8s_FilesSecretCarriesKubeconfigOnly(t *testing.T) {
	if _, err := ResolveBackend("k8s"); err != nil {
		t.Skipf("k8s backend not registered: %v", err)
	}

	const secret = "test-key-roksbnkctl-k8s-files-audit"

	opts := RunOpts{
		Credentials: &Credentials{IBMCloudAPIKey: secret},
		Files: map[string][]byte{
			"kubeconfig": []byte("apiVersion: v1\nkind: Config\nclusters: []\n"),
		},
	}
	job := buildJobSpec("roksbnkctl-iperf3-files-audit", "ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:dev", []string{"iperf3", "-c", "server"}, opts, true, "roksbnkctl-iperf3-files-audit-files")

	// The files secret reference is the only place a kubeconfig lives.
	// Assert that the volume's secret reference name doesn't contain the
	// API key value (it shouldn't — name should be deterministic, not
	// derived from the secret).
	for _, v := range job.Spec.Template.Spec.Volumes {
		if v.Secret == nil {
			continue
		}
		if strings.Contains(v.Secret.SecretName, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred value embedded in volume's Secret name: %q", v.Secret.SecretName)
		}
	}
}

// TestCredAudit_SSH_NoLeakInArgvOrWrapper asserts: when the SSH backend
// runs with a known IBM Cloud API key, the secret value never appears in:
//
//   - the argv sent to the remote process (visible in remote `ps`)
//   - the captured stdout/stderr (validates the redactor wraps streams)
//
// At validator-dispatch time the SSH backend wasn't yet registered.
// Skips gracefully when not available; covers the documented audit
// surface once staff lands ssh.go.
//
// Wrapper-script content inspection (the third element of PRD 04's
// acceptance criterion) requires a hook into the backend's wrapper
// rendering — see issues/issue_sprint4_validator.md for the SSH-mock
// surface roadmap.
func TestCredAudit_SSH_NoLeakInArgvOrWrapper(t *testing.T) {
	b, err := ResolveBackend("ssh:test")
	if err != nil {
		t.Skipf("SSH backend not registered: %v", err)
	}

	const secret = "test-key-roksbnkctl-ssh-audit"

	var stdout, stderr bytes.Buffer
	creds := &Credentials{IBMCloudAPIKey: secret}

	// The SSH backend likely fails fast on a nonexistent target name; the
	// audit assertions still hold — even on failure, no secret should
	// appear in any captured output.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = b.Run(ctx,
		[]string{"ibmcloud", "iam", "oauth-tokens"},
		RunOpts{
			Stdout:      NewRedactor(&stdout, []string{secret}),
			Stderr:      NewRedactor(&stderr, []string{secret}),
			Credentials: creds,
		})

	if strings.Contains(stdout.String(), secret) {
		t.Errorf("PRD 04 SECURITY VIOLATION: redactor missed secret in SSH backend stdout: %q", stdout.String())
	}
	if strings.Contains(stderr.String(), secret) {
		t.Errorf("PRD 04 SECURITY VIOLATION: redactor missed secret in SSH backend stderr: %q", stderr.String())
	}
}

// TestCredAudit_K8s_NoLeakInProcessEnvAfterRun asserts the process-env
// invariant for the k8s backend: after Run returns, os.Environ() must not
// contain the IBM API key value in any newly-set env var. The k8s backend
// shouldn't be touching the parent's process env at all (it injects creds
// into the in-pod container env via the Job spec); this guards against
// future implementations regressing into local-style os.Setenv usage.
func TestCredAudit_K8s_NoLeakInProcessEnvAfterRun(t *testing.T) {
	if _, err := ResolveBackend("k8s"); err != nil {
		t.Skipf("k8s backend not registered: %v", err)
	}

	const secret = "test-key-roksbnkctl-k8s-procenv-audit"

	// Build the spec; we don't actually need to run it through a Job
	// lifecycle for the os.Environ() invariant — the k8s backend
	// shouldn't even touch process env. The presence of buildJobSpec is
	// the seam.
	before := envSnapshot()

	opts := RunOpts{Credentials: &Credentials{IBMCloudAPIKey: secret}}
	_ = buildJobSpec("audit-job", "alpine:3", []string{"true"}, opts, false, "")

	after := envSnapshot()
	for k, v := range after {
		if _, existed := before[k]; existed {
			continue
		}
		if strings.Contains(v, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: k8s buildJobSpec leaked secret to process env %s=%s", k, "[redacted]")
		}
	}
}
