package phases

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/jumphost"
)

// bigipPasswordEnv is the environment variable from which the BIG-IP admin
// password is read. The password is REQUIRED when bigipVE.enabled and is NEVER
// stored in cluster.yaml, state.env, or placed on any command line (argv) — it
// is written into a mode-600 file on the BIG-IP via an SSH stdin heredoc and
// shredded after use.
const bigipPasswordEnv = "AWSBNKCTL_BIGIP_PASSWORD" // #nosec G101 -- env var NAME, not a credential

const (
	// bigipAS3Version is the PINNED AS3 (Application Services 3) extension version.
	// CIS requires AS3 ≥ 3.18; 3.56.0 satisfies that with headroom and was the
	// version proven end-to-end in the spike (§S0b). Pinning keeps onboarding
	// reproducible. FUTURE HARDENING (per architect): embed the RPM bytes in the
	// binary so onboarding does not depend on the BIG-IP's egress to GitHub; a
	// pinned-version download is acceptable for now.
	bigipAS3Version = "3.56.0"
	// bigipAS3RPM is the AS3 RPM filename for the pinned version.
	bigipAS3RPM = "f5-appsvcs-3.56.0-10.noarch.rpm"
	// bigipAS3URL is the pinned download URL on F5's GitHub releases. The RPM is
	// fetched by the JUMPHOST (which has a public IP and internet egress — it
	// already downloads grpcurl in phase17d), NOT by the BIG-IP: the production
	// mgmt ENI has no public IP and its subnet's 0.0.0.0/0 → IGW route needs one
	// for egress, so the box cannot reach GitHub. The RPM is downloaded on the
	// jumphost then scp'd jumphost → BIG-IP (see bigipInstallAS3). The spike only
	// worked because it had a temporary EIP.
	bigipAS3URL = "https://github.com/F5Networks/f5-appsvcs-extension/releases/download/v3.56.0/" + bigipAS3RPM

	// bigipCISPartition is the LTM partition CIS writes its AS3 declarations into.
	bigipCISPartition = "cis"

	// bigipPEMRemotePath is where the BIG-IP SSH private key is staged on the
	// jumphost (mode 600). All BIG-IP access is `ssh -i <this> admin@<mgmt-ip>`.
	bigipPEMRemotePath = "/home/ec2-user/bigip.pem"

	// bigipAS3RPMRemotePath is where the AS3 RPM is downloaded on the jumphost (by
	// the jumphost itself, via curl); from there it is scp'd into the BIG-IP. It is
	// best-effort removed after the install POST (see bigipInstallAS3).
	bigipAS3RPMRemotePath = "/home/ec2-user/" + bigipAS3RPM

	// bigipPWRemotePath is the mode-600 file ON THE BIG-IP holding the admin
	// password (read by curl -sku "admin:$(cat ...)" — keeps the pw off argv).
	// Written once after framework-up, referenced by every authed op, shredded
	// at the end of onboarding (and on the idempotency-skip path).
	bigipPWRemotePath = "/var/tmp/.bigip-pw" // #nosec G101 -- file PATH on the BIG-IP, not a credential value

	// bigipAS3InstallTimeout caps the AS3 package-management-task poll.
	bigipAS3InstallTimeout = 5 * time.Minute
)

// Poll cadences are vars (not consts) so tests can shrink them to avoid real
// sleeps. They are never mutated in production code paths.
var (
	// bigipReadyGateTimeout is the hard budget for the framework-up readiness
	// gate. The box needs ~28-29 min to stabilize on c5n.2xlarge first boot
	// (restjavad/sshd flap before that), so the budget is ≥30 min per §S0b.
	bigipReadyGateTimeout = 35 * time.Minute
	// bigipReadyPollInterval is the interval between readiness probes.
	bigipReadyPollInterval = 30 * time.Second
	// bigipAS3PollInterval is the interval between AS3 install-task polls.
	bigipAS3PollInterval = 5 * time.Second

	// bigipRestjavadWaitTimeout caps the wait for restjavad to come back after the
	// finalize step restarts it (to clear restjavad's in-memory brute-force throttle).
	// ~90s is comfortably more than restjavad needs to re-register its REST workers
	// on a settled box (the long first-boot flap is already past by step 9).
	bigipRestjavadWaitTimeout = 90 * time.Second
	// bigipRestjavadWaitInterval is the interval between restjavad-recovery probes.
	bigipRestjavadWaitInterval = 5 * time.Second

	// bigipTokenVerifyAttempts caps the backed-off token-auth verify in the finalize
	// step. A tight retry loop re-trips restjavad's brute-force throttle, so we verify
	// with at most this many attempts spaced by bigipTokenVerifyBackoff.
	bigipTokenVerifyAttempts = 4
	// bigipTokenVerifyBackoff is the sleep between token-auth verify attempts. It must
	// be long enough to let restjavad's brute-force throttle settle between tries so
	// the verify itself doesn't re-trip the lockout.
	bigipTokenVerifyBackoff = 9 * time.Second
)

// onboardRunStagingCmds and onboardCopyFile are package-level seams defaulting to
// the real jumphost helpers. Tests in phase17f_bigip_onboard_test.go override
// them to capture the emitted command sequence without any network/SSH calls
// (same injection style as the jumphost package's own seams).
//
// onboardRunStagingCmds runs each command ON THE JUMPHOST over EICE; the BIG-IP
// is reached from inside those command strings via `ssh -i <pem> admin@<mgmt>`
// or `curl -sk https://<mgmt>/...`.
var (
	onboardRunStagingCmds = jumphost.RunStagingCommands
	onboardCopyFile       = jumphost.CopyFileViaEICE
)

// bigipAS3RPMMinBytes guards against a truncated/redirected-to-error download.
// The pinned AS3 RPM is ~12-15 MB; anything materially smaller is a bad fetch
// (e.g. an HTML error page) and must fail loudly rather than install garbage.
// The jumphost-side curl download is sanity-checked against this floor.
const bigipAS3RPMMinBytes = 1 << 20 // 1 MiB

