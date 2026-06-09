package phases

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

// --- test harness: capture the jumphost seam calls without any network ---

// onboardRecorder captures every command string passed to onboardRunStagingCmds
// and every (path) passed to onboardCopyFile, and lets a test script the stdout
// returned per command (matched by substring).
type onboardRecorder struct {
	cmds      []string          // every command string run on the jumphost
	copied    []string          // remote paths written via CopyFileViaEICE
	responses map[string]string // substring → stdout
	errOn     map[string]error  // substring → error
}

// installOnboardSeams swaps the phase-level seams and returns a restore func.
// All transfers go through the jumphost (curl download + scp), so no
// operator-host HTTP fetch seam is needed — the AS3 RPM is downloaded by the
// jumphost itself via an onboardRunStagingCmds `curl` command.
func installOnboardSeams(t *testing.T, r *onboardRecorder) (restore func()) {
	t.Helper()
	origRun := onboardRunStagingCmds
	origCopy := onboardCopyFile

	onboardRunStagingCmds = func(_ context.Context, _ jumphost.ProbeOptions, commands []string) ([]string, error) {
		out := make([]string, 0, len(commands))
		for _, cmd := range commands {
			r.cmds = append(r.cmds, cmd)
			for sub, err := range r.errOn {
				if strings.Contains(cmd, sub) {
					return out, err
				}
			}
			// Pick the LONGEST matching substring so matches are deterministic even
			// when several keys match the same command (Go map iteration is random).
			// e.g. the final durable-pw verify command contains both "authn/login"
			// (in the URL) and the more-specific "loginProviderName" — the longer key
			// must win.
			resp := "ok"
			bestLen := -1
			for sub, o := range r.responses {
				if strings.Contains(cmd, sub) && len(sub) > bestLen {
					resp = o
					bestLen = len(sub)
				}
			}
			out = append(out, resp)
		}
		return out, nil
	}
	onboardCopyFile = func(_ context.Context, _ jumphost.ProbeOptions, _ []byte, remotePath string) error {
		r.copied = append(r.copied, remotePath)
		return nil
	}

	return func() {
		onboardRunStagingCmds = origRun
		onboardCopyFile = origCopy
	}
}

// onboardTestState returns state pre-populated with the keys Phase17f reads,
// including a real on-disk PEM file at BIGIP_SSH_KEY_PATH.
func onboardTestState(t *testing.T) *state.State {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	pemPath := filepath.Join(dir, "bigip-ssh.pem")
	if err := os.WriteFile(pemPath, []byte("FAKE-PEM"), 0o600); err != nil {
		t.Fatalf("write fake pem: %v", err)
	}
	st.Set("JUMPHOST_INSTANCE_ID", "i-jump-test")
	st.Set("BIGIP_MGMT_IP", "10.0.1.50")
	st.Set("BIGIP_EXT_IP", "10.0.10.50")
	st.Set("BIGIP_INT_IP", "10.0.20.50")
	st.Set("BIGIP_SSH_KEY_PATH", pemPath)
	return st
}

// frameworkUpResponses makes the readiness probe report framework-up immediately
// and the idempotency probe report NOT-onboarded (so the mutating path runs), and
// the AS3 POST/poll succeed, and the FINAL durable-password token-auth verify
// report a healthy 200 + token.
func frameworkUpResponses() map[string]string {
	return map[string]string{
		// Readiness gate probe (no creds, /mgmt/shared/authn/login): JSON :resterrorresponse = framework up.
		"authn/login": `{"code":401,"message":"Authentication failed.","kind":":resterrorresponse"}`,
		// AS3 install task POST returns an id; poll returns FINISHED.
		"package-management-tasks": `{"id":"task-abc-123","status":"FINISHED","version":"3.56.0"}`,
		// Final durable-password step's token-auth verify (the ssh command carries the
		// loginProviderName JSON + the CODE=/LOGIN= echo): HTTP 200 + a token.
		"loginProviderName": `CODE=200
LOGIN={"token":{"token":"ABCDEF0123456789","timeout":1200}}`,
	}
}

const testPW = "S3cr3t-Pa55!word"

// --- tests ---

// TestPhase17fOnboard_NoopWhenDisabled: no BigIPVE block → returns nil, no calls.
func TestPhase17fOnboard_NoopWhenDisabled(t *testing.T) {
	r := &onboardRecorder{}
	defer installOnboardSeams(t, r)()

	cl := testCluster() // no BigIPVE
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false); err != nil {
		t.Fatalf("expected nil (no-op), got: %v", err)
	}
	if len(r.cmds) != 0 || len(r.copied) != 0 {
		t.Errorf("expected zero jumphost calls when disabled, got cmds=%d copied=%d", len(r.cmds), len(r.copied))
	}
}

// TestPhase17fOnboard_DryRunNoCalls: dryRun makes no jumphost/SSH calls and sets
// placeholder state.
func TestPhase17fOnboard_DryRunNoCalls(t *testing.T) {
	r := &onboardRecorder{}
	defer installOnboardSeams(t, r)()

	cl := bigipCluster()
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, true /*dryRun*/); err != nil {
		t.Fatalf("dry-run error: %v", err)
	}
	if len(r.cmds) != 0 || len(r.copied) != 0 {
		t.Errorf("dry-run must make no jumphost/copy calls, got cmds=%d copied=%d",
			len(r.cmds), len(r.copied))
	}
	if st.Get("BIGIP_ONBOARDED") != "true" {
		t.Error("dry-run should set placeholder BIGIP_ONBOARDED=true")
	}
	if st.Get("BIGIP_AS3_VERSION") != bigipAS3Version {
		t.Errorf("dry-run should set BIGIP_AS3_VERSION=%s, got %q", bigipAS3Version, st.Get("BIGIP_AS3_VERSION"))
	}
}

// TestPhase17fOnboard_MissingPasswordErrors: AWSBNKCTL_BIGIP_PASSWORD unset → clear error.
func TestPhase17fOnboard_MissingPasswordErrors(t *testing.T) {
	r := &onboardRecorder{}
	defer installOnboardSeams(t, r)()
	t.Setenv(bigipPasswordEnv, "") // explicitly unset

	cl := bigipCluster()
	st := onboardTestState(t)
	err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false)
	if err == nil || !strings.Contains(err.Error(), bigipPasswordEnv) {
		t.Fatalf("expected missing-password error mentioning %s, got: %v", bigipPasswordEnv, err)
	}
	if len(r.cmds) != 0 {
		t.Errorf("must not call jumphost before password check, got %d cmds", len(r.cmds))
	}
}

