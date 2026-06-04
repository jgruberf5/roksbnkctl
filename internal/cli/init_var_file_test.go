// Sprint 19 validator Issue 1 — hermetic test surface for the staff
// feature `roksbnkctl init --var-file <path>` (workspace-persistent
// tfvars at init time).
//
// Additive ONLY — this file is new and never edits any pre-existing
// _test.go (Sprint 18 parity discipline carries forward). Drives the
// `init` cobra command via its public flag surface (the `--var-file`
// flag staff adds in init_var_file.go). No reach into private helpers.
//
// All five sub-cases use t.TempDir() + ROKSBNKCTL_HOME redirection so
// they never touch the operator's real workspace tree.
//
// Sub-test → acceptance-criterion map:
//
//	(a) HappyPath          → AC1 (terraform.tfvars.user lands at
//	                              workspace root sibling to config.yaml,
//	                              mode 0600, byte-identical to input)
//	(b) ConfigSeeding      → AC2 (config.yaml reflects tfvars values)
//	(c) MissingFile        → AC3 (non-zero exit; error names path)
//	(d) MalformedFile      → AC4 (non-zero exit; error points at
//	                              terraform.tfvars.example)
//	(e) NoFlagByteIdentical→ AC5 (existing init unchanged when --var-file
//	                              not passed — no terraform.tfvars.user)
//
// Staff's shipped design parses + validates the var-file BEFORE the
// IBM Cloud verify call (so the negative-path tests (c) + (d) trip
// hermetically with zero network), but performs the file COPY after
// verify (so the positive-path tests (a) + (b) need IBM creds).
// Cases (a) + (b) detect that gap and skip with a clear message when
// no live IBM API key is available; they remain assertive once staff
// (or a follow-up) adds a non-interactive test seam that bypasses
// verify. The integrator's live `!` cycle covers the positive path
// end-to-end through scripts/e2e-init-var-file.sh.

package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/naming"
)

// hasInitVarFileFlag reports whether staff Issue 1 has landed by
// looking for the `--var-file` flag on `initCmd`. Until staff ships
// every sub-case skips with an informative message, keeping the
// package's test suite GREEN while the validator + staff agents run
// in parallel.
func hasInitVarFileFlag(t *testing.T) bool {
	t.Helper()
	return initCmd.Flags().Lookup("var-file") != nil
}

// skipIfNoFlag short-circuits a sub-test when staff Issue 1's flag is
// not yet wired. Logged once per case so the operator running the
// validator gate sees the gating reason.
func skipIfNoFlag(t *testing.T) {
	t.Helper()
	if !hasInitVarFileFlag(t) {
		t.Skipf("init --var-file flag not yet wired on initCmd; staff Issue 1 pending — test goes assertive automatically once staff lands the flag")
	}
}

// skipIfNoLiveIBMCreds short-circuits the positive-path sub-tests
// (happy + config-seeding) when no IBM Cloud API key is reachable.
// Staff's current design calls ibm.Client.Verify(ctx) before the
// var-file copy step, so the positive cases need real credentials to
// reach the assertion surface. The gated live driver
// (scripts/e2e-init-var-file.sh) is the integrator's path to GREEN on
// those two cases.
func skipIfNoLiveIBMCreds(t *testing.T) {
	t.Helper()
	for _, v := range []string{"IBMCLOUD_API_KEY", "IC_API_KEY"} {
		if os.Getenv(v) != "" {
			return
		}
	}
	t.Skipf("hermetic positive-path skipped: staff's init runs ibm.Verify() before the var-file copy; no IBMCLOUD_API_KEY in env. Run scripts/e2e-init-var-file.sh for the live assertion.")
}

// stageHermeticHome points ROKSBNKCTL_HOME at a fresh tempdir so the
// test never touches the operator's real workspace tree.
func stageHermeticHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.ROKSBNKCTLHomeEnv, home)
	return home
}

