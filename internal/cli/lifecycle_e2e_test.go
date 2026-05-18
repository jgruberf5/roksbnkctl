package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// Sprint 14 deliverable 3 — the e2e/blind-spot regression guard.
//
// This defect reached the user LIVE, not the gate: Sprint 13 fixed the
// KUBECONFIG env leak, but the jumphost still had no kubeconfig at all
// and `--on jumphost kubectl` deterministically hit localhost:8080.
// These tests drive the `up → --on <target>` path against a stubbed
// backend and assert BOTH:
//
//   1. the remote-vs-local env composition (the Sprint 13 Issue 1
//      surface — KUBECONFIG absent on the wire, present locally), and
//   2. the part-B self-heal heal-vs-outage discrimination (no remote
//      kubeconfig → heal attempted/succeeds; cluster genuinely down →
//      the real ibmcloud error surfaced after bounded retry, never
//      masked as success, never an infinite spin).
//
// An Issue-1-class leak or a missing-remote-kubeconfig defect now FAILS
// A TEST instead of reaching a human.

// --- stub remoteRunner -------------------------------------------------

// scriptedRunner is a programmable remoteRunner: each call consumes the
// next step. A step matches either the kubeconfig probe (the
// `kubectl config current-context` form) or the heal command
// (`ibmcloud ks cluster config ... --admin`) and returns a canned
// exit code / stdout / transport error.
type scriptedRunner struct {
	t         *testing.T
	steps     []step
	calls     int
	probeSeen int
	healSeen  int
	lastHeal  string // the heal `sh -c` script text, for assertions
}

type step struct {
	wantHeal bool   // true: expect the ibmcloud heal cmd; false: the probe
	code     int    // remote exit code
	stdout   string // probe writes "rc=0"/"rc=1"; heal stdout rarely read
	stderr   string
	err      error // transport-level error (not a remote exit)
}

func (s *scriptedRunner) Run(_ context.Context, argv []string, stdout, stderr io.Writer) (int, error) {
	if s.calls >= len(s.steps) {
		s.t.Fatalf("scriptedRunner: unexpected extra call %d: %v", s.calls+1, argv)
	}
	st := s.steps[s.calls]
	s.calls++

	// Probe and heal are both `sh -c <script> …`; disjoint by script
	// content (probe reads `kubectl config current-context`, heal runs
	// `ibmcloud ks cluster config … --admin`, optionally preceded by
	// `ibmcloud login` since Sprint 14's login-extension).
	script := ""
	if len(argv) >= 3 && argv[0] == "sh" && argv[1] == "-c" {
		script = argv[2]
	}
	isProbe := strings.Contains(script, "current-context")
	isHeal := strings.Contains(script, "ks cluster config")
	switch {
	case st.wantHeal && !isHeal:
		s.t.Fatalf("step %d: expected heal cmd, got %v", s.calls, argv)
	case !st.wantHeal && !isProbe:
		s.t.Fatalf("step %d: expected probe cmd, got %v", s.calls, argv)
	}
	if isHeal {
		s.healSeen++
		s.lastHeal = script
	}
	if isProbe {
		s.probeSeen++
	}
	if st.stdout != "" && stdout != nil {
		_, _ = io.WriteString(stdout, st.stdout)
	}
	if st.stderr != "" && stderr != nil {
		_, _ = io.WriteString(stderr, st.stderr)
	}
	return st.code, st.err
}

// fastSelfHeal shrinks the self-heal backoff for the test process so the
// bounded-retry path doesn't add real wall-clock seconds. Restored via
// t.Cleanup.
func fastSelfHeal(t *testing.T) {
	t.Helper()
	pa, pb := selfHealMaxAttempts, selfHealBackoff
	selfHealMaxAttempts = 3
	selfHealBackoff = time.Millisecond
	t.Cleanup(func() { selfHealMaxAttempts, selfHealBackoff = pa, pb })
}

// --- 1. env composition (Sprint 13 Issue 1 surface) --------------------