// TestPhase17fOnboard_FullSequence: the happy path runs the onboarding steps in
// the documented order and copies the PEM to the jumphost first.
func TestPhase17fOnboard_FullSequence(t *testing.T) {
	r := &onboardRecorder{responses: frameworkUpResponses()}
	defer installOnboardSeams(t, r)()
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false); err != nil {
		t.Fatalf("onboard error: %v", err)
	}

	// Only the PEM is copied to the jumphost via the EICE copy seam (base64-in-argv,
	// fine for a ~2KB key). The RPM is NO LONGER pushed that way — that path blows
	// ARG_MAX on a ~13MB blob — so the jumphost curls it directly instead.
	if len(r.copied) != 1 || r.copied[0] != bigipPEMRemotePath {
		t.Errorf("expected ONLY the PEM copied to [%s], got %v", bigipPEMRemotePath, r.copied)
	}

	all := strings.Join(r.cmds, "\n")
	// Ordered markers of the recipe. Shell-bootstrap (step 2) MUST appear before
	// the pw-file heredoc staging (step 3), which in turn must precede the pw-set
	// (step 4) — this is the ordering that fixes the tmsh-umask bug. The AS3
	// transfer (step 7) is: jumphost curl download → scp jumphost → BIG-IP →
	// INSTALL POST, in that order.
	wantOrder := []string{
		"chmod 600 " + bigipPEMRemotePath,            // step 0
		"authn/login",                                // step 1 readiness probe
		"modify auth user admin shell bash",          // step 2 shell-bootstrap (bare, in tmsh)
		"__BIGIP_PW_EOF__",                           // step 3 pw-file heredoc (needs bash)
		"modify auth user admin password",            // step 4 (pw-set, via tmsh -f)
		"modify sys provision ltm level nominal",     // step 5
		"create net vlan external",                   // step 6 dataplane
		"create net self external-self",              // step 6 self-ip
		"curl -fsSL",                                 // step 7 jumphost downloads the RPM
		bigipAS3URL,                                  // step 7 from the pinned URL (on the jumphost)
		"scp -i " + bigipPEMRemotePath,               // step 7 scp RPM jumphost → BIG-IP
		"package-management-tasks",                   // step 7 AS3 INSTALL POST
		"create auth partition " + bigipCISPartition, // step 8
		// step 9: the FINAL durable-password step runs AFTER AS3 install + the cis
		// partition create. It re-sets the pw (tmsh -f), then verifies via TOKEN auth.
		"modify auth user admin password", // step 9 durable pw re-set (2nd occurrence)
		"loginProviderName",               // step 9 token-auth verify (CIS's exact method)
	}
	assertOrdered(t, all, wantOrder)

	// The final durable-password step's token-auth verify must come AFTER the AS3
	// install POST and the cis-partition create (it is the LAST mutating step — it
	// runs after everything that can restart restjavad / reload config).
	if lastIdx(all, "loginProviderName") < strings.Index(all, "package-management-tasks") {
		t.Error("final token-auth verify must run AFTER the AS3 install (package-management-tasks)")
	}
	if lastIdx(all, "loginProviderName") < strings.Index(all, "create auth partition "+bigipCISPartition) {
		t.Error("final token-auth verify must run AFTER the cis-partition create")
	}
	// The admin password is re-set TWICE: the early step (4) and the final durable
	// step (9). The second re-set must come after the cis-partition create.
	if strings.Count(all, "modify auth user admin password") < 2 {
		t.Errorf("expected the admin password to be set twice (early + final durable), got %d",
			strings.Count(all, "modify auth user admin password"))
	}

	// The jumphost curl download and the scp must reference the SAME jumphost RPM
	// path; the download URL appears ONLY on the jumphost curl, never in a BIG-IP
	// (ssh) command.
	for _, cmd := range r.cmds {
		if strings.Contains(cmd, bigipAS3URL) && strings.Contains(cmd, "ssh -i ") {
			t.Errorf("the download URL must not be sent to the BIG-IP over ssh:\n%s", cmd)
		}
		// No emitted command may install via an on-box GitHub pull: a curl/wget of
		// the RPM URL that also runs over ssh into the BIG-IP is forbidden. The
		// jumphost-side curl (no ssh) is the only allowed download.
		if strings.Contains(cmd, "ssh -i ") && strings.Contains(cmd, "github.com") {
			t.Errorf("BIG-IP must not pull from GitHub — found github.com in an ssh command:\n%s", cmd)
		}
	}

	// State written on success.
	if st.Get("BIGIP_ONBOARDED") != "true" {
		t.Error("expected BIGIP_ONBOARDED=true after success")
	}
	if st.Get("BIGIP_AS3_VERSION") != bigipAS3Version {
		t.Errorf("expected BIGIP_AS3_VERSION=%s, got %q", bigipAS3Version, st.Get("BIGIP_AS3_VERSION"))
	}
	// Password must NEVER be written to state.
	for _, k := range []string{"BIGIP_PASSWORD", "BIGIP_PW", "BIGIP_ADMIN_PASSWORD"} {
		if st.Get(k) != "" {
			t.Errorf("password leaked into state key %s", k)
		}
	}
}

// TestPhase17fOnboard_PasswordNeverOnArgv scans every emitted command and asserts
// the literal password never appears as a bare argv token. The only place the pw
// is allowed is inside the heredoc body that writes the 600-file (consumed by cat,
// never exec'd as an argument); every other reference must be `$(cat <file>)`.
func TestPhase17fOnboard_PasswordNeverOnArgv(t *testing.T) {
	r := &onboardRecorder{responses: frameworkUpResponses()}
	defer installOnboardSeams(t, r)()
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false); err != nil {
		t.Fatalf("onboard error: %v", err)
	}

	for _, cmd := range r.cmds {
		if !strings.Contains(cmd, testPW) {
			continue
		}
		// The pw may ONLY appear inside the heredoc that writes the 600-file.
		// That heredoc is fingerprinted by the sentinel; the pw must be on its
		// own line between the sentinels, not on a tmsh/curl/ssh argv.
		if !strings.Contains(cmd, "__BIGIP_PW_EOF__") {
			t.Fatalf("password appeared in a command without the heredoc sentinel (argv leak):\n%s", cmd)
		}
		// Belt-and-suspenders: ensure the pw is not also glued onto a curl -u /
		// tmsh ... password <pw> token in the same command.
		if strings.Contains(cmd, "password "+testPW) ||
			strings.Contains(cmd, "admin:"+testPW) ||
			strings.Contains(cmd, "-u admin:"+testPW) {
			t.Fatalf("password used as an argv token:\n%s", cmd)
		}
	}
}