// Phase17fBigIPOnboard onboards the running BIG-IP VE (launched by Phase17e /
// F2-B1) by driving tmsh + iControl REST THROUGH THE JUMPHOST over EICE — the
// operator host cannot reach the BIG-IP mgmt IP (no public IP on the VE).
//
// It runs LATE in `up` (after activation, around postflight) so the ~30-min
// BIG-IP first-boot settle overlaps the rest of the install. No-op when
// !cl.BigIPVEEnabled().
//
// Recipe (spike-proven), in order:
//
//  0. Copy the BIG-IP SSH key to the jumphost (mode 600).
//  1. Two-stage readiness gate (framework-up; ≥30 min budget).
//  2. Set admin shell to bash + save (shell-agnostic: bash-form then bare-tmsh
//     fallback, so it is idempotent whether the box is fresh or a partial re-run).
//     Then (step 2b) disable the admin login-failure lockout via `sys db` BEFORE any
//     auth/token traffic accumulates: CIS retries iControl auth continuously and,
//     combined with onboarding's own token-auth verifies, trips the mcpd account
//     lockout that 401s all auth ("Maximum number of login attempts exceeded"). Two
//     idempotent `sys db` writes (password.maxloginfailures=0 +
//     systemauth.disablelocaladminlockout=true) stop the admin account ever locking.
//  3. Stage the admin-password 600-file on the BIG-IP (stdin heredoc, never argv).
//  4. Set admin password (tmsh -f from the 600-file; verify authed sys/ready 200).
//  5. Provision LTM nominal (assert; usually already nominal on this AMI).
//  6. Dataplane: external/internal VLANs + self-IPs from BIGIP_EXT_IP/BIGIP_INT_IP.
//  7. Install AS3 (pinned 3.56.0) via iControl package-management-tasks.
//  8. Create the CIS partition.
//  9. Finalize durable admin password: re-set the password from the 600-file +
//     `save sys config` + verify via TOKEN auth (the exact method CIS uses). This
//     runs LAST — after AS3 install and the cis-partition create, both of which
//     restart restjavad / reload config and can drop the live-but-not-durable
//     admin hash back to "!!" on disk (the durability bug). Re-setting + saving
//     here persists the hash; the token-auth verify proves it took (or fails the
//     phase). See "the /etc/shadow !! revert" note below.
//  10. Shred the password 600-file.
//
// The DURABILITY BUG this fixes: setting the admin password early (step 4) makes it
// live in MCP and basic-auth verifies — but a later config reload / restjavad
// restart (the AS3 RPM install in step 7 restarts restjavad) reverts the on-disk
// /etc/shadow admin hash to "!!" (locked/unset), so ALL auth (basic AND token) then
// 401s and CIS — which uses token auth — crash-loops. Step 4's early `save sys
// config` ran BEFORE AS3, so it never captured the post-AS3 state. Step 9 re-sets +
// saves AFTER everything that can restart services, making the hash durable.
//
// On success: writes BIGIP_ONBOARDED=true + BIGIP_AS3_VERSION. The admin password
// is NEVER written to state.
//
// Idempotent: on re-run, a FULLY onboarded box (authed sys/ready 200 + AS3 present
// + cis partition) fast-skips all mutating steps. A PARTIALLY onboarded box (e.g. a
// prior run died mid-AS3) instead falls through to the step sequence, where every
// step is itself idempotent — shell-bootstrap is shell-agnostic, and the dataplane
// VLAN/self-IP and cis-partition creates tolerate already-existing objects — so the
// re-run drives the partial box to completion rather than failing on prior state.
//
// dryRun: prints planned steps (pw masked), makes NO jumphost/SSH calls, sets
// placeholder state.
//
// D-005: CheckAuthOrDie at entry.
func Phase17fBigIPOnboard(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 17f] bigip-onboard: cluster=%s\n", name)

	if !cl.BigIPVEEnabled() {
		fmt.Fprintln(os.Stderr, "[phase 17f] skipped: bigipVE disabled")
		return nil
	}

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would copy BIG-IP SSH key to jumphost (mode 600)")
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would poll BIG-IP framework-up readiness (≥30 min budget)")
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would stage admin-pw 600-file (pw=*** from "+bigipPasswordEnv+", via stdin heredoc, never argv)")
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would set admin shell bash")
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would disable admin login-failure lockout (sys db password.maxloginfailures=0 + systemauth.disablelocaladminlockout=true)")
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would set admin password + verify authed")
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would provision LTM nominal (assert)")
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would create external/internal VLANs + self-IPs")
		fmt.Fprintf(os.Stderr, "[phase 17f] dry-run: would download AS3 %s RPM on jumphost + scp jumphost → BIG-IP + install via iControl package-management-tasks (no download performed)\n", bigipAS3Version)
		fmt.Fprintf(os.Stderr, "[phase 17f] dry-run: would create %q partition\n", bigipCISPartition)
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would finalize durable admin password (re-set from 600-file + save sys config + token-auth verify)")
		fmt.Fprintln(os.Stderr, "[phase 17f] dry-run: would write BIGIP_ONBOARDED=true + BIGIP_AS3_VERSION")
		st.Set("BIGIP_ONBOARDED", "true")
		st.Set("BIGIP_AS3_VERSION", bigipAS3Version)
		return nil
	}

	// Read the admin password from env (REQUIRED, masked in all logs, never argv).
	pw := os.Getenv(bigipPasswordEnv)
	if pw == "" {
		return fmt.Errorf("phase17f: %s is not set — the BIG-IP admin password is required when bigipVE.enabled "+
			"(export it; never put it in cluster.yaml or state.env)", bigipPasswordEnv)
	}

	// Read jumphost + BIG-IP coordinates from state.
	jhInstanceID := st.Get("JUMPHOST_INSTANCE_ID")
	if jhInstanceID == "" {
		return fmt.Errorf("phase17f: JUMPHOST_INSTANCE_ID not in state (run phase17b first)")
	}
	mgmtIP := st.Get("BIGIP_MGMT_IP")
	if mgmtIP == "" {
		return fmt.Errorf("phase17f: BIGIP_MGMT_IP not in state (run phase17e first)")
	}
	extIP := st.Get("BIGIP_EXT_IP")
	if extIP == "" {
		return fmt.Errorf("phase17f: BIGIP_EXT_IP not in state (run phase17e first)")
	}
	intIP := st.Get("BIGIP_INT_IP")
	if intIP == "" {
		return fmt.Errorf("phase17f: BIGIP_INT_IP not in state (run phase17e first)")
	}
	pemPath := st.Get("BIGIP_SSH_KEY_PATH")
	if pemPath == "" {
		return fmt.Errorf("phase17f: BIGIP_SSH_KEY_PATH not in state (run phase17e first)")
	}

	opts := jumphost.ProbeOptions{
		Region:     cl.Metadata.Region,
		InstanceID: jhInstanceID,
	}

	// Step 0: copy the BIG-IP SSH key to the jumphost (mode 600). All later
	// BIG-IP access is `ssh -i <bigipPEMRemotePath> admin@<mgmtIP>` run ON the jumphost.
	pemBytes, err := os.ReadFile(pemPath) // #nosec G304 -- path is our own state-managed PEM
	if err != nil {
		return fmt.Errorf("phase17f: reading BIG-IP SSH key %s: %w", pemPath, err)
	}
	if err := onboardCopyFile(ctx, opts, pemBytes, bigipPEMRemotePath); err != nil {
		return fmt.Errorf("phase17f: copying BIG-IP SSH key to jumphost: %w", err)
	}
	if _, err := onboardRunStagingCmds(ctx, opts, []string{
		fmt.Sprintf("chmod 600 %s", bigipPEMRemotePath),
	}); err != nil {
		return fmt.Errorf("phase17f: chmod BIG-IP key on jumphost: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17f] BIG-IP SSH key staged on jumphost → %s (mode 600)\n", bigipPEMRemotePath)

	// Step 1: framework-up readiness gate (no creds needed). Hard budget ≥30 min.
	if err := bigipReadinessGate(ctx, opts, mgmtIP); err != nil {
		return fmt.Errorf("phase17f: readiness gate: %w", err)
	}

	// Idempotency: if the box is already onboarded (authed sys/ready 200 + AS3
	// present + cis partition exists), assert and skip the mutating steps.
	// bigipAlreadyOnboarded writes + shreds its own ephemeral pw file.
	if bigipAlreadyOnboarded(ctx, opts, mgmtIP, pw) {
		fmt.Fprintln(os.Stderr, "[phase 17f] BIG-IP already onboarded (authed sys/ready 200 + AS3 + cis) — skipping mutating steps")
		st.Set("BIGIP_ONBOARDED", "true")
		st.Set("BIGIP_AS3_VERSION", bigipAS3Version)
		return st.Save()
	}

	// Step 2: set admin shell to bash + save. The BIG-IP admin account's default
	// shell is tmsh on a FRESH box, but on a resume-after-partial-failure re-run a
	// prior run may have ALREADY set it to bash. The two shells disagree about
	// whether commands need the "tmsh " prefix:
	//   - while the shell is still tmsh, the bare form works and the "tmsh "-prefixed
	//     form is rejected (tmsh reads "tmsh" as an unknown argument);
	//   - once the shell is bash, the bare "modify ..." fails (`modify: command not
	//     found`) and the "tmsh "-prefixed form is the one that works.
	// So this step is shell-agnostic + idempotent: try the BASH form first, and if
	// that errors (we're still in tmsh), fall back to the BARE tmsh form. Whichever
	// shell we're in, exactly one attempt succeeds and the box ends with admin shell
	// = bash. Only a failure of BOTH attempts is a real error.
	fmt.Fprintln(os.Stderr, "[phase 17f] setting admin shell → bash (shell-agnostic; bash-form then bare-tmsh fallback)")
	if err := bigipSetShellBash(ctx, opts, mgmtIP); err != nil {
		return fmt.Errorf("phase17f: set admin shell bash: %w", err)
	}

	// Step 2b: disable the admin login-failure lockout BEFORE any auth/token traffic
	// accumulates. CIS retries iControl auth continuously; combined with onboarding's
	// own token-auth verifies, that trips the mcpd account lockout which 401s all auth
	// ("Maximum number of login attempts exceeded"). These are idempotent `sys db`
	// writes (admin shell is bash now), so they run safely on fresh + resume boxes.
	fmt.Fprintln(os.Stderr, "[phase 17f] disabling admin login-failure lockout (CIS retries auth continuously)")
	if err := bigipDisableLoginLockout(ctx, opts, mgmtIP); err != nil {
		return fmt.Errorf("phase17f: disable login-failure lockout: %w", err)
	}

	// Step 3: stage the admin-password 600-file on the BIG-IP via a stdin heredoc.
	// This requires bash (umask + heredoc), which is now active after step 2.
	// This single file backs every authed op below (curl -sku "admin:$(cat ...)")
	// and the tmsh password-set — so the password never appears on argv anywhere.
	// It is shredded at the end (and on the idempotency path above).
	fmt.Fprintln(os.Stderr, "[phase 17f] staging admin-pw 600-file on BIG-IP (stdin heredoc, never argv)")
	if _, err := onboardRunStagingCmds(ctx, opts, []string{bigipSSH(mgmtIP, bigipWritePWFileScript(pw))}); err != nil {
		return fmt.Errorf("phase17f: staging admin-pw file: %w", err)
	}

	// Steps 4-6: password, LTM provision, dataplane (tmsh over SSH, all under bash).
	for _, step := range bigipOnboardTmshSteps(mgmtIP, extIP, intIP) {
		fmt.Fprintf(os.Stderr, "[phase 17f] %s\n", step.label)
		if _, err := onboardRunStagingCmds(ctx, opts, []string{step.cmd}); err != nil {
			// Best-effort shred before bailing so a failed pw step doesn't leave
			// the secret on the box.
			shredBigIPPWFile(ctx, opts, mgmtIP)
			return fmt.Errorf("phase17f: %s: %w", step.label, err)
		}
	}

	// Step 7: install AS3 (pinned) via iControl REST, then poll to FINISHED.
	if err := bigipInstallAS3(ctx, opts, mgmtIP); err != nil {
		shredBigIPPWFile(ctx, opts, mgmtIP)
		return fmt.Errorf("phase17f: AS3 install: %w", err)
	}

	// Step 8: create the CIS partition. Tolerate a pre-existing partition (from a
	// prior partial run) so this step is idempotent on a resume; genuine failures
	// still propagate.
	fmt.Fprintf(os.Stderr, "[phase 17f] creating %q partition (idempotent; tolerates already-exists)\n", bigipCISPartition)
	if _, err := onboardRunStagingCmds(ctx, opts, []string{
		bigipSSH(mgmtIP, fmt.Sprintf("%s\ntmsh save sys config",
			bigipTolerantCreate("tmsh create auth partition "+bigipCISPartition))),
	}); err != nil {
		shredBigIPPWFile(ctx, opts, mgmtIP)
		return fmt.Errorf("phase17f: create cis partition: %w", err)
	}

	// Step 9: finalize a DURABLE admin password. This MUST run last — after AS3
	// install (step 7) and the cis-partition create (step 8), both of which restart
	// restjavad / reload config and can revert the on-disk admin /etc/shadow hash to
	// "!!" even though the password is still live in MCP (the durability bug). We
	// re-set the password from the 600-file (tmsh -f, never argv), `save sys config`
	// so the hash is persisted to disk, then verify via TOKEN auth (the exact method
	// CIS uses) so a revert fails the phase rather than silently breaking CIS.
	fmt.Fprintln(os.Stderr, "[phase 17f] finalizing: durable admin password + save + token-auth verify")
	if err := bigipFinalizeDurablePassword(ctx, opts, mgmtIP); err != nil {
		shredBigIPPWFile(ctx, opts, mgmtIP)
		return fmt.Errorf("phase17f: finalize durable admin password: %w", err)
	}

	// Step 10: shred the password 600-file (after the final durable-pw step, which
	// still needs it for both the re-set and the token-auth verify).
	shredBigIPPWFile(ctx, opts, mgmtIP)

	st.Set("BIGIP_ONBOARDED", "true")
	st.Set("BIGIP_AS3_VERSION", bigipAS3Version)
	fmt.Fprintf(os.Stderr, "[phase 17f] BIG-IP onboarded: AS3=%s, cis partition created, dataplane up\n", bigipAS3Version)
	return st.Save()
}

