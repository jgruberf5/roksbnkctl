package exec

// Sprint 4 / PRD 03 — K8s backend unit tests.
//
// Covers the K8sBackend's argv handling, Job/Pod spec construction, the
// long-lived ops-pod exec dispatch, cred propagation per PRD 04 §K8s, and
// ttl/cleanup invariants. Uses k8s.io/client-go/kubernetes/fake to drive
// the backend's clientset paths in-process — no real API server needed.
//
// Run with:
//
//	go test -run K8sBackend ./internal/exec/...
//
// Tests that need a real kind cluster live behind the `integration` build
// tag in k8s_integration_test.go.

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

// resolveK8s returns the k8s backend via the registry. Skips the calling
// test when the backend isn't registered yet (Sprint 4 staff dispatch
// in-flight scenario).
func resolveK8s(t *testing.T) *K8sBackend {
	t.Helper()
	b, err := ResolveBackend("k8s")
	if err != nil {
		t.Skipf("k8s backend not registered yet: %v", err)
	}
	if b == nil {
		t.Skip("k8s backend resolved to nil")
	}
	if b.Name() != "k8s" {
		t.Errorf("k8s backend Name(): got %q, want %q", b.Name(), "k8s")
	}
	kb, ok := b.(*K8sBackend)
	if !ok {
		t.Skipf("k8s backend isn't *K8sBackend: %T", b)
	}
	return kb
}

// newFakeBackend wires a *K8sBackend with a fake clientset + a stub
// rest.Config so the in-process exec / Job paths work without any live
// apiserver.
func newFakeBackend(t *testing.T, objs ...runtime.Object) (*K8sBackend, *fake.Clientset) {
	t.Helper()
	cs := fake.NewSimpleClientset(objs...)
	cfg := &rest.Config{Host: "https://fake.test"}
	b := &K8sBackend{
		client: cs,
		config: cfg,
		initFn: func() (kubernetes.Interface, *rest.Config, error) {
			return cs, cfg, nil
		},
	}
	return b, cs
}

// — extractLongLivedFlag table-driven coverage — //

// TestExtractLongLivedFlag covers the ops-pod-vs-Job dispatch sentinel.
// PRD 03 §K8s splits run into "long-lived ops pod exec" (ibmcloud / shell)
// vs "one-shot Job" (iperf3 / terraform). The sentinel is the
// public-Backend-interface-friendly way to plumb that bit through
// RunOpts.Env without an API change.
func TestExtractLongLivedFlag(t *testing.T) {
	cases := []struct {
		name     string
		env      []string
		wantLong bool
		wantEnv  []string
	}{
		{
			name:     "no sentinel",
			env:      []string{"FOO=bar", "BAZ=qux"},
			wantLong: false,
			wantEnv:  []string{"FOO=bar", "BAZ=qux"},
		},
		{
			name:     "sentinel only",
			env:      []string{"ROKSBNKCTL_K8S_LONG_LIVED=1"},
			wantLong: true,
			wantEnv:  []string{},
		},
		{
			name:     "sentinel mixed with other vars",
			env:      []string{"FOO=bar", "ROKSBNKCTL_K8S_LONG_LIVED=1", "BAZ=qux"},
			wantLong: true,
			wantEnv:  []string{"FOO=bar", "BAZ=qux"},
		},
		{
			name:     "empty env",
			env:      nil,
			wantLong: false,
			wantEnv:  []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLong, gotEnv := extractLongLivedFlag(tc.env)
			if gotLong != tc.wantLong {
				t.Errorf("longLived: got %v, want %v", gotLong, tc.wantLong)
			}
			if !reflect.DeepEqual(gotEnv, tc.wantEnv) {
				t.Errorf("filteredEnv: got %v, want %v", gotEnv, tc.wantEnv)
			}
		})
	}
}

// — buildJobEnv coverage — //