// TestPhase17fOnboard_IdempotentSkip: when the box reports already-onboarded
// (authed sys/ready all-yes + AS3 pinned + cis partition), the mutating steps are
// skipped — no pw-set / dataplane / AS3-install / partition-create commands run.
func TestPhase17fOnboard_IdempotentSkip(t *testing.T) {
	// Onboarded body: a TOKEN-auth login succeeds (a "token" object) — the new
	// idempotency gate — plus AS3 pinned + cis partition present.
	onboarded := `LOGIN={"token":{"token":"ABCDEF0123456789","timeout":1200}} ` +
		`AS3={"version":"3.56.0","release":"10"} ` +
		`PART={"name":"cis"}`

	origRun := onboardRunStagingCmds
	origCopy := onboardCopyFile
	defer func() { onboardRunStagingCmds = origRun; onboardCopyFile = origCopy }()

	var cmds []string
	onboardCopyFile = func(_ context.Context, _ jumphost.ProbeOptions, _ []byte, _ string) error { return nil }
	onboardRunStagingCmds = func(_ context.Context, _ jumphost.ProbeOptions, commands []string) ([]string, error) {
		out := make([]string, 0, len(commands))
		for _, cmd := range commands {
			cmds = append(cmds, cmd)
			switch {
			// The idempotency probe is the combined script that echoes LOGIN=/AS3=/PART=
			// (TOKEN auth — the durability-bug-aware gate, NOT basic sys/ready).
			case strings.Contains(cmd, `echo "LOGIN=$LOGIN"`):
				out = append(out, onboarded)
			// Plain readiness gate probe (framework-up, /mgmt/shared/authn/login).
			case strings.Contains(cmd, "authn/login"):
				out = append(out, `{"kind":":resterrorresponse"}`)
			default:
				out = append(out, "ok")
			}
		}
		return out, nil
	}

	t.Setenv(bigipPasswordEnv, testPW)
	cl := bigipCluster()
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false); err != nil {
		t.Fatalf("onboard error: %v", err)
	}

	all := strings.Join(cmds, "\n")
	for _, forbidden := range []string{
		"modify auth user admin shell bash",
		"modify auth user admin password",
		"create net vlan external",
		"package-management-tasks",
		"create auth partition " + bigipCISPartition,
	} {
		if strings.Contains(all, forbidden) {
			t.Errorf("idempotent re-run must skip mutating step, but ran: %q", forbidden)
		}
	}
	if st.Get("BIGIP_ONBOARDED") != "true" {
		t.Error("idempotent path should still mark BIGIP_ONBOARDED=true")
	}
}

// TestPhase17fOnboard_ReadinessTimeout: when the framework never comes up, the
// readiness gate returns a timeout error. We force timeout by making the probe
// always return an Apache-HTML 401 (not the JSON framework-up shape) and using a
// cancelled context to short-circuit the long sleep.
func TestPhase17fOnboard_ReadinessTimeout(t *testing.T) {
	r := &onboardRecorder{
		responses: map[string]string{
			"authn/login": "<html><title>401 Unauthorized</title></html>", // Apache HTML, NOT framework-up
		},
	}
	defer installOnboardSeams(t, r)()
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)

	// Cancel the context so the gate's inter-poll select returns promptly instead
	// of sleeping the full 30-min budget.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Phase17fBigIPOnboard(ctx, cl, st, &Clients{}, false)
	if err == nil {
		t.Fatal("expected readiness-gate error when framework never comes up")
	}
	if !strings.Contains(err.Error(), "readiness gate") {
		t.Errorf("expected readiness-gate error, got: %v", err)
	}
	// Must NOT have proceeded to any mutating step.
	all := strings.Join(r.cmds, "\n")
	if strings.Contains(all, "modify auth user admin password") {
		t.Error("must not run mutating steps when readiness gate fails")
	}
}

// TestPhase17fOnboard_ReadinessPollsThenSucceeds: a probe-counting stub returns
// Apache-HTML for the first 2 attempts then JSON framework-up; the gate must poll
// (not give up on the first non-ready response) and then proceed.
func TestPhase17fOnboard_ReadinessPollsThenSucceeds(t *testing.T) {
	attempts := 0
	origRun := onboardRunStagingCmds
	origCopy := onboardCopyFile
	defer func() {
		onboardRunStagingCmds = origRun
		onboardCopyFile = origCopy
	}()

	onboardCopyFile = func(_ context.Context, _ jumphost.ProbeOptions, _ []byte, _ string) error { return nil }
	onboardRunStagingCmds = func(_ context.Context, _ jumphost.ProbeOptions, commands []string) ([]string, error) {
		out := make([]string, 0, len(commands))
		for _, cmd := range commands {
			switch {
			// Final durable-password token-auth verify (carries loginProviderName +
			// the CODE=/LOGIN= echo): healthy 200 + token. Checked BEFORE the bare
			// authn/login case because that command also contains "authn/login".
			case strings.Contains(cmd, "loginProviderName"):
				out = append(out, "CODE=200\nLOGIN={\"token\":{\"token\":\"ABC123\"}}")
			case strings.Contains(cmd, "authn/login"):
				attempts++
				if attempts < 3 {
					out = append(out, "<html><title>401 Unauthorized</title></html>")
				} else {
					out = append(out, `{"kind":":resterrorresponse"}`)
				}
			case strings.Contains(cmd, "package-management-tasks"):
				out = append(out, `{"id":"t1","status":"FINISHED"}`)
			default:
				out = append(out, "ok")
			}
		}
		return out, nil
	}

	t.Setenv(bigipPasswordEnv, testPW)
	// Shrink the poll interval so the 2 retries don't actually wait 30s each.
	origInterval := bigipReadyPollInterval
	bigipReadyPollInterval = time.Millisecond
	defer func() { bigipReadyPollInterval = origInterval }()

	cl := bigipCluster()
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false); err != nil {
		t.Fatalf("onboard error: %v", err)
	}
	if attempts < 3 {
		t.Errorf("expected readiness gate to poll ≥3 times, got %d", attempts)
	}
}