// TestE2E_RemoteVsLocalEnvComposition is the Issue-1 regression guard at
// the dispatch-env level: the env that crosses the --on SSH boundary
// (workspaceEnvCore) must carry the IBMCLOUD_* values and NEVER the
// local KUBECONFIG path; the local-exec env (workspaceEnv) must still
// carry KUBECONFIG. A regression here is exactly the defect the user hit
// before Sprint 13.
func TestE2E_RemoteVsLocalEnvComposition(t *testing.T) {
	stageEnvSplitWorkspace(t)
	stageRealKubeconfig(t)

	_, remoteEnv, err := workspaceEnvCore()
	if err != nil {
		t.Fatalf("workspaceEnvCore: %v", err)
	}
	if hasEnv(remoteEnv, "KUBECONFIG") {
		t.Fatalf("REGRESSION: KUBECONFIG crossed the --on boundary (Sprint 13 Issue 1)")
	}
	for _, k := range []string{"IBMCLOUD_API_KEY", "IC_API_KEY", "IBMCLOUD_REGION"} {
		if !hasEnv(remoteEnv, k) {
			t.Errorf("remote env missing machine-portable %s", k)
		}
	}
	// Defense-in-depth: even a hand-built full env is scrubbed by the
	// dispatchRemote backstop.
	scrubbed := remoteSafeEnv([]string{"KUBECONFIG=/x", "IBMCLOUD_API_KEY=k"})
	if hasEnv(scrubbed, "KUBECONFIG") {
		t.Errorf("remoteSafeEnv backstop failed to strip KUBECONFIG")
	}

	_, localEnv, err := workspaceEnv()
	if err != nil {
		t.Fatalf("workspaceEnv: %v", err)
	}
	if !hasEnv(localEnv, "KUBECONFIG") {
		t.Errorf("local exec env must keep KUBECONFIG (local kubectl/oc unchanged)")
	}
}

// --- 2. part-B self-heal: heal-vs-outage matrix ------------------------

// TestE2E_SelfHeal_HealthyConfig_NoOp: a target that already has a
// usable kubeconfig is probed once and NOT healed (idempotent, zero
// extra round-trips on the healthy path).
func TestE2E_SelfHeal_HealthyConfig_NoOp(t *testing.T) {
	fastSelfHeal(t)
	r := &scriptedRunner{t: t, steps: []step{
		{wantHeal: false, code: 0, stdout: "rc=0\n"}, // probe: config present
	}}
	if err := maybeSelfHealRemoteKubeconfig(context.Background(), r,
		[]string{"kubectl", "get", "pods"}, "my-cluster", "apikey-xyz", "us-south", ""); err != nil {
		t.Fatalf("healthy config should be a no-op, got: %v", err)
	}
	if r.healSeen != 0 {
		t.Errorf("healthy config must NOT trigger a heal (heals=%d)", r.healSeen)
	}
}

// TestE2E_SelfHeal_MissingConfig_HealsAndSucceeds: no kubeconfig on the
// target → heal attempted; the ibmcloud command succeeds and the
// re-probe confirms a usable config. This is the user's 2026-05-18
// already-broken-jumphost case, now repaired with no terraform recreate.
func TestE2E_SelfHeal_MissingConfig_HealsAndSucceeds(t *testing.T) {
	fastSelfHeal(t)
	r := &scriptedRunner{t: t, steps: []step{
		{wantHeal: false, code: 0, stdout: "rc=1\n"}, // probe: no config
		{wantHeal: true, code: 0},                    // heal: ibmcloud ok
		{wantHeal: false, code: 0, stdout: "rc=0\n"}, // re-probe: now present
	}}
	if err := maybeSelfHealRemoteKubeconfig(context.Background(), r,
		[]string{"kubectl", "get", "pods"}, "my-cluster", "apikey-xyz", "us-south", ""); err != nil {
		t.Fatalf("missing config should self-heal, got: %v", err)
	}
	if r.healSeen != 1 {
		t.Errorf("expected exactly 1 heal attempt, got %d", r.healSeen)
	}
}