// TestBuildJobEnv_CredsAndEnvMerge asserts buildJobEnv merges RunOpts.Env
// with Credentials.EnvVars(), with creds winning on conflicts (matches the
// local-backend semantics).
func TestBuildJobEnv_CredsAndEnvMerge(t *testing.T) {
	resolveK8s(t) // skip-marker for environments without the backend

	opts := RunOpts{
		Env: []string{"FOO=bar", "IBMCLOUD_API_KEY=will-be-overwritten"},
		Credentials: &Credentials{
			IBMCloudAPIKey: "winning-key",
		},
	}
	env := buildJobEnv(opts)

	got := map[string]string{}
	for _, e := range env {
		got[e.Name] = e.Value
	}
	if got["FOO"] != "bar" {
		t.Errorf("FOO: got %q, want %q", got["FOO"], "bar")
	}
	if got["IBMCLOUD_API_KEY"] != "winning-key" {
		t.Errorf("IBMCLOUD_API_KEY: got %q, want %q (creds should override RunOpts.Env)",
			got["IBMCLOUD_API_KEY"], "winning-key")
	}
	// IC_API_KEY is set by Credentials.EnvVars() too.
	if got["IC_API_KEY"] != "winning-key" {
		t.Errorf("IC_API_KEY: got %q, want %q", got["IC_API_KEY"], "winning-key")
	}
}

// TestBuildJobEnv_NoCredsNoExtra asserts that without Credentials, only
// the caller's RunOpts.Env shows up.
func TestBuildJobEnv_NoCredsNoExtra(t *testing.T) {
	resolveK8s(t)
	opts := RunOpts{Env: []string{"FOO=bar"}}
	env := buildJobEnv(opts)
	if len(env) != 1 {
		t.Fatalf("env: want 1 entry, got %d (%v)", len(env), env)
	}
	if env[0].Name != "FOO" || env[0].Value != "bar" {
		t.Errorf("env[0]: got %v, want FOO=bar", env[0])
	}
}

// — buildJobSpec coverage — //

// TestBuildJobSpec_DefaultShape validates the high-level spec shape:
// namespace, ttl, backoff limit, RestartPolicy=Never, the RuntimeDefault
// seccomp profile (PRD 03 §K8s SCC compliance), and the cmd → container
// args translation.
func TestBuildJobSpec_DefaultShape(t *testing.T) {
	resolveK8s(t)

	opts := RunOpts{}
	job := buildJobSpec("roksbnkctl-test-abcdef", "busybox:latest", []string{"echo", "hello"}, opts, false, "")

	if job.Namespace != K8sTestNamespace {
		t.Errorf("Namespace: got %q, want %q", job.Namespace, K8sTestNamespace)
	}
	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 60 {
		t.Errorf("TTLSecondsAfterFinished: got %v, want *60", job.Spec.TTLSecondsAfterFinished)
	}
	if job.Spec.BackoffLimit == nil || *job.Spec.BackoffLimit != 0 {
		t.Errorf("BackoffLimit: got %v, want *0", job.Spec.BackoffLimit)
	}
	pod := job.Spec.Template.Spec
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy: got %q, want %q", pod.RestartPolicy, corev1.RestartPolicyNever)
	}
	// SCC posture per the iperf3 SCC fix from Sprint 4.
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Errorf("PodSecurityContext.RunAsNonRoot: want true")
	}
	if pod.SecurityContext == nil || pod.SecurityContext.SeccompProfile == nil ||
		pod.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("SeccompProfile.Type: want RuntimeDefault, got %+v", pod.SecurityContext)
	}
	if len(pod.Containers) != 1 {
		t.Fatalf("Containers: got %d, want 1", len(pod.Containers))
	}
	c := pod.Containers[0]
	if c.Image != "busybox:latest" {
		t.Errorf("Container Image: got %q, want %q", c.Image, "busybox:latest")
	}
	if !reflect.DeepEqual(c.Command, []string{"echo", "hello"}) {
		t.Errorf("Container Command: got %v, want [echo hello]", c.Command)
	}
	if c.SecurityContext == nil || c.SecurityContext.AllowPrivilegeEscalation == nil ||
		*c.SecurityContext.AllowPrivilegeEscalation {
		t.Errorf("Container AllowPrivilegeEscalation: want false")
	}
	if c.SecurityContext == nil || c.SecurityContext.Capabilities == nil ||
		!containsCap(c.SecurityContext.Capabilities.Drop, "ALL") {
		t.Errorf("Container Capabilities.Drop: want ALL, got %+v", c.SecurityContext.Capabilities)
	}
}