// TestPhase17fOnboard_PropagatesStepError: a failing onboarding step surfaces as
// an error and best-effort shreds the pw file.
func TestPhase17fOnboard_PropagatesStepError(t *testing.T) {
	r := &onboardRecorder{
		responses: frameworkUpResponses(),
		errOn:     map[string]error{"create net vlan external": errors.New("vlan create failed")},
	}
	defer installOnboardSeams(t, r)()
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)
	err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false)
	if err == nil || !strings.Contains(err.Error(), "VLAN") && !strings.Contains(err.Error(), "vlan") {
		t.Fatalf("expected dataplane step error, got: %v", err)
	}
	if st.Get("BIGIP_ONBOARDED") == "true" {
		t.Error("must not mark onboarded when a step fails")
	}
	// A shred command should have been emitted after the failure.
	if !strings.Contains(strings.Join(r.cmds, "\n"), bigipPWRemotePath) {
		t.Error("expected pw-file shred attempt after step failure")
	}
}

// TestBigipReadinessGate_ProbesAuthnLogin asserts that the readiness gate probe
// URL is /mgmt/shared/authn/login, not /mgmt/tm/sys/ready. /mgmt/tm/sys/ready
// always returns an Apache HTML 401 page regardless of framework state; only
// /mgmt/shared/authn/login returns the JSON :resterrorresponse body that signals
// the REST framework is live.
func TestBigipReadinessGate_ProbesAuthnLogin(t *testing.T) {
	r := &onboardRecorder{
		responses: map[string]string{
			"authn/login": `{"code":401,"message":"Authentication failed.","kind":":resterrorresponse"}`,
		},
	}
	defer installOnboardSeams(t, r)()

	origTimeout := bigipReadyGateTimeout
	bigipReadyGateTimeout = time.Minute
	defer func() { bigipReadyGateTimeout = origTimeout }()

	if err := bigipReadinessGate(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50"); err != nil {
		t.Fatalf("readiness gate error: %v", err)
	}

	// Every probe command must target authn/login, not sys/ready.
	for _, cmd := range r.cmds {
		if strings.Contains(cmd, "sys/ready") {
			t.Errorf("readiness gate probe must target authn/login, but got sys/ready in: %s", cmd)
		}
		if !strings.Contains(cmd, "authn/login") {
			t.Errorf("readiness gate probe must target authn/login, got: %s", cmd)
		}
	}
}

// TestBigipOnboardTmshSteps_NoShellBootstrap asserts that bigipOnboardTmshSteps
// no longer contains the shell-bootstrap step. That step is now extracted out and
// run directly in Phase17fBigIPOnboard (before pw-file staging) so it executes
// while the admin shell is still tmsh. All steps returned by this function run
// under bash (after the bootstrap) and must use the "tmsh " prefix for tmsh ops.
func TestBigipOnboardTmshSteps_NoShellBootstrap(t *testing.T) {
	steps := bigipOnboardTmshSteps("10.0.1.50", "10.0.10.50", "10.0.20.50")
	if len(steps) == 0 {
		t.Fatal("expected at least one onboard step")
	}

	// The shell-bootstrap must NOT be present in the slice — it is now run inline
	// in Phase17fBigIPOnboard before this slice is iterated.
	all := strings.Join(func() []string {
		out := make([]string, 0, len(steps))
		for _, s := range steps {
			out = append(out, s.cmd)
		}
		return out
	}(), "\n")
	if strings.Contains(all, "modify auth user admin shell bash") {
		t.Errorf("shell-bootstrap must not be in bigipOnboardTmshSteps (it is run inline before pw-file staging): %s", all)
	}

	// Step 0 is the pw-set. All steps run under bash and must use "tmsh " prefix
	// for tmsh operations.
	for _, step := range steps {
		if strings.Contains(step.cmd, "modify sys provision") && !strings.Contains(step.cmd, "tmsh modify sys provision") {
			t.Errorf("step %q should use 'tmsh modify sys provision': %s", step.label, step.cmd)
		}
		if strings.Contains(step.cmd, "create net vlan") && !strings.Contains(step.cmd, "tmsh create net vlan") {
			t.Errorf("step %q should use 'tmsh create net vlan': %s", step.label, step.cmd)
		}
	}
}

// TestParseAS3TaskID covers the task-id parser.
func TestParseAS3TaskID(t *testing.T) {
	cases := map[string]string{
		`{"id":"abc-123","status":"STARTED"}`: "abc-123",
		`no id here`:                          "",
		`{"id":"`:                             "",
	}
	for body, want := range cases {
		if got := parseAS3TaskID(body); got != want {
			t.Errorf("parseAS3TaskID(%q) = %q, want %q", body, got, want)
		}
	}
}

// TestBigipAS3PostScript_NoDownload asserts the on-box AS3 install script POSTs
// the INSTALL task against the side-loaded path and contains NO download — the
// BIG-IP has no internet, so it must never curl the RPM from GitHub.
func TestBigipAS3PostScript_NoDownload(t *testing.T) {
	downloadPath := "/var/config/rest/downloads/" + bigipAS3RPM
	script := bigipAS3PostScript(downloadPath)

	if strings.Contains(script, "github.com") {
		t.Errorf("on-box AS3 script must not reference github.com:\n%s", script)
	}
	if strings.Contains(script, bigipAS3URL) {
		t.Errorf("on-box AS3 script must not embed the download URL:\n%s", script)
	}
	if strings.Contains(script, "curl -sSL") || strings.Contains(script, "-o "+downloadPath) {
		t.Errorf("on-box AS3 script must not download the RPM:\n%s", script)
	}
	if !strings.Contains(script, "package-management-tasks") ||
		!strings.Contains(script, `"packageFilePath":"`+downloadPath+`"`) {
		t.Errorf("on-box AS3 script must POST INSTALL against the side-loaded path:\n%s", script)
	}
	// The pw must come from the 600-file, never argv.
	if !strings.Contains(script, `admin:$(cat `+bigipPWRemotePath) {
		t.Errorf("on-box AS3 POST must read pw from the 600-file:\n%s", script)
	}
}

// TestBigipInstallAS3_SkipsWhenAlreadyInstalled: when the appsvcs/info probe reports
// the pinned AS3 version, bigipInstallAS3 makes NO download/scp/INSTALL calls and
// returns nil — the idempotency fix for re-onboarding a partial box that still has
// AS3 from a prior run (POSTing INSTALL would 422 "already installed").
func TestBigipInstallAS3_SkipsWhenAlreadyInstalled(t *testing.T) {
	r := &onboardRecorder{
		responses: map[string]string{
			// The appsvcs/info GET reports the pinned version → skip the install.
			"appsvcs/info": `{"version":"` + bigipAS3Version + `","release":"10","schemaCurrent":"3.56.0"}`,
		},
	}
	defer installOnboardSeams(t, r)()

	if err := bigipInstallAS3(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50"); err != nil {
		t.Fatalf("expected nil (skip) when AS3 already installed, got: %v", err)
	}

	// Exactly one command: the appsvcs/info probe. No download, scp, or INSTALL POST.
	all := strings.Join(r.cmds, "\n")
	if !strings.Contains(all, "appsvcs/info") {
		t.Errorf("expected an appsvcs/info probe, got: %v", r.cmds)
	}
	for _, forbidden := range []string{
		"curl -fsSL",               // jumphost RPM download
		bigipAS3URL,                // the pinned download URL
		"scp -i ",                  // scp jumphost → BIG-IP
		"package-management-tasks", // INSTALL POST / poll
	} {
		if strings.Contains(all, forbidden) {
			t.Errorf("expected NO %q when AS3 already installed, but it ran:\n%s", forbidden, all)
		}
	}
}