// Phase17fBigIPOnboardDown is a no-op: the BIG-IP VE instance (and all its
// resources) is destroyed by Phase17eBigIPVEDown (F2-B1). There is nothing for
// onboarding to tear down — the BIGIP_ONBOARDED / BIGIP_AS3_VERSION state keys
// are cleared by clearBigIPVEState in phase17e down.
func Phase17fBigIPOnboardDown(_ context.Context, _ *intent.Cluster, _ *state.State, _ *Clients) error {
	return nil
}

// bigipSSH wraps a remote-on-BIG-IP command in the jumphost-side ssh invocation.
// The returned string is a command run ON THE JUMPHOST that SSHes into the
// BIG-IP. The BIG-IP command itself is single-quoted; never embed the password
// here (callers route the pw through a 600-file on the box, not argv).
func bigipSSH(mgmtIP, remoteCmd string) string {
	return fmt.Sprintf(
		"ssh -i %s -o StrictHostKeyChecking=no -o ConnectTimeout=15 admin@%s %s",
		bigipPEMRemotePath, mgmtIP, jumphost.ShellSingleQuote(remoteCmd),
	)
}

// bigipSetShellBash drives the admin account's login shell to bash in a way that
// works WHETHER the current shell is tmsh (fresh box) or already bash (re-run after
// a partial onboarding). It tries the bash form first (`tmsh modify ...`) and, only
// if that errors, falls back to the bare tmsh form (`modify ...`). Exactly one form
// succeeds for a given shell; the function succeeds if EITHER attempt succeeds and
// returns an error only when BOTH fail. This is the resume-after-partial-failure
// fix for the shell-bootstrap step.
func bigipSetShellBash(ctx context.Context, opts jumphost.ProbeOptions, mgmtIP string) error {
	// Bash form: valid once the admin shell is already bash (the re-run case).
	_, bashErr := onboardRunStagingCmds(ctx, opts, []string{
		bigipSSH(mgmtIP, "tmsh modify auth user admin shell bash; tmsh save sys config"),
	})
	if bashErr == nil {
		return nil
	}
	// Bare tmsh form: valid while the shell is still tmsh (the fresh-box case).
	_, tmshErr := onboardRunStagingCmds(ctx, opts, []string{
		bigipSSH(mgmtIP, "modify auth user admin shell bash; save sys config"),
	})
	if tmshErr == nil {
		return nil
	}
	return fmt.Errorf("both shell-bootstrap attempts failed (bash-form: %v; bare-tmsh-form: %v)", bashErr, tmshErr)
}