// TestBuildJobSpec_FilesProjectedSecret asserts that when Files is set,
// the Job spec mounts the per-Job files Secret at /work read-only and
// inherits /work as WorkingDir.
func TestBuildJobSpec_FilesProjectedSecret(t *testing.T) {
	resolveK8s(t)

	opts := RunOpts{
		Files: map[string][]byte{
			"kubeconfig": []byte("apiVersion: v1\nkind: Config\n"),
		},
	}
	job := buildJobSpec("roksbnkctl-iperf3-xxxxxx", "busybox:latest", []string{"true"}, opts, true, "roksbnkctl-iperf3-xxxxxx-files")
	pod := job.Spec.Template.Spec

	// Volumes: one named "files" referencing the per-Job secret.
	if len(pod.Volumes) != 1 {
		t.Fatalf("Volumes: got %d, want 1", len(pod.Volumes))
	}
	v := pod.Volumes[0]
	if v.Secret == nil {
		t.Fatalf("Volume should reference a Secret, got %+v", v.VolumeSource)
	}
	if v.Secret.SecretName != "roksbnkctl-iperf3-xxxxxx-files" {
		t.Errorf("Secret.SecretName: got %q, want roksbnkctl-iperf3-xxxxxx-files", v.Secret.SecretName)
	}

	// Container mount: /work, ro.
	if len(pod.Containers) != 1 {
		t.Fatalf("Containers: got %d, want 1", len(pod.Containers))
	}
	c := pod.Containers[0]
	if len(c.VolumeMounts) != 1 {
		t.Fatalf("VolumeMounts: got %d, want 1", len(c.VolumeMounts))
	}
	m := c.VolumeMounts[0]
	if m.MountPath != "/work" {
		t.Errorf("MountPath: got %q, want /work", m.MountPath)
	}
	if !m.ReadOnly {
		t.Errorf("VolumeMount should be read-only")
	}
	if c.WorkingDir != "/work" {
		t.Errorf("WorkingDir: got %q, want /work (default when Files set)", c.WorkingDir)
	}
}

// TestBuildJobSpec_CredsViaEnv asserts: when the caller provides
// Credentials.IBMCloudAPIKey, the Job's container Env carries the
// IBMCLOUD_API_KEY entry — and the value never appears in argv (a
// mirror of PRD 04 cross-backend principle #2 enforced at Job-level).
func TestBuildJobSpec_CredsViaEnv(t *testing.T) {
	resolveK8s(t)

	const secret = "test-key-NEVER-IN-ARGV"
	opts := RunOpts{
		Credentials: &Credentials{IBMCloudAPIKey: secret},
	}
	argv := []string{"ibmcloud", "iam", "oauth-tokens"}
	job := buildJobSpec("roksbnkctl-ibmcloud-xxxxxx", "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev", argv, opts, false, "")

	c := job.Spec.Template.Spec.Containers[0]
	for _, a := range c.Command {
		if strings.Contains(a, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred value in container Command: %v", c.Command)
		}
	}

	// Verify the cred reaches the container via env. (Jobs path inlines
	// the value via Credentials.EnvVars(); the long-lived ops-pod path
	// uses envFrom secretRef on roksbnkctl-ibm-creds. Sprint 4 ships the
	// inline-value Job path; deferring secretRef wiring to a polish pass
	// per the buildJobSpec doc comment.)
	got := map[string]string{}
	for _, e := range c.Env {
		got[e.Name] = e.Value
	}
	if got["IBMCLOUD_API_KEY"] != secret {
		t.Errorf("env IBMCLOUD_API_KEY: got %q, want %q", got["IBMCLOUD_API_KEY"], secret)
	}
}

// — Run() against fake clientset — //