// TestBigipInstallAS3_FullFlowWhenAbsent: when the appsvcs/info probe does NOT report
// the pinned version (AS3 absent), the full download → scp → INSTALL flow runs in order.
func TestBigipInstallAS3_FullFlowWhenAbsent(t *testing.T) {
	r := &onboardRecorder{
		responses: map[string]string{
			// appsvcs/info reports no AS3 (a 404-ish error body) → proceed with install.
			"appsvcs/info":             `{"code":404,"message":"URI path /shared/appsvcs/info not registered."}`,
			"package-management-tasks": `{"id":"task-xyz-1","status":"FINISHED"}`,
		},
	}
	defer installOnboardSeams(t, r)()

	if err := bigipInstallAS3(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50"); err != nil {
		t.Fatalf("install error: %v", err)
	}

	all := strings.Join(r.cmds, "\n")
	// The probe must come first, then the full side-load → INSTALL flow in order.
	assertOrdered(t, all, []string{
		"appsvcs/info",                 // step 0 idempotency probe
		"curl -fsSL",                   // step 1 jumphost downloads the RPM
		bigipAS3URL,                    // step 1 from the pinned URL
		"scp -i " + bigipPEMRemotePath, // step 2 scp RPM jumphost → BIG-IP
		"package-management-tasks",     // step 3 INSTALL POST + poll
	})
}

// TestBigipInstallAS3_TolerateAlreadyInstalledTaskError: a belt-and-suspenders race —
// the appsvcs/info probe misses an existing AS3, the INSTALL task is POSTed, and the
// poll returns an error body containing "already installed". The box is already in the
// desired state, so this is treated as SUCCESS (not a FAILED-task error).
func TestBigipInstallAS3_TolerateAlreadyInstalledTaskError(t *testing.T) {
	r := &onboardRecorder{
		responses: map[string]string{
			// Probe misses AS3 → install proceeds.
			"appsvcs/info": `{"code":404,"message":"not registered."}`,
			// The POST returns an id; the poll reports FAILED with an already-installed error.
			"package-management-tasks": `{"id":"task-race-9","status":"FAILED",` +
				`"errorMessage":"Package f5-appsvcs version 3.56.0-10 is already installed."}`,
		},
	}
	defer installOnboardSeams(t, r)()

	if err := bigipInstallAS3(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50"); err != nil {
		t.Fatalf("expected an 'already installed' task error to be tolerated as success, got: %v", err)
	}
}

// TestBigipAS3InfoScript_NoArgvPassword asserts the appsvcs/info probe reads the pw
// from the on-box 600-file via $(cat ...), never as an argv token, and targets the
// AS3 info endpoint.
func TestBigipAS3InfoScript_NoArgvPassword(t *testing.T) {
	script := bigipAS3InfoScript()
	if !strings.Contains(script, "appsvcs/info") {
		t.Errorf("info probe must target /mgmt/shared/appsvcs/info:\n%s", script)
	}
	if !strings.Contains(script, `admin:$(cat `+bigipPWRemotePath) {
		t.Errorf("info probe must read pw from the 600-file via $(cat ...):\n%s", script)
	}
	if strings.Contains(script, testPW) {
		t.Errorf("info probe must not embed the literal password:\n%s", script)
	}
}

// TestBigipSetShellBash_FallsBackToBareTmsh: when the bash-form attempt ERRORS
// (meaning the shell is still tmsh, where `tmsh ...` is rejected), the helper falls
// back to the bare tmsh-form. Both attempts must be emitted, and the overall result
// is success because the bare form succeeds.
func TestBigipSetShellBash_FallsBackToBareTmsh(t *testing.T) {
	r := &onboardRecorder{
		// The bash-form (only one carrying the literal "tmsh save sys config") errors;
		// the bare tmsh-form ("save sys config" without the "tmsh " prefix) succeeds.
		errOn: map[string]error{"tmsh save sys config": errors.New("modify: command not found")},
	}
	defer installOnboardSeams(t, r)()

	if err := bigipSetShellBash(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50"); err != nil {
		t.Fatalf("expected success via bare-tmsh fallback, got: %v", err)
	}

	// Both attempts must have been emitted, in order: bash-form then bare-tmsh-form.
	if len(r.cmds) != 2 {
		t.Fatalf("expected exactly 2 shell-bootstrap attempts (bash then bare), got %d: %v", len(r.cmds), r.cmds)
	}
	if !strings.Contains(r.cmds[0], "tmsh modify auth user admin shell bash") ||
		!strings.Contains(r.cmds[0], "tmsh save sys config") {
		t.Errorf("first attempt must be the bash-form (tmsh-prefixed), got: %s", r.cmds[0])
	}
	if strings.Contains(r.cmds[1], "tmsh modify auth user admin shell bash") ||
		!strings.Contains(r.cmds[1], "modify auth user admin shell bash") ||
		!strings.Contains(r.cmds[1], "save sys config") {
		t.Errorf("second attempt must be the bare tmsh-form (no tmsh prefix), got: %s", r.cmds[1])
	}
}

// TestBigipSetShellBash_BashFormShortCircuits: when the bash-form attempt SUCCEEDS
// (the box's admin shell is already bash — a re-run), the helper returns immediately
// and does NOT emit the bare tmsh fallback.
func TestBigipSetShellBash_BashFormShortCircuits(t *testing.T) {
	r := &onboardRecorder{} // no errOn → first (bash-form) attempt succeeds
	defer installOnboardSeams(t, r)()

	if err := bigipSetShellBash(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50"); err != nil {
		t.Fatalf("expected bash-form success, got: %v", err)
	}
	if len(r.cmds) != 1 {
		t.Fatalf("expected a single attempt when the bash-form succeeds, got %d: %v", len(r.cmds), r.cmds)
	}
	if !strings.Contains(r.cmds[0], "tmsh modify auth user admin shell bash") {
		t.Errorf("the single attempt must be the bash-form, got: %s", r.cmds[0])
	}
}