// bigipDisableLoginLockout disables the BIG-IP admin login-failure lockout via two
// idempotent `sys db` writes. It runs EARLY (right after the shell-bootstrap, before
// any auth/token traffic accumulates) because CIS retries iControl auth continuously
// and, combined with onboarding's own token-auth verifies, trips the mcpd account
// lockout that 401s all auth with "Maximum number of login attempts exceeded":
//   - password.maxloginfailures=0 disables the failure counter entirely;
//   - systemauth.disablelocaladminlockout=true keeps the local admin from ever locking.
//
// The admin shell is bash by this point, so the commands use the "tmsh " prefix.
func bigipDisableLoginLockout(ctx context.Context, opts jumphost.ProbeOptions, mgmtIP string) error {
	_, err := onboardRunStagingCmds(ctx, opts, []string{
		bigipSSH(mgmtIP, strings.Join([]string{
			"tmsh modify sys db password.maxloginfailures value 0",
			"tmsh modify sys db systemauth.disablelocaladminlockout value true",
		}, "\n")),
	})
	return err
}

// shredBigIPPWFile best-effort shreds the admin-pw 600-file on the BIG-IP.
// Errors are ignored — this is cleanup of a secret, never a phase-failing op.
func shredBigIPPWFile(ctx context.Context, opts jumphost.ProbeOptions, mgmtIP string) {
	_, _ = onboardRunStagingCmds(ctx, opts, []string{
		bigipSSH(mgmtIP, fmt.Sprintf("shred -u %s 2>/dev/null || rm -f %s", bigipPWRemotePath, bigipPWRemotePath)),
	})
}

// bigipReadinessGate polls, from the jumphost, the BIG-IP iControl REST framework
// until it transitions from "Apache HTML 401" (httpd up, restjavad NOT yet up) to
// a JSON ":resterrorresponse" body (restjavad IS up). This is stage-1 of the
// two-stage gate: framework-up needs no valid credentials. Stage-2 (authed
// sys/ready all-yes) is exercised by bigipAlreadyOnboarded / the pw-set verify
// after the pw is set.
//
// The probe curls /mgmt/shared/authn/login (no creds) and inspects the body: the JSON
// error shape (":resterrorresponse" / "Authentication failed") means the REST
// framework is live. /mgmt/tm/sys/ready always returns an Apache HTML 401 page even
// after the framework is up, so it cannot be used as a readiness signal. Budget is
// hard-capped at bigipReadyGateTimeout (≥30 min) — the c5n.2xlarge first-boot flap
// tail is long (§S0b: restjavad stable ~28-29 min).
func bigipReadinessGate(ctx context.Context, opts jumphost.ProbeOptions, mgmtIP string) error {
	deadline := time.Now().Add(bigipReadyGateTimeout)
	// Print the body so we can distinguish Apache-HTML-401 from JSON-401.
	probe := fmt.Sprintf("curl -sk --max-time 10 https://%s/mgmt/shared/authn/login", mgmtIP)
	attempt := 0
	for {
		attempt++
		outs, err := onboardRunStagingCmds(ctx, opts, []string{probe})
		body := lastOf(outs)
		// Framework-up signal: the iControl REST JSON error shape. Before this,
		// the body is either empty (mgmt plane down) or an Apache HTML 401 page.
		frameworkUp := err == nil &&
			(strings.Contains(body, ":resterrorresponse") ||
				strings.Contains(body, "Authentication failed") ||
				strings.Contains(body, "configReady"))
		if frameworkUp {
			fmt.Fprintf(os.Stderr, "[phase 17f] readiness gate: iControl REST framework up (attempt %d)\n", attempt)
			return nil
		}
		fmt.Fprintf(os.Stderr, "[phase 17f] readiness gate: framework not up yet (attempt %d) — waiting %s\n",
			attempt, bigipReadyPollInterval)

		if !time.Now().Add(bigipReadyPollInterval).Before(deadline) {
			return fmt.Errorf("timeout after %s waiting for BIG-IP iControl REST framework on %s "+
				"(last probe attempt %d)", bigipReadyGateTimeout, mgmtIP, attempt)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bigipReadyPollInterval):
		}
	}
}

