// Sprint 21 validator Issue 1 — hermetic tests for the argv-parser
// strictness contract + the `cobra.NoArgs` audit staff landed in
// `cmd/roksbnkctl/main.go` and across the cobra tree under
// `internal/cli/`. Additive-only — this file is new and never edits
// any pre-existing _test.go (Sprint 18 parity discipline carries
// forward).
//
// Two surfaces are exercised:
//
//  1. The argv preflight in `cmd/roksbnkctl/main.go` (`argvPreflight`,
//     called from `main()` BEFORE `cli.Execute()`). Because that
//     preflight is `package main` it is not reachable from this
//     `package cli` test directly — the rejection sub-tests build the
//     binary once via `go build` and exercise it as a subprocess. This
//     mirrors the integration-style pattern already in
//     `internal/cli/ops_integration_test.go`, but without the build
//     tag — these tests run hermetically (no network, no live IBM
//     Cloud) under the standard `go test -race ./internal/cli/` gate.
//
//  2. The per-command `cobra.NoArgs` audit. These run via the existing
//     `runRootCmd` harness (the same path
//     `internal/cli/init_var_file_test.go` uses) since `ValidateArgs`
//     fires inside `rootCmd.Execute()` BEFORE `PersistentPreRunE` and
//     before any RunE — no subprocess needed, no preflight
//     involvement.
//
// Sub-test → acceptance-criterion map (the "validator AC" numbers below
// are the validator's; the "staff AC" numbers are the staff review's):
//
//	StuckTogether_WS_RejectedWithActionableError
//	    → validator AC1 (stuck-together rejected; stderr names
//	       `-ws`, both forms, `--workspace`; no workspace dir
//	       created)
//	    → staff AC1 (preflight in main.go fires before Execute)
//	    → staff AC2 (error wording carries the named substrings)
//
//	StuckTogether_VFTypo_Rejected
//	    → validator AC1/sub-test 2 (multi-character stuck-together
//	       `-vfpath/to/file` rejected; no workspace dir created)
//
//	Canonical_Space_NotRejectedByPreflight
//	    → validator AC3 (`-w s` accepted by preflight; falls
//	       through to runInit; no preflight-rejection text in
//	       stderr; tolerates IBM Cloud verify failure on no
//	       creds — Sprint 19 parity)
//	    → staff AC4 (canonical forms still work byte-identically)
//
//	Canonical_Equals_NotRejectedByPreflight
//	    → validator AC4 (`-w=s` accepted by preflight)
//	    → staff AC4
//
//	NoArgs_Init_StrayPositional → validator AC5 (init -w foo bar
//	    rejected; the original failure mode)
//
//	NoArgs_Sweep/<cmd> → validator AC6 (one sub-test per command
//	    staff added `cobra.NoArgs` to, per the audit table in
//	    the 2026-05-21 review closure)
//
// Discipline:
//   - One new file only; no edits to any pre-existing _test.go.
//   - Negative-path sub-tests run hermetically — they trip BEFORE any
//     verify step because the argv preflight runs first.
//   - Positive-path sub-tests tolerate IBM Cloud verify failure (the
//     assertion is the absence of the preflight-rejection text in
//     stderr; the binary's subsequent failure on missing creds is
//     expected and ignored).
//   - The NoArgs sweep runs in-process via runRootCmd; each sub-test
//     gets a fresh ROKSBNKCTL_HOME via t.Setenv + t.TempDir() so the
//     RunE — if cobra ever reaches it (it must not) — has no shared
//     state to mutate. Cobra's ValidateArgs fires before
//     PersistentPreRunE so no RunE runs on any sub-test.

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// ── Subprocess harness — builds the roksbnkctl binary once per test
// run and invokes it with the argv shape under test. The preflight
// lives in package main, so this is the only way an in-package
// (`internal/cli`) test can exercise it.