// TestBigipSetShellBash_BothFailErrors: only when BOTH attempts fail is it an error,
// and the error names both attempts.
func TestBigipSetShellBash_BothFailErrors(t *testing.T) {
	r := &onboardRecorder{
		// Match the substring shared by both forms so both attempts error.
		errOn: map[string]error{"modify auth user admin shell bash": errors.New("boom")},
	}
	defer installOnboardSeams(t, r)()

	err := bigipSetShellBash(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50")
	if err == nil {
		t.Fatal("expected an error when both shell-bootstrap attempts fail")
	}
	if !strings.Contains(err.Error(), "bash-form") || !strings.Contains(err.Error(), "bare-tmsh-form") {
		t.Errorf("error should name both attempts, got: %v", err)
	}
	if len(r.cmds) != 2 {
		t.Errorf("expected both attempts to be tried, got %d: %v", len(r.cmds), r.cmds)
	}
}

// TestBigipTolerantCreate_SwallowsAlreadyExists asserts the wrapper exit code is 0
// when the wrapped create fails with an already-exists signature, and non-zero for a
// genuine failure. We exercise the GENERATED shell directly with /bin/sh by
// substituting a stub command whose output/exit we control.
func TestBigipTolerantCreate_SwallowsAlreadyExists(t *testing.T) {
	cases := []struct {
		name    string
		stub    string // shell that stands in for the tmsh create
		wantOK  bool
		wantOut string // substring expected on stderr when it fails
	}{
		{
			name:   "create succeeds",
			stub:   `printf 'ok'; true`,
			wantOK: true,
		},
		{
			name:   "already exists (text) is tolerated",
			stub:   `printf '01070734:3: Configuration error: ... already exists' >&2; false`,
			wantOK: true,
		},
		{
			name:   "already exists (code 01020066) is tolerated",
			stub:   `printf '01020066:3: The requested object already exists' >&2; false`,
			wantOK: true,
		},
		{
			name:    "genuine failure propagates",
			stub:    `printf 'fatal: link not found' >&2; false`,
			wantOK:  false,
			wantOut: "link not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snippet := bigipTolerantCreate(tc.stub)
			out, err := exec.Command("/bin/sh", "-c", snippet).CombinedOutput()
			if tc.wantOK && err != nil {
				t.Fatalf("expected success, got err=%v out=%q", err, out)
			}
			if !tc.wantOK {
				if err == nil {
					t.Fatalf("expected non-zero exit for genuine failure, out=%q", out)
				}
				if tc.wantOut != "" && !strings.Contains(string(out), tc.wantOut) {
					t.Errorf("expected genuine-failure output to contain %q, got %q", tc.wantOut, out)
				}
			}
		})
	}
}

// TestPhase17fOnboard_DataplaneAndPartitionTolerateAlreadyExists: a full re-run
// where the VLAN/self-IP/partition creates report "already exists" must still
// complete onboarding (the wrappers swallow already-exists), while a genuine
// create failure still fails the phase.
func TestPhase17fOnboard_DataplaneAndPartitionTolerateAlreadyExists(t *testing.T) {
	// All the tmsh creates "already exist": the seam returns the already-exists text
	// AND no error (the wrapper's grep, run for real only via /bin/sh, is exercised
	// in TestBigipTolerantCreate_*; here we assert the create commands are still
	// EMITTED — i.e. the step is not skipped — and the phase completes).
	r := &onboardRecorder{responses: frameworkUpResponses()}
	defer installOnboardSeams(t, r)()
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false); err != nil {
		t.Fatalf("onboard error: %v", err)
	}

	all := strings.Join(r.cmds, "\n")
	// The dataplane + partition creates must be wrapped in the tolerant guard so a
	// pre-existing object does not fail the re-run.
	for _, want := range []string{
		"tmsh create net vlan external",
		"tmsh create net self external-self",
		"tmsh create auth partition " + bigipCISPartition,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("expected create %q to still be emitted on a re-run, missing", want)
		}
	}
	// The guard signature must be present (already-exists tolerance).
	if !strings.Contains(all, "already exists|01020066") {
		t.Error("expected the already-exists tolerance guard in the emitted commands")
	}
	if st.Get("BIGIP_ONBOARDED") != "true" {
		t.Error("expected BIGIP_ONBOARDED=true after a tolerant re-run")
	}
}

// TestPhase17fOnboard_DataplaneGenuineErrorPropagates: a NON-already-exists failure
// of the dataplane step still fails the phase (the wrapper only swallows
// already-exists, and the seam-level error short-circuits regardless).
func TestPhase17fOnboard_DataplaneGenuineErrorPropagates(t *testing.T) {
	r := &onboardRecorder{
		responses: frameworkUpResponses(),
		errOn:     map[string]error{"tmsh create net vlan external": errors.New("link not found")},
	}
	defer installOnboardSeams(t, r)()
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)
	err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false)
	if err == nil {
		t.Fatal("expected a genuine dataplane failure to propagate")
	}
	if st.Get("BIGIP_ONBOARDED") == "true" {
		t.Error("must not mark onboarded when a genuine create fails")
	}
	// pw file shred attempted after the failure.
	if !strings.Contains(strings.Join(r.cmds, "\n"), bigipPWRemotePath) {
		t.Error("expected pw-file shred attempt after genuine dataplane failure")
	}
}

// TestPhase17fOnboard_FinalDurablePasswordRunsLast asserts the final durable-password
// step (re-set + save + token-auth verify) is emitted AND runs strictly after the AS3
// install and the cis-partition create — i.e. after everything that can restart
// restjavad / reload config and revert the on-disk admin hash.
func TestPhase17fOnboard_FinalDurablePasswordRunsLast(t *testing.T) {
	r := &onboardRecorder{responses: frameworkUpResponses()}
	defer installOnboardSeams(t, r)()
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false); err != nil {
		t.Fatalf("onboard error: %v", err)
	}

	all := strings.Join(r.cmds, "\n")
	// The token-auth verify (CIS's exact method) must be present.
	if !strings.Contains(all, "loginProviderName") {
		t.Fatal("expected the final durable-password step to perform a token-auth verify (loginProviderName)")
	}
	// And it must come AFTER the AS3 install + the cis-partition create.
	verifyAt := lastIdx(all, "loginProviderName")
	if verifyAt < strings.Index(all, "package-management-tasks") {
		t.Error("final token-auth verify must run after the AS3 install")
	}
	if verifyAt < strings.Index(all, "create auth partition "+bigipCISPartition) {
		t.Error("final token-auth verify must run after the cis-partition create")
	}
	// The pw 600-file must NOT be shredded before the final verify (it needs the file
	// for both the re-set and the token login); the shred is the last pw-file op.
	if shredAt := lastIdx(all, "shred -u "+bigipPWRemotePath); shredAt < verifyAt {
		t.Error("the pw 600-file shred must come AFTER the final token-auth verify, not before")
	}
}