// TestE2E_SelfHeal_ClusterDown_SurfacesRealError is the critical
// discrimination: a genuinely-down cluster must NOT be masked as
// success and must NOT spin forever — the real `ibmcloud ks cluster
// config` error is surfaced after exactly selfHealMaxAttempts bounded
// retries.
func TestE2E_SelfHeal_ClusterDown_SurfacesRealError(t *testing.T) {
	fastSelfHeal(t)
	steps := []step{{wantHeal: false, code: 0, stdout: "rc=1\n"}} // probe: no config
	for i := 0; i < selfHealMaxAttempts; i++ {
		steps = append(steps, step{
			wantHeal: true, code: 1,
			stderr: "FAILED Get cluster: The cluster could not be found / is not Ready",
		})
	}
	r := &scriptedRunner{t: t, steps: steps}

	err := maybeSelfHealRemoteKubeconfig(context.Background(), r,
		[]string{"kubectl", "get", "pods"}, "down-cluster", "apikey-xyz", "us-south", "")
	if err == nil {
		t.Fatalf("cluster-down MUST surface an error, not mask as success")
	}
	if !strings.Contains(err.Error(), "genuinely") ||
		!strings.Contains(err.Error(), "not Ready") {
		t.Errorf("error must name the real ibmcloud failure (heal-vs-outage), got: %v", err)
	}
	if r.healSeen != selfHealMaxAttempts {
		t.Errorf("bounded retry: want %d heal attempts, got %d (no infinite spin)",
			selfHealMaxAttempts, r.healSeen)
	}
}

// TestE2E_SelfHeal_NonKubectl_NoOp: ibmcloud / arbitrary exec argv must
// NOT be probed or healed (no wasted round-trips, no masking unrelated
// failures).
func TestE2E_SelfHeal_NonKubectl_NoOp(t *testing.T) {
	fastSelfHeal(t)
	r := &scriptedRunner{t: t, steps: nil} // any call => t.Fatal
	for _, argv := range [][]string{
		{"ibmcloud", "ks", "cluster", "ls"},
		{"ls", "-la"},
		nil,
	} {
		if err := maybeSelfHealRemoteKubeconfig(context.Background(), r, argv, "c", "apikey-xyz", "us-south", ""); err != nil {
			t.Errorf("non-kubectl argv %v should be a no-op, got: %v", argv, err)
		}
	}
	if r.calls != 0 {
		t.Errorf("non-kubectl argv must not touch the remote (calls=%d)", r.calls)
	}
}

// TestE2E_SelfHeal_NoClusterID_ClearError: if the workspace can't yield
// a cluster id/name, the heal can't run — surface a clear actionable
// error rather than a confusing downstream ibmcloud message.
func TestE2E_SelfHeal_NoClusterID_ClearError(t *testing.T) {
	fastSelfHeal(t)
	r := &scriptedRunner{t: t, steps: []step{
		{wantHeal: false, code: 0, stdout: "rc=1\n"}, // probe: no config
	}}
	err := maybeSelfHealRemoteKubeconfig(context.Background(), r,
		[]string{"oc", "get", "nodes"}, "", "apikey-xyz", "us-south", "")
	if err == nil || !strings.Contains(err.Error(), "no cluster id") {
		t.Fatalf("empty cluster id must yield a clear error, got: %v", err)
	}
	if r.healSeen != 0 {
		t.Errorf("must not attempt heal with no cluster id (heals=%d)", r.healSeen)
	}
}

// TestE2E_SelfHeal_ProbeTransportError_Surfaces: a transport-level
// failure probing the target must surface, not silently proceed into a
// likely-broken kubectl run.
func TestE2E_SelfHeal_ProbeTransportError_Surfaces(t *testing.T) {
	fastSelfHeal(t)
	r := &scriptedRunner{t: t, steps: []step{
		{wantHeal: false, err: fmt.Errorf("ssh: connection reset")},
	}}
	err := maybeSelfHealRemoteKubeconfig(context.Background(), r,
		[]string{"kubectl", "version"}, "c", "apikey-xyz", "us-south", "")
	if err == nil || !strings.Contains(err.Error(), "probing remote kubeconfig") {
		t.Fatalf("probe transport error must surface, got: %v", err)
	}
}