var (
	binBuildOnce sync.Once
	binBuildPath string
	binBuildErr  error
	// binBuildDir is the directory binBuildPath lives in, kept so TestMain can
	// remove it. See #157: it used to be left behind on every run.
	binBuildDir   string
	binBuildSetup = func(t *testing.T) (string, error) { return buildRoksbnkctlBinary(t) }
)

// buildRoksbnkctlBinary compiles the roksbnkctl binary to a temp
// directory and returns its path. Skips the calling test cleanly if
// `go` is not on PATH or the build fails for an environmental reason
// (so the test doesn't false-fail in a stripped sandbox).
//
// The build runs `go build` against the repo root inferred from the
// test CWD (`internal/cli` → repo root is two levels up). The binary
// goes to t.TempDir() so the per-test cleanup deletes it.
func buildRoksbnkctlBinary(t *testing.T) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no `go` on PATH; subprocess preflight tests need to build the binary: %v", err)
		return "", err
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// internal/cli → repo root is two levels up; matches repoRel() in
	// chokepoint_guard_test.go.
	repoRoot := filepath.Clean(filepath.Join(wd, "..", ".."))
	exeName := "roksbnkctl-argv-test"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	// Build into a process-lifetime tempdir rather than t.TempDir(), because the
	// same binary serves every subprocess sub-test in the run via the sync.Once
	// cache and per-test cleanup would delete it out from under a later caller.
	//
	// TestMain removes it when the process exits, which is the binary's actual
	// lifetime. This used to rely on "the OS cleans up os.TempDir" instead (#157)
	// — which is not true on a tmpfs that only clears at reboot. Each run left
	// 112MB behind; 94 runs filled a 16GB /tmp and the symptom was a linker
	// error in an unrelated package.
	outDir, err := os.MkdirTemp("", "roksbnkctl-argv-build-")
	if err != nil {
		return "", err
	}
	binBuildDir = outDir
	bin := filepath.Join(outDir, exeName)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/roksbnkctl")
	cmd.Dir = repoRoot
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", &buildErr{output: string(out), err: err}
	}
	return bin, nil
}

type buildErr struct {
	output string
	err    error
}

func (b *buildErr) Error() string {
	return "go build: " + b.err.Error() + "\n" + b.output
}

// roksbnkctlTestBin returns the cached test-binary path, building it on
// first call. Subsequent callers reuse the cached path. Skip-handling
// is per-call so multiple tests can each get a clean skip when go is
// missing — no panic, no shared-state corruption.
func roksbnkctlTestBin(t *testing.T) string {
	t.Helper()
	binBuildOnce.Do(func() {
		binBuildPath, binBuildErr = binBuildSetup(t)
	})
	if binBuildErr != nil {
		t.Skipf("argv preflight subprocess test skipped — binary build failed: %v", binBuildErr)
	}
	if binBuildPath == "" {
		t.Skip("argv preflight subprocess test skipped — no binary path (likely no `go` on PATH)")
	}
	return binBuildPath
}

// runRoksbnkctlBin invokes the test binary with the given argv. Returns
// stdout, stderr, and the cmd's exit error (nil on exit 0). A
// per-invocation ROKSBNKCTL_HOME=t.TempDir() is wired so the process
// can never touch the operator's real workspace tree even if a regression
// drove the run past the preflight. HOME is also redirected so cred
// lookups don't reach a real keychain. Stdin is /dev/null so the
// process can't hang on an interactive prompt.
func runRoksbnkctlBin(t *testing.T, home string, argv ...string) (stdout, stderr string, err error) {
	t.Helper()
	bin := roksbnkctlTestBin(t)
	cmd := exec.Command(bin, argv...)
	// Hermetic env: minimal PATH, isolated HOME + ROKSBNKCTL_HOME, no
	// IBMCLOUD_API_KEY (preflight tests don't need it; the canonical-
	// accepted tests deliberately tolerate the no-creds failure).
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"ROKSBNKCTL_HOME=" + home,
		// Force non-interactive: term.IsTerminal returns false on a
		// pipe → cred.apiKeyFromPrompt returns an error immediately
		// instead of trying to read the key with no echo.
		"TERM=dumb",
	}
	cmd.Stdin = nil // explicit: no stdin; the process can't hang
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	// Belt-and-suspenders timeout: the preflight should exit instantly
	// (<1s on any developer laptop); 30s is a generous ceiling that
	// still surfaces a hang as a test failure rather than a CI timeout.
	done := make(chan error, 1)
	if startErr := cmd.Start(); startErr != nil {
		return "", "", startErr
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("subprocess exceeded 30s timeout; argv=%v", argv)
	}
	return outBuf.String(), errBuf.String(), err
}