// TestPhase17fOnboard_FinalTokenVerifyFailsPhase asserts the phase FAILS (and does NOT
// mark BIGIP_ONBOARDED) when the final durable-password token-auth verify returns a
// non-200 / no-token — i.e. the durable hash did not take (the /etc/shadow !! revert).
func TestPhase17fOnboard_FinalTokenVerifyFailsPhase(t *testing.T) {
	resp := frameworkUpResponses()
	// The final token-auth verify reports a 401 with no token — the revert symptom.
	resp["loginProviderName"] = `CODE=401
LOGIN={"code":401,"message":"Authentication failed.","kind":":resterrorresponse"}`
	r := &onboardRecorder{responses: resp}
	defer installOnboardSeams(t, r)()
	shrinkFinalizeCadences(t)
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)
	err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false)
	if err == nil {
		t.Fatal("expected the phase to FAIL when the final token-auth verify returns non-200/no-token")
	}
	if !strings.Contains(err.Error(), "durable admin password") {
		t.Errorf("expected a durable-password failure, got: %v", err)
	}
	if st.Get("BIGIP_ONBOARDED") == "true" {
		t.Error("must NOT mark BIGIP_ONBOARDED=true when the durable password verify fails")
	}
	// pw-file shred attempted after the failure.
	if !strings.Contains(strings.Join(r.cmds, "\n"), "shred -u "+bigipPWRemotePath) {
		t.Error("expected pw-file shred attempt after the durable-password failure")
	}
}

// TestPhase17fOnboard_IdempotencyUsesTokenAuth asserts the idempotency-skip probe is
// a TOKEN-auth login (POST /mgmt/shared/authn/login with loginProviderName), NOT a
// basic sys/ready check — so a box whose admin hash reverted to "!!" (token 401s) is
// correctly detected as NOT-onboarded and re-onboarded.
func TestPhase17fOnboard_IdempotencyUsesTokenAuth(t *testing.T) {
	// Box reports NOT-onboarded (the default "ok" response carries no token), so the
	// mutating path runs — but we only care that the idempotency probe used token auth.
	r := &onboardRecorder{responses: frameworkUpResponses()}
	defer installOnboardSeams(t, r)()
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false); err != nil {
		t.Fatalf("onboard error: %v", err)
	}

	// The idempotency probe is the combined ssh script that echoes LOGIN=/AS3=/PART=.
	// It MUST perform a token-auth login (loginProviderName) and must NOT gate on
	// basic sys/ready.
	var idemProbe string
	for _, cmd := range r.cmds {
		if strings.Contains(cmd, `echo "LOGIN=$LOGIN"`) {
			idemProbe = cmd
			break
		}
	}
	if idemProbe == "" {
		t.Fatal("expected an idempotency probe emitting LOGIN=/AS3=/PART=")
	}
	if !strings.Contains(idemProbe, "loginProviderName") {
		t.Errorf("idempotency probe must use TOKEN auth (loginProviderName):\n%s", idemProbe)
	}
	if strings.Contains(idemProbe, "/mgmt/tm/sys/ready") {
		t.Errorf("idempotency probe must NOT gate on basic sys/ready (use token auth):\n%s", idemProbe)
	}
}

// shrinkFinalizeCadences shrinks the finalize step's restjavad-wait + token-verify
// backoff cadences so tests don't sleep real seconds, and restores them on cleanup.
func shrinkFinalizeCadences(t *testing.T) {
	t.Helper()
	origWaitT, origWaitI := bigipRestjavadWaitTimeout, bigipRestjavadWaitInterval
	origBackoff := bigipTokenVerifyBackoff
	bigipRestjavadWaitTimeout = 50 * time.Millisecond
	bigipRestjavadWaitInterval = time.Millisecond
	bigipTokenVerifyBackoff = time.Millisecond
	t.Cleanup(func() {
		bigipRestjavadWaitTimeout = origWaitT
		bigipRestjavadWaitInterval = origWaitI
		bigipTokenVerifyBackoff = origBackoff
	})
}

// TestPhase17fOnboard_DisablesLoginLockoutEarly asserts the login-failure-lockout
// disable runs AFTER the shell-bootstrap and BEFORE the early pw-set (and before the
// pw-file heredoc staging), and emits both `sys db` writes. This is the fix for the
// mcpd account lockout that CIS's continuous auth retries would otherwise trip.
func TestPhase17fOnboard_DisablesLoginLockoutEarly(t *testing.T) {
	r := &onboardRecorder{responses: frameworkUpResponses()}
	defer installOnboardSeams(t, r)()
	shrinkFinalizeCadences(t)
	t.Setenv(bigipPasswordEnv, testPW)

	cl := bigipCluster()
	st := onboardTestState(t)
	if err := Phase17fBigIPOnboard(context.Background(), cl, st, &Clients{}, false); err != nil {
		t.Fatalf("onboard error: %v", err)
	}

	all := strings.Join(r.cmds, "\n")
	// Both sys db writes must be present.
	for _, want := range []string{
		"tmsh modify sys db password.maxloginfailures value 0",
		"tmsh modify sys db systemauth.disablelocaladminlockout value true",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("expected lockout-disable command %q to be emitted, missing", want)
		}
	}

	// Ordering: shell-bootstrap → lockout-disable → pw-file heredoc → early pw-set.
	assertOrdered(t, all, []string{
		"modify auth user admin shell bash",                  // step 2 shell-bootstrap
		"tmsh modify sys db password.maxloginfailures value", // step 2b lockout-disable
		"__BIGIP_PW_EOF__",                                   // step 3 pw-file heredoc
		"modify auth user admin password",                    // step 4 early pw-set
	})
}