// resetInitFlags zeroes the package-global flag vars init reads, both
// before AND after the test, so a stray value from a previous sub-test
// (or a prior package test) cannot bleed into the run.
func resetInitFlags(t *testing.T) {
	t.Helper()
	prevWS, prevTF, prevUpg, prevVF, prevInitVF :=
		flagWorkspace, flagTFSource, flagUpgradeTF, flagVarFiles, flagInitVarFile
	flagWorkspace, flagTFSource, flagUpgradeTF, flagVarFiles, flagInitVarFile =
		"", "", false, nil, ""
	t.Cleanup(func() {
		flagWorkspace, flagTFSource, flagUpgradeTF, flagVarFiles, flagInitVarFile =
			prevWS, prevTF, prevUpg, prevVF, prevInitVF
	})
}

// runRootCmd drives the public cobra surface — the same code path
// `roksbnkctl ...` runs from a shell. Captures both stdout/stderr so
// negative-path assertions can grep error text without scraping the
// test's own output.
func runRootCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	rootCmd.SetArgs(args)
	err = rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

// completeTFVarsFixture is the shape a real operator's
// `./terraform.tfvars` carries — the variables init's interview asks
// about, plus the API key the workspace's lifecycle needs. Variable
// names match what staff's loadInitVarFile() consumes
// (ibmcloud_cluster_region, openshift_cluster_name, etc.).
func completeTFVarsFixture() string {
	return `# Sprint 19 hermetic-test fixture — not a real key.
ibmcloud_api_key          = "TEST_FIXTURE_KEY_NOT_A_REAL_CREDENTIAL"
ibmcloud_cluster_region   = "us-south"
ibmcloud_resource_group   = "test-rg"
openshift_cluster_name    = "test-cluster"
openshift_cluster_version = "4.18"
roks_workers_per_zone     = 2
create_roks_cluster       = true
`
}

// writeTFVars drops content at <dir>/<name> and returns the absolute
// path the test passes to --var-file.
func writeTFVars(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("seeding tfvars fixture %s: %v", p, err)
	}
	return p
}