// --- 3. Sprint 14 login-extension (the 2026-05-18 live finding) --------

// TestE2E_SelfHeal_NotLoggedIn_LoginThenConfig is the regression guard
// for the exact live failure: the jumphost's ubuntu ibmcloud profile was
// never authenticated (cloud-init's `su - ubuntu -c "ibmcloud login … ||
// true"` fork failed silently), so `ks cluster config --admin` returned
// `FAILED — Log in to the IBM Cloud CLI`. The heal MUST (re)authenticate
// the target itself, not assume a prior login — otherwise an
// already-broken jumphost can never be unblocked without a redeploy
// (the explicit Part B goal).
func TestE2E_SelfHeal_NotLoggedIn_LoginThenConfig(t *testing.T) {
	fastSelfHeal(t)
	r := &scriptedRunner{t: t, steps: []step{
		{wantHeal: false, code: 0, stdout: "rc=1\n"}, // probe: no config (not logged in)
		{wantHeal: true, code: 0},                    // heal: login && ks config ok
		{wantHeal: false, code: 0, stdout: "rc=0\n"}, // re-probe: now present
	}}
	if err := maybeSelfHealRemoteKubeconfig(context.Background(), r,
		[]string{"kubectl", "get", "pods"}, "d8586grr0uqkhn7bhkj0",
		"my-api-key", "ca-tor", "my-rg"); err != nil {
		t.Fatalf("not-logged-in target should self-heal via login+config, got: %v", err)
	}
	if r.healSeen != 1 {
		t.Fatalf("expected exactly 1 heal attempt, got %d", r.healSeen)
	}
	// The heal must actually perform the login (the whole point of the
	// 2026-05-18 fix) and then provision the kubeconfig, and must NOT
	// interpolate the API key into the script literal (it's a positional
	// param — injection-safe, key absent from the script text).
	if !strings.Contains(r.lastHeal, "ibmcloud login --apikey") {
		t.Errorf("heal must (re)authenticate the target: script=%q", r.lastHeal)
	}
	if !strings.Contains(r.lastHeal, "ks cluster config") {
		t.Errorf("heal must still provision the kubeconfig: script=%q", r.lastHeal)
	}
	if strings.Contains(r.lastHeal, "my-api-key") {
		t.Errorf("API key must NOT be interpolated into the script literal (positional only): script=%q", r.lastHeal)
	}
}

// TestE2E_SelfHeal_BadCredentials_SurfacedAsOutage: a bad/expired key (or
// region/RG/IAM mismatch) makes `ibmcloud login` fail every bounded
// attempt. This is NOT a healable missing-kubeconfig — it must be
// surfaced (real error, bounded, no spin, never masked), exactly like
// the cluster-down case.
func TestE2E_SelfHeal_BadCredentials_SurfacedAsOutage(t *testing.T) {
	fastSelfHeal(t)
	steps := []step{{wantHeal: false, code: 0, stdout: "rc=1\n"}}
	for i := 0; i < selfHealMaxAttempts; i++ {
		steps = append(steps, step{
			wantHeal: true, code: 1,
			stderr: "FAILED Log in to the IBM Cloud CLI by running 'ibmcloud login'",
		})
	}
	r := &scriptedRunner{t: t, steps: steps}
	err := maybeSelfHealRemoteKubeconfig(context.Background(), r,
		[]string{"kubectl", "get", "pods"}, "c", "bad-key", "us-south", "")
	if err == nil {
		t.Fatalf("bad credentials MUST surface an error, not mask as success")
	}
	if !strings.Contains(err.Error(), "Last error:") ||
		!strings.Contains(err.Error(), "ibmcloud login") {
		t.Errorf("error must surface the real ibmcloud failure, got: %v", err)
	}
	if r.healSeen != selfHealMaxAttempts {
		t.Errorf("bounded retry: want %d attempts, got %d (no infinite spin)",
			selfHealMaxAttempts, r.healSeen)
	}
}