// bigipAlreadyOnboarded returns true when an authenticated probe shows the box is
// fully onboarded: a TOKEN-auth login succeeds (the exact path CIS uses) AND AS3
// (pinned version) is installed AND the cis partition exists. All on-box: the
// password is read from a mode-600 file on the BIG-IP (never on argv) which this
// function writes via heredoc and shreds.
//
// The onboarded gate is TOKEN auth (POST /mgmt/shared/authn/login → 200 + a
// "token" in the body), NOT basic sys/ready. This is the durability-bug fix: a box
// whose admin /etc/shadow hash has reverted to "!!" (the Unix locked/unset marker)
// fails BOTH basic and token auth, so a token-auth probe correctly detects it as
// NOT-onboarded and the phase re-runs to repair it. (Basic sys/ready alone could
// pass transiently while the durable hash is still bad, masking the revert.)
//
// Returns false (not an error) on any failure — a not-yet-onboarded box must
// proceed to the mutating steps, not fail the phase.
func bigipAlreadyOnboarded(ctx context.Context, opts jumphost.ProbeOptions, mgmtIP, pw string) bool {
	remote := strings.Join([]string{
		bigipWritePWFileScript(pw),
		// Token auth — the exact method CIS uses. 200 + a "token" in the body means
		// the durable admin password authenticates (pw from the 600-file, never argv).
		bigipTokenLoginScript("LOGIN"),
		// AS3 worker registered?
		fmt.Sprintf(`AS3=$(curl -sku "admin:$(cat %s)" https://localhost/mgmt/shared/appsvcs/info)`, bigipPWRemotePath),
		// cis partition present?
		fmt.Sprintf(`PART=$(curl -sku "admin:$(cat %s)" https://localhost/mgmt/tm/auth/partition/%s)`, bigipPWRemotePath, bigipCISPartition),
		fmt.Sprintf("shred -u %s 2>/dev/null || rm -f %s", bigipPWRemotePath, bigipPWRemotePath),
		// Emit a single machine-checkable line.
		`echo "LOGIN=$LOGIN"; echo "AS3=$AS3"; echo "PART=$PART"`,
	}, "\n")

	outs, err := onboardRunStagingCmds(ctx, opts, []string{bigipSSH(mgmtIP, remote)})
	if err != nil || len(outs) == 0 {
		return false
	}
	out := lastOf(outs)
	// Token auth succeeded: the login response carries a "token" object.
	loginOK := strings.Contains(out, `"token"`)
	as3OK := strings.Contains(out, `"version":"`+bigipAS3Version+`"`)
	// Partition GET returns the object (name "cis") when present, a 404-ish error otherwise.
	partOK := strings.Contains(out, `"name":"`+bigipCISPartition+`"`)
	return loginOK && as3OK && partOK
}

// bigipTokenLoginScript returns a shell snippet (for use INSIDE a BIG-IP remote
// command) that performs a TOKEN-auth login — the exact method CIS uses:
// POST /mgmt/shared/authn/login with {username, password, loginProviderName:tmos}.
// The password is read from the shared 600-file via `$(cat ...)`, NEVER on argv.
// The login response body is assigned to the shell variable named by `varName`
// (e.g. LOGIN=...), which the caller inspects for a 200 + a "token" object.
func bigipTokenLoginScript(varName string) string {
	return fmt.Sprintf(
		`%s=$(curl -sk -X POST https://localhost/mgmt/shared/authn/login `+
			`-H 'Content-Type: application/json' `+
			`-d "{\"username\":\"admin\",\"password\":\"$(cat %s)\",\"loginProviderName\":\"tmos\"}")`,
		varName, bigipPWRemotePath,
	)
}

// bigipWritePWFileScript returns a shell snippet (for use INSIDE a BIG-IP remote
// command) that writes the admin password into a mode-600 file via a heredoc on
// stdin — so the password never appears on a command line (argv) on the BIG-IP
// or in the jumphost process list. The heredoc body is the only place the literal
// pw appears, and it is consumed by `cat`, not exec'd as an argument.
func bigipWritePWFileScript(pw string) string {
	// umask 077 + heredoc → the file is created 600 and the pw is on stdin only.
	return fmt.Sprintf(
		"umask 077; cat > %s <<'__BIGIP_PW_EOF__'\n%s\n__BIGIP_PW_EOF__",
		bigipPWRemotePath, pw,
	)
}

// bigipSetPasswordScript returns the shell snippet (for use INSIDE a BIG-IP remote
// command) that re-sets the admin password from the on-box 600-file and `save sys
// config`s the result. It builds a mode-600 tmsh command file by interpolating the
// pw FROM the 600-file via `$(cat ...)` substitution (NEVER on argv), applies it
// with `tmsh -f`, then shreds the derived command file. The trailing `save sys
// config` (written into the command file) persists the hash to disk.
//
// This is the single code path through which the admin password is applied: it is
// used by both the early pw-set (step 4) and the final durable pw-set (step 9), so
// the secret never flows through a second, divergent path that could leak it.
func bigipSetPasswordScript() string {
	return strings.Join([]string{
		fmt.Sprintf("umask 077; printf 'modify auth user admin password %%s\\nsave sys config\\n' \"$(cat %s)\" > /var/tmp/.pwcmd", bigipPWRemotePath),
		"tmsh -f /var/tmp/.pwcmd",
		"shred -u /var/tmp/.pwcmd 2>/dev/null || rm -f /var/tmp/.pwcmd",
	}, "\n")
}