// ── (a) Happy path ──────────────────────────────────────────────────
//
// AC1: a complete terraform.tfvars-shaped file is passed; post-init
// ~/.roksbnkctl/<name>/terraform.tfvars.user (sibling to config.yaml)
// exists with mode 0600 and is byte-identical to the input. ONE copy
// at the workspace root — tf.Workspace.UserTFVarsPath() resolves to
// filepath.Dir(stateDir)/terraform.tfvars.user for BOTH the trial and
// cluster phases, so a single workspace-root file serves both. Round-1
// of Sprint 19 erroneously wrote two copies inside the state dirs
// where HasUserTFVars() does NOT look.
//
// Skipped hermetically (no live IBM creds → ibm.Verify() fails before
// the copy step). The gated live driver covers this end-to-end.
func TestInitVarFile_HappyPath_BothCopiesLand(t *testing.T) {
	skipIfNoFlag(t)
	skipIfNoLiveIBMCreds(t)
	resetInitFlags(t)
	home := stageHermeticHome(t)
	wsName := "test-ws-happy"

	fixtureDir := t.TempDir()
	tfvars := writeTFVars(t, fixtureDir, "terraform.tfvars", completeTFVarsFixture())
	want, err := os.ReadFile(tfvars)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	_, errOut, runErr := runRootCmd(t, "init", "-w", wsName, "--var-file", tfvars)
	if runErr != nil {
		t.Fatalf("init --var-file unexpectedly failed: %v\nstderr:\n%s", runErr, errOut)
	}

	// AC1 — single workspace-root copy present, mode 0600, byte-identical.
	dst := filepath.Join(home, wsName, "terraform.tfvars.user")
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("AC1: expected %s to exist: %v", dst, err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("AC1: %s mode = %o, want 0600", dst, mode)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("AC1: reading %s: %v", dst, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("AC1: %s bytes differ from input fixture\nwant %d bytes, got %d bytes", dst, len(want), len(got))
	}
	// And the obsolete in-state-dir locations must NOT exist (the
	// HasUserTFVars()-blind spot the round-1 live verify caught).
	for _, sub := range []string{"state", "state-cluster"} {
		stale := filepath.Join(home, wsName, sub, "terraform.tfvars.user")
		if _, err := os.Stat(stale); err == nil {
			t.Errorf("AC1: %s should not exist (HasUserTFVars() looks at the workspace root, not the state dirs)", stale)
		}
	}
}

// ── (b) Config seeding ──────────────────────────────────────────────
//
// AC2: the tfvars sets ibmcloud_cluster_region, openshift_cluster_name,
// openshift_cluster_version, roks_workers_per_zone, etc.; post-init
// config.yaml reflects those values (interview prompts for those
// fields skipped).
//
// Skipped hermetically for the same reason as (a) — ibm.Verify() runs
// before config.yaml is written. The live driver covers this case.
func TestInitVarFile_ConfigSeeding(t *testing.T) {
	skipIfNoFlag(t)
	skipIfNoLiveIBMCreds(t)
	resetInitFlags(t)
	stageHermeticHome(t)
	wsName := "test-ws-seed"

	fixtureDir := t.TempDir()
	tfvars := writeTFVars(t, fixtureDir, "terraform.tfvars", completeTFVarsFixture())

	_, errOut, runErr := runRootCmd(t, "init", "-w", wsName, "--var-file", tfvars)
	if runErr != nil {
		t.Fatalf("init --var-file unexpectedly failed: %v\nstderr:\n%s", runErr, errOut)
	}

	ws, err := config.LoadWorkspace(wsName)
	if err != nil {
		t.Fatalf("AC2: loading seeded workspace config: %v", err)
	}

	// AC2 — config.yaml carries the tfvars-seeded fields. Field
	// mapping mirrors staff's loadInitVarFile() in init_var_file.go.
	cases := []struct {
		got, want, desc string
	}{
		{ws.IBMCloud.Region, "us-south", "IBMCloud.Region from ibmcloud_cluster_region"},
		{ws.IBMCloud.ResourceGroup, "test-rg", "IBMCloud.ResourceGroup from ibmcloud_resource_group"},
		{ws.Cluster.Name, "test-cluster", "Cluster.Name from openshift_cluster_name"},
		{ws.Cluster.OpenShiftVersion, "4.18", "Cluster.OpenShiftVersion from openshift_cluster_version"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("AC2: %s: got %q, want %q", c.desc, c.got, c.want)
		}
	}
	if ws.Cluster.WorkersPerZone != 2 {
		t.Errorf("AC2: Cluster.WorkersPerZone: got %d, want 2 (from roks_workers_per_zone)", ws.Cluster.WorkersPerZone)
	}
	if !ws.Cluster.Create {
		t.Errorf("AC2: Cluster.Create: got false, want true (from create_roks_cluster)")
	}

	// Sprint 26 — the --var-file path now ALSO sets a sanitized Prefix
	// (seeded from the file's openshift_cluster_name, else the workspace
	// name) + an all-create resources block, so the generated base is
	// collision-safe while the operator's terraform.tfvars.user still wins
	// via layering. Intended additive expectation, not a regression.
	wantPrefix := naming.SanitizeToPrefix("test-cluster") // == the fixture's openshift_cluster_name
	if ws.Prefix != wantPrefix {
		t.Errorf("AC2 (Sprint 26): ws.Prefix = %q; want sanitized %q (from openshift_cluster_name)", ws.Prefix, wantPrefix)
	}
	if ws.Resources == nil {
		t.Error("AC2 (Sprint 26): ws.Resources is nil; the --var-file path must default to an all-create resources block")
	} else if !ws.Resources.TransitGateway.Create {
		t.Error("AC2 (Sprint 26): ws.Resources.TransitGateway.Create = false; --var-file defaults to all-create")
	}
}

// ── (c) Missing file ────────────────────────────────────────────────
//
// AC3: --var-file pointing at a nonexistent path → non-zero exit; the
// error message names the offending path so the operator can act.
// Staff's loadInitVarFile() pre-stats the file BEFORE the IBM verify
// step, so this case trips hermetically with zero network.
func TestInitVarFile_MissingFile_Fails(t *testing.T) {
	skipIfNoFlag(t)
	resetInitFlags(t)
	stageHermeticHome(t)

	missing := filepath.Join(t.TempDir(), "does-not-exist.tfvars")

	_, errOut, runErr := runRootCmd(t, "init", "-w", "test-ws-missing", "--var-file", missing)
	if runErr == nil {
		t.Fatalf("AC3: expected non-zero exit on missing --var-file, got nil\nstderr:\n%s", errOut)
	}
	// The error must name the offending path (the operator's only cue
	// about what went wrong is the path they typed). Either the cobra
	// error or the stderr capture must mention it.
	//
	// absVarFilePath in staff's impl resolves the path to absolute before
	// the error fires, so we match by basename — survives the abs-path
	// transformation without coupling the test to invocation-CWD.
	combined := runErr.Error() + "\n" + errOut
	if !strings.Contains(combined, filepath.Base(missing)) {
		t.Errorf("AC3: missing-file error did not name the path %q\nerror+stderr:\n%s", missing, combined)
	}
}

// ── (d) Malformed file ──────────────────────────────────────────────
//
// AC4: a file that doesn't parse as tfvars → non-zero exit; the error
// points the operator at terraform.tfvars.example as the canonical
// reference shape.
//
// Staff's loadInitVarFile() rejects an empty assignment map with an
// error that explicitly names terraform.tfvars.example. A binary blob
// + nonsense braces yields zero recognised assignments under
// config.ReadTFVarsAssignments's tolerant parser, so this trips the
// "no recognised tfvars assignments" branch. Runs hermetically — the
// rejection happens before the IBM verify step.
func TestInitVarFile_MalformedFile_Fails(t *testing.T) {
	skipIfNoFlag(t)
	resetInitFlags(t)
	stageHermeticHome(t)

	malformed := writeTFVars(t, t.TempDir(), "broken.tfvars",
		"\x00\x01\x02 this is not tfvars { } [ , ; @@@\n")

	_, errOut, runErr := runRootCmd(t, "init", "-w", "test-ws-malformed", "--var-file", malformed)
	if runErr == nil {
		t.Fatalf("AC4: expected non-zero exit on malformed --var-file, got nil\nstderr:\n%s", errOut)
	}
	combined := runErr.Error() + "\n" + errOut
	if !strings.Contains(combined, "terraform.tfvars.example") {
		t.Errorf("AC4: malformed-file error did not reference terraform.tfvars.example\nerror+stderr:\n%s", combined)
	}
}

// ── (e) No-flag byte-identical behaviour ────────────────────────────
//
// AC5: with no --var-file flag the existing init behaviour is byte-
// identical to v1.6.3 — specifically, no terraform.tfvars.user file is
// created in either phase state dir. The workspace stays exactly as
// today's init leaves it.
//
// Driving the full interactive interview would require a real IBM
// Cloud API key (init verifies the key before saving config.yaml), so
// this assertion runs WITHOUT a live key + checks the post-condition
// that matters for parity: regardless of how init exits, no
// terraform.tfvars.user is created when --var-file is absent. The
// happy-path test above covers the `--var-file` positive case; this
// test pins the negative side of the parity contract.
func TestInitVarFile_NoFlagByteIdentical(t *testing.T) {
	skipIfNoFlag(t)
	resetInitFlags(t)
	home := stageHermeticHome(t)
	wsName := "test-ws-noflag"

	// Run init without --var-file. The verify step needs network +
	// real creds so we expect this to fail in the hermetic harness —
	// the parity assertion is about the file-system state, not the
	// command's exit code. We ignore the error deliberately.
	_, _, _ = runRootCmd(t, "init", "-w", wsName)

	// AC5 — neither the workspace root nor the phase state dirs carry a
	// terraform.tfvars.user. (Workspace root is the canonical Sprint 19
	// destination; the state-dir paths are pinned too so a regression
	// back to round-1's mis-located copies trips the test.)
	for _, sub := range []string{"", "state", "state-cluster"} {
		dst := filepath.Join(home, wsName, sub, "terraform.tfvars.user")
		_, err := os.Stat(dst)
		if err == nil {
			t.Errorf("AC5: %s exists but --var-file was not passed; parity contract requires the file NOT be created", dst)
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("AC5: unexpected stat error on %s: %v", dst, err)
		}
	}
}