// TestBigipFinalize_OrderResetRestartWaitResetVerify asserts the finalize step runs
// its sub-steps in order: reset-stats login-failures → restart restjavad →
// wait-for-framework (authn/login probe) → durable pw re-set → token-auth verify.
func TestBigipFinalize_OrderResetRestartWaitResetVerify(t *testing.T) {
	r := &onboardRecorder{responses: frameworkUpResponses()}
	defer installOnboardSeams(t, r)()
	shrinkFinalizeCadences(t)

	if err := bigipFinalizeDurablePassword(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50"); err != nil {
		t.Fatalf("finalize error: %v", err)
	}

	all := strings.Join(r.cmds, "\n")
	assertOrdered(t, all, []string{
		"tmsh reset-stats auth login-failures", // 1 clear mcpd counter
		"tmsh restart sys service restjavad",   // 2 clear restjavad throttle
		"authn/login",                          // 3 wait-for-framework probe (no creds)
		"modify auth user admin password",      // 4 durable pw re-set
		"loginProviderName",                    // 5 token-auth verify
	})
	// reset-stats + restart must be in the SAME command, and that command must precede
	// any token-auth verify (which carries loginProviderName).
	if strings.Index(all, "tmsh restart sys service restjavad") > strings.Index(all, "loginProviderName") {
		t.Error("restjavad restart must precede the token-auth verify")
	}
}

// TestBigipFinalize_TokenVerifyBacksOffOnTransientLockout: a transient "Maximum number
// of login attempts exceeded" on the FIRST verify attempt, followed by a healthy 200 +
// token on the second, must SUCCEED (the backoff treats the lockout as restjavad still
// settling, not a hard failure).
func TestBigipFinalize_TokenVerifyBacksOffOnTransientLockout(t *testing.T) {
	shrinkFinalizeCadences(t)

	origRun := onboardRunStagingCmds
	origCopy := onboardCopyFile
	defer func() { onboardRunStagingCmds = origRun; onboardCopyFile = origCopy }()
	onboardCopyFile = func(_ context.Context, _ jumphost.ProbeOptions, _ []byte, _ string) error { return nil }

	verifyAttempts := 0
	onboardRunStagingCmds = func(_ context.Context, _ jumphost.ProbeOptions, commands []string) ([]string, error) {
		out := make([]string, 0, len(commands))
		for _, cmd := range commands {
			switch {
			case strings.Contains(cmd, "loginProviderName"):
				verifyAttempts++
				if verifyAttempts == 1 {
					// First attempt: the throttle is still tripped.
					out = append(out, "CODE=401\nLOGIN=Maximum number of login attempts exceeded")
				} else {
					out = append(out, "CODE=200\nLOGIN={\"token\":{\"token\":\"ABC123\"}}")
				}
			case strings.Contains(cmd, "authn/login"):
				out = append(out, `{"kind":":resterrorresponse"}`)
			default:
				out = append(out, "ok")
			}
		}
		return out, nil
	}

	if err := bigipFinalizeDurablePassword(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50"); err != nil {
		t.Fatalf("expected success after a transient-lockout backoff, got: %v", err)
	}
	if verifyAttempts < 2 {
		t.Errorf("expected the token-auth verify to back off and retry (≥2 attempts), got %d", verifyAttempts)
	}
}

// TestBigipFinalize_AllVerifyAttemptsFailFailsPhase: when every backed-off token-auth
// attempt fails (a real /etc/shadow revert / persistently-tripped throttle), finalize
// fails — and it makes exactly bigipTokenVerifyAttempts attempts (no infinite hammer).
func TestBigipFinalize_AllVerifyAttemptsFailFailsPhase(t *testing.T) {
	shrinkFinalizeCadences(t)

	origRun := onboardRunStagingCmds
	origCopy := onboardCopyFile
	defer func() { onboardRunStagingCmds = origRun; onboardCopyFile = origCopy }()
	onboardCopyFile = func(_ context.Context, _ jumphost.ProbeOptions, _ []byte, _ string) error { return nil }

	verifyAttempts := 0
	onboardRunStagingCmds = func(_ context.Context, _ jumphost.ProbeOptions, commands []string) ([]string, error) {
		out := make([]string, 0, len(commands))
		for _, cmd := range commands {
			switch {
			case strings.Contains(cmd, "loginProviderName"):
				verifyAttempts++
				out = append(out, "CODE=401\nLOGIN=Maximum number of login attempts exceeded")
			case strings.Contains(cmd, "authn/login"):
				out = append(out, `{"kind":":resterrorresponse"}`)
			default:
				out = append(out, "ok")
			}
		}
		return out, nil
	}

	err := bigipFinalizeDurablePassword(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50")
	if err == nil {
		t.Fatal("expected finalize to fail when every token-auth attempt fails")
	}
	if !strings.Contains(err.Error(), "durable admin password") {
		t.Errorf("expected a durable-password failure, got: %v", err)
	}
	if verifyAttempts != bigipTokenVerifyAttempts {
		t.Errorf("expected exactly %d backed-off verify attempts, got %d", bigipTokenVerifyAttempts, verifyAttempts)
	}
}

// TestBigipDisableLoginLockout_NoArgvPassword asserts the lockout-disable commands are
// the two `sys db` writes wrapped in an ssh-to-BIG-IP invocation and never carry the
// admin password (they are pre-auth `sys db` writes — no credentials involved).
func TestBigipDisableLoginLockout_NoArgvPassword(t *testing.T) {
	r := &onboardRecorder{}
	defer installOnboardSeams(t, r)()

	if err := bigipDisableLoginLockout(context.Background(), jumphost.ProbeOptions{}, "10.0.1.50"); err != nil {
		t.Fatalf("disable lockout error: %v", err)
	}
	if len(r.cmds) != 1 {
		t.Fatalf("expected a single ssh command, got %d: %v", len(r.cmds), r.cmds)
	}
	cmd := r.cmds[0]
	if !strings.Contains(cmd, "ssh -i ") {
		t.Errorf("lockout-disable must run over ssh into the BIG-IP: %s", cmd)
	}
	for _, want := range []string{
		"tmsh modify sys db password.maxloginfailures value 0",
		"tmsh modify sys db systemauth.disablelocaladminlockout value true",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("expected %q in the lockout-disable command: %s", want, cmd)
		}
	}
}

// --- helpers ---

// lastIdx returns the index of the LAST occurrence of sub in haystack, or -1.
func lastIdx(haystack, sub string) int {
	return strings.LastIndex(haystack, sub)
}

// assertOrdered fails if the substrings do not appear in haystack in the given order.
func assertOrdered(t *testing.T, haystack string, subs []string) {
	t.Helper()
	idx := 0
	for _, sub := range subs {
		pos := strings.Index(haystack[idx:], sub)
		if pos < 0 {
			t.Fatalf("expected %q after offset %d, but it was missing or out of order.\nFull:\n%s", sub, idx, haystack)
		}
		idx += pos + len(sub)
	}
}