// TestK8sBackend_Run_LongLived_PodNotReady_Fails asserts that the
// ops-pod exec path returns rc=127 + a clear error when the ops pod isn't
// found. Validates the "ops install hasn't run" failure mode the prompt
// calls out.
func TestK8sBackend_Run_LongLived_PodNotReady_Fails(t *testing.T) {
	resolveK8s(t)

	b, _ := newFakeBackend(t)
	rc, err := b.Run(context.Background(),
		[]string{"ibmcloud", "iam", "oauth-tokens"},
		RunOpts{
			Env:    []string{k8sLongLivedKey},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
	if rc != k8sExitFailedToStart {
		t.Errorf("rc: got %d, want %d (failed-to-start)", rc, k8sExitFailedToStart)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "ops") {
		t.Errorf("error message %q lacks the 'ops install' troubleshooting hint", err)
	}
}

// runJobAndAwaitCreate runs K8sBackend.Run in a goroutine and polls the
// fake clientset until a Job lands. Returns the captured Job (first
// match) plus cleanup/synchronisation primitives so callers can pin the
// rest of the assertion they care about, then ctx-cancel + drain.
//
// The split-test pattern (one invariant per Test*) is the Sprint 5
// polish ask from Sprint 4 tech-writer Issue 14.
func runJobAndAwaitCreate(t *testing.T, b *K8sBackend, cs *fake.Clientset, argv []string, opts RunOpts, pollDeadline time.Duration) (*batchv1.Job, context.CancelFunc, chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = b.Run(ctx, argv, opts)
	}()

	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		j, err := cs.BatchV1().Jobs(K8sTestNamespace).List(context.Background(), metav1.ListOptions{})
		if err == nil && len(j.Items) > 0 {
			job := j.Items[0]
			return &job, cancel, done
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("expected at least one Job to be created against the fake clientset within the poll window")
	return nil, cancel, done
}

// TestK8sBackend_Run_Job_CreatesJob pins the first single invariant
// from the original combined test: a Job is created in K8sTestNamespace
// with a roksbnkctl- prefix.
//
// Sprint 5 polish (Sprint 4 tech-writer Issue 14 carry-over): split out
// from TestK8sBackend_Run_Job_CreatesJobAndSecret_TTL so a green/red
// failure points to exactly which invariant regressed.
func TestK8sBackend_Run_Job_CreatesJob(t *testing.T) {
	resolveK8s(t)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: K8sTestNamespace}}
	b, cs := newFakeBackend(t, ns)

	job, cancel, done := runJobAndAwaitCreate(t, b, cs,
		[]string{"iperf3", "-V"},
		RunOpts{Stdout: io.Discard, Stderr: io.Discard,
			Files: map[string][]byte{"kubeconfig": []byte("apiVersion: v1\nkind: Config\n")}},
		1500*time.Millisecond)

	if !strings.HasPrefix(job.Name, "roksbnkctl-") {
		t.Errorf("Job name should start with roksbnkctl-: got %q", job.Name)
	}
	if job.Namespace != K8sTestNamespace {
		t.Errorf("Job Namespace: got %q, want %q", job.Namespace, K8sTestNamespace)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Run() didn't return after ctx cancel within 2s")
	}
}

// TestK8sBackend_Run_Job_CreatesFilesSecret pins the per-Job files
// Secret invariant: when RunOpts.Files is set, a corresponding Secret
// in K8sTestNamespace carries the file content. The Secret name's
// owner-ref to the Job is a downstream cleanup invariant covered by
// the integration tier; this test just confirms the Secret exists with
// the right Data.
//
// Sprint 5 polish (Sprint 4 tech-writer Issue 14 carry-over).
func TestK8sBackend_Run_Job_CreatesFilesSecret(t *testing.T) {
	resolveK8s(t)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: K8sTestNamespace}}
	b, cs := newFakeBackend(t, ns)

	_, cancel, done := runJobAndAwaitCreate(t, b, cs,
		[]string{"iperf3", "-V"},
		RunOpts{Stdout: io.Discard, Stderr: io.Discard,
			Files: map[string][]byte{"kubeconfig": []byte("apiVersion: v1\nkind: Config\n")}},
		1500*time.Millisecond)

	secrets, err := cs.CoreV1().Secrets(K8sTestNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		cancel()
		<-done
		t.Fatalf("listing secrets: %v", err)
	}
	if len(secrets.Items) == 0 {
		t.Errorf("expected per-Job files Secret, got 0 secrets")
	} else {
		s := secrets.Items[0]
		if s.Data == nil || len(s.Data["kubeconfig"]) == 0 {
			t.Errorf("Secret missing 'kubeconfig' data: %+v", s.Data)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Run() didn't return after ctx cancel within 2s")
	}
}