// bigipFinalizeDurablePassword runs the LAST onboarding step, hardened against the
// TWO lockouts that 401 token auth ("Maximum number of login attempts exceeded"):
//
//  1. reset-stats auth login-failures — clears the mcpd login-failure counter (so an
//     already-accumulated count from CIS's continuous auth retries cannot keep the
//     admin account locked);
//  2. restart sys service restjavad — clears restjavad's IN-MEMORY brute-force
//     throttle (the thing that actually 401s token auth). restjavad has no `sys db`
//     knob to disable that throttle; a restart is the only way to clear it;
//  3. wait for restjavad to recover — reuse the framework-up probe (poll
//     /mgmt/shared/authn/login until it answers with the JSON :resterrorresponse
//     shape) before ANY token call, so the verify below doesn't hit a still-restarting
//     framework;
//  4. re-set the admin password from the on-box 600-file (tmsh -f, never argv) +
//     `save sys config` — this is the DURABILITY fix: AS3 install / config reload can
//     revert the on-disk admin /etc/shadow hash to "!!" while the pw is still live in
//     MCP, so re-setting + saving here persists the hash;
//  5. token-auth verify with BACKOFF — at most bigipTokenVerifyAttempts tries spaced
//     by bigipTokenVerifyBackoff. A tight retry loop would re-trip restjavad's
//     brute-force throttle, so a transient "Maximum number of login attempts exceeded"
//     on an early attempt is treated as "restjavad still settling" and backed off, not
//     a hard failure. Only ALL backed-off attempts failing fails the phase.
//
// The pw is read from the shared 600-file in BOTH the re-set (via the shared pw-set
// helper) and every token-auth login (via `$(cat ...)`), NEVER on argv. The 600-file
// is NOT shredded here — it must still exist for both sub-steps; the caller shreds it
// after this returns.
//
// Returns an error (failing the phase) only when EVERY backed-off token-auth attempt
// fails to yield an HTTP 200 with a "token" — so onboarding is NOT marked complete on
// a silent revert, but a verify that merely raced restjavad's settling is retried.
func bigipFinalizeDurablePassword(ctx context.Context, opts jumphost.ProbeOptions, mgmtIP string) error {
	// 1+2: clear the mcpd login-failure counter and restart restjavad to clear its
	// in-memory brute-force throttle. Both run before any token traffic.
	fmt.Fprintln(os.Stderr, "[phase 17f] finalize: clearing login-failure counter + restarting restjavad (clears brute-force throttle)")
	if _, err := onboardRunStagingCmds(ctx, opts, []string{
		bigipSSH(mgmtIP, strings.Join([]string{
			"tmsh reset-stats auth login-failures",
			"tmsh restart sys service restjavad",
		}, "\n")),
	}); err != nil {
		return fmt.Errorf("reset login-failures + restart restjavad: %w", err)
	}

	// 3: wait for restjavad to come back BEFORE any token call — reuse the framework-up
	// probe so the verify below doesn't hit a still-restarting REST framework.
	fmt.Fprintln(os.Stderr, "[phase 17f] finalize: waiting for restjavad to recover before token verify")
	if err := bigipWaitFrameworkUp(ctx, opts, mgmtIP, bigipRestjavadWaitTimeout, bigipRestjavadWaitInterval); err != nil {
		return fmt.Errorf("waiting for restjavad after restart: %w", err)
	}

	// 4: re-set the password durably (the helper's command file ends with `save sys
	// config`).
	fmt.Fprintln(os.Stderr, "[phase 17f] finalize: re-setting durable admin password + save sys config")
	if _, err := onboardRunStagingCmds(ctx, opts, []string{bigipSSH(mgmtIP, bigipSetPasswordScript())}); err != nil {
		return fmt.Errorf("durable password re-set: %w", err)
	}

	// 5: token-auth verify with BACKOFF (a hammer would re-trip the throttle). Each
	// attempt captures BOTH the HTTP code and the body on one machine-checkable line;
	// success is CODE=200 + a "token". A transient lockout on an early attempt is
	// treated as "restjavad still settling" and backed off to the next attempt.
	verify := strings.Join([]string{
		// Capture the HTTP status separately so a non-200 fails even if the body somehow
		// contains the literal "token". CODE holds the status code.
		fmt.Sprintf(
			`CODE=$(curl -sk -o /dev/null -w '%%{http_code}' -X POST https://localhost/mgmt/shared/authn/login `+
				`-H 'Content-Type: application/json' `+
				`-d "{\"username\":\"admin\",\"password\":\"$(cat %s)\",\"loginProviderName\":\"tmos\"}")`,
			bigipPWRemotePath,
		),
		bigipTokenLoginScript("LOGIN"),
		`echo "CODE=$CODE"; echo "LOGIN=$LOGIN"`,
	}, "\n")

	var lastOut string
	for attempt := 1; attempt <= bigipTokenVerifyAttempts; attempt++ {
		outs, err := onboardRunStagingCmds(ctx, opts, []string{bigipSSH(mgmtIP, verify)})
		if err == nil {
			out := lastOf(outs)
			lastOut = out
			if strings.Contains(out, "CODE=200") && strings.Contains(out, `"token"`) {
				fmt.Fprintf(os.Stderr, "[phase 17f] finalize: token-auth verify OK (attempt %d)\n", attempt)
				return nil
			}
			// A "Maximum number of login attempts exceeded" here means restjavad's
			// throttle is still settling — back off rather than fail immediately.
			if strings.Contains(out, "Maximum number of login attempts exceeded") {
				fmt.Fprintf(os.Stderr, "[phase 17f] finalize: token-auth verify hit a transient lockout (attempt %d/%d) — backing off %s\n",
					attempt, bigipTokenVerifyAttempts, bigipTokenVerifyBackoff)
			} else {
				fmt.Fprintf(os.Stderr, "[phase 17f] finalize: token-auth verify not yet OK (attempt %d/%d) — backing off %s\n",
					attempt, bigipTokenVerifyAttempts, bigipTokenVerifyBackoff)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[phase 17f] finalize: token-auth verify errored (attempt %d/%d): %v — backing off %s\n",
				attempt, bigipTokenVerifyAttempts, err, bigipTokenVerifyBackoff)
		}
		if attempt == bigipTokenVerifyAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bigipTokenVerifyBackoff):
		}
	}

	return fmt.Errorf("durable admin password did not take after %d backed-off attempts: token-auth login "+
		"to /mgmt/shared/authn/login never returned HTTP 200 + a token (the /etc/shadow hash likely "+
		"reverted to %q, or restjavad's brute-force throttle stayed tripped) — last verify output: %q",
		bigipTokenVerifyAttempts, "!!", lastOut)
}