// ── Preflight rejection tests (subprocess — exercises
// cmd/roksbnkctl/main.go's argvPreflight directly) ──────────────────

// TestArgvStrictness_StuckTogether_WS_RejectedWithActionableError pins
// the original failure-mode argv: `roksbnkctl init -ws foo --var-file
// <fixture>`. Maps to validator AC1 + staff AC1/AC2.
//
// Asserted contract per the staff smoke transcript at
// Shape 1 of the 2026-05-21 review closure:
//   - non-zero exit
//   - stderr names the literal offending token `-ws`
//   - stderr names BOTH acceptable forms (substring `-w s` AND `-w=s`)
//   - stderr names the long form `--workspace`
//   - NO workspace dir created under ROKSBNKCTL_HOME (the preflight
//     fires before cli.Execute() so RunE / workspace-dir creation /
//     IAM verify never happen)
func TestArgvStrictness_StuckTogether_WS_RejectedWithActionableError(t *testing.T) {
	home := t.TempDir()
	fixture := writeArgvFixture(t, t.TempDir(), "terraform.tfvars",
		`ibmcloud_cluster_region = "us-south"`+"\n")

	stdout, stderr, err := runRoksbnkctlBin(t, home,
		"init", "-ws", "foo", "--var-file", fixture)

	if err == nil {
		t.Fatalf("validator AC1: expected non-zero exit on `init -ws foo --var-file %s`; got exit 0\nstdout:\n%s\nstderr:\n%s",
			fixture, stdout, stderr)
	}
	// Offending token verbatim — the operator's only cue about what
	// went wrong is the literal argv they typed.
	if !strings.Contains(stderr, "-ws") {
		t.Errorf("validator AC1: stderr must name the offending token `-ws`; got:\n%s", stderr)
	}
	// Both acceptable short forms (substring matches per the spec —
	// the exact wording is staff's, but the contract requires both
	// shapes appear so the operator can copy/paste a fix).
	if !strings.Contains(stderr, "-w s") {
		t.Errorf("validator AC1: stderr must name the space form `-w s`; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "-w=s") {
		t.Errorf("validator AC1: stderr must name the equals form `-w=s`; got:\n%s", stderr)
	}
	// Long form must appear so the operator knows the canonical name.
	if !strings.Contains(stderr, "--workspace") {
		t.Errorf("validator AC1: stderr must name the long form `--workspace`; got:\n%s", stderr)
	}
	// Pre-Execute() guarantee: no side effect — no workspace dir
	// created. Re-discovers the original failure-mode bug if a future
	// regression drops the preflight or moves the check past
	// rootCmd.Execute().
	entries, statErr := os.ReadDir(home)
	if statErr != nil {
		t.Fatalf("reading ROKSBNKCTL_HOME %s: %v", home, statErr)
	}
	for _, e := range entries {
		// The original failure mode dropped a `s/` workspace dir
		// here. Any subdir of ROKSBNKCTL_HOME after a rejected
		// preflight is a regression.
		t.Errorf("validator AC1: ROKSBNKCTL_HOME must be empty after rejected preflight; found entry %q (a workspace dir was created — the preflight did not fire before cli.Execute())", e.Name())
	}
}

// TestArgvStrictness_StuckTogether_VFTypo_Rejected pins
// `roksbnkctl init -vfpath/to/file` — the operator typed `-vf` thinking
// of `--var-file` (which has no shorthand). Maps to validator AC2.
//
// `-vf...` is not the same shape as `-ws...` (v is the persistent root
// `--verbose` bool flag, NoOptDefVal="true"; preflight skips it). pflag
// then sees `-v -f...` and rejects on the unknown-shorthand `f`. The
// contract this test pins is the END-USER-VISIBLE outcome: the typo is
// rejected at parse time (non-zero exit) BEFORE any workspace dir is
// created. The exact rejecting layer (preflight vs pflag) is staff's
// implementation detail.
func TestArgvStrictness_StuckTogether_VFTypo_Rejected(t *testing.T) {
	home := t.TempDir()

	stdout, stderr, err := runRoksbnkctlBin(t, home,
		"init", "-vfpath/to/file")

	if err == nil {
		t.Fatalf("validator AC2: expected non-zero exit on `init -vfpath/to/file`; got exit 0\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	}
	// No workspace dir must exist — the rejection (either at the
	// preflight or at pflag's unknown-shorthand check) fires before
	// any RunE runs.
	entries, statErr := os.ReadDir(home)
	if statErr != nil {
		t.Fatalf("reading ROKSBNKCTL_HOME %s: %v", home, statErr)
	}
	for _, e := range entries {
		t.Errorf("validator AC2: ROKSBNKCTL_HOME must be empty after rejected typo; found entry %q (parse-time rejection did not happen before workspace mutation)", e.Name())
	}
	// Sanity — stderr must carry something. An empty stderr on a
	// non-zero exit would be the worst case (operator gets no clue).
	combined := stdout + stderr
	if combined == "" {
		t.Errorf("validator AC2: expected an actionable error on stderr; got empty output")
	}
}

// ── Canonical-form acceptance tests (subprocess — exercises that the
// preflight does NOT reject the legitimate shapes) ──────────────────

// TestArgvStrictness_Canonical_Space_NotRejectedByPreflight drives the
// space form `-w s --var-file <fixture>`. The preflight must let it
// through. The binary will then attempt runInit and (because there are
// no IBM creds in the hermetic env) fail at the api-key resolution
// step — that failure is expected and tolerated per Sprint 19 parity;
// the assertion is on the absence of preflight-rejection text, not on
// the binary's exit code. Maps to validator AC3.
func TestArgvStrictness_Canonical_Space_NotRejectedByPreflight(t *testing.T) {
	home := t.TempDir()
	fixture := writeArgvFixture(t, t.TempDir(), "terraform.tfvars",
		`ibmcloud_cluster_region = "us-south"`+"\n")

	_, stderr, _ := runRoksbnkctlBin(t, home,
		"init", "-w", "s", "--var-file", fixture)

	// Negative assertion: the preflight's rejection text must NOT
	// appear. If it does, the preflight is over-rejecting the
	// canonical form, which would break every existing operator
	// invocation.
	if strings.Contains(stderr, "is not a recognised flag") {
		t.Errorf("validator AC3: canonical space form `-w s` was rejected by the preflight; stderr:\n%s", stderr)
	}
	// Sanity — the binary did proceed past the preflight. With no
	// creds the runInit path fails at IBM verify / api-key
	// resolution, which produces a *different* error than the
	// preflight wording. Either path → non-empty stderr; the test
	// tolerates the exit code per Sprint 19 parity.
	if stderr == "" {
		t.Errorf("validator AC3: expected the binary to run past the preflight and produce some stderr; got empty output")
	}
}

// TestArgvStrictness_Canonical_Equals_NotRejectedByPreflight drives the
// equals form `-w=s --var-file <fixture>`. Same contract as the space
// form. Maps to validator AC4.
func TestArgvStrictness_Canonical_Equals_NotRejectedByPreflight(t *testing.T) {
	home := t.TempDir()
	fixture := writeArgvFixture(t, t.TempDir(), "terraform.tfvars",
		`ibmcloud_cluster_region = "us-south"`+"\n")

	_, stderr, _ := runRoksbnkctlBin(t, home,
		"init", "-w=s", "--var-file", fixture)

	if strings.Contains(stderr, "is not a recognised flag") {
		t.Errorf("validator AC4: canonical equals form `-w=s` was rejected by the preflight; stderr:\n%s", stderr)
	}
	if stderr == "" {
		t.Errorf("validator AC4: expected the binary to run past the preflight and produce some stderr; got empty output")
	}
}

// ── cobra.NoArgs pinning ────────────────────────────────────────────
//
// These exercise the OTHER half of the strictness contract — every
// command that doesn't accept positionals declares `Args: cobra.NoArgs`,
// so cobra's ValidateArgs surfaces the stray positional BEFORE any RunE
// runs. ValidateArgs fires before PersistentPreRunE (cobra command.go
// 1.8.1 lines 938–957) so the assertion is purely cobra-level — no
// preflight involvement needed; runRootCmd (the existing in-process
// harness) suffices.

// TestArgvStrictness_NoArgs_Init_StrayPositional pins the exact original
// failure mode: `init -w foo bar` — cobra catches `bar` as a stray
// positional (init now has `Args: cobra.NoArgs`) and errors out before
// runInit. Maps to validator AC5 + staff AC3 (the `Args: cobra.NoArgs`
// audit).
func TestArgvStrictness_NoArgs_Init_StrayPositional(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	resetArgvFlags(t)

	_, errOut, runErr := runRootCmd(t, "init", "-w", "foo", "bar")
	if runErr == nil {
		t.Fatalf("validator AC5: expected non-zero exit on `init -w foo bar`; got nil error\nstderr:\n%s", errOut)
	}
	combined := runErr.Error() + "\n" + errOut
	// Cobra phrases the stray positional in one of two equivalent ways
	// depending on whether the leaf has subcommands:
	//   - "unknown command \"bar\" for \"roksbnkctl init\"" (the smoke
	//     transcript's exact wording — staff Shape 4)
	//   - "Error: accepts 0 arg(s), received 1" (pure NoArgs wording)
	// Either is fine — the test asserts the offending positional OR
	// the equivalent surfaces in the error so the operator gets
	// actionable feedback.
	if !strings.Contains(combined, "bar") && !strings.Contains(combined, "unknown command") && !strings.Contains(combined, "accepts 0 arg") {
		t.Errorf("validator AC5: error must name positional `bar` or surface the cobra NoArgs message; got:\n%s", combined)
	}
}

// TestArgvStrictness_NoArgs_Sweep runs one sub-test per command staff
// added `cobra.NoArgs` to (verified against the audit table at
// the 2026-05-21 review closure). Each
// sub-test drives a stray-positional invocation and asserts the error.
// Maps to validator AC6.
//
// `init` is covered by its own dedicated test (above) and is repeated
// here only via the sweep entry — both assertions are intentional: the
// dedicated test pins the exact original-failure-mode argv; the sweep
// entry pins parity with every other NoArgs-bearing command.
//
// Every entry in this slice corresponds 1:1 with a "→ ADDED" row in
// the staff audit table. If staff adds a NoArgs to a new command in a
// future sprint, append a row here. If staff removes one, drop the
// row — the test would otherwise red-flag a non-regression.
func TestArgvStrictness_NoArgs_Sweep(t *testing.T) {
	// Each entry is the argv (after the binary name) that should
	// produce a NoArgs / unknown-command rejection. The trailing
	// "stray" token is the positional that violates the constraint.
	cases := []struct {
		name string
		argv []string
	}{
		// lifecycle.go
		{"init", []string{"init", "stray"}},
		{"up", []string{"up", "stray"}},
		{"plan", []string{"plan", "stray"}},
		{"apply", []string{"apply", "stray"}},
		{"down", []string{"down", "stray"}},
		// bnk_phase.go
		{"bnk_up", []string{"bnk", "up", "stray"}},
		{"bnk_down", []string{"bnk", "down", "stray"}},
		// cluster.go — `shell` and `kubeconfig` are TOP-LEVEL
		// commands (siblings of the `cluster` parent group), not
		// `cluster` subcommands. They live in cluster.go because
		// the env-loading machinery is shared with the cluster-
		// phase commands, but they're registered directly on
		// rootCmd (see cluster.go init()'s rootCmd.AddCommand).
		{"shell", []string{"shell", "stray"}},
		{"kubeconfig", []string{"kubeconfig", "stray"}},
		// cluster_phase.go
		{"cluster_show", []string{"cluster", "show", "stray"}},
		{"cluster_up", []string{"cluster", "up", "stray"}},
		{"cluster_down", []string{"cluster", "down", "stray"}},
		// cos.go
		{"cos_instance_list", []string{"cos", "instance", "list", "stray"}},
		{"cos_bucket_list", []string{"cos", "bucket", "list", "stray"}},
		// inspect.go
		{"status", []string{"status", "stray"}},
		// install.go
		{"install", []string{"install", "stray"}},
		// k_apply.go
		{"k_apply", []string{"k", "apply", "stray"}},
		// meta.go
		{"version", []string{"version", "stray"}},
		{"self_update", []string{"self", "update", "stray"}},
		{"doctor", []string{"doctor", "stray"}},
		// ops.go
		{"ops_install", []string{"ops", "install", "stray"}},
		{"ops_show", []string{"ops", "show", "stray"}},
		{"ops_uninstall", []string{"ops", "uninstall", "stray"}},
		// targets.go
		{"targets_list", []string{"targets", "list", "stray"}},
		// test.go
		{"test_connectivity", []string{"test", "connectivity", "stray"}},
		{"test_dns", []string{"test", "dns", "stray"}},
		{"test_throughput", []string{"test", "throughput", "stray"}},
		{"test_list", []string{"test", "list", "stray"}},
		// tfvars.go
		{"tfvars", []string{"tfvars", "stray"}},
		// workspaces.go
		{"workspaces_list", []string{"workspaces", "list", "stray"}},
		{"workspaces_current", []string{"workspaces", "current", "stray"}},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
			resetArgvFlags(t)

			_, errOut, runErr := runRootCmd(t, c.argv...)
			if runErr == nil {
				t.Fatalf("AC6/%s: expected non-zero exit on `%s`; got nil error\nstderr:\n%s",
					c.name, strings.Join(c.argv, " "), errOut)
			}
			combined := runErr.Error() + "\n" + errOut
			// Same acceptance shape as the dedicated init test —
			// cobra phrases the rejection one of two equivalent ways
			// and either is fine, as long as the operator gets an
			// actionable, non-zero-exit signal.
			if !strings.Contains(combined, "stray") &&
				!strings.Contains(combined, "unknown command") &&
				!strings.Contains(combined, "accepts 0 arg") {
				t.Errorf("AC6/%s: error must name positional `stray` or surface a cobra NoArgs / unknown-command message; got:\n%s",
					c.name, combined)
			}
		})
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

// writeArgvFixture drops content at <dir>/<name> and returns the abs
// path. Mirrors writeTFVars() in init_var_file_test.go — kept under a
// distinct name so the two helpers don't collide on package-level
// imports while staying additive.
func writeArgvFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("seeding argv fixture %s: %v", p, err)
	}
	return p
}

// resetArgvFlags zeroes the package-global flag vars an in-process
// runRootCmd run reads, both before AND after the test, so a stray
// value from a previous sub-test (or a prior package test) cannot
// bleed into the run. Distinct from the shared resetInitFlags() helper
// so the two don't collide.
func resetArgvFlags(t *testing.T) {
	t.Helper()
	prevWS, prevTF, prevUpg, prevVF :=
		flagWorkspace, flagTFSource, flagUpgradeTF, flagVarFiles
	flagWorkspace, flagTFSource, flagUpgradeTF, flagVarFiles =
		"", "", false, nil
	t.Cleanup(func() {
		flagWorkspace, flagTFSource, flagUpgradeTF, flagVarFiles =
			prevWS, prevTF, prevUpg, prevVF
	})
}