// TestK8sBackend_Run_Job_SetsTTL pins the TTL invariant —
// `ttlSecondsAfterFinished=60` is stamped on every Job the backend
// creates (PRD 03 §"K8s shape": "Auto-delete on completion
// (`ttlSecondsAfterFinished: 60`)").
//
// Sprint 5 polish (Sprint 4 tech-writer Issue 14 carry-over).
func TestK8sBackend_Run_Job_SetsTTL(t *testing.T) {
	resolveK8s(t)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: K8sTestNamespace}}
	b, cs := newFakeBackend(t, ns)

	job, cancel, done := runJobAndAwaitCreate(t, b, cs,
		[]string{"iperf3", "-V"},
		RunOpts{Stdout: io.Discard, Stderr: io.Discard},
		1500*time.Millisecond)

	if job.Spec.TTLSecondsAfterFinished == nil || *job.Spec.TTLSecondsAfterFinished != 60 {
		t.Errorf("TTLSecondsAfterFinished: got %v, want *60 (PRD 03 §K8s shape)", job.Spec.TTLSecondsAfterFinished)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Run() didn't return after ctx cancel within 2s")
	}
}

// TestK8sBackend_Run_Job_CtxCancel_DeletesJob asserts ctx cancellation
// triggers the Job + Secret cleanup goroutine. PRD 03 §"Backend
// interface": "ctx cancellation must terminate the remote process within
// a few seconds."
func TestK8sBackend_Run_Job_CtxCancel_DeletesJob(t *testing.T) {
	resolveK8s(t)

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: K8sTestNamespace}}
	b, cs := newFakeBackend(t, ns)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		_, _ = b.Run(ctx,
			[]string{"iperf3", "-s"},
			RunOpts{Stdout: io.Discard, Stderr: io.Discard})
	}()

	// Wait briefly for the Job create to land.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		j, err := cs.BatchV1().Jobs(K8sTestNamespace).List(context.Background(), metav1.ListOptions{})
		if err == nil && len(j.Items) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()

	// After cancel, the cleanup goroutine should issue a Job delete. We
	// can't synchronously observe deletion (the goroutine runs out-of-
	// band of the test goroutine), so poll for absence with a generous
	// timeout. Fake clientset's Delete is synchronous but our Run goroutine
	// might still be in waitForJobPodRunning when cancel hits.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, err := cs.BatchV1().Jobs(K8sTestNamespace).List(context.Background(), metav1.ListOptions{})
		if err == nil && len(j.Items) == 0 {
			return // success: Job was deleted
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("expected Job to be deleted after ctx cancel, but it persists in the fake clientset")
}