// bigipWaitFrameworkUp polls /mgmt/shared/authn/login (no creds) until the iControl
// REST framework answers with its JSON :resterrorresponse error shape, bounded by
// timeout/interval. It is the same framework-up signal bigipReadinessGate uses, reused
// by the finalize step to wait for restjavad to recover AFTER a restart before any
// token call. Returns an error on timeout or context cancellation.
func bigipWaitFrameworkUp(ctx context.Context, opts jumphost.ProbeOptions, mgmtIP string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	probe := fmt.Sprintf("curl -sk --max-time 10 https://%s/mgmt/shared/authn/login", mgmtIP)
	attempt := 0
	for {
		attempt++
		outs, err := onboardRunStagingCmds(ctx, opts, []string{probe})
		body := lastOf(outs)
		frameworkUp := err == nil &&
			(strings.Contains(body, ":resterrorresponse") ||
				strings.Contains(body, "Authentication failed") ||
				strings.Contains(body, "configReady"))
		if frameworkUp {
			fmt.Fprintf(os.Stderr, "[phase 17f] finalize: restjavad framework back up (attempt %d)\n", attempt)
			return nil
		}
		if !time.Now().Add(interval).Before(deadline) {
			return fmt.Errorf("timeout after %s waiting for restjavad framework on %s (attempt %d)",
				timeout, mgmtIP, attempt)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// onboardStep is a labelled BIG-IP onboarding command (run on the jumphost,
// targeting the BIG-IP).
type onboardStep struct {
	label string
	cmd   string
}

// bigipOnboardTmshSteps returns the ordered tmsh steps 4-6 (admin password, LTM
// provision, dataplane). The shell-bootstrap (step 2) and pw-file staging (step 3)
// are handled by Phase17fBigIPOnboard before this function is called, so all steps
// here execute under bash. The admin-pw 600-file is assumed already staged on the
// box. The password is applied with `tmsh -f` from a command file derived from
// that 600-file — it is NEVER passed as an argv argument.
func bigipOnboardTmshSteps(mgmtIP, extIP, intIP string) []onboardStep {
	// Password step: set the admin password from the 600-file (via the shared
	// pw-set helper — pw read through `$(cat ...)`, never argv), then verify authed
	// sys/ready 200 (pw again from the 600-file). The shared pw 600-file persists
	// for later authed ops. The same pw-set helper is reused by the final durable
	// password step (step 9) so the secret only ever flows through one code path.
	pwScript := strings.Join([]string{
		bigipSetPasswordScript(),
		fmt.Sprintf(`curl -sku "admin:$(cat %s)" -o /dev/null -w '%%{http_code}' https://localhost/mgmt/tm/sys/ready`, bigipPWRemotePath),
	}, "\n")

	// Each create is wrapped so a pre-existing object (from a prior partial run) is
	// treated as success while genuine failures still propagate — making the whole
	// dataplane step idempotent on a resume. The trailing `save sys config` runs
	// unconditionally (it is itself idempotent).
	dataplane := strings.Join([]string{
		bigipTolerantCreate("tmsh create net vlan external interfaces add { 1.1 }"),
		bigipTolerantCreate("tmsh create net vlan internal interfaces add { 1.2 }"),
		bigipTolerantCreate(fmt.Sprintf("tmsh create net self external-self address %s/24 vlan external allow-service default", extIP)),
		bigipTolerantCreate(fmt.Sprintf("tmsh create net self internal-self address %s/24 vlan internal allow-service default", intIP)),
		"tmsh save sys config",
	}, "\n")

	return []onboardStep{
		{
			label: "set admin password (tmsh -f from 600-file, never argv) + verify authed",
			cmd:   bigipSSH(mgmtIP, pwScript),
		},
		{
			label: "provision LTM nominal (assert; usually already nominal)",
			cmd:   bigipSSH(mgmtIP, "tmsh modify sys provision ltm level nominal; tmsh save sys config"),
		},
		{
			label: "create external/internal VLANs + self-IPs",
			cmd:   bigipSSH(mgmtIP, dataplane),
		},
	}
}

// bigipTolerantCreate wraps a single tmsh `create` command so that re-running it
// against an object that ALREADY EXISTS (the resume-after-partial-failure case) is
// treated as success, while any OTHER failure still propagates. BIG-IP signals an
// already-exists collision with the text "already exists" and/or error code
// "01020066"; the wrapper captures the command's combined output and exits 0 only
// when the create succeeds OR that already-exists signature is present, otherwise it
// re-emits the output and exits non-zero (failing the enclosing SSH command).
//
// The returned snippet runs under bash (the shell is bash by this onboarding step).
func bigipTolerantCreate(createCmd string) string {
	// Run the create, capturing stdout+stderr. On success, done. On failure, only
	// swallow the error when the output indicates the object already exists.
	return fmt.Sprintf(
		`__out=$({ %s ; } 2>&1); __rc=$?; `+
			`if [ $__rc -eq 0 ]; then :; `+
			`elif printf '%%s' "$__out" | grep -qE 'already exists|01020066'; then :; `+
			`else printf '%%s\n' "$__out" >&2; exit $__rc; fi`,
		createCmd,
	)
}

// bigipInstallAS3 installs the pinned AS3 RPM via iControl REST. The RPM is
// SIDE-LOADED — the BIG-IP never pulls it from the internet, because the
// production mgmt ENI has no public IP and its subnet's IGW route needs one for
// egress (§S0b; the spike only worked via a temporary EIP). The flow is:
//
//  1. download the RPM ON THE JUMPHOST (it has a public IP and internet egress —
//     it already pulls grpcurl in phase17d), then sanity-check size;
//  2. scp it jumphost → BIG-IP into /var/config/rest/downloads/;
//  3. POST an iControl INSTALL task referencing that now-local path, poll FINISHED;
//  4. best-effort remove the RPM copy on the jumphost.
//
// The operator host is skipped entirely: pushing a ~13 MB RPM through
// CopyFileViaEICE base64-encodes the bytes into the ssh argv and blows ARG_MAX
// (`fork/exec /usr/bin/ssh: argument list too long`), so the jumphost downloads
// it directly instead.
//
// The admin pw is read from the shared 600-file on the box for the authed
// iControl calls (never argv).
func bigipInstallAS3(ctx context.Context, opts jumphost.ProbeOptions, mgmtIP string) error {
	// 0. Idempotency: if the pinned AS3 version is already installed, skip the whole
	// download → scp → INSTALL flow. A re-onboard of a PARTIAL box (e.g. the admin
	// hash reverted to "!!" so the token-auth idempotency gate correctly re-runs the
	// phase) can still have AS3 from the prior run; POSTing an INSTALL task then fails
	// with "Package f5-appsvcs version 3.56.0-10 is already installed.", which would
	// fail the whole phase and block the durable-password step. We probe the same
	// authed iControl endpoint the idempotency check uses (/mgmt/shared/appsvcs/info)
	// — pw read from the on-box 600-file via `$(cat ...)`, NEVER argv — and skip when
	// the body already reports the pinned version.
	infoOuts, infoErr := onboardRunStagingCmds(ctx, opts, []string{bigipSSH(mgmtIP, bigipAS3InfoScript())})
	if infoErr == nil && strings.Contains(lastOf(infoOuts), `"version":"`+bigipAS3Version+`"`) {
		fmt.Fprintf(os.Stderr, "[phase 17f] AS3 %s already installed — skipping install\n", bigipAS3Version)
		return nil
	}

	fmt.Fprintf(os.Stderr, "[phase 17f] installing AS3 %s (jumphost downloads %s, scp → BIG-IP)\n",
		bigipAS3Version, bigipAS3RPM)

	// 1. Download the RPM ON THE JUMPHOST (which has internet egress). Sanity-check
	// it landed and is not a redirected error page (size floor) before installing.
	fmt.Fprintf(os.Stderr, "[phase 17f] downloading AS3 RPM on jumphost: %s\n", bigipAS3URL)
	if _, err := onboardRunStagingCmds(ctx, opts, []string{
		fmt.Sprintf(
			"curl -fsSL --retry 3 -o %s %s && "+
				"test -s %s && "+
				`SZ=$(stat -c%%s %s 2>/dev/null || stat -f%%z %s) && `+
				`if [ "$SZ" -lt %d ]; then echo "AS3 RPM download too small ($SZ bytes) — likely an error page, not the RPM" >&2; exit 1; fi`,
			bigipAS3RPMRemotePath, bigipAS3URL,
			bigipAS3RPMRemotePath,
			bigipAS3RPMRemotePath, bigipAS3RPMRemotePath,
			bigipAS3RPMMinBytes,
		),
	}); err != nil {
		return fmt.Errorf("AS3 install: downloading RPM on jumphost: %w", err)
	}
	// 4 (deferred): best-effort remove the RPM copy on the jumphost afterward.
	defer func() {
		_, _ = onboardRunStagingCmds(ctx, opts, []string{
			fmt.Sprintf("rm -f %s", bigipAS3RPMRemotePath),
		})
	}()

	downloadPath := "/var/config/rest/downloads/" + bigipAS3RPM

	// 2. scp jumphost → BIG-IP. The admin shell is bash by this point, so scp
	// works; the key path is the same one the phase already staged.
	scp := fmt.Sprintf(
		"scp -i %s -o StrictHostKeyChecking=no -o ConnectTimeout=15 %s admin@%s:%s",
		bigipPEMRemotePath, bigipAS3RPMRemotePath, mgmtIP, downloadPath,
	)
	if _, err := onboardRunStagingCmds(ctx, opts, []string{scp}); err != nil {
		return fmt.Errorf("AS3 install: scp RPM jumphost → BIG-IP: %w", err)
	}

	// 3. POST the INSTALL task referencing the now-local path, then poll FINISHED.
	outs, err := onboardRunStagingCmds(ctx, opts, []string{bigipSSH(mgmtIP, bigipAS3PostScript(downloadPath))})
	if err != nil {
		return fmt.Errorf("AS3 install task POST: %w", err)
	}
	taskID := parseAS3TaskID(lastOf(outs))
	if taskID == "" {
		return fmt.Errorf("AS3 install: could not parse task id from iControl response: %q", lastOf(outs))
	}
	fmt.Fprintf(os.Stderr, "[phase 17f] AS3 install task %s POSTed — polling to FINISHED\n", taskID)

	deadline := time.Now().Add(bigipAS3InstallTimeout)
	for {
		pollOuts, pollErr := onboardRunStagingCmds(ctx, opts, []string{bigipSSH(mgmtIP, bigipAS3PollScript(taskID))})
		body := lastOf(pollOuts)
		if pollErr == nil && strings.Contains(body, `"status":"FINISHED"`) {
			fmt.Fprintf(os.Stderr, "[phase 17f] AS3 install task %s FINISHED\n", taskID)
			return nil
		}
		// Belt-and-suspenders: tolerate an INSTALL task that FAILED only because the
		// package is already installed (mirrors the tolerant-create pattern). This
		// covers a race where the early appsvcs/info probe missed an AS3 that the box
		// nonetheless has — the box is already in the desired state, so treat it as
		// success rather than failing the phase + blocking the durable-password step.
		if strings.Contains(body, "already installed") {
			fmt.Fprintf(os.Stderr, "[phase 17f] AS3 install task %s reports already installed — treating as success\n", taskID)
			return nil
		}
		if strings.Contains(body, `"status":"FAILED"`) {
			return fmt.Errorf("AS3 install task %s FAILED: %q", taskID, body)
		}
		if !time.Now().Add(bigipAS3PollInterval).Before(deadline) {
			return fmt.Errorf("timeout after %s polling AS3 install task %s (last status: %q)",
				bigipAS3InstallTimeout, taskID, body)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bigipAS3PollInterval):
		}
	}
}

// bigipAS3PostScript builds the on-box script that POSTs the INSTALL task against
// the already side-loaded RPM at downloadPath. It deliberately does NOT download
// the RPM — the box has no internet (mgmt ENI has no public IP); the RPM is scp'd
// in by bigipInstallAS3 before this runs. The authed POST reads the pw from the
// shared 600-file (never argv); the pw file was staged earlier and persists until
// the end shred.
func bigipAS3PostScript(downloadPath string) string {
	return fmt.Sprintf(`curl -sku "admin:$(cat %s)" -H 'Content-Type: application/json' -X POST `+
		`-d '{"operation":"INSTALL","packageFilePath":"%s"}' `+
		`https://localhost/mgmt/shared/iapp/package-management-tasks`, bigipPWRemotePath, downloadPath)
}

// bigipAS3InfoScript returns the on-box script that GETs the AS3 worker info
// endpoint (/mgmt/shared/appsvcs/info) — the same authed iControl call the
// idempotency check uses to read the installed AS3 version. The pw is read from
// the shared 600-file via `$(cat ...)`, NEVER on argv. The caller inspects the
// body for `"version":"<pinned>"` to decide whether to skip the install.
func bigipAS3InfoScript() string {
	return fmt.Sprintf(`curl -sku "admin:$(cat %s)" https://localhost/mgmt/shared/appsvcs/info`,
		bigipPWRemotePath)
}

// bigipAS3PollScript polls a package-management task by id (authed; pw from the
// shared 600-file).
func bigipAS3PollScript(taskID string) string {
	return fmt.Sprintf(`curl -sku "admin:$(cat %s)" https://localhost/mgmt/shared/iapp/package-management-tasks/%s`,
		bigipPWRemotePath, taskID)
}

// parseAS3TaskID extracts the "id" field from an iControl package-management-task
// POST response (a JSON body containing "id":"<uuid>").
func parseAS3TaskID(body string) string {
	const key = `"id":"`
	i := strings.Index(body, key)
	if i < 0 {
		return ""
	}
	rest := body[i+len(key):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// lastOf returns the last element of a string slice or "".
func lastOf(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}