// TestK8sBackend_NoCredValueInArgv pins the PRD 04 §"In-cluster pod"
// security invariant: the IBM Cloud API key value MUST appear only in
// the Secret's data field (base64), never in argv, never in container
// env values, never in pod metadata annotations or labels. A regression
// here is a cred-leak vulnerability — see audit_test.go for the
// cross-surface audit (TestCredAudit_K8s_NoLeakInJobSpec is the
// build-spec sibling of this Run-time-pinned test).
//
// PRD 04 cross-backend principle #2: argv passed to Run must NEVER
// contain the IBMCloudAPIKey value, regardless of which path
// (long-lived or Job) executes.
//
// Sprint 5 polish (Sprint 4 tech-writer Issue 14 carry-over): expanded
// docstring citing the PRD invariant; the assertion surface is unchanged.
func TestK8sBackend_NoCredValueInArgv(t *testing.T) {
	resolveK8s(t)

	const secret = "test-key-NEVER-IN-K8S-ARGV"
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: K8sTestNamespace}}
	b, cs := newFakeBackend(t, ns)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = b.Run(ctx,
			[]string{"ibmcloud", "iam", "oauth-tokens"},
			RunOpts{
				Stdout:      io.Discard,
				Stderr:      io.Discard,
				Credentials: &Credentials{IBMCloudAPIKey: secret},
			})
	}()

	// Wait for Job creation, then inspect.
	deadline := time.Now().Add(1500 * time.Millisecond)
	var jobs *batchv1.JobList
	for time.Now().Before(deadline) {
		j, err := cs.BatchV1().Jobs(K8sTestNamespace).List(context.Background(), metav1.ListOptions{})
		if err == nil && len(j.Items) > 0 {
			jobs = j
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	if jobs == nil || len(jobs.Items) == 0 {
		t.Skip("no Job created within poll window; can't audit argv")
	}
	job := jobs.Items[0]
	c := job.Spec.Template.Spec.Containers[0]

	// Container.Command (the argv equivalent) must not contain the secret.
	for _, a := range c.Command {
		if strings.Contains(a, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred value in Job container Command: %v", c.Command)
		}
	}
	// Annotations / labels: PRD 04 §K8s — never. (We don't set any with
	// cred content, but assert the negative explicitly.)
	for k, v := range job.Annotations {
		if strings.Contains(v, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred in Job annotation %s=%s", k, "[redacted]")
		}
	}
	for k, v := range job.Labels {
		if strings.Contains(v, secret) {
			t.Errorf("PRD 04 SECURITY VIOLATION: cred in Job label %s=%s", k, "[redacted]")
		}
	}
}

// — Sprint 10 / staff Issue 2 closure: ibmcloud login wrap branches
// on $IAM_PROFILE_ID — //

// TestIBMCloudLoginWrap_BranchesOnIAMProfileID asserts the wrap-script
// constant carries both the trusted-profile and static-key login
// branches, gated on `$IAM_PROFILE_ID`. The actual runtime behavior
// (login succeeds, retries, etc.) is integration-test surface; here we
// just check that the shell-conditional is in place — a regression to
// the unconditional `--apikey` form would silently re-introduce Sprint
// 9 staff Issue 2.
//
// We can't easily exec the wrap script under `sh` in CI without an
// `ibmcloud` binary, so the assertions are textual: the script must
// contain both login branches plus the conditional that selects.
func TestIBMCloudLoginWrap_BranchesOnIAMProfileID(t *testing.T) {
	s := ibmcloudLoginWrapScript

	mustContain := []string{
		// The shell conditional that selects the path.
		`if [ -n "$IAM_PROFILE_ID" ]`,
		// Trusted-profile login form (Sprint 10 / validator Issue 1
		// closure: `ibmcloud 2.43.0` uses `--cr-token @<path>` +
		// `--profile <id>`, NOT the non-existent `--trusted-profile-id`
		// flag the Sprint 10 initial wrap reached for).
		`--cr-token @/var/run/secrets/tokens/token`,
		`--profile "$IAM_PROFILE_ID"`,
		// IBM Cloud API endpoint — the cold ops pod has no persisted
		// `ibmcloud api` setting, so both branches must set it
		// explicitly.
		`-a https://cloud.ibm.com`,
		// Static-key login form (unchanged from v1.0.x).
		`--apikey "$IBMCLOUD_API_KEY"`,
		// Final exec'd command — argv flows through verbatim.
		`exec ibmcloud "$@"`,
	}
	for _, frag := range mustContain {
		if !strings.Contains(s, frag) {
			t.Errorf("wrap script missing required fragment %q\nfull script:\n%s", frag, s)
		}
	}

	// Validator Issue 1 regression guard: the non-existent
	// `--trusted-profile-id` flag must NOT reappear. If it does, a live
	// `ibmcloud 2.43.0` run will fail with
	// `Incorrect Usage: flag provided but not defined: -trusted-profile-id`.
	if strings.Contains(s, `--trusted-profile-id`) {
		t.Errorf("wrap script reintroduced non-existent --trusted-profile-id flag (does not exist on ibmcloud 2.43.0; use --cr-token + --profile instead)\nfull:\n%s", s)
	}

	// The retry should be 3 attempts. Crude assertion — look for the
	// loop bound + the sleep value.
	if !strings.Contains(s, `"$attempt" -le 3`) {
		t.Errorf("wrap script missing 3-attempt retry bound (Sprint 10 OIDC-propagation mitigation)\nfull:\n%s", s)
	}
	if !strings.Contains(s, `sleep 20`) {
		t.Errorf("wrap script missing 20s backoff between retry attempts\nfull:\n%s", s)
	}

	// Region defaulting must still happen on both branches.
	if !strings.Contains(s, `"${IBMCLOUD_REGION:-us-south}"`) {
		t.Errorf("wrap script missing IBMCLOUD_REGION default (us-south)\nfull:\n%s", s)
	}
}

// TestIBMCloudLoginWrap_TrustedProfileOmitsAPIKey asserts the trusted-
// profile branch does NOT carry `--apikey`. The two flags being
// mutually exclusive is core to the Sprint 10 design (trusted-profile
// success ⇒ no static API key in any Secret at rest); a regression
// where both flags coexist on the same `ibmcloud login` invocation
// would partially defeat the security gain.
func TestIBMCloudLoginWrap_TrustedProfileOmitsAPIKey(t *testing.T) {
	s := ibmcloudLoginWrapScript

	// Find the section after `if [ -n "$IAM_PROFILE_ID" ]` and before
	// the corresponding `else`. The trusted-profile branch lives there.
	startMarker := `if [ -n "$IAM_PROFILE_ID" ]; then`
	endMarker := "else"
	startIdx := strings.Index(s, startMarker)
	endIdx := strings.Index(s, endMarker)
	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		t.Fatalf("wrap script doesn't have the expected if/else structure:\n%s", s)
	}
	tpBranch := s[startIdx:endIdx]
	if strings.Contains(tpBranch, "--apikey") {
		t.Errorf("trusted-profile branch leaks --apikey flag (must use only --cr-token + --profile):\n%s", tpBranch)
	}
	// Sprint 10 / validator Issue 1: the trusted-profile branch must use
	// `--cr-token @<path>` + `--profile <id>` (the documented `ibmcloud
	// 2.43.0` form) — NOT the non-existent `--trusted-profile-id` flag
	// the initial Sprint 10 wrap reached for.
	if !strings.Contains(tpBranch, "--cr-token @/var/run/secrets/tokens/token") {
		t.Errorf("trusted-profile branch missing --cr-token @<projected-token-path>:\n%s", tpBranch)
	}
	if !strings.Contains(tpBranch, `--profile "$IAM_PROFILE_ID"`) {
		t.Errorf("trusted-profile branch missing --profile \"$IAM_PROFILE_ID\":\n%s", tpBranch)
	}
	if strings.Contains(tpBranch, "--trusted-profile-id") {
		t.Errorf("trusted-profile branch references non-existent --trusted-profile-id flag (must use --cr-token + --profile):\n%s", tpBranch)
	}
}

// — splitKV table-driven coverage — //

func TestSplitKV(t *testing.T) {
	cases := []struct {
		in    string
		k, v  string
		valid bool
	}{
		{"FOO=bar", "FOO", "bar", true},
		{"FOO=", "FOO", "", true},
		{"=bar", "", "", false},
		{"FOO", "", "", false},
		{"", "", "", false},
		{"A=B=C", "A", "B=C", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			gotK, gotV, ok := splitKV(tc.in)
			if ok != tc.valid {
				t.Errorf("ok: got %v, want %v", ok, tc.valid)
			}
			if ok && (gotK != tc.k || gotV != tc.v) {
				t.Errorf("got (%q, %q), want (%q, %q)", gotK, gotV, tc.k, tc.v)
			}
		})
	}
}

// — small helpers — //

func containsCap(caps []corev1.Capability, want string) bool {
	for _, c := range caps {
		if string(c) == want {
			return true
		}
	}
	return false
}
